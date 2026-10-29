// Standalone poker hand indexer for backfilling historical blocks
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
)

// Config holds indexer configuration
type Config struct {
	NodeRPC    string
	NodeAPI    string // REST API endpoint for game state queries
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	StartBlock int64
	EndBlock   int64
	BatchSize  int
	// BlockDelay pauses after each chain read (block_results, historical state
	// queries) so a backfill can't starve the node it reads from.
	BlockDelay time.Duration
}

// BlockResult from RPC
type BlockResult struct {
	Result struct {
		Block struct {
			Header struct {
				Height  string    `json:"height"`
				AppHash string    `json:"app_hash"`
				Time    time.Time `json:"time"`
			} `json:"header"`
			Data struct {
				Txs []string `json:"txs"`
			} `json:"data"`
		} `json:"block"`
	} `json:"result"`
}

// BlockResultsResponse from RPC
type BlockResultsResponse struct {
	Result struct {
		Height     string `json:"height"`
		TxsResults []struct {
			Events []Event `json:"events"`
		} `json:"txs_results"`
		BeginBlockEvents []Event `json:"begin_block_events"`
		EndBlockEvents   []Event `json:"end_block_events"`
	} `json:"result"`
}

// Event from Cosmos
type Event struct {
	Type       string `json:"type"`
	Attributes []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Index bool   `json:"index"`
	} `json:"attributes"`
}

// StatusResult from RPC
type StatusResult struct {
	Result struct {
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
		} `json:"sync_info"`
	} `json:"result"`
}

// GameStateResponse from the REST API: `game_state` is a JSON-encoded string
// (a TexasHoldemStateDTO, or a GameStateResponseDTO with it under `gameState`).
type GameStateResponse struct {
	GameState string `json:"game_state"`
}

// GameState represents the poker game state
type GameState struct {
	Type           string   `json:"type"`
	Address        string   `json:"address"`
	Dealer         int      `json:"dealer"`
	Players        []Player `json:"players"`
	CommunityCards []string `json:"communityCards"`
	Round          string   `json:"round"`
	HandNumber     int      `json:"handNumber"`
	Winners        []Winner `json:"winners"`
	// This hand's actions (the engine resets them at every new hand).
	PreviousActions []HandAction `json:"previousActions"`
	// Rake taken from this hand's pot (micro-units), for club reporting.
	RakeCollected string `json:"rakeCollected"`
	GameOptions   struct {
		SmallBlind *string `json:"smallBlind"`
		BigBlind   *string `json:"bigBlind"`
	} `json:"gameOptions"`
}

// HandAction is one recorded action in previousActions.
type HandAction struct {
	PlayerID string `json:"playerId"`
	Seat     int    `json:"seat"`
	Action   string `json:"action"`
	// What the player put in with this action (chips or micro-USDC).
	Amount string `json:"amount"`
	Round  string `json:"round"`
}

// Player in game state
type Player struct {
	Address   string   `json:"address"`
	Seat      int      `json:"seat"`
	Stack     string   `json:"stack"`
	HoleCards []string `json:"holeCards"`
	Status    string   `json:"status"`
}

// Winner info
type Winner struct {
	Address string   `json:"address"`
	Amount  string   `json:"amount"`
	Cards   []string `json:"cards"`
}

