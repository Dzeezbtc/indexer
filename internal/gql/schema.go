package gql

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/lib/pq"
)

type scalarKind int

const (
	kInt scalarKind = iota
	kBigInt
	kFloat
	kString
	kBool
	kTimestamp
	kStringArray
)

// scalarFor maps a Postgres type to the API's scalar. Unknown types are not
// exposed.
func scalarFor(udt string) (scalarKind, bool) {
	switch udt {
	case "int2", "int4":
		return kInt, true
	case "int8":
		return kBigInt, true
	case "numeric", "float4", "float8":
		return kFloat, true
	case "text", "varchar", "bpchar":
		return kString, true
	case "bool":
		return kBool, true
	case "timestamptz", "timestamp", "date":
		return kTimestamp, true
	case "_text", "_varchar":
		return kStringArray, true
	}
	return 0, false
}

func (k scalarKind) numeric() bool { return k == kInt || k == kBigInt || k == kFloat }

// BigInt is a 64-bit integer: block heights, micro-USDC amounts and chip
// counts can pass 2^31. It is a JSON number; every value the indexer stores
// is below 2^53, so it's exact in JavaScript too.
var BigInt = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "BigInt",
	Description: "A 64-bit integer, as a JSON number. Accepts a number or a numeric string.",
	Serialize:   func(v interface{}) interface{} { return toInt64OrFloat(v) },
	ParseValue: func(v interface{}) interface{} {
		switch x := v.(type) {
		case int:
			return int64(x)
		case int64:
			return x
		case float64:
			if x == math.Trunc(x) {
				return int64(x)
			}
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
		case json.Number:
			if n, err := x.Int64(); err == nil {
				return n
			}
		}
		return nil
	},
	ParseLiteral: func(v ast.Value) interface{} {
		switch x := v.(type) {
		case *ast.IntValue:
			if n, err := strconv.ParseInt(x.Value, 10, 64); err == nil {
				return n
			}
		case *ast.StringValue:
			if n, err := strconv.ParseInt(x.Value, 10, 64); err == nil {
				return n
			}
		}
		return nil
	},
})

// Timestamp is an ISO 8601 date-time string. Filters accept anything Postgres
// can read as a timestamp, e.g. "2026-11-01" or "2026-11-01T09:30:00Z".
var Timestamp = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Timestamp",
	Description: "An ISO 8601 date-time string. Filters accept anything Postgres reads as a timestamp, e.g. \"2026-11-01\" or \"2026-11-01T09:30:00Z\".",
	Serialize:   func(v interface{}) interface{} { return v },
	ParseValue: func(v interface{}) interface{} {
		if s, ok := v.(string); ok {
			return s
		}
		return nil
	},
	ParseLiteral: func(v ast.Value) interface{} {
		if s, ok := v.(*ast.StringValue); ok {
			return s.Value
		}
		return nil
	},
})

func toInt64OrFloat(v interface{}) interface{} {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return nil
	case int64, int, float64:
		return x
	}
	return nil
}

func (k scalarKind) output() graphql.Output {
	switch k {
	case kInt:
		return graphql.Int
	case kBigInt:
		return BigInt
	case kFloat:
		return graphql.Float
	case kBool:
		return graphql.Boolean
	case kTimestamp:
		return Timestamp
	case kStringArray:
		return graphql.NewList(graphql.String)
	}
	return graphql.String
}

func (k scalarKind) input() graphql.Input {
	if k == kStringArray {
		return graphql.NewList(graphql.String)
	}
	return k.output().(graphql.Input)
}

// orderBy is the direction enum. Values are their own names; the compiler maps
// them to SQL.
var orderBy = graphql.NewEnum(graphql.EnumConfig{
	Name:        "order_by",
	Description: "Sort direction.",
	Values: graphql.EnumValueConfigMap{
		"asc":              {Value: "asc"},
		"asc_nulls_first":  {Value: "asc_nulls_first"},
		"asc_nulls_last":   {Value: "asc_nulls_last"},
		"desc":             {Value: "desc"},
		"desc_nulls_first": {Value: "desc_nulls_first"},
		"desc_nulls_last":  {Value: "desc_nulls_last"},
	},
})

var orderSQL = map[string]string{
	"asc":              "ASC",
	"asc_nulls_first":  "ASC NULLS FIRST",
	"asc_nulls_last":   "ASC NULLS LAST",
	"desc":             "DESC",
	"desc_nulls_first": "DESC NULLS FIRST",
	"desc_nulls_last":  "DESC NULLS LAST",
}

