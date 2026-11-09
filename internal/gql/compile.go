package gql

import (
	"fmt"
	"sort"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/lib/pq"
)

// builder accumulates one SQL statement and its parameters.
type builder struct {
	cat       *Catalog
	lim       Limits
	w         *walker
	args      []interface{}
	aliases   int
	relations int
}

func (b *builder) param(v interface{}) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *builder) alias() string {
	b.aliases++
	return fmt.Sprintf("r%d", b.aliases)
}

func ident(s string) string { return pq.QuoteIdentifier(s) }

func col(alias, name string) string { return alias + "." + ident(name) }

// errorf makes a GraphQL error message for a bad request.
func errorf(format string, a ...interface{}) error { return fmt.Errorf(format, a...) }

func checkRoots(p graphql.ResolveParams, lim Limits) error {
	op, ok := p.Info.Operation.(*ast.OperationDefinition)
	if !ok || op.SelectionSet == nil {
		return nil
	}
	if n := len(op.SelectionSet.Selections); n > lim.MaxRoots {
		return errorf("at most %d root fields per request (got %d)", lim.MaxRoots, n)
	}
	return nil
}

// compileRoot compiles a root list field into SQL returning a JSON array.
func compileRoot(cat *Catalog, lim Limits, t *Table, p graphql.ResolveParams) (string, []interface{}, error) {
	if err := checkRoots(p, lim); err != nil {
		return "", nil, err
	}
	b := &builder{cat: cat, lim: lim, w: newWalker(p)}
	f := &field{key: p.Info.FieldName, name: p.Info.FieldName, nodes: p.Info.FieldASTs}
	q, err := b.list(t, f, nil, "", 0)
	if err != nil {
		return "", nil, err
	}
	return "SELECT " + q, b.args, nil
}

// list compiles a list of rows from t (optionally joined to a parent row) into
// a JSON-array expression. Rows are aggregated in the requested order.
func (b *builder) list(t *Table, f *field, join [][2]string, parent string, depth int) (string, error) {
	a := b.alias()
	args := b.w.args(f.nodes[0])

	where, err := b.where(t, a, args["where"])
	if err != nil {
		return "", err
	}
	conds := []string{where}
	for _, p := range join {
		conds = append(conds, col(a, p[1])+" = "+col(parent, p[0]))
	}

	order, err := b.order(t, a, args["order_by"])
	if err != nil {
		return "", err
	}
	limit, offset, err := b.page(args)
	if err != nil {
		return "", err
	}

	obj, err := b.object(t, a, f, depth)
	if err != nil {
		return "", err
	}

	// Project the sort keys so the outer aggregate can keep the order.
	proj := []string{obj + " AS j"}
	outer := make([]string, len(order))
	inner := make([]string, len(order))
	for i, o := range order {
		proj = append(proj, fmt.Sprintf("%s AS o%d", o.expr, i))
		outer[i] = fmt.Sprintf("s.o%d %s", i, o.dir)
		inner[i] = o.expr + " " + o.dir
	}

	sql := fmt.Sprintf("SELECT %s FROM public.%s %s WHERE %s ORDER BY %s LIMIT %d",
		strings.Join(proj, ", "), ident(t.Spec.Name), a, strings.Join(conds, " AND "), strings.Join(inner, ", "), limit)
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", offset)
	}
	return fmt.Sprintf("(SELECT COALESCE(jsonb_agg(s.j ORDER BY %s), '[]'::jsonb) FROM (%s) s)", strings.Join(outer, ", "), sql), nil
}

// object compiles the selected fields of one row of t (alias a) into a
// jsonb object expression.
func (b *builder) object(t *Table, a string, f *field, depth int) (string, error) {
	var pairs []string
	for _, sf := range b.w.fields(f.nodes) {
		if strings.HasPrefix(sf.name, "__") {
			continue // __typename is answered by the GraphQL layer
		}
		if c := t.Column(sf.name); c != nil {
			pairs = append(pairs, quoteLit(c.Name), col(a, c.Name))
			continue
		}
		r := t.Relation(sf.name)
		if r == nil {
			return "", errorf("unknown field %q on %s", sf.name, t.Spec.Name)
		}
		if depth+1 > b.lim.MaxDepth {
			return "", errorf("relations nest at most %d deep", b.lim.MaxDepth)
		}
		b.relations++
		if b.relations > b.lim.MaxRelations {
			return "", errorf("at most %d relation fields per root field", b.lim.MaxRelations)
		}
		target := b.cat.Tables[r.Target]
		var expr string
		var err error
		if r.Kind == ArrayRel {
			expr, err = b.list(target, sf, r.On, a, depth+1)
		} else {
			expr, err = b.one(target, sf, r.On, a, depth+1)
		}
		if err != nil {
			return "", err
		}
		pairs = append(pairs, quoteLit(relKey(sf.key)), expr)
	}
	return buildObject(pairs), nil
}