func main() {
	// Parse flags
	nodeRPC := flag.String("node", "", "Node RPC URL (e.g., http://localhost:26657)")
	nodeAPI := flag.String("api", "", "Node REST API URL (e.g., http://localhost:1317) - derived from RPC if not set")
	dbHost := flag.String("db-host", "localhost", "PostgreSQL host")
	dbPort := flag.Int("db-port", 5432, "PostgreSQL port")
	dbUser := flag.String("db-user", "poker", "PostgreSQL user")
	dbPass := flag.String("db-pass", "poker_indexer_dev", "PostgreSQL password")
	dbName := flag.String("db-name", "poker_hands", "PostgreSQL database")
	startBlock := flag.Int64("start", 1, "Start block height")
	endBlock := flag.Int64("end", 0, "End block height (0 = latest)")
	batchSize := flag.Int("batch", 100, "Blocks per batch")
	continuous := flag.Bool("continuous", false, "Run continuously, resuming from last indexed block")
	loopDelay := flag.Int("loop-delay", 60, "Seconds to wait between indexing runs in continuous mode")
	blockDelayMs := flag.Int("block-delay-ms", 0, "Milliseconds to pause after each chain read (0 = none). Use ~25 when backfilling from a validator's local RPC so it keeps up with consensus")

	flag.Parse()

	if *nodeRPC == "" {
		*nodeRPC = os.Getenv("NODE_RPC")
		if *nodeRPC == "" {
			log.Fatal("Node RPC URL required. Use -node flag or NODE_RPC env var")
		}
	}

	// Derive API URL from RPC if not provided
	if *nodeAPI == "" {
		*nodeAPI = os.Getenv("NODE_API")
		if *nodeAPI == "" {
			// Try to derive from RPC URL (replace port or path)
			*nodeAPI = strings.Replace(*nodeRPC, ":26657", ":1317", 1)
			*nodeAPI = strings.Replace(*nodeAPI, "/rpc", "/api", 1)
		}
	}

	config := Config{
		NodeRPC:    strings.TrimSuffix(*nodeRPC, "/"),
		NodeAPI:    strings.TrimSuffix(*nodeAPI, "/"),
		DBHost:     *dbHost,
		DBPort:     *dbPort,
		DBUser:     *dbUser,
		DBPassword: *dbPass,
		DBName:     *dbName,
		StartBlock: *startBlock,
		EndBlock:   *endBlock,
		BatchSize:  *batchSize,
		BlockDelay: time.Duration(*blockDelayMs) * time.Millisecond,
	}

	// Connect to database
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost, config.DBPort, config.DBUser, config.DBPassword, config.DBName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("Connected to PostgreSQL")

	// Get latest block if end not specified
	if config.EndBlock == 0 {
		latest, err := getLatestBlockHeight(config.NodeRPC)
		if err != nil {
			log.Fatalf("Failed to get latest block: %v", err)
		}
		config.EndBlock = latest
		log.Printf("Latest block: %d", latest)
	}

	log.Printf("Using RPC: %s", config.NodeRPC)
	log.Printf("Using API: %s", config.NodeAPI)
	log.Printf("Chain read delay: %s", config.BlockDelay)

	if *continuous {
		log.Println("Running in continuous mode...")
		runContinuous(db, config, *loopDelay)
	} else {
		// Run indexer once
		indexer := NewIndexer(db, config)
		if err := indexer.Run(context.Background()); err != nil {
			log.Fatalf("Indexer failed: %v", err)
		}
		log.Println("Indexing complete!")
	}
}

// runContinuous runs the indexer in a continuous loop
func runContinuous(db *sql.DB, baseConfig Config, loopDelay int) {
	for {
		log.Println("========================================")
		log.Printf("Starting indexing run at %s", time.Now().Format(time.RFC3339))
		log.Println("========================================")

		// Get the last scanned block from indexing_progress (tracks all scanned blocks, not just those with hands)
		var lastBlock int64
		err := db.QueryRow("SELECT COALESCE(last_scanned_block, 0) FROM indexing_progress WHERE id = 1").Scan(&lastBlock)
		if err != nil {
			// Fall back to poker_hands if indexing_progress is empty
			err = db.QueryRow("SELECT COALESCE(MAX(block_height), 0) FROM poker_hands").Scan(&lastBlock)
			if err != nil {
				log.Printf("Warning: failed to get last indexed block: %v", err)
				lastBlock = 0
			}
		}

		// Determine start block
		startBlock := lastBlock + 1
		if lastBlock == 0 {
			startBlock = baseConfig.StartBlock
			log.Printf("No blocks indexed yet, starting from block %d", startBlock)
		} else {
			log.Printf("Continuing from block %d (last indexed: %d)", startBlock, lastBlock)
		}

		// Get latest block height
		latestBlock, err := getLatestBlockHeight(baseConfig.NodeRPC)
		if err != nil {
			log.Printf("Error: failed to get latest block height: %v", err)
			log.Printf("Retrying in %d seconds...", loopDelay)
			time.Sleep(time.Duration(loopDelay) * time.Second)
			continue
		}

		// Check if we're caught up
		if startBlock > latestBlock {
			log.Printf("Already caught up! Current block: %d, Latest block: %d", lastBlock, latestBlock)
			log.Printf("Waiting %d seconds for new blocks...", loopDelay)
			time.Sleep(time.Duration(loopDelay) * time.Second)
			continue
		}

		// Create config for this run
		config := baseConfig
		config.StartBlock = startBlock
		config.EndBlock = latestBlock

		// Run indexer
		indexer := NewIndexer(db, config)
		if err := indexer.Run(context.Background()); err != nil {
			log.Printf("Error: indexing failed: %v", err)
			log.Printf("Retrying in %d seconds...", loopDelay)
			time.Sleep(time.Duration(loopDelay) * time.Second)
			continue
		}

		log.Printf("Indexing run completed at %s", time.Now().Format(time.RFC3339))

		// Refresh the aggregate player_stats + VIP tiers so the /players
		// directory and profile endpoints stay current. Non-fatal: a failure
		// here must not stop indexing.
		refreshPlayerStats(db)

		log.Printf("Waiting %d seconds before next run...", loopDelay)
		time.Sleep(time.Duration(loopDelay) * time.Second)
	}
}

