package main

import (
	"errors"
	"strconv"
	"strings"
)

// Card privacy rules for the indexer:
//   - Decks are never read or stored. (Older chain events carry a `deck`
//     attribute on hand_started; it is ignored.)
//   - The only hole cards recorded are those of players whose status was
//     "showing" in the chain's masked public game state when the hand ended.
//     The `revealed_hole_cards` event attribute is NOT trusted: on older blocks
//     it lists every seat's cards, folded hands included.

// statusShowing is the chain's PlayerStatus for a player who showed their cards.
const statusShowing = "showing"

// errStatePruned means the node no longer keeps state at the requested height.
var errStatePruned = errors.New("state at height is pruned")

// normalizeCard converts a chain card code ("AH", "TH", "10h", "[AS]") into the
// canonical form used by card_distribution_stats: rank upper + suit lower
// ("Ah", "Th"). It reports false for masked ("X") or malformed codes.
func normalizeCard(raw string) (string, bool) {
	c := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "[]"))
	if len(c) < 2 || len(c) > 3 {
		return "", false
	}
	rank := strings.ToUpper(c[:len(c)-1])
	suit := strings.ToLower(c[len(c)-1:])
	if rank == "10" {
		rank = "T"
	}
	if len(rank) != 1 || !strings.Contains("23456789TJQKA", rank) || !strings.Contains("hdcs", suit) {
		return "", false
	}
	return rank + suit, true
}

// normalizeCards normalizes a list, dropping masked/malformed entries.
func normalizeCards(raw []string) []string {
	var out []string
	for _, r := range raw {
		if c, ok := normalizeCard(r); ok {
			out = append(out, c)
		}
	}
	return out
}

// splitCards parses a comma-separated card list from an event attribute.
func splitCards(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return normalizeCards(strings.Split(s, ","))
}

// shownHoleCards returns the hole cards of players who showed (status
// "showing"). Folded, active, mucked or masked hands contribute nothing.
func shownHoleCards(players []Player) []string {
	var out []string
	for _, p := range players {
		if p.Status != statusShowing {
			continue
		}
		out = append(out, normalizeCards(p.HoleCards)...)
	}
	return out
}

// isPrunedError reports whether a node error means the state at that height is
// no longer kept (Cosmos SDK: "version does not exist ... pruned").
func isPrunedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "version does not exist") ||
		strings.Contains(msg, "pruned") ||
		strings.Contains(msg, "failed to load state at height")
}

// handEndSnapshot finds the public game state of hand `handNumber` as it ended,
// given the block where the end was observed. That block's state normally holds
// the final state; if the next hand started in the same block, the block before
// has it. It returns errStatePruned when the node no longer keeps the state, and
// (nil, nil) when neither block shows that hand finished.
func handEndSnapshot(fetch func(height int64) (*GameState, error), handNumber int, height int64) (*GameState, error) {
	heights := []int64{height}
	if height > 1 {
		heights = append(heights, height-1)
	}
	for _, h := range heights {
		st, err := fetch(h)
		if err != nil {
			if isPrunedError(err) {
				return nil, errStatePruned
			}
			return nil, err
		}
		if st != nil && st.HandNumber == handNumber && (st.Round == "showdown" || st.Round == "end") {
			return st, nil
		}
	}
	return nil, nil
}

// handPlayer is one wallet's part in a finished hand (ui#721 "My Hand History").
type handPlayer struct {
	Address string
	Seat    int
	Status  string // status at the hand's end ("" if the player has since left)
	Won     int64  // sum of this player's winner amounts (chips or micro-USDC)
	// Club reporting (club.go): what they put in, their share of the rake, and won - contributed.
	Contributed int64
	RakeShare   int64
	Net         int64
}

// nonHandActions are recorded in previousActions but don't put a player in the hand.
var nonHandActions = map[string]bool{
	"join": true, "leave": true, "new-hand": true, "deal": true,
	"sit-in": true, "sit-out": true, "sit-in-and-wait": true, "top-up": true,
	"claim-winnings": true,
}

// handPlayers lists who played a finished hand, from the masked public
// hand-end state. The engine resets previousActions at every new hand, and
// every player dealt in posts or acts, so the hand's own actions name its
// players with their seats; busted or sitting-out seats never appear. Winners
// with no recorded action (shouldn't happen) are still included.
func handPlayers(snap *GameState) []handPlayer {
	if snap == nil {
		return nil
	}
	won := map[string]int64{}
	for _, w := range snap.Winners {
		amount, err := strconv.ParseInt(w.Amount, 10, 64)
		if err == nil && w.Address != "" {
			won[w.Address] += amount
		}
	}
	status := map[string]string{}
	seatOf := map[string]int{}
	for _, p := range snap.Players {
		status[p.Address] = p.Status
		seatOf[p.Address] = p.Seat
	}

	var out []handPlayer
	seen := map[string]bool{}
	add := func(address string, seat int) {
		if address == "" || seen[address] {
			return
		}
		seen[address] = true
		out = append(out, handPlayer{Address: address, Seat: seat, Status: status[address], Won: won[address]})
	}
	for _, a := range snap.PreviousActions {
		if !nonHandActions[a.Action] {
			add(a.PlayerID, a.Seat)
		}
	}
	for _, w := range snap.Winners {
		add(w.Address, seatOf[w.Address])
	}
	return out
}
