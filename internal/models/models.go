package models

import "time"

// PokerHand represents a poker hand record
type PokerHand struct {
	GameID      string    `json:"game_id"`
	HandNumber  int       `json:"hand_number"`
	BlockHeight int64     `json:"block_height"`
	DeckSeed    string    `json:"deck_seed"` // block hash used as the shuffle seed (public)
	TxHash      string    `json:"tx_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

// HandResult represents the result of a completed hand
type HandResult struct {
	GameID         string    `json:"game_id"`
	HandNumber     int       `json:"hand_number"`
	BlockHeight    int64     `json:"block_height"`
	CommunityCards []string  `json:"community_cards"`
	WinnerCount    int       `json:"winner_count"`
	TxHash         string    `json:"tx_hash"`
	CreatedAt      time.Time `json:"created_at"`
}

// RevealedCard represents a single revealed card
type RevealedCard struct {
	ID          int64     `json:"id"`
	GameID      string    `json:"game_id"`
	HandNumber  int       `json:"hand_number"`
	BlockHeight int64     `json:"block_height"`
	Card        string    `json:"card"`
	CardType    string    `json:"card_type"` // "community" or "hole"
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
}

// HandDetails combines hand info with results and cards
type HandDetails struct {
	PokerHand
	Result        *HandResult    `json:"result,omitempty"`
	RevealedCards []RevealedCard `json:"revealed_cards,omitempty"`
}

// CardStats represents card frequency statistics
type CardStats struct {
	Card              string  `json:"card"`
	Rank              string  `json:"rank"`
	Suit              string  `json:"suit"`
	TotalAppearances  int64   `json:"total_appearances"`
	ExpectedFrequency float64 `json:"expected_frequency"`
	ActualFrequency   float64 `json:"actual_frequency"`
	Deviation         float64 `json:"deviation"`
	DeviationPercent  float64 `json:"deviation_percent"`
}

// SuitStats represents suit distribution statistics
type SuitStats struct {
	Suit              string  `json:"suit"`
	TotalAppearances  int64   `json:"total_appearances"`
	ExpectedFrequency float64 `json:"expected_frequency"`
	ActualFrequency   float64 `json:"actual_frequency"`
	Deviation         float64 `json:"deviation"`
}

// RankStats represents rank distribution statistics
type RankStats struct {
	Rank              string  `json:"rank"`
	TotalAppearances  int64   `json:"total_appearances"`
	ExpectedFrequency float64 `json:"expected_frequency"`
	ActualFrequency   float64 `json:"actual_frequency"`
	Deviation         float64 `json:"deviation"`
}

// ChiSquaredResult represents chi-squared test results
type ChiSquaredResult struct {
	ChiSquared       float64 `json:"chi_squared"`
	DegreesOfFreedom int     `json:"degrees_of_freedom"`
	PValue           float64 `json:"p_value"`
	Result           string  `json:"result"`
	Interpretation   string  `json:"interpretation"`
}

// StatsSummary represents overall statistics
type StatsSummary struct {
	TotalHands          int64      `json:"total_hands"`
	TotalCompletedHands int64      `json:"total_completed_hands"`
	TotalRevealedCards  int64      `json:"total_revealed_cards"`
	UniqueGames         int64      `json:"unique_games"`
	BlockHeightRange    *string    `json:"block_height_range,omitempty"`
	FirstIndexedAt      *time.Time `json:"first_indexed_at,omitempty"`
	LastIndexedAt       *time.Time `json:"last_indexed_at,omitempty"`
}

// RandomnessReport represents a comprehensive randomness analysis
type RandomnessReport struct {
	Summary        StatsSummary     `json:"summary"`
	CardChiSquared ChiSquaredResult `json:"card_chi_squared"`
	SuitChiSquared ChiSquaredResult `json:"suit_chi_squared"`
	RankChiSquared ChiSquaredResult `json:"rank_chi_squared"`
	OutlierCards   []CardStats      `json:"outlier_cards"`
	DuplicateSeeds int64            `json:"duplicate_seeds"`
}

// PlayerProfile is the full per-player profile: live-computed volume/money
// (always fresh, from player_sessions/player_actions) merged with the
// aggregate VIP + playing-style fields (from the player_stats table, populated
// by refresh_all_player_stats). All money fields are raw USDC micro-units
// (6 decimals); percentage fields are integers scaled x100 (2550 = 25.50%).
type PlayerProfile struct {
	PlayerAddress string `json:"player_address"`

	// Volume & money (live-computed)
	TotalHands       int64   `json:"total_hands"`
	TotalActions     int64   `json:"total_actions"`
	TotalBuyIns      int64   `json:"total_buy_ins"`
	TotalCashOuts    int64   `json:"total_cash_outs"`
	NetProfit        int64   `json:"net_profit"`
	SessionCount     int64   `json:"session_count"`
	AvgSessionLength float64 `json:"avg_session_length"`

	// VIP (aggregate)
	VipTier              string `json:"vip_tier"`
	RakebackPct          int    `json:"rakeback_pct"`
	VipPoints            int64  `json:"vip_points"`
	TotalRakeContributed int64  `json:"total_rake_contributed"`
	CurrentMonthRake     int64  `json:"current_month_rake"`

	// Playing style (aggregate; integers scaled x100)
	Vpip             int `json:"vpip"`
	Pfr              int `json:"pfr"`
	AggressionFactor int `json:"aggression_factor"`
	Wtsd             int `json:"wtsd"`
	WonAtShowdown    int `json:"won_at_showdown"`

	// Action counts (aggregate)
	TotalBets   int64 `json:"total_bets"`
	TotalRaises int64 `json:"total_raises"`
	TotalCalls  int64 `json:"total_calls"`
	TotalFolds  int64 `json:"total_folds"`
	TotalChecks int64 `json:"total_checks"`

	// Records (aggregate)
	BiggestPotWon        int64 `json:"biggest_pot_won"`
	BiggestHandProfit    int64 `json:"biggest_hand_profit"`
	LongestSessionBlocks int64 `json:"longest_session_blocks"`

	// Timestamps (aggregate)
	FirstSeenBlock *int64     `json:"first_seen_block,omitempty"`
	LastSeenBlock  *int64     `json:"last_seen_block,omitempty"`
	StatsUpdatedAt *time.Time `json:"stats_updated_at,omitempty"`
}

// PlayerListItem is a lightweight row for the searchable player directory.
type PlayerListItem struct {
	PlayerAddress        string `json:"player_address"`
	VipTier              string `json:"vip_tier"`
	RakebackPct          int    `json:"rakeback_pct"`
	TotalHands           int64  `json:"total_hands"`
	TotalActions         int64  `json:"total_actions"`
	NetProfit            int64  `json:"net_profit"`
	TotalRakeContributed int64  `json:"total_rake_contributed"`
	LastSeenBlock        *int64 `json:"last_seen_block,omitempty"`
}

// PlayerSearchParams are the query params for GET /api/v1/players.
type PlayerSearchParams struct {
	Search string `form:"search" binding:"omitempty"`
	Sort   string `form:"sort" binding:"omitempty,oneof=net_profit total_hands total_rake_contributed vip_points last_seen_block"`
	Order  string `form:"order" binding:"omitempty,oneof=asc desc"`
	Limit  int    `form:"limit" binding:"omitempty,min=1,max=1000"`
	Offset int    `form:"offset" binding:"omitempty,min=0"`
}

// PlayerSession represents a player's game session
type PlayerSession struct {
	PlayerAddress string    `json:"player_address"`
	GameID        string    `json:"game_id"`
	JoinBlock     int64     `json:"join_block"`
	LeaveBlock    *int64    `json:"leave_block,omitempty"`
	BuyInAmount   int64     `json:"buy_in_amount"`
	CashOutAmount *int64    `json:"cash_out_amount,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// PlayerHand is one finished hand a wallet played (ui#721 "My Hand History").
type PlayerHand struct {
	GameID         string     `json:"game_id"`
	HandNumber     int        `json:"hand_number"`
	Seat           int        `json:"seat"`
	Status         string     `json:"status"`     // status at the hand's end ("" if since left)
	WonAmount      int64      `json:"won_amount"` // chips (SNG/tournament) or micro-USDC (cash)
	BlockHeight    int64      `json:"block_height"`
	EndedAt        *time.Time `json:"ended_at,omitempty"` // block time of the hand's end
	CommunityCards []string   `json:"community_cards"`
	WinnerCount    int        `json:"winner_count"`
}

// TimelineDataPoint represents distribution over time
type TimelineDataPoint struct {
	BlockRange string  `json:"block_range"`
	TotalCards int64   `json:"total_cards"`
	ChiSquared float64 `json:"chi_squared"`
	PassesTest bool    `json:"passes_test"`
}

// SeedEntropyData represents deck seed analysis
type SeedEntropyData struct {
	TotalSeeds     int64   `json:"total_seeds"`
	UniqueSeeds    int64   `json:"unique_seeds"`
	DuplicateCount int64   `json:"duplicate_count"`
	EntropyScore   float64 `json:"entropy_score"`
}

// PaginationParams represents pagination parameters
type PaginationParams struct {
	Limit  int `form:"limit" binding:"omitempty,min=1,max=1000"`
	Offset int `form:"offset" binding:"omitempty,min=0"`
}

// PaginatedResponse wraps paginated data
type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Pagination struct {
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
		Total  int64 `json:"total"`
	} `json:"pagination"`
}