// refreshPlayerStats rebuilds the aggregate player_stats table and recomputes
// VIP tiers from the raw player_actions/player_sessions the indexer just wrote.
func refreshPlayerStats(db *sql.DB) {
	if _, err := db.Exec("SELECT refresh_all_player_stats()"); err != nil {
		log.Printf("Warning: refresh_all_player_stats failed: %v", err)
		return
	}
	if _, err := db.Exec("SELECT update_vip_tiers()"); err != nil {
		log.Printf("Warning: update_vip_tiers failed: %v", err)
		return
	}
	log.Println("Player stats + VIP tiers refreshed")
}

// Indexer handles block indexing
type Indexer struct {
	db           *sql.DB
	config       Config
	client       *http.Client
	seenNewHands map[string]bool // track game_id to avoid duplicate queries
	knownGames   map[string]bool // tables whose details are already in `games` (club.go)
}

// NewIndexer creates a new indexer
func NewIndexer(db *sql.DB, config Config) *Indexer {
	return &Indexer{
		db:           db,
		config:       config,
		client:       &http.Client{Timeout: 30 * time.Second},
		seenNewHands: make(map[string]bool),
	}
}

// Run starts the indexing process
func (idx *Indexer) Run(ctx context.Context) error {
	totalBlocks := idx.config.EndBlock - idx.config.StartBlock + 1
	log.Printf("Indexing blocks %d to %d (%d total)", idx.config.StartBlock, idx.config.EndBlock, totalBlocks)

	processed := int64(0)
	actionsFound := 0
	newHandsFound := 0
	showdownsFound := 0
	startTime := time.Now()

	for height := idx.config.StartBlock; height <= idx.config.EndBlock; height++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		blockInfo, events, err := idx.fetchBlockWithEvents(height)
		if err != nil {
			log.Printf("Warning: failed to fetch block %d: %v", height, err)
			continue
		}

		// Update progress tracking after each block
		idx.updateProgress(height)

		for _, event := range events {
			// Handle new event types (v0.1.33+)
			if event.Type == "hand_started" || event.Type == "hand_completed" {
				if err := idx.processNewEvent(event, height, blockInfo.Time, ""); err != nil {
					log.Printf("Warning: failed to process event at block %d: %v", height, err)
				} else {
					if event.Type == "hand_started" {
						newHandsFound++
					} else {
						showdownsFound++
					}
				}
				continue
			}

			// Handle legacy action_performed events
			if event.Type == "action_performed" {
				actionsFound++
				attrs := idx.parseEventAttrs(event)
				action := attrs["action"]
				gameID := attrs["game_id"]
				player := attrs["player"]

				// Record player action for stats
				if player != "" && gameID != "" {
					idx.recordPlayerAction(player, gameID, action, attrs["amount"], height)
				}

				// New hand started - record the hand (never a deck)
				if action == "new-hand" {
					newHandsFound++
					if err := idx.handleNewHandAction(attrs, height, blockInfo.AppHash); err != nil {
						log.Printf("Warning: failed to handle new-hand at block %d: %v", height, err)
					}
				}

				// Check for showdown by querying game state
				if gameID != "" && (action == "show" || action == "muck" || action == "fold") {
					if err := idx.checkAndHandleShowdown(gameID, height, blockInfo.Time); err != nil {
						// Don't log every error - showdowns are rare
						if strings.Contains(err.Error(), "showdown") {
							showdownsFound++
						}
					}
				}
			}

			// Handle player join/leave events
			if event.Type == "player_joined_game" {
				attrs := idx.parseEventAttrs(event)
				idx.recordPlayerSession(attrs, height, true)
			}
			if event.Type == "player_left_game" {
				attrs := idx.parseEventAttrs(event)
				idx.recordPlayerSession(attrs, height, false)
			}
		}

		processed++
		if processed%100 == 0 {
			elapsed := time.Since(startTime)
			blocksPerSec := float64(processed) / elapsed.Seconds()
			remaining := float64(totalBlocks-processed) / blocksPerSec
			log.Printf("Progress: %d/%d (%.1f%%), actions: %d, new-hands: %d, showdowns: %d, %.1f blk/s, ETA: %s",
				processed, totalBlocks,
				float64(processed)/float64(totalBlocks)*100,
				actionsFound, newHandsFound, showdownsFound,
				blocksPerSec,
				time.Duration(remaining*float64(time.Second)).Round(time.Second))
		}
	}

	// Force a final progress update so the checkpoint is always saved at end of batch
	idx.forceUpdateProgress(idx.config.EndBlock)

	log.Printf("Finished: %d blocks, %d actions, %d new-hands, %d showdowns",
		processed, actionsFound, newHandsFound, showdownsFound)
	return nil
}

