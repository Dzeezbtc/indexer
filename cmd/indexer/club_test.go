package main

import (
	"encoding/json"
	"testing"
)

func strp(s string) *string { return &s }

// A real fold-out from a local chain: SB posts 10000, BB posts 20000, SB folds; BB wins 30000.
func foldOut() *GameState {
	g := &GameState{
		Round:      "end",
		HandNumber: 316,
		PreviousActions: []HandAction{
			{PlayerID: "sb", Seat: 1, Action: "post-small-blind", Amount: "10000", Round: "ante"},
			{PlayerID: "bb", Seat: 2, Action: "post-big-blind", Amount: "20000", Round: "ante"},
			{PlayerID: "sb", Seat: 1, Action: "deal", Amount: "", Round: "ante"},
			{PlayerID: "sb", Seat: 1, Action: "fold", Amount: "", Round: "preflop"},
		},
		Winners: []Winner{{Address: "bb", Amount: "30000"}},
		Players: []Player{{Address: "sb", Seat: 1}, {Address: "bb", Seat: 2}},
	}
	g.GameOptions.SmallBlind = strp("10000")
	g.GameOptions.BigBlind = strp("20000")
	return g
}

func TestEconomicsFoldOut(t *testing.T) {
	e := economics(foldOut())
	if e.Contributed["sb"] != 10000 || e.Contributed["bb"] != 20000 {
		t.Fatalf("contributed = %v", e.Contributed)
	}
	if e.Pot != 30000 || e.PlayersDealt != 2 || e.SawFlop || e.Rake != 0 || e.BigBlind != 20000 {
		t.Fatalf("economics = %+v", e)
	}
	players := withEconomics(handPlayers(foldOut()), e)
	net := map[string]int64{}
	for _, p := range players {
		net[p.Address] = p.Net
	}
	if net["sb"] != -10000 || net["bb"] != 10000 {
		t.Fatalf("net = %v (want sb -10000, bb +10000)", net)
	}
}

func TestRakeSharesAddUpExactly(t *testing.T) {
	won := map[string]int64{"a": 1, "b": 1, "c": 1}
	shares := rakeShares(100, won)
	if shares["a"]+shares["b"]+shares["c"] != 100 {
		t.Fatalf("shares %v don't add up to 100", shares)
	}
	if got := rakeShares(0, won); len(got) != 0 {
		t.Fatalf("no rake should give no shares, got %v", got)
	}
	if got := rakeShares(100, map[string]int64{}); len(got) != 0 {
		t.Fatalf("no winners should give no shares, got %v", got)
	}
}

func TestOnlyWinnersPayRake(t *testing.T) {
	shares := rakeShares(5000, map[string]int64{"winner": 115000, "loser": 0})
	if shares["winner"] != 5000 {
		t.Fatalf("winner pays all of it, got %v", shares)
	}
	if _, paid := shares["loser"]; paid {
		t.Fatalf("a loser shouldn't pay rake, got %v", shares)
	}
}

func TestSplitPotSharesRakeByWhatEachWon(t *testing.T) {
	shares := rakeShares(1000, map[string]int64{"a": 300, "b": 100})
	if shares["a"] != 750 || shares["b"] != 250 {
		t.Fatalf("got %v", shares)
	}
}

func TestRakedShowdownNetsOut(t *testing.T) {
	g := &GameState{
		Round: "end",
		PreviousActions: []HandAction{
			{PlayerID: "a", Action: "post-small-blind", Amount: "10000", Round: "ante"},
			{PlayerID: "b", Action: "post-big-blind", Amount: "20000", Round: "ante"},
			{PlayerID: "a", Action: "call", Amount: "10000", Round: "preflop"},
			{PlayerID: "a", Action: "bet", Amount: "40000", Round: "flop"},
			{PlayerID: "b", Action: "call", Amount: "40000", Round: "flop"},
		},
		CommunityCards: []string{"AS", "KD", "2C"},
		RakeCollected:  "5000",
		Winners:        []Winner{{Address: "a", Amount: "115000"}},
		Players:        []Player{{Address: "a"}, {Address: "b"}},
	}
	e := economics(g)
	if !e.SawFlop || e.Pot != 120000 || e.Rake != 5000 {
		t.Fatalf("economics = %+v", e)
	}
	var netSum, rakeSum, loserRake int64
	for _, p := range withEconomics(handPlayers(g), e) {
		netSum += p.Net
		rakeSum += p.RakeShare
		if p.Address == "b" {
			loserRake = p.RakeShare
		}
	}
	if loserRake != 0 {
		t.Fatalf("the loser paid rake: %d", loserRake)
	}
	// Players' combined result is minus the rake.
	if netSum != -5000 || rakeSum != 5000 {
		t.Fatalf("net sum %d (want -5000), rake shares %d (want 5000)", netSum, rakeSum)
	}
}

func TestParseGameResponse(t *testing.T) {
	inner, _ := json.Marshal(map[string]interface{}{
		"gameId": "0xabc", "creator": "b521x", "format": "cash", "variant": "texas-holdem", "name": "callfix",
		"gameState": map[string]interface{}{"gameOptions": map[string]interface{}{"maxPlayers": 4, "bettingLimit": "no-limit", "rake": map[string]interface{}{"owner": "b521treasury"}}},
	})
	body, _ := json.Marshal(map[string]string{"game": string(inner)})
	g, err := parseGameResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if g.Format != "cash" || g.Variant != "texas-holdem" || g.MaxPlayers != 4 || g.RakeOwner != "b521treasury" || g.Betting != "no-limit" {
		t.Fatalf("game = %+v", g)
	}
}
