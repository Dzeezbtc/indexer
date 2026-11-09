// Package gql serves a read-only GraphQL API over the indexer's tables
// (block52/indexer#16).
//
// Each root field compiles to a single parameterised SQL statement that
// returns JSON, run in a read-only transaction with a statement timeout. Only
// the tables listed in Specs are reachable, and there are no mutations.
package gql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// RelKind says whether a relation returns one row or a list.
type RelKind int

const (
	// ObjectRel returns at most one row (nullable).
	ObjectRel RelKind = iota
	// ArrayRel returns a list and takes where/order_by/limit/offset.
	ArrayRel
)

// Relation joins a table to another on one or more column pairs. The tables
// have unique keys but no foreign keys, so relations are declared here.
type Relation struct {
	Name   string
	Kind   RelKind
	Target string
	On     [][2]string // {local column, target column}
}

// TableSpec is a table the API exposes.
type TableSpec struct {
	Name        string
	Description string
	// Key is a unique key, appended to every ORDER BY so paging is stable.
	Key       []string
	Relations []Relation
}

var handKey = [][2]string{{"game_id", "game_id"}, {"hand_number", "hand_number"}}
var gameKey = [][2]string{{"game_id", "game_id"}}
var playerKey = [][2]string{{"player_address", "player_address"}}

// Specs lists every table the API can reach. A table that isn't here can't be
// queried, whatever the database role can see (api_keys is deliberately
// absent). Tables that don't exist yet (hands_meta and games arrive with
// block52/indexer#15) are skipped at startup, along with relations to them.
var Specs = []TableSpec{
	{
		Name:        "poker_hands",
		Description: "One row per dealt hand: the block it was dealt in and its public shuffle seed (the block hash).",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "result", Kind: ObjectRel, Target: "hand_results", On: handKey},
			{Name: "players", Kind: ArrayRel, Target: "hand_players", On: handKey},
			{Name: "cards", Kind: ArrayRel, Target: "revealed_cards", On: handKey},
			{Name: "actions", Kind: ArrayRel, Target: "player_actions", On: handKey},
			{Name: "meta", Kind: ObjectRel, Target: "hands_meta", On: handKey},
			{Name: "game", Kind: ObjectRel, Target: "games", On: gameKey},
		},
	},
	{
		Name:        "hand_results",
		Description: "How each hand ended: the board and the number of winners.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "hand", Kind: ObjectRel, Target: "poker_hands", On: handKey},
		},
	},
	{
		Name:        "revealed_cards",
		Description: "Cards shown at the table: the board and any hole cards revealed at showdown. Unshown cards are never stored.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "hand", Kind: ObjectRel, Target: "poker_hands", On: handKey},
		},
	},
	{
		Name:        "hand_players",
		Description: "Who played each hand, their seat, their status at the end and what they won.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "hand", Kind: ObjectRel, Target: "poker_hands", On: handKey},
			{Name: "player", Kind: ObjectRel, Target: "player_stats", On: playerKey},
			{Name: "meta", Kind: ObjectRel, Target: "hands_meta", On: handKey},
			{Name: "game", Kind: ObjectRel, Target: "games", On: gameKey},
		},
	},
	{
		Name:        "player_actions",
		Description: "Every action a player took (bet, call, raise, fold, check, join, leave and so on).",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "hand", Kind: ObjectRel, Target: "poker_hands", On: handKey},
			{Name: "player", Kind: ObjectRel, Target: "player_stats", On: playerKey},
			{Name: "game", Kind: ObjectRel, Target: "games", On: gameKey},
		},
	},
	{
		Name:        "player_sessions",
		Description: "A player's time at a table, from join to leave, with buy-in and cash-out.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "player", Kind: ObjectRel, Target: "player_stats", On: playerKey},
			{Name: "game", Kind: ObjectRel, Target: "games", On: gameKey},
		},
	},
	{
		Name:        "player_stats",
		Description: "Per-player totals and play-style stats, refreshed periodically from the raw tables.",
		Key:         []string{"player_address"},
		Relations: []Relation{
			{Name: "hands", Kind: ArrayRel, Target: "hand_players", On: playerKey},
			{Name: "sessions", Kind: ArrayRel, Target: "player_sessions", On: playerKey},
			{Name: "actions", Kind: ArrayRel, Target: "player_actions", On: playerKey},
			{Name: "monthly", Kind: ArrayRel, Target: "player_monthly_stats", On: playerKey},
			{Name: "tier_history", Kind: ArrayRel, Target: "vip_tier_history", On: playerKey},
		},
	},
	{
		Name:        "player_monthly_stats",
		Description: "Per-player totals by calendar month.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "player", Kind: ObjectRel, Target: "player_stats", On: playerKey},
		},
	},
	{
		Name:        "vip_tier_history",
		Description: "Changes to a player's VIP tier.",
		Key:         []string{"id"},
		Relations: []Relation{
			{Name: "player", Kind: ObjectRel, Target: "player_stats", On: playerKey},
		},
	},
	{
		Name:        "card_distribution_stats",
		Description: "How often each card has been seen, for the randomness checks.",
		Key:         []string{"id"},
	},
	{
		Name:        "indexing_progress",
		Description: "How far the indexer has read the chain.",
		Key:         []string{"id"},
	},
	{
		Name:        "hands_meta",
		Description: "Per-hand totals: rake, pot, blinds, players dealt and whether the hand saw a flop.",
		Key:         []string{"game_id", "hand_number"},
		Relations: []Relation{
			{Name: "hand", Kind: ObjectRel, Target: "poker_hands", On: handKey},
			{Name: "players", Kind: ArrayRel, Target: "hand_players", On: handKey},
			{Name: "game", Kind: ObjectRel, Target: "games", On: gameKey},
		},
	},
	{
		Name:        "games",
		Description: "Each table's details: creator, format, variant, limit, name, seats and rake owner.",
		Key:         []string{"game_id"},
		Relations: []Relation{
			{Name: "hands", Kind: ArrayRel, Target: "poker_hands", On: gameKey},
			{Name: "hand_players", Kind: ArrayRel, Target: "hand_players", On: gameKey},
			{Name: "sessions", Kind: ArrayRel, Target: "player_sessions", On: gameKey},
		},
	},
}

