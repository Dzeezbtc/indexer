package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/block52/indexer/internal/db"
	"github.com/block52/indexer/internal/models"
	"github.com/gin-gonic/gin"
)

// Handler holds dependencies for HTTP handlers
type Handler struct {
	db        *db.DB
	nodeRPC   string
	startTime time.Time
}

// NewHandler creates a new handler instance
func NewHandler(database *db.DB, nodeRPC string) *Handler {
	return &Handler{
		db:        database,
		nodeRPC:   nodeRPC,
		startTime: time.Now(),
	}
}

// Health returns service health status
func (h *Handler) Health(c *gin.Context) {
	dbStatus := "connected"
	if err := h.db.Health(); err != nil {
		dbStatus = fmt.Sprintf("error: %v", err)
	}

	c.JSON(http.StatusOK, models.HealthStatus{
		Status:   "healthy",
		Database: dbStatus,
		Uptime:   time.Since(h.startTime).String(),
	})
}

// Status returns indexing progress and statistics
func (h *Handler) Status(c *gin.Context) {
	status, err := h.db.GetIndexingStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: fmt.Sprintf("failed to get status: %v", err),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	// Get current chain height from RPC node
	chainHeight, err := h.getChainHeight()
	if err == nil && chainHeight > 0 {
		status.TotalBlocks = chainHeight
		// Recalculate percentage based on actual chain height
		if chainHeight > 0 {
			status.PercentComplete = float64(status.LastBlockIndexed) / float64(chainHeight) * 100
		}
	}

	c.JSON(http.StatusOK, status)
}

// getChainHeight fetches the current chain height from the RPC node
func (h *Handler) getChainHeight() (int64, error) {
	type StatusResult struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}

	url := fmt.Sprintf("%s/status", h.nodeRPC)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result StatusResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	height, err := strconv.ParseInt(result.Result.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil {
		return 0, err
	}

	return height, nil
}

// GetHands returns paginated list of poker hands
func (h *Handler) GetHands(c *gin.Context) {
	var params models.PaginationParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid parameters",
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}

	// Default pagination
	if params.Limit == 0 {
		params.Limit = 50
	}

	// Optional filters
	gameID := c.Query("game_id")
	startBlock, _ := strconv.ParseInt(c.Query("start_block"), 10, 64)
	endBlock, _ := strconv.ParseInt(c.Query("end_block"), 10, 64)

	hands, total, err := h.db.GetHands(params.Limit, params.Offset, gameID, startBlock, endBlock)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	response := models.PaginatedResponse{
		Data: hands,
	}
	response.Pagination.Limit = params.Limit
	response.Pagination.Offset = params.Offset
	response.Pagination.Total = total

	c.JSON(http.StatusOK, response)
}

// GetHandDetails returns detailed information about a specific hand
func (h *Handler) GetHandDetails(c *gin.Context) {
	gameID := c.Param("game_id")
	handNumber, err := strconv.Atoi(c.Param("hand_number"))
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid hand number",
			Message: "Hand number must be a valid integer",
			Code:    http.StatusBadRequest,
		})
		return
	}

	details, err := h.db.GetHandDetails(gameID, handNumber)
	if err != nil {
		if err.Error() == "hand not found" {
			c.JSON(http.StatusNotFound, models.ErrorResponse{
				Error:   "Hand not found",
				Message: fmt.Sprintf("Hand %d not found for game %s", handNumber, gameID),
				Code:    http.StatusNotFound,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, details)
}

// GetRevealedCards returns all revealed cards for a specific hand
func (h *Handler) GetRevealedCards(c *gin.Context) {
	gameID := c.Param("game_id")
	handNumber, err := strconv.Atoi(c.Param("hand_number"))
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid hand number",
			Message: "Hand number must be a valid integer",
			Code:    http.StatusBadRequest,
		})
		return
	}

	cards, err := h.db.GetRevealedCards(gameID, handNumber)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"game_id":     gameID,
		"hand_number": handNumber,
		"cards":       cards,
	})
}

// GetStatsSummary returns overall statistics summary
func (h *Handler) GetStatsSummary(c *gin.Context) {
	summary, err := h.db.GetStatsSummary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, summary)
}