// one compiles an object relation: the first matching row, or null.
func (b *builder) one(t *Table, f *field, join [][2]string, parent string, depth int) (string, error) {
	a := b.alias()
	obj, err := b.object(t, a, f, depth)
	if err != nil {
		return "", err
	}
	conds := make([]string, len(join))
	for i, p := range join {
		conds[i] = col(a, p[1]) + " = " + col(parent, p[0])
	}
	return fmt.Sprintf("(SELECT %s FROM public.%s %s WHERE %s LIMIT 1)", obj, ident(t.Spec.Name), a, strings.Join(conds, " AND ")), nil
}

// buildObject turns key/value pairs into a jsonb object. jsonb_build_object
// takes at most 100 arguments, so wide rows are built in chunks and merged.
func buildObject(pairs []string) string {
	if len(pairs) == 0 {
		return "'{}'::jsonb"
	}
	const maxArgs = 100
	var parts []string
	for i := 0; i < len(pairs); i += maxArgs {
		end := i + maxArgs
		if end > len(pairs) {
			end = len(pairs)
		}
		parts = append(parts, "jsonb_build_object("+strings.Join(pairs[i:end], ", ")+")")
	}
	return strings.Join(parts, " || ")
}

func quoteLit(s string) string { return pq.QuoteLiteral(s) }

type orderTerm struct{ expr, dir string }

// order reads order_by and appends the table's key so ties break the same way
// on every page.
func (b *builder) order(t *Table, a string, v interface{}) ([]orderTerm, error) {
	var out []orderTerm
	used := map[string]bool{}
	list, _ := v.([]interface{})
	if m, ok := v.(map[string]interface{}); ok {
		list = []interface{}{m} // a single object is accepted as a one-item list
	}
	for _, e := range list {
		m, _ := e.(map[string]interface{})
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names) // several columns in one entry: deterministic order
		for _, name := range names {
			dir, ok := orderSQL[fmt.Sprint(m[name])]
			c := t.Column(name)
			if !ok || c == nil {
				return nil, errorf("can't order %s by %q", t.Spec.Name, name)
			}
			if used[name] {
				continue
			}
			used[name] = true
			out = append(out, orderTerm{col(a, name), dir})
		}
	}
	for _, k := range t.Spec.Key {
		if !used[k] {
			out = append(out, orderTerm{col(a, k), "ASC"})
		}
	}
	if len(out) == 0 {
		out = append(out, orderTerm{"1", "ASC"})
	}
	return out, nil
}

func (b *builder) page(args map[string]interface{}) (int64, int64, error) {
	limit := int64(b.lim.DefaultLimit)
	if v, ok := args["limit"].(int64); ok {
		if v < 0 {
			return 0, 0, errorf("limit can't be negative")
		}
		if v > int64(b.lim.MaxLimit) {
			return 0, 0, errorf("limit is at most %d", b.lim.MaxLimit)
		}
		limit = v
	}
	var offset int64
	if v, ok := args["offset"].(int64); ok {
		if v < 0 {
			return 0, 0, errorf("offset can't be negative")
		}
		offset = v
	}
	return limit, offset, nil
}