// Column is a column read from the database.
type Column struct {
	Name        string
	UDT         string // Postgres type name, e.g. int8, text, _text
	Description string
	scalar      scalarKind
}

// Table is a spec joined with the columns the database actually has.
type Table struct {
	Spec      TableSpec
	Columns   []Column
	Relations []Relation // only those whose target and columns exist
	byName    map[string]*Column
}

// Catalog is every exposed table, by name.
type Catalog struct {
	Tables map[string]*Table
	Order  []string // table names, sorted, for deterministic schema output
}

// Column returns the named column, or nil.
func (t *Table) Column(name string) *Column { return t.byName[name] }

// Relation returns the named relation, or nil.
func (t *Table) Relation(name string) *Relation {
	for i := range t.Relations {
		if t.Relations[i].Name == name {
			return &t.Relations[i]
		}
	}
	return nil
}

// LoadCatalog reads the columns of every spec'd table from the database.
func LoadCatalog(ctx context.Context, db *sql.DB, specs []TableSpec) (*Catalog, error) {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	rows, err := db.QueryContext(ctx, `
		SELECT c.table_name, c.column_name, c.udt_name,
		       COALESCE(col_description(format('%I.%I', c.table_schema, c.table_name)::regclass, c.ordinal_position), '')
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.table_name = ANY($1)
		ORDER BY c.table_name, c.ordinal_position`, pqStringArray(names))
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}
	defer rows.Close()
	cols := map[string][]Column{}
	for rows.Next() {
		var table string
		var c Column
		if err := rows.Scan(&table, &c.Name, &c.UDT, &c.Description); err != nil {
			return nil, err
		}
		cols[table] = append(cols[table], c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return BuildCatalog(specs, cols), nil
}

// BuildCatalog joins specs with columns (table → columns). Columns of a type
// the API can't represent are left out; so are tables with no columns, and
// relations whose target table or join columns are missing.
func BuildCatalog(specs []TableSpec, cols map[string][]Column) *Catalog {
	cat := &Catalog{Tables: map[string]*Table{}}
	for _, s := range specs {
		cs := cols[s.Name]
		if len(cs) == 0 {
			continue
		}
		t := &Table{Spec: s, byName: map[string]*Column{}}
		for _, c := range cs {
			k, ok := scalarFor(c.UDT)
			if !ok {
				continue
			}
			c.scalar = k
			t.Columns = append(t.Columns, c)
		}
		for i := range t.Columns {
			t.byName[t.Columns[i].Name] = &t.Columns[i]
		}
		cat.Tables[s.Name] = t
	}
	for _, t := range cat.Tables {
		// A key column that's missing would break ordering; fall back to none.
		key := t.Spec.Key[:0:0]
		for _, k := range t.Spec.Key {
			if t.Column(k) != nil {
				key = append(key, k)
			}
		}
		t.Spec.Key = key
		for _, r := range t.Spec.Relations {
			target := cat.Tables[r.Target]
			if target == nil || !joinable(t, target, r.On) {
				continue
			}
			t.Relations = append(t.Relations, r)
		}
	}
	for name := range cat.Tables {
		cat.Order = append(cat.Order, name)
	}
	sort.Strings(cat.Order)
	return cat
}

func joinable(from, to *Table, on [][2]string) bool {
	for _, p := range on {
		if from.Column(p[0]) == nil || to.Column(p[1]) == nil {
			return false
		}
	}
	return len(on) > 0
}
