package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Club reporting (Dzeezbtc/block52-club): each player's money in every
// finished hand, so a club service can report results and rake by player,
// day, game and stake.
//
// From the masked hand-end state:
//   - contributed: the sum of the player's action amounts this hand (blinds,
//     antes, straddles, bets, calls, raises). Amounts are what the player
//     put in with that action.
//   - won: the sum of their winner amounts (which include their own chips
//     coming back, e.g. an uncalled bet).
//   - net = won - contributed.
//   - rake_share: the hand's rakeCollected split by contribution, the usual
//     "contributed rake" method clubs use for rakeback.

// handEconomics is one finished hand's money.
type handEconomics struct {
	Rake         int64
	Pot          int64
	SmallBlind   int64
	BigBlind     int64
	PlayersDealt int
	SawFlop      bool
	Contributed  map[string]int64
}

func parseAmount(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func optAmount(s *string) int64 {
	if s == nil {
		return 0
	}
	return parseAmount(*s)
}

// economics reads a finished hand's money from its hand-end state.
func economics(snap *GameState) handEconomics {
	e := handEconomics{Contributed: map[string]int64{}}
	if snap == nil {
		return e
	}
	e.Rake = parseAmount(snap.RakeCollected)
	e.SmallBlind = optAmount(snap.GameOptions.SmallBlind)
	e.BigBlind = optAmount(snap.GameOptions.BigBlind)
	for _, a := range snap.PreviousActions {
		if nonHandActions[a.Action] || a.PlayerID == "" {
			continue
		}
		if _, ok := e.Contributed[a.PlayerID]; !ok {
			e.Contributed[a.PlayerID] = 0
		}
		e.Contributed[a.PlayerID] += parseAmount(a.Amount)
		if a.Round != "" && a.Round != "ante" && a.Round != "preflop" {
			e.SawFlop = true
		}
	}
	if len(snap.CommunityCards) >= 3 {
		e.SawFlop = true
	}
	e.PlayersDealt = len(e.Contributed)
	for _, c := range e.Contributed {
		e.Pot += c
	}
	return e
}

// rakeShares splits the rake by contribution. Remainders from rounding go to
// the biggest contributor, so the shares always add up to the rake exactly.
func rakeShares(rake int64, contributed map[string]int64) map[string]int64 {
	out := map[string]int64{}
	var total int64
	for _, c := range contributed {
		total += c
	}
	if rake <= 0 || total <= 0 {
		return out
	}
	var given int64
	biggest, biggestAmt := "", int64(-1)
	for addr, c := range contributed {
		share := rake * c / total
		out[addr] = share
		given += share
		if c > biggestAmt || (c == biggestAmt && addr < biggest) {
			biggest, biggestAmt = addr, c
		}
	}
	out[biggest] += rake - given
	return out
}

// withEconomics fills each hand player's contribution, rake share and net.
func withEconomics(players []handPlayer, e handEconomics) []handPlayer {
	shares := rakeShares(e.Rake, e.Contributed)
	out := make([]handPlayer, len(players))
	for i, p := range players {
		p.Contributed = e.Contributed[p.Address]
		p.RakeShare = shares[p.Address]
		p.Net = p.Won - p.Contributed
		out[i] = p
	}
	return out
}

// recordHandMeta stores a finished hand's totals.
func (idx *Indexer) recordHandMeta(gameID string, handNumber int, e handEconomics, blockHeight int64, blockTime time.Time) {
	var endedAt interface{}
	if !blockTime.IsZero() {
		endedAt = blockTime
	}
	if _, err := idx.db.Exec(`
		INSERT INTO hands_meta (game_id, hand_number, rake, pot, small_blind, big_blind, players_dealt, saw_flop, block_height, ended_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (game_id, hand_number) DO UPDATE SET rake = EXCLUDED.rake, pot = EXCLUDED.pot, small_blind = EXCLUDED.small_blind,
			big_blind = EXCLUDED.big_blind, players_dealt = EXCLUDED.players_dealt, saw_flop = EXCLUDED.saw_flop,
			block_height = EXCLUDED.block_height, ended_at = EXCLUDED.ended_at
	`, gameID, handNumber, e.Rake, e.Pot, e.SmallBlind, e.BigBlind, e.PlayersDealt, e.SawFlop, blockHeight, endedAt); err != nil {
		log.Printf("Hand %s/%d: failed to record hand totals: %v", gameID, handNumber, err)
	}
}

// gameInfo is the part of a table's on-chain record reporting needs.
type gameInfo struct {
	GameID     string `json:"gameId"`
	Creator    string `json:"creator"`
	Format     string `json:"format"`
	Variant    string `json:"variant"`
	Name       string `json:"name"`
	MaxPlayers int
	RakeOwner  string
	Betting    string
}

// ensureGame records a table's format, variant and owner the first time a hand from it is seen.
func (idx *Indexer) ensureGame(gameID string) {
	if idx.knownGames == nil {
		idx.knownGames = map[string]bool{}
	}
	if idx.knownGames[gameID] {
		return
	}
	var exists bool
	if err := idx.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM games WHERE game_id = $1)`, gameID).Scan(&exists); err == nil && exists {
		idx.knownGames[gameID] = true
		return
	}
	g, err := idx.fetchGame(gameID)
	if err != nil {
		log.Printf("Game %s: couldn't read its details (%v); will retry on its next hand", gameID, err)
		return
	}
	if _, err := idx.db.Exec(`
		INSERT INTO games (game_id, creator, format, variant, betting_limit, name, max_players, rake_owner)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (game_id) DO NOTHING
	`, g.GameID, g.Creator, g.Format, g.Variant, g.Betting, g.Name, g.MaxPlayers, g.RakeOwner); err != nil {
		log.Printf("Game %s: failed to record details: %v", gameID, err)
		return
	}
	idx.knownGames[gameID] = true
}

func (idx *Indexer) fetchGame(gameID string) (*gameInfo, error) {
	resp, err := http.Get(fmt.Sprintf("%s/block52/pokerchain/poker/v1/game/%s", idx.config.NodeAPI, gameID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseGameResponse(body)
}

// parseGameResponse reads /game/{id}: `game` is a JSON-encoded string.
func parseGameResponse(body []byte) (*gameInfo, error) {
	var wrap struct {
		Game string `json:"game"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	var g struct {
		gameInfo
		GameState struct {
			GameOptions struct {
				MaxPlayers   int    `json:"maxPlayers"`
				BettingLimit string `json:"bettingLimit"`
				Rake         *struct {
					Owner string `json:"owner"`
				} `json:"rake"`
			} `json:"gameOptions"`
		} `json:"gameState"`
	}
	if err := json.Unmarshal([]byte(wrap.Game), &g); err != nil {
		return nil, err
	}
	info := g.gameInfo
	info.MaxPlayers = g.GameState.GameOptions.MaxPlayers
	info.Betting = g.GameState.GameOptions.BettingLimit
	if g.GameState.GameOptions.Rake != nil {
		info.RakeOwner = g.GameState.GameOptions.Rake.Owner
	}
	if info.GameID == "" {
		return nil, fmt.Errorf("no game in response")
	}
	return &info, nil
}