// BlockInfo contains parsed block header info
type BlockInfo struct {
	Height  int64
	AppHash string
	Time    time.Time
}

// fetchBlockWithEvents gets block info and events
func (idx *Indexer) fetchBlockWithEvents(height int64) (*BlockInfo, []Event, error) {
	// Get block results (events)
	url := fmt.Sprintf("%s/block_results?height=%d", idx.config.NodeRPC, height)
	resp, err := idx.client.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result BlockResultsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, fmt.Errorf("JSON decode failed: %w", err)
	}
	idx.throttle()

	// Get block header for app hash
	blockURL := fmt.Sprintf("%s/block?height=%d", idx.config.NodeRPC, height)
	blockResp, err := idx.client.Get(blockURL)
	if err != nil {
		return nil, nil, fmt.Errorf("block request failed: %w", err)
	}
	defer blockResp.Body.Close()

	var blockResult BlockResult
	if err := json.NewDecoder(blockResp.Body).Decode(&blockResult); err != nil {
		return nil, nil, fmt.Errorf("block JSON decode failed: %w", err)
	}

	blockInfo := &BlockInfo{
		Height:  height,
		AppHash: blockResult.Result.Block.Header.AppHash,
		Time:    blockResult.Result.Block.Header.Time,
	}

	var events []Event
	for _, txResult := range result.Result.TxsResults {
		events = append(events, txResult.Events...)
	}
	events = append(events, result.Result.BeginBlockEvents...)
	events = append(events, result.Result.EndBlockEvents...)

	return blockInfo, events, nil
}

// parseEventAttrs extracts attributes from an event
func (idx *Indexer) parseEventAttrs(event Event) map[string]string {
	attrs := make(map[string]string)
	for _, attr := range event.Attributes {
		key := decodeIfBase64(attr.Key)
		value := decodeIfBase64(attr.Value)
		attrs[key] = value
	}
	return attrs
}

// handleNewHandAction processes a new-hand action from action_performed event
// (legacy chains without hand_started events). Records the hand; never a deck.
func (idx *Indexer) handleNewHandAction(attrs map[string]string, blockHeight int64, appHash string) error {
	gameID := attrs["game_id"]
	if gameID == "" {
		return fmt.Errorf("no game_id in event")
	}

	handNumber := 0
	if gameState, err := idx.queryGameStateAt(gameID, blockHeight); err == nil {
		handNumber = gameState.HandNumber
	}

	_, err := idx.db.Exec(`
		INSERT INTO poker_hands (game_id, hand_number, block_height, deck_seed, tx_hash)
		VALUES ($1, $2, $3, $4, '')
		ON CONFLICT (game_id, hand_number) DO UPDATE SET
			deck_seed = EXCLUDED.deck_seed
	`, gameID, handNumber, blockHeight, appHash)
	return err
}

