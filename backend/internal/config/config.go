package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port               string
	DatabaseURL        string
	RedisURL           string
	JWTSecret          string
	StripeSecretKey    string
	PayPalClientID     string
	PayPalClientSecret string
	FbPixelID          string
	FbAccessToken      string
}

var AppConfig *Config

func LoadConfig() {
	// Load .env file if it exists (local development)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	AppConfig = &Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/novel_db?sslmode=disable"),
		RedisURL:           getEnv("REDIS_URL", "localhost:6379"),
		JWTSecret:          getEnv("JWT_SECRET", "super-secret-key-star-novel-2026"),
		StripeSecretKey:    getEnv("STRIPE_SECRET_KEY", ""),
		PayPalClientID:     getEnv("PAYPAL_CLIENT_ID", ""),
		PayPalClientSecret: getEnv("PAYPAL_CLIENT_SECRET", ""),
		FbPixelID:          getEnv("FB_PIXEL_ID", ""),
		FbAccessToken:      getEnv("FB_ACCESS_TOKEN", ""),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
