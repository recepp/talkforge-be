package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds the application configuration parameters.
type Config struct {
	Port                   string
	GinMode                string
	DatabaseURL            string
	JWTSecret              string
	GoogleClientID         string
	AdminBootstrapName     string
	AdminBootstrapPassword string
	GeminiAPIKey           string
	QueuePollIntervalSeconds int
}

// LoadConfig reads configuration parameters from the environment and optional .env file.
func LoadConfig() *Config {
	// Attempt to load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables.")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = "debug"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgresql://postgres:postgres@localhost:5432/talkforge?sslmode=disable"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "talkforge_super_secret_jwt_key_change_me_in_production"
	}

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")

	adminName := os.Getenv("ADMIN_BOOTSTRAP_NAME")
	if adminName == "" {
		adminName = "admin"
	}

	adminPassword := os.Getenv("ADMIN_BOOTSTRAP_PASSWORD")
	if adminPassword == "" {
		adminPassword = "admin123"
	}

	geminiAPIKey := os.Getenv("GEMINI_API_KEY")

	pollStr := os.Getenv("QUEUE_POLL_INTERVAL_SECONDS")
	pollSecs := 5
	if pollStr != "" {
		if val, err := strconv.Atoi(pollStr); err == nil {
			pollSecs = val
		}
	}

	return &Config{
		Port:                   port,
		GinMode:                ginMode,
		DatabaseURL:            databaseURL,
		JWTSecret:              jwtSecret,
		GoogleClientID:         googleClientID,
		AdminBootstrapName:     adminName,
		AdminBootstrapPassword: adminPassword,
		GeminiAPIKey:           geminiAPIKey,
		QueuePollIntervalSeconds: pollSecs,
	}
}
