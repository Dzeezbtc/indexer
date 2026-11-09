package gql

import (
	"testing"

	"github.com/graphql-go/graphql"
)

func cols(names ...string) []Column {
	out := make([]Column, len(names))
	for i, n := range names {
		out[i] = Column{Name: n, UDT: "text"}
	}
	return out
}

// Before block52/indexer#15 there are no hands_meta or games tables: relations
// to them are dropped and the schema still builds. Once they exist, they and
// their relations appear with no code change.
func TestCatalogFollowsTheDatabase(t *testing.T) {
	base := map[string][]Column{
		"poker_hands":  cols("id", "game_id", "hand_number"),
		"hand_players": cols("id", "game_id", "hand_number", "player_address"),
		"api_keys":     cols("id", "key_hash"), // never exposed: not in Specs
	}
	cat := BuildCatalog(Specs, base)
	if cat.Tables["api_keys"] != nil || cat.Tables["games"] != nil {
		t.Fatal("unexpected table")
	}
	ph := cat.Tables["poker_hands"]
	if ph.Relation("players") == nil || ph.Relation("game") != nil || ph.Relation("meta") != nil {
		t.Fatalf("poker_hands relations: %+v", ph.Relations)
	}
	if _, err := BuildSchema(cat, DefaultLimits, nil); err != nil {
		t.Fatal(err)
	}

	base["games"] = cols("game_id", "format", "rake_owner")
	base["hands_meta"] = cols("game_id", "hand_number", "rake")
	cat = BuildCatalog(Specs, base)
	if cat.Tables["poker_hands"].Relation("game") == nil || cat.Tables["hand_players"].Relation("meta") == nil {
		t.Fatal("relations to the new tables are missing")
	}
	s, err := BuildSchema(cat, DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.QueryType().Fields()["games"] == nil || s.QueryType().Fields()["hands_meta_aggregate"] == nil {
		t.Fatal("new tables not in the schema")
	}
}

// Unknown Postgres types are left out rather than guessed at.
func TestUnknownColumnTypesAreSkipped(t *testing.T) {
	cat := BuildCatalog(Specs, map[string][]Column{
		"indexing_progress": {{Name: "id", UDT: "int4"}, {Name: "blob", UDT: "bytea"}},
	})
	tbl := cat.Tables["indexing_progress"]
	if tbl.Column("id") == nil || tbl.Column("blob") != nil {
		t.Fatalf("columns: %+v", tbl.Columns)
	}
	s, err := BuildSchema(cat, DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := graphql.Do(graphql.Params{Schema: s, RequestString: `{ __type(name: "indexing_progress") { fields { name } } }`})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
}
