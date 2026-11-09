package gql

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// These tests need a Postgres with the indexer schema (sql/*.sql). Set
// TEST_DATABASE_URL, e.g.
//
//	TEST_DATABASE_URL="postgres://poker:poker_indexer_dev@localhost:5432/poker_hands?sslmode=disable" go test ./internal/gql/
//
// They add rows whose ids start with "gqltest-" and remove them afterwards.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	clean := func() {
		for _, q := range []string{
			"DELETE FROM poker_hands WHERE game_id LIKE 'gqltest-%'",
			"DELETE FROM hand_results WHERE game_id LIKE 'gqltest-%'",
			"DELETE FROM hand_players WHERE game_id LIKE 'gqltest-%'",
			"DELETE FROM revealed_cards WHERE game_id LIKE 'gqltest-%'",
			"DELETE FROM player_stats WHERE player_address LIKE 'gqltest-%'",
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	clean()
	t.Cleanup(func() { clean(); db.Close() })
	for _, q := range []string{
		`INSERT INTO poker_hands (game_id, hand_number, block_height, deck_seed) VALUES
			('gqltest-g1', 1, 9007199254740000, 'seed-a'), ('gqltest-g1', 2, 101, 'seed-b'), ('gqltest-g2', 1, 102, 'seed-c')`,
		`INSERT INTO hand_results (game_id, hand_number, block_height, community_cards, winner_count) VALUES
			('gqltest-g1', 1, 100, '{As,Kd,7h,2c,9s}', 1), ('gqltest-g1', 2, 101, '{}', 1), ('gqltest-g2', 1, 102, '{Qh,Qs,3d}', 1)`,
		`INSERT INTO hand_players (game_id, hand_number, player_address, seat, status, won_amount, block_height, ended_at) VALUES
			('gqltest-g1', 1, 'gqltest-alice', 1, 'active', 300, 100, '2026-11-01T10:00:00Z'),
			('gqltest-g1', 1, 'gqltest-bob',   2, 'folded', 0,   100, '2026-11-01T10:00:00Z'),
			('gqltest-g1', 2, 'gqltest-alice', 1, 'folded', 0,   101, '2026-11-02T10:00:00Z'),
			('gqltest-g1', 2, 'gqltest-bob',   2, 'active', 150, 101, '2026-11-02T10:00:00Z'),
			('gqltest-g2', 1, 'gqltest-carol', 1, 'active', 500, 102, '2026-11-03T10:00:00Z'),
			('gqltest-g2', 1, 'gqltest-alice', 2, 'folded', 0,   102, '2026-11-03T10:00:00Z')`,
		`INSERT INTO revealed_cards (game_id, hand_number, block_height, card, card_type, position) VALUES
			('gqltest-g1', 1, 100, 'As', 'community', 0), ('gqltest-g1', 1, 100, 'Kd', 'community', 1)`,
		`INSERT INTO player_stats (player_address, vip_tier) VALUES ('gqltest-alice', 'gold'), ('gqltest-bob', 'bronze'), ('gqltest-carol', 'silver')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func testService(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	cat, err := LoadCatalog(context.Background(), db, Specs)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(cat, DefaultLimits, DBRunner(db, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// run executes a query and returns its data as JSON, failing on errors.
func run(t *testing.T, svc *Service, q string, vars map[string]interface{}) string {
	t.Helper()
	res := svc.Do(context.Background(), q, vars)
	if len(res.Errors) > 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	b, _ := json.Marshal(res.Data)
	return string(b)
}

// runErr executes a query that must fail and returns the first message.
func runErr(t *testing.T, svc *Service, q string) string {
	t.Helper()
	res := svc.Do(context.Background(), q, nil)
	if len(res.Errors) == 0 {
		t.Fatalf("expected an error for %s", q)
	}
	return res.Errors[0].Message
}

func expectJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w interface{}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("\n got %s\nwant %s", gb, wb)
	}
}

func TestListFilterOrderAndExactBigInt(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ poker_hands(where: {game_id: {_like: "gqltest-%"}}, order_by: [{game_id: asc}, {hand_number: desc}]) { game_id hand_number block_height } }`, nil)
	expectJSON(t, got, `{"poker_hands":[
		{"game_id":"gqltest-g1","hand_number":2,"block_height":101},
		{"game_id":"gqltest-g1","hand_number":1,"block_height":9007199254740000},
		{"game_id":"gqltest-g2","hand_number":1,"block_height":102}]}`)
}

func TestNestedRelationsKeepOrder(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ poker_hands(where: {game_id: {_eq: "gqltest-g1"}, hand_number: {_eq: 1}}) {
		result { community_cards winner_count }
		players(order_by: {won_amount: desc}) { player_address won_amount player { vip_tier } }
		cards(order_by: {position: desc}) { card }
	} }`, nil)
	expectJSON(t, got, `{"poker_hands":[{
		"result":{"community_cards":["As","Kd","7h","2c","9s"],"winner_count":1},
		"players":[{"player_address":"gqltest-alice","won_amount":300,"player":{"vip_tier":"gold"}},
		           {"player_address":"gqltest-bob","won_amount":0,"player":{"vip_tier":"bronze"}}],
		"cards":[{"card":"Kd"},{"card":"As"}]}]}`)
}

func TestFilterByRelationOrAndNot(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ poker_hands(where: {game_id: {_like: "gqltest-%"}, players: {player_address: {_eq: "gqltest-carol"}}}) { game_id hand_number } }`, nil)
	expectJSON(t, got, `{"poker_hands":[{"game_id":"gqltest-g2","hand_number":1}]}`)

	got = run(t, svc, `{ hand_players(where: {game_id: {_like: "gqltest-%"}, _or: [{won_amount: {_gte: 500}}, {player: {vip_tier: {_eq: "bronze"}}}], _not: {status: {_eq: "folded"}}}, order_by: {won_amount: asc}) { player_address won_amount } }`, nil)
	expectJSON(t, got, `{"hand_players":[{"player_address":"gqltest-bob","won_amount":150},{"player_address":"gqltest-carol","won_amount":500}]}`)
}