// ErrorResponse represents an API error
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code"`
}

// HealthStatus represents service health
type HealthStatus struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Uptime   string `json:"uptime"`
}

// IndexingStatus represents indexing progress and statistics
type IndexingStatus struct {
	TotalBlocks            int64   `json:"total_blocks"`
	BlocksIndexed          int64   `json:"blocks_indexed"`
	PercentComplete        float64 `json:"percent_complete"`
	LastBlockIndexed       int64   `json:"last_block_indexed"`
	FirstBlockIndexed      int64   `json:"first_block_indexed"`
	TotalHands             int64   `json:"total_hands"`
	TotalGames             int64   `json:"total_games"`
	BlocksPerSecond        float64 `json:"blocks_per_second,omitempty"`
	EstimatedTimeRemaining string  `json:"estimated_time_remaining,omitempty"`
}

// ActivityRow is one player's play for one day at one kind of table: the
// club-reporting roll-up of hand_players (Dzeezbtc/block52-club). Money is
// in the table's units: micro-USDC for cash, chips for SNG/tournaments.
type ActivityRow struct {
	Day         string    `json:"day"`
	Player      string    `json:"player"`
	Format      string    `json:"format"`
	Variant     string    `json:"variant"`
	BigBlind    int64     `json:"big_blind"`
	Hands       int       `json:"hands"`
	Contributed int64     `json:"contributed"`
	Won         int64     `json:"won"`
	Net         int64     `json:"net"`
	Rake        int64     `json:"rake"`
	Tables      int       `json:"tables"`
	FirstAt     time.Time `json:"first_at"`
	LastAt      time.Time `json:"last_at"`
}