// comparison builds the <Scalar>_comparison_exp input for a scalar kind.
func comparison(k scalarKind) *graphql.InputObject {
	name := map[scalarKind]string{kInt: "Int", kBigInt: "BigInt", kFloat: "Float", kString: "String", kBool: "Boolean", kTimestamp: "Timestamp", kStringArray: "String_array"}[k]
	fields := graphql.InputObjectConfigFieldMap{
		"_is_null": {Type: graphql.Boolean, Description: "true: the value is null; false: it isn't."},
	}
	if k == kStringArray {
		fields["_contains"] = &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String)), Description: "The array contains every one of these values."}
	} else {
		t := k.input()
		list := graphql.NewList(graphql.NewNonNull(t))
		fields["_eq"] = &graphql.InputObjectFieldConfig{Type: t}
		fields["_neq"] = &graphql.InputObjectFieldConfig{Type: t}
		fields["_in"] = &graphql.InputObjectFieldConfig{Type: list}
		fields["_nin"] = &graphql.InputObjectFieldConfig{Type: list}
		if k != kBool {
			fields["_gt"] = &graphql.InputObjectFieldConfig{Type: t}
			fields["_gte"] = &graphql.InputObjectFieldConfig{Type: t}
			fields["_lt"] = &graphql.InputObjectFieldConfig{Type: t}
			fields["_lte"] = &graphql.InputObjectFieldConfig{Type: t}
		}
		if k == kString {
			fields["_like"] = &graphql.InputObjectFieldConfig{Type: graphql.String, Description: "SQL LIKE: % matches any run of characters, _ one character."}
			fields["_nlike"] = &graphql.InputObjectFieldConfig{Type: graphql.String}
			fields["_ilike"] = &graphql.InputObjectFieldConfig{Type: graphql.String, Description: "Case-insensitive LIKE."}
			fields["_nilike"] = &graphql.InputObjectFieldConfig{Type: graphql.String}
		}
	}
	return graphql.NewInputObject(graphql.InputObjectConfig{Name: name + "_comparison_exp", Fields: fields})
}

// Limits bound what one request can ask for.
type Limits struct {
	DefaultLimit int // rows per list when the query sets no limit
	MaxLimit     int // most rows per list
	MaxDepth     int // most nested relation levels
	MaxRelations int // most relation fields in one root field
	MaxRoots     int // most root fields in one operation
}

// DefaultLimits are the limits used unless configured otherwise.
var DefaultLimits = Limits{DefaultLimit: 100, MaxLimit: 1000, MaxDepth: 4, MaxRelations: 25, MaxRoots: 10}

// BuildSchema builds the GraphQL schema for a catalog. run executes compiled
// SQL and returns the decoded JSON.
func BuildSchema(cat *Catalog, lim Limits, run Runner) (graphql.Schema, error) {
	comps := map[scalarKind]*graphql.InputObject{}
	for _, k := range []scalarKind{kInt, kBigInt, kFloat, kString, kBool, kTimestamp, kStringArray} {
		comps[k] = comparison(k)
	}
	objects := map[string]*graphql.Object{}
	boolExps := map[string]*graphql.InputObject{}
	orderBys := map[string]*graphql.InputObject{}

	for _, name := range cat.Order {
		t := cat.Tables[name]
		boolExps[name] = graphql.NewInputObject(graphql.InputObjectConfig{
			Name:        name + "_bool_exp",
			Description: fmt.Sprintf("Filter for %s. Fields are ANDed; use _or and _not for anything else. A relation matches if any related row matches.", name),
			Fields: graphql.InputObjectConfigFieldMapThunk(func() graphql.InputObjectConfigFieldMap {
				f := graphql.InputObjectConfigFieldMap{
					"_and": {Type: graphql.NewList(graphql.NewNonNull(boolExps[t.Spec.Name]))},
					"_or":  {Type: graphql.NewList(graphql.NewNonNull(boolExps[t.Spec.Name]))},
					"_not": {Type: boolExps[t.Spec.Name]},
				}
				for _, c := range t.Columns {
					f[c.Name] = &graphql.InputObjectFieldConfig{Type: comps[c.scalar], Description: c.Description}
				}
				for _, r := range t.Relations {
					f[r.Name] = &graphql.InputObjectFieldConfig{Type: boolExps[r.Target]}
				}
				return f
			}),
		})
		ob := graphql.InputObjectConfigFieldMap{}
		for _, c := range t.Columns {
			if c.scalar != kStringArray {
				ob[c.Name] = &graphql.InputObjectFieldConfig{Type: orderBy}
			}
		}
		orderBys[name] = graphql.NewInputObject(graphql.InputObjectConfig{
			Name:        name + "_order_by",
			Description: "Sort order. Give one column per entry and list entries in priority order: order_by: [{a: desc}, {b: asc}].",
			Fields:      ob,
		})
	}

	listArgs := func(table string) graphql.FieldConfigArgument {
		return graphql.FieldConfigArgument{
			"where":    {Type: boolExps[table]},
			"order_by": {Type: graphql.NewList(graphql.NewNonNull(orderBys[table]))},
			"limit":    {Type: graphql.Int, Description: fmt.Sprintf("Rows to return (default %d, at most %d).", lim.DefaultLimit, lim.MaxLimit)},
			"offset":   {Type: graphql.Int, Description: "Rows to skip."},
		}
	}

	for _, name := range cat.Order {
		t := cat.Tables[name]
		objects[name] = graphql.NewObject(graphql.ObjectConfig{
			Name:        name,
			Description: t.Spec.Description,
			Fields: graphql.FieldsThunk(func() graphql.Fields {
				f := graphql.Fields{}
				for _, c := range t.Columns {
					f[c.Name] = &graphql.Field{Type: c.scalar.output(), Description: c.Description, Resolve: columnResolver(c.Name, c.scalar)}
				}
				for _, r := range t.Relations {
					r := r
					target := objects[r.Target]
					if r.Kind == ObjectRel {
						f[r.Name] = &graphql.Field{Type: target, Description: fmt.Sprintf("The %s row for this row.", r.Target), Resolve: byResponseKey}
					} else {
						f[r.Name] = &graphql.Field{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(target))), Description: fmt.Sprintf("The %s rows for this row.", r.Target), Args: listArgs(r.Target), Resolve: byResponseKey}
					}
				}
				return f
			}),
		})
	}

	root := graphql.Fields{}
	for _, name := range cat.Order {
		t := cat.Tables[name]
		root[name] = &graphql.Field{
			Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(objects[name]))),
			Description: t.Spec.Description,
			Args:        listArgs(name),
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				sqlText, args, err := compileRoot(cat, lim, t, p)
				if err != nil {
					return nil, err
				}
				return run(p.Context, sqlText, args)
			},
		}
		root[name+"_aggregate"] = &graphql.Field{
			Type:        graphql.NewNonNull(aggregateType(t)),
			Description: fmt.Sprintf("Totals over the %s rows matching where. Group in the client if you need a breakdown.", name),
			Args:        graphql.FieldConfigArgument{"where": {Type: boolExps[name]}},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				sqlText, args, err := compileAggregate(cat, lim, t, p)
				if err != nil {
					return nil, err
				}
				return run(p.Context, sqlText, args)
			},
		}
	}

	return graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{Name: "query_root", Fields: root}),
	})
}