func TestVariablesAliasesFragmentsAndSkip(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `query Q($p: String!, $n: Int, $hide: Boolean!, $since: Timestamp) {
		a: hand_players(where: {player_address: {_eq: $p}, ended_at: {_gte: $since}}, order_by: {ended_at: asc}, limit: $n) { ...money }
		b: hand_players(where: {player_address: {_eq: $p}}, order_by: {ended_at: desc}, limit: 1) { hand_number seat @skip(if: $hide) h: hand { s: deck_seed } }
	}
	fragment money on hand_players { won_amount game_id }`, map[string]interface{}{"p": "gqltest-alice", "n": 2, "hide": true, "since": "2026-11-02"})
	expectJSON(t, got, `{
		"a":[{"won_amount":0,"game_id":"gqltest-g1"},{"won_amount":0,"game_id":"gqltest-g2"}],
		"b":[{"hand_number":1,"h":{"s":"seed-c"}}]}`)
}

func TestTwoAliasesOfOneRelation(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ poker_hands(where: {game_id: {_eq: "gqltest-g1"}, hand_number: {_eq: 1}}) {
		top: players(order_by: {won_amount: desc}, limit: 1) { player_address }
		all: players { player_address }
		game_id: result { winner_count }
	} }`, nil)
	expectJSON(t, got, `{"poker_hands":[{"top":[{"player_address":"gqltest-alice"}],"all":[{"player_address":"gqltest-alice"},{"player_address":"gqltest-bob"}],"game_id":{"winner_count":1}}]}`)
}

func TestInAndArrayContains(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ hand_results(where: {game_id: {_like: "gqltest-%"}, community_cards: {_contains: ["Qh"]}}) { game_id } hand_players(where: {player_address: {_in: ["gqltest-bob", "gqltest-carol"]}, won_amount: {_nin: [0]}}, order_by: {won_amount: asc}) { won_amount } }`, nil)
	expectJSON(t, got, `{"hand_results":[{"game_id":"gqltest-g2"}],"hand_players":[{"won_amount":150},{"won_amount":500}]}`)
}

func TestAggregate(t *testing.T) {
	svc := testService(t, testDB(t))
	got := run(t, svc, `{ hand_players_aggregate(where: {game_id: {_like: "gqltest-%"}}) { aggregate { count sum { won_amount } max { won_amount ended_at } top: max { seat } avg { won_amount } } } }`, nil)
	expectJSON(t, got, `{"hand_players_aggregate":{"aggregate":{"count":6,"sum":{"won_amount":950},"max":{"won_amount":500,"ended_at":"2026-11-03T10:00:00+00:00"},"top":{"seat":2},"avg":{"won_amount":158.33333333333334}}}}`)
}

func TestLimitsAndNoMutations(t *testing.T) {
	svc := testService(t, testDB(t))
	if msg := runErr(t, svc, `{ poker_hands(limit: 5000) { id } }`); !strings.Contains(msg, "limit is at most 1000") {
		t.Fatal(msg)
	}
	four := `{ hand_players(limit: 1) { hand { players(limit: 1) { hand { players(limit: 1) { id } } } } } }`
	run(t, svc, four, nil)
	five := `{ hand_players(limit: 1) { hand { players(limit: 1) { hand { players(limit: 1) { hand { id } } } } } } }`
	if msg := runErr(t, svc, five); !strings.Contains(msg, "nest at most 4") {
		t.Fatal(msg)
	}
	many := "{ poker_hands(limit: 1) { "
	for i := 0; i < 26; i++ {
		many += "r" + string(rune('a'+i)) + ": result { id } "
	}
	many += "} }"
	if msg := runErr(t, svc, many); !strings.Contains(msg, "at most 25 relation fields") {
		t.Fatal(msg)
	}
	roots := "{ "
	for i := 0; i < 11; i++ {
		roots += "q" + string(rune('a'+i)) + ": indexing_progress { id } "
	}
	if msg := runErr(t, svc, roots+"}"); !strings.Contains(msg, "at most 10 root fields") {
		t.Fatal(msg)
	}
	if msg := runErr(t, svc, `mutation { delete_poker_hands { affected_rows } }`); !strings.Contains(strings.ToLower(msg), "mutation") {
		t.Fatal(msg)
	}
	if msg := runErr(t, svc, `{ api_keys { key_hash } }`); !strings.Contains(msg, "api_keys") {
		t.Fatal(msg)
	}
	// graphql-go has no null literal; a null arrives through a variable.
	res := svc.Do(context.Background(), `query($w: BigInt) { hand_players(where: {won_amount: {_eq: $w}}) { id } }`, map[string]interface{}{"w": nil})
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0].Message, "_is_null") {
		t.Fatalf("expected a null-filter error, got %v", res.Errors)
	}
}

func TestRunnerIsReadOnlyAndTimesOut(t *testing.T) {
	db := testDB(t)
	_, err := DBRunner(db, 5*time.Second)(context.Background(), `INSERT INTO player_stats (player_address) VALUES ('gqltest-x') RETURNING to_jsonb(player_address)`, nil)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only error, got %v", err)
	}
	_, err = DBRunner(db, 50*time.Millisecond)(context.Background(), `SELECT to_jsonb(pg_sleep(1)::text)`, nil)
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