// checkAndHandleShowdown checks if the game reached showdown at this block and
// records the shown cards (legacy chains without hand_completed events).
func (idx *Indexer) checkAndHandleShowdown(gameID string, blockHeight int64, blockTime time.Time) error {
	gameState, err := idx.queryGameStateAt(gameID, blockHeight)
	if err != nil {
		return err
	}

	// Only process if at showdown with winners
	if gameState.Round != "showdown" || len(gameState.Winners) == 0 {
		return nil
	}

	if err := idx.recordHandResult(gameID, gameState.HandNumber, blockHeight, normalizeCards(gameState.CommunityCards),
		len(gameState.Winners), shownHoleCards(gameState.Players), ""); err != nil {
		return err
	}
	econ := economics(gameState)
	idx.recordHandPlayers(gameID, gameState.HandNumber, blockHeight, blockTime, withEconomics(handPlayers(gameState), econ))
	idx.recordHandMeta(gameID, gameState.HandNumber, econ, blockHeight, blockTime)
	idx.ensureGame(gameID)
	return fmt.Errorf("showdown processed") // Signal that we found one
}

// recordHandResult stores a finished hand's community cards and the hole cards
// that were shown. Cards must already be normalized.
func (idx *Indexer) recordHandResult(gameID string, handNumber int, blockHeight int64, community []string, winnerCount int, shownHole []string, txHash string) error {
	_, err := idx.db.Exec(`
		INSERT INTO hand_results (game_id, hand_number, block_height, community_cards, winner_count, tx_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (game_id, hand_number) DO UPDATE SET
			community_cards = EXCLUDED.community_cards,
			winner_count = EXCLUDED.winner_count
	`, gameID, handNumber, blockHeight, pq.Array(community), winnerCount, txHash)
	if err != nil {
		return fmt.Errorf("failed to insert hand result: %w", err)
	}

	for i, card := range community {
		idx.db.Exec(`
			INSERT INTO revealed_cards (game_id, hand_number, block_height, card, card_type, position)
			VALUES ($1, $2, $3, $4, 'community', $5)
			ON CONFLICT DO NOTHING
		`, gameID, handNumber, blockHeight, card, i)
	}
	for i, card := range shownHole {
		idx.db.Exec(`
			INSERT INTO revealed_cards (game_id, hand_number, block_height, card, card_type, position)
			VALUES ($1, $2, $3, $4, 'hole', $5)
			ON CONFLICT DO NOTHING
		`, gameID, handNumber, blockHeight, card, i)
	}
	return nil
}

// recordHandPlayers stores who played a finished hand, for per-wallet hand
// history (ui#721). Re-indexing a hand replaces its rows.
func (idx *Indexer) recordHandPlayers(gameID string, handNumber int, blockHeight int64, blockTime time.Time, players []handPlayer) {
	var endedAt interface{}
	if !blockTime.IsZero() {
		endedAt = blockTime
	}
	for _, p := range players {
		if _, err := idx.db.Exec(`
			INSERT INTO hand_players (game_id, hand_number, player_address, seat, status, won_amount, block_height, ended_at, contributed, rake_share, net)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (game_id, hand_number, player_address) DO UPDATE SET
				seat = EXCLUDED.seat, status = EXCLUDED.status, won_amount = EXCLUDED.won_amount,
				block_height = EXCLUDED.block_height, ended_at = EXCLUDED.ended_at,
				contributed = EXCLUDED.contributed, rake_share = EXCLUDED.rake_share, net = EXCLUDED.net
		`, gameID, handNumber, p.Address, p.Seat, p.Status, p.Won, blockHeight, endedAt, p.Contributed, p.RakeShare, p.Net); err != nil {
			log.Printf("Hand %s/%d: failed to record player %s: %v", gameID, handNumber, p.Address, err)
		}
	}
}