// aggregateType builds <table>_aggregate { aggregate { count sum avg min max } }.
func aggregateType(t *Table) *graphql.Object {
	name := t.Spec.Name
	sub := func(fn string, keep func(scalarKind) bool, out func(scalarKind) graphql.Output) *graphql.Object {
		f := graphql.Fields{}
		for _, c := range t.Columns {
			if keep(c.scalar) {
				f[c.Name] = &graphql.Field{Type: out(c.scalar), Resolve: columnResolver(c.Name, aggScalar(fn, c.scalar))}
			}
		}
		if len(f) == 0 {
			return nil
		}
		return graphql.NewObject(graphql.ObjectConfig{Name: name + "_" + fn + "_fields", Fields: f})
	}
	numeric := func(k scalarKind) bool { return k.numeric() }
	ordered := func(k scalarKind) bool { return k.numeric() || k == kTimestamp }
	fields := graphql.Fields{
		"count": {Type: graphql.NewNonNull(BigInt), Resolve: columnResolver("count", kBigInt)},
	}
	for fn, o := range map[string]*graphql.Object{
		"sum": sub("sum", numeric, func(k scalarKind) graphql.Output { return aggScalar("sum", k).output() }),
		"avg": sub("avg", numeric, func(scalarKind) graphql.Output { return graphql.Float }),
		"min": sub("min", ordered, func(k scalarKind) graphql.Output { return k.output() }),
		"max": sub("max", ordered, func(k scalarKind) graphql.Output { return k.output() }),
	} {
		if o != nil {
			fields[fn] = &graphql.Field{Type: o, Resolve: byResponseKey}
		}
	}
	inner := graphql.NewObject(graphql.ObjectConfig{Name: name + "_aggregate_fields", Fields: fields})
	return graphql.NewObject(graphql.ObjectConfig{
		Name:   name + "_aggregate",
		Fields: graphql.Fields{"aggregate": {Type: graphql.NewNonNull(inner), Resolve: byResponseKey}},
	})
}

// aggScalar is the type an aggregate of a column comes back as.
func aggScalar(fn string, k scalarKind) scalarKind {
	switch fn {
	case "avg":
		return kFloat
	case "sum":
		if k == kInt || k == kBigInt {
			return kBigInt
		}
		return kFloat
	}
	return k
}

// columnResolver reads a column from the row JSON. Numbers arrive as
// json.Number so 64-bit values stay exact.
func columnResolver(key string, k scalarKind) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		m, _ := p.Source.(map[string]interface{})
		v, ok := m[key]
		if !ok || v == nil {
			return nil, nil
		}
		n, isNum := v.(json.Number)
		switch k {
		case kInt:
			if isNum {
				i, err := n.Int64()
				return int(i), err
			}
		case kBigInt:
			return toInt64OrFloat(v), nil
		case kFloat:
			if isNum {
				return n.Float64()
			}
		}
		return v, nil
	}
}

// byResponseKey reads a relation or aggregate part from the row JSON, keyed by
// the field's response name so two aliases of one relation don't collide. The
// key carries an "@" prefix (never valid in a GraphQL name) so an alias can't
// collide with a column, which is keyed by its plain name.
func byResponseKey(p graphql.ResolveParams) (interface{}, error) {
	m, _ := p.Source.(map[string]interface{})
	key, _ := p.Info.Path.Key.(string)
	return m[relKey(key)], nil
}

func relKey(responseKey string) string { return "@" + responseKey }

func pqStringArray(s []string) interface{} { return pq.Array(s) }
