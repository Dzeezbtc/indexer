package gql

import (
	"strconv"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

// field is one response key with every AST node that contributes to it.
// GraphQL merges same-key fields (validation guarantees their arguments
// match), so their sub-selections are merged too.
type field struct {
	key   string
	name  string
	nodes []*ast.Field
}

type walker struct {
	fragments map[string]ast.Definition
	vars      map[string]interface{}
}

func newWalker(p graphql.ResolveParams) *walker {
	return &walker{fragments: p.Info.Fragments, vars: p.Info.VariableValues}
}

// fields collects the fields selected under nodes, in first-seen order,
// following fragments and honouring @skip / @include.
func (w *walker) fields(nodes []*ast.Field) []*field {
	var out []*field
	byKey := map[string]*field{}
	var visit func(ss *ast.SelectionSet, seen map[string]bool)
	visit = func(ss *ast.SelectionSet, seen map[string]bool) {
		if ss == nil {
			return
		}
		for _, sel := range ss.Selections {
			switch s := sel.(type) {
			case *ast.Field:
				if !w.include(s.Directives) {
					continue
				}
				key := s.Name.Value
				if s.Alias != nil && s.Alias.Value != "" {
					key = s.Alias.Value
				}
				f := byKey[key]
				if f == nil {
					f = &field{key: key, name: s.Name.Value}
					byKey[key] = f
					out = append(out, f)
				}
				f.nodes = append(f.nodes, s)
			case *ast.InlineFragment:
				if w.include(s.Directives) {
					visit(s.SelectionSet, seen)
				}
			case *ast.FragmentSpread:
				if !w.include(s.Directives) || seen[s.Name.Value] {
					continue
				}
				if def, ok := w.fragments[s.Name.Value].(*ast.FragmentDefinition); ok {
					seen[s.Name.Value] = true
					visit(def.SelectionSet, seen)
					delete(seen, s.Name.Value)
				}
			}
		}
	}
	for _, n := range nodes {
		visit(n.SelectionSet, map[string]bool{})
	}
	return out
}

func (w *walker) include(ds []*ast.Directive) bool {
	for _, d := range ds {
		var cond interface{}
		for _, a := range d.Arguments {
			if a.Name.Value == "if" {
				cond = w.value(a.Value)
			}
		}
		b, _ := cond.(bool)
		switch d.Name.Value {
		case "skip":
			if b {
				return false
			}
		case "include":
			if !b {
				return false
			}
		}
	}
	return true
}

// args reads a field's arguments into Go values (variables substituted).
func (w *walker) args(f *ast.Field) map[string]interface{} {
	out := map[string]interface{}{}
	for _, a := range f.Arguments {
		out[a.Name.Value] = w.value(a.Value)
	}
	return out
}

// value turns an AST value into a Go value: objects become maps, lists
// slices, ints int64, floats float64, enums their name. The query has already
// been validated against the schema, so types are as declared.
func (w *walker) value(v ast.Value) interface{} {
	switch x := v.(type) {
	case *ast.Variable:
		return normalise(w.vars[x.Name.Value])
	case *ast.IntValue:
		n, _ := strconv.ParseInt(x.Value, 10, 64)
		return n
	case *ast.FloatValue:
		f, _ := strconv.ParseFloat(x.Value, 64)
		return f
	case *ast.StringValue:
		return x.Value
	case *ast.BooleanValue:
		return x.Value
	case *ast.EnumValue:
		return x.Value
	case *ast.ListValue:
		out := make([]interface{}, len(x.Values))
		for i, e := range x.Values {
			out[i] = w.value(e)
		}
		return out
	case *ast.ObjectValue:
		out := map[string]interface{}{}
		for _, f := range x.Fields {
			out[f.Name.Value] = w.value(f.Value)
		}
		return out
	}
	return nil
}

// normalise makes coerced variable values look like literal ones (ints as
// int64), so the compiler sees one shape.
func normalise(v interface{}) interface{} {
	switch x := v.(type) {
	case int:
		return int64(x)
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, e := range x {
			out[i] = normalise(e)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, e := range x {
			out[k] = normalise(e)
		}
		return out
	}
	return v
}