// queryGameStateAt fetches the masked public game state from the REST API, as
// of `height` (0 = latest) via the x-cosmos-block-height header. Hole cards come
// back masked except for players who showed.
func (idx *Indexer) queryGameStateAt(gameID string, height int64) (*GameState, error) {
	url := fmt.Sprintf("%s/block52/pokerchain/poker/v1/game_state_public/%s", idx.config.NodeAPI, gameID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if height > 0 {
		req.Header.Set("x-cosmos-block-height", strconv.FormatInt(height, 10))
	}

	resp, err := idx.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("game state request failed: %w", err)
	}
	defer resp.Body.Close()
	defer idx.throttle()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("game state read failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("game state HTTP %d: %s", resp.StatusCode, string(body))
	}
	return parseGameStateResponse(body)
}

// parseGameStateResponse decodes `{"game_state": "<json>"}`, where the inner
// JSON is a TexasHoldemStateDTO or a GameStateResponseDTO wrapping one.
func parseGameStateResponse(body []byte) (*GameState, error) {
	var outer GameStateResponse
	if err := json.Unmarshal(body, &outer); err != nil {
		return nil, fmt.Errorf("game state JSON decode failed: %w", err)
	}
	if outer.GameState == "" {
		return nil, fmt.Errorf("no game state in response")
	}
	var wrapped struct {
		GameState *GameState `json:"gameState"`
	}
	if err := json.Unmarshal([]byte(outer.GameState), &wrapped); err == nil && wrapped.GameState != nil {
		return wrapped.GameState, nil
	}
	var state GameState
	if err := json.Unmarshal([]byte(outer.GameState), &state); err != nil {
		return nil, fmt.Errorf("game state payload decode failed: %w", err)
	}
	return &state, nil
}

// throttle pauses for the configured chain read delay.
func (idx *Indexer) throttle() {
	if idx.config.BlockDelay > 0 {
		time.Sleep(idx.config.BlockDelay)
	}
}

// processNewEvent handles the new hand_started/hand_completed events (v0.1.33+)
func (idx *Indexer) processNewEvent(event Event, blockHeight int64, blockTime time.Time, txHash string) error {
	attrs := idx.parseEventAttrs(event)

	switch event.Type {
	case "hand_started":
		return idx.handleHandStarted(attrs, blockHeight, txHash)
	case "hand_completed":
		return idx.handleHandCompleted(attrs, blockHeight, blockTime, txHash)
	}
	return nil
}

// handleHandStarted processes hand_started events
func (idx *Indexer) handleHandStarted(attrs map[string]string, blockHeight int64, txHash string) error {
	gameID := attrs["game_id"]
	handNumber, _ := strconv.Atoi(attrs["hand_number"])
	deckSeed := attrs["deck_seed"]
	// Older chain events also carry a `deck` attribute. It is deliberately
	// ignored: decks are never read or stored.

	if h, ok := attrs["block_height"]; ok {
		if parsed, err := strconv.ParseInt(h, 10, 64); err == nil {
			blockHeight = parsed
		}
	}

	_, err := idx.db.Exec(`
		INSERT INTO poker_hands (game_id, hand_number, block_height, deck_seed, tx_hash)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (game_id, hand_number) DO UPDATE SET
			deck_seed = EXCLUDED.deck_seed
	`, gameID, handNumber, blockHeight, deckSeed, txHash)
	return err
}

