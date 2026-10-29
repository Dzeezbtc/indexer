package main

import (
	"log"
	"os"
	"strconv"

	"github.com/block52/indexer/internal/db"
	"github.com/block52/indexer/internal/handlers"
	"github.com/block52/indexer/internal/middleware"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration from environment
	cfg := loadConfig()

	// Connect to database
	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	log.Println("Connected to PostgreSQL")

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create router
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.Logger())

	// CORS configuration
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = cfg.CORSOrigins
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	router.Use(cors.New(corsConfig))

	// Create handlers
	h := handlers.NewHandler(database, cfg.NodeRPC)

	// Health check
	router.GET("/health", h.Health)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Status endpoint
		v1.GET("/status", h.Status)

		// Hand data endpoints
		hands := v1.Group("/hands")
		{
			hands.GET("", h.GetHands)
			hands.GET("/:game_id/:hand_number", h.GetHandDetails)
			hands.GET("/:game_id/:hand_number/cards", h.GetRevealedCards)
		}

		// Statistics endpoints
		stats := v1.Group("/stats")
		{
			stats.GET("/summary", h.GetStatsSummary)
			stats.GET("/cards", h.GetAllCardStats)
			stats.GET("/cards/:card", h.GetCardStats)
			stats.GET("/suits", h.GetSuitStats)
			stats.GET("/ranks", h.GetRankStats)
			stats.GET("/chi-squared", h.GetChiSquaredTest)
		}

		// Analysis endpoints
		analysis := v1.Group("/analysis")
		{
			analysis.GET("/randomness", h.GetRandomnessReport)
			analysis.GET("/outliers", h.GetOutlierCards)
			analysis.GET("/seeds", h.GetSeedEntropy)
		}

		// Player endpoints
		players := v1.Group("/players")
		{
			players.GET("", h.SearchPlayers)
			players.GET("/:address/stats", h.GetPlayerStats)
			players.GET("/:address/sessions", h.GetPlayerSessions)
			players.GET("/:address/hands", h.GetPlayerHands)
		}

		// Club reporting (Dzeezbtc/block52-club)
		v1.GET("/activity", h.GetActivity)
	}

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Starting API server on %s", addr)
	log.Printf("Environment: %s", cfg.Environment)
	log.Printf("CORS Origins: %v", cfg.CORSOrigins)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// Config holds application configuration
type Config struct {
	Port        string
	Environment string
	CORSOrigins []string
	NodeRPC     string
	DB          db.Config
}

// loadConfig loads configuration from environment variables
func loadConfig() Config {
	return Config{
		Port:        getEnv("API_PORT", "8000"),
		Environment: getEnv("ENVIRONMENT", "development"),
		CORSOrigins: []string{getEnv("CORS_ORIGINS", "*")},
		NodeRPC:     getEnv("NODE_RPC", "http://localhost:26657"),
		DB: db.Config{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "poker"),
			Password: getEnv("DB_PASSWORD", "poker_indexer_dev"),
			DBName:   getEnv("DB_NAME", "poker_hands"),
			MaxConns: getEnvInt("DB_MAX_CONNS", 25),
			MaxIdle:  getEnvInt("DB_MAX_IDLE", 5),
		},
	}
}

// getEnv gets environment variable with fallback
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// getEnvInt gets integer environment variable with fallback
func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return fallback
}