// GetCardStats returns card frequency statistics
func (h *Handler) GetCardStats(c *gin.Context) {
	card := c.Param("card")

	stats, err := h.db.GetCardStats(card)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	if card != "" && len(stats) == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:   "Card not found",
			Message: fmt.Sprintf("No statistics found for card: %s", card),
			Code:    http.StatusNotFound,
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetAllCardStats returns all card frequency statistics
func (h *Handler) GetAllCardStats(c *gin.Context) {
	stats, err := h.db.GetCardStats("")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetSuitStats returns suit distribution statistics
func (h *Handler) GetSuitStats(c *gin.Context) {
	stats, err := h.db.GetSuitStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetRankStats returns rank distribution statistics
func (h *Handler) GetRankStats(c *gin.Context) {
	stats, err := h.db.GetRankStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetChiSquaredTest returns chi-squared test results
func (h *Handler) GetChiSquaredTest(c *gin.Context) {
	result, err := h.db.GetChiSquaredTest()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetOutlierCards returns cards with significant deviation
func (h *Handler) GetOutlierCards(c *gin.Context) {
	threshold := 1.0 // Default 1% deviation
	if t := c.Query("threshold"); t != "" {
		if parsed, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = parsed
		}
	}

	cards, err := h.db.GetOutlierCards(threshold)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"threshold":     threshold,
		"outlier_count": len(cards),
		"outliers":      cards,
	})
}

// GetSeedEntropy returns seed entropy analysis
func (h *Handler) GetSeedEntropy(c *gin.Context) {
	data, err := h.db.GetSeedEntropy()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, data)
}

// GetRandomnessReport returns comprehensive randomness analysis
func (h *Handler) GetRandomnessReport(c *gin.Context) {
	report := &models.RandomnessReport{}

	// Get summary
	summary, err := h.db.GetStatsSummary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}
	report.Summary = *summary

	// Get card chi-squared test
	chiSquared, err := h.db.GetChiSquaredTest()
	if err == nil {
		report.CardChiSquared = *chiSquared
	}

	// Get suit chi-squared test
	suitChiSquared, err := h.db.GetSuitChiSquaredTest()
	if err == nil {
		report.SuitChiSquared = *suitChiSquared
	}

	// Get rank chi-squared test
	rankChiSquared, err := h.db.GetRankChiSquaredTest()
	if err == nil {
		report.RankChiSquared = *rankChiSquared
	}

	// Get outlier cards
	outliers, err := h.db.GetOutlierCards(1.0)
	if err == nil {
		report.OutlierCards = outliers
	}

	// Get seed entropy
	seedData, err := h.db.GetSeedEntropy()
	if err == nil {
		report.DuplicateSeeds = seedData.DuplicateCount
	}

	c.JSON(http.StatusOK, report)
}

// GetPlayerStats returns player statistics
func (h *Handler) GetPlayerStats(c *gin.Context) {
	playerAddress := c.Param("address")

	profile, err := h.db.GetPlayerProfile(playerAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	c.JSON(http.StatusOK, profile)
}

// SearchPlayers lists players (address-searchable, sortable, paginated) for the
// player directory.
func (h *Handler) SearchPlayers(c *gin.Context) {
	var params models.PlayerSearchParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid parameters",
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}

	if params.Limit == 0 {
		params.Limit = 50
	}

	players, total, err := h.db.SearchPlayers(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	response := models.PaginatedResponse{Data: players}
	response.Pagination.Limit = params.Limit
	response.Pagination.Offset = params.Offset
	response.Pagination.Total = total

	c.JSON(http.StatusOK, response)
}

// GetPlayerSessions returns player game sessions
// GetPlayerHands returns the finished hands a wallet played, newest first (ui#721).
func (h *Handler) GetPlayerHands(c *gin.Context) {
	playerAddress := c.Param("address")

	var params models.PaginationParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid parameters",
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}

	// Default pagination
	if params.Limit == 0 {
		params.Limit = 50
	}

	hands, total, err := h.db.GetPlayerHands(playerAddress, params.Limit, params.Offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	response := models.PaginatedResponse{
		Data: hands,
	}
	response.Pagination.Limit = params.Limit
	response.Pagination.Offset = params.Offset
	response.Pagination.Total = total

	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetPlayerSessions(c *gin.Context) {
	playerAddress := c.Param("address")

	var params models.PaginationParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid parameters",
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}

	// Default pagination
	if params.Limit == 0 {
		params.Limit = 50
	}

	sessions, total, err := h.db.GetPlayerSessions(playerAddress, params.Limit, params.Offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Database error",
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	response := models.PaginatedResponse{
		Data: sessions,
	}
	response.Pagination.Limit = params.Limit
	response.Pagination.Offset = params.Offset
	response.Pagination.Total = total

	c.JSON(http.StatusOK, response)
}

// GetActivity returns per-player daily play for club reporting.
// GET /api/v1/activity?from=YYYY-MM-DD&to=YYYY-MM-DD[&player=b52...][&rake_owner=b52...]
// `to` is inclusive. At least one filter (player or rake_owner) is required,
// so the endpoint never dumps every player's results.
func (h *Handler) GetActivity(c *gin.Context) {
	from, err1 := time.Parse("2006-01-02", c.Query("from"))
	to, err2 := time.Parse("2006-01-02", c.Query("to"))
	if err1 != nil || err2 != nil || to.Before(from) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "from and to must be dates (YYYY-MM-DD), from <= to"})
		return
	}
	if to.Sub(from) > 400*24*time.Hour {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "ask for at most 400 days at a time"})
		return
	}
	players := c.QueryArray("player")
	owner := c.Query("rake_owner")
	if len(players) == 0 && owner == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "filter by player and/or rake_owner"})
		return
	}
	rows, err := h.db.GetActivity(from, to.Add(24*time.Hour), players, owner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}