// where compiles a <table>_bool_exp into a SQL condition.
func (b *builder) where(t *Table, a string, v interface{}) (string, error) {
	if v == nil {
		return "TRUE", nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", errorf("where on %s must be an object", t.Spec.Name)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var conds []string
	for _, k := range keys {
		val := m[k]
		switch k {
		case "_and", "_or":
			list, _ := val.([]interface{})
			if len(list) == 0 {
				if k == "_and" {
					conds = append(conds, "TRUE")
				} else {
					conds = append(conds, "FALSE")
				}
				continue
			}
			parts := make([]string, len(list))
			for i, e := range list {
				c, err := b.where(t, a, e)
				if err != nil {
					return "", err
				}
				parts[i] = c
			}
			sep := " AND "
			if k == "_or" {
				sep = " OR "
			}
			conds = append(conds, "("+strings.Join(parts, sep)+")")
		case "_not":
			c, err := b.where(t, a, val)
			if err != nil {
				return "", err
			}
			conds = append(conds, "NOT ("+c+")")
		default:
			if c := t.Column(k); c != nil {
				cond, err := b.compare(c, col(a, k), val)
				if err != nil {
					return "", err
				}
				conds = append(conds, cond)
				continue
			}
			r := t.Relation(k)
			if r == nil {
				return "", errorf("unknown filter %q on %s", k, t.Spec.Name)
			}
			b.relations++
			if b.relations > b.lim.MaxRelations {
				return "", errorf("at most %d relation fields per root field", b.lim.MaxRelations)
			}
			target := b.cat.Tables[r.Target]
			ra := b.alias()
			inner, err := b.where(target, ra, val)
			if err != nil {
				return "", err
			}
			join := make([]string, len(r.On))
			for i, p := range r.On {
				join[i] = col(ra, p[1]) + " = " + col(a, p[0])
			}
			conds = append(conds, fmt.Sprintf("EXISTS (SELECT 1 FROM public.%s %s WHERE %s AND %s)", ident(target.Spec.Name), ra, strings.Join(join, " AND "), inner))
		}
	}
	if len(conds) == 0 {
		return "TRUE", nil
	}
	return "(" + strings.Join(conds, " AND ") + ")", nil
}

var cmpSQL = map[string]string{"_eq": "=", "_neq": "<>", "_gt": ">", "_gte": ">=", "_lt": "<", "_lte": "<=", "_like": "LIKE", "_nlike": "NOT LIKE", "_ilike": "ILIKE", "_nilike": "NOT ILIKE"}

// compare compiles one column's <Scalar>_comparison_exp.
func (b *builder) compare(c *Column, expr string, v interface{}) (string, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", errorf("filter on %s must be an object like {_eq: ...}", c.Name)
	}
	ops := make([]string, 0, len(m))
	for k := range m {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	var conds []string
	for _, op := range ops {
		val := m[op]
		if val == nil {
			return "", errorf("%s.%s is null: use _is_null to test for null", c.Name, op)
		}
		switch op {
		case "_is_null":
			if val == true {
				conds = append(conds, expr+" IS NULL")
			} else {
				conds = append(conds, expr+" IS NOT NULL")
			}
		case "_in", "_nin":
			arr, err := typedArray(c, val)
			if err != nil {
				return "", err
			}
			cond := expr + " = ANY(" + b.param(arr) + ")"
			if op == "_nin" {
				cond = "NOT (" + cond + ")"
			}
			conds = append(conds, cond)
		case "_contains":
			arr, err := typedArray(c, val)
			if err != nil {
				return "", err
			}
			conds = append(conds, expr+" @> "+b.param(arr))
		default:
			sqlOp, ok := cmpSQL[op]
			if !ok {
				return "", errorf("unknown operator %s on %s", op, c.Name)
			}
			conds = append(conds, expr+" "+sqlOp+" "+b.param(val))
		}
	}
	if len(conds) == 0 {
		return "TRUE", nil
	}
	return strings.Join(conds, " AND "), nil
}

// typedArray turns a list into a typed slice lib/pq can send as an array.
func typedArray(c *Column, v interface{}) (interface{}, error) {
	list, ok := v.([]interface{})
	if !ok {
		return nil, errorf("%s: expected a list", c.Name)
	}
	switch c.scalar {
	case kInt, kBigInt:
		out := make([]int64, len(list))
		for i, e := range list {
			n, ok := e.(int64)
			if !ok {
				return nil, errorf("%s: expected integers", c.Name)
			}
			out[i] = n
		}
		return pq.Array(out), nil
	case kFloat:
		out := make([]float64, len(list))
		for i, e := range list {
			switch n := e.(type) {
			case float64:
				out[i] = n
			case int64:
				out[i] = float64(n)
			default:
				return nil, errorf("%s: expected numbers", c.Name)
			}
		}
		return pq.Array(out), nil
	case kBool:
		out := make([]bool, len(list))
		for i, e := range list {
			bv, ok := e.(bool)
			if !ok {
				return nil, errorf("%s: expected booleans", c.Name)
			}
			out[i] = bv
		}
		return pq.Array(out), nil
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, errorf("%s: expected strings", c.Name)
		}
		out[i] = s
	}
	return pq.Array(out), nil
}

// compileAggregate compiles <table>_aggregate into SQL returning one object.
func compileAggregate(cat *Catalog, lim Limits, t *Table, p graphql.ResolveParams) (string, []interface{}, error) {
	if err := checkRoots(p, lim); err != nil {
		return "", nil, err
	}
	b := &builder{cat: cat, lim: lim, w: newWalker(p)}
	a := b.alias()
	args := b.w.args(p.Info.FieldASTs[0])
	where, err := b.where(t, a, args["where"])
	if err != nil {
		return "", nil, err
	}
	var outer []string
	for _, af := range b.w.fields(p.Info.FieldASTs) {
		if af.name != "aggregate" {
			continue
		}
		var inner []string
		for _, fn := range b.w.fields(af.nodes) {
			switch fn.name {
			case "count":
				inner = append(inner, quoteLit("count"), "count(*)")
			case "sum", "avg", "min", "max":
				var cols []string
				for _, cf := range b.w.fields(fn.nodes) {
					if strings.HasPrefix(cf.name, "__") {
						continue
					}
					c := t.Column(cf.name)
					if c == nil {
						return "", nil, errorf("unknown column %q on %s", cf.name, t.Spec.Name)
					}
					cols = append(cols, quoteLit(c.Name), fmt.Sprintf("%s(%s)", fn.name, col(a, c.Name)))
				}
				inner = append(inner, quoteLit(relKey(fn.key)), buildObject(cols))
			}
		}
		outer = append(outer, quoteLit(relKey(af.key)), buildObject(inner))
	}
	return fmt.Sprintf("SELECT %s FROM public.%s %s WHERE %s", buildObject(outer), ident(t.Spec.Name), a, where), b.args, nil
}