// handleHandCompleted processes hand_completed events. Community cards come
// from the event; hole cards ONLY from the masked public state at the hand's
// end (players with status "showing"). The event's revealed_hole_cards is
// ignored: on older blocks it lists every seat's cards, folded ones included.
func (idx *Indexer) handleHandCompleted(attrs map[string]string, blockHeight int64, blockTime time.Time, txHash string) error {
	gameID := attrs["game_id"]
	handNumber, _ := strconv.Atoi(attrs["hand_number"])
	winnerCount, _ := strconv.Atoi(attrs["winner_count"])

	if h, ok := attrs["block_height"]; ok {
		if parsed, err := strconv.ParseInt(h, 10, 64); err == nil {
			blockHeight = parsed
		}
	}

	community := splitCards(attrs["community_cards"])

	var shown []string
	snap, err := handEndSnapshot(func(h int64) (*GameState, error) { return idx.queryGameStateAt(gameID, h) }, handNumber, blockHeight)
	switch {
	case errors.Is(err, errStatePruned):
		log.Printf("Hand %s/%d: state at block %d is pruned on the node; storing community cards only", gameID, handNumber, blockHeight)
	case err != nil:
		log.Printf("Hand %s/%d: hand-end state unavailable (%v); storing community cards only", gameID, handNumber, err)
	case snap == nil:
		log.Printf("Hand %s/%d: no finished state at block %d or %d; storing community cards only", gameID, handNumber, blockHeight, blockHeight-1)
	default:
		shown = shownHoleCards(snap.Players)
		if len(community) == 0 {
			community = normalizeCards(snap.CommunityCards)
		}
		econ := economics(snap)
		idx.recordHandPlayers(gameID, handNumber, blockHeight, blockTime, withEconomics(handPlayers(snap), econ))
		idx.recordHandMeta(gameID, handNumber, econ, blockHeight, blockTime)
		idx.ensureGame(gameID)
	}

	return idx.recordHandResult(gameID, handNumber, blockHeight, community, winnerCount, shown, txHash)
}

// getLatestBlockHeight fetches the current chain height
func getLatestBlockHeight(nodeRPC string) (int64, error) {
	url := fmt.Sprintf("%s/status", nodeRPC)

	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result StatusResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return strconv.ParseInt(result.Result.SyncInfo.LatestBlockHeight, 10, 64)
}

// decodeIfBase64 attempts to decode a base64 string, returns original if not base64
// Only returns decoded value if it's valid UTF-8
func decodeIfBase64(s string) string {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	// Only use decoded value if it's valid UTF-8
	str := string(decoded)
	if !utf8.ValidString(str) {
		return s
	}
	return str
}

// recordPlayerAction stores a player action for stats tracking
func (idx *Indexer) recordPlayerAction(player, gameID, action, amountStr string, blockHeight int64) {
	amount, _ := strconv.ParseInt(amountStr, 10, 64)

	idx.db.Exec(`
		INSERT INTO player_actions (player_address, game_id, block_height, action, amount)
		VALUES ($1, $2, $3, $4, $5)
	`, player, gameID, blockHeight, action, amount)
}

// recordPlayerSession tracks player join/leave for session stats
func (idx *Indexer) recordPlayerSession(attrs map[string]string, blockHeight int64, isJoin bool) {
	player := attrs["player"]
	gameID := attrs["game_id"]

	if player == "" || gameID == "" {
		return
	}

	if isJoin {
		buyIn, _ := strconv.ParseInt(attrs["buy_in_amount"], 10, 64)
		idx.db.Exec(`
			INSERT INTO player_sessions (player_address, game_id, join_block, buy_in_amount)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (player_address, game_id, join_block) DO NOTHING
		`, player, gameID, blockHeight, buyIn)
	} else {
		refund, _ := strconv.ParseInt(attrs["refund_amount"], 10, 64)
		// Update the most recent session for this player/game
		idx.db.Exec(`
			UPDATE player_sessions
			SET leave_block = $1, cash_out_amount = $2
			WHERE player_address = $3 AND game_id = $4 AND leave_block IS NULL
		`, blockHeight, refund, player, gameID)
	}
}

// updateProgress updates the indexing progress in the database every 100 blocks
func (idx *Indexer) updateProgress(blockHeight int64) {
	if blockHeight%100 == 0 {
		idx.forceUpdateProgress(blockHeight)
	}
}

// forceUpdateProgress unconditionally writes the progress checkpoint
func (idx *Indexer) forceUpdateProgress(blockHeight int64) {
	totalBlocksScanned := blockHeight - idx.config.StartBlock + 1
	_, err := idx.db.Exec(`
		INSERT INTO indexing_progress (id, last_scanned_block, total_blocks_scanned, last_updated)
		VALUES (1, $1, $2, NOW())
		ON CONFLICT (id) DO UPDATE SET
			last_scanned_block = EXCLUDED.last_scanned_block,
			total_blocks_scanned = EXCLUDED.total_blocks_scanned,
			last_updated = EXCLUDED.last_updated
	`, blockHeight, totalBlocksScanned)
	if err != nil {
		log.Printf("Warning: failed to update progress at block %d: %v", blockHeight, err)
	}
}
