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
	PayPalMode         string // "sandbox" or "live"
	FbPixelID          string
	FbAccessToken      string
	DefaultDomain      string
	StorageType        string
	OSSEndpoint        string
	OSSAccessKeyID     string
	OSSAccessKeySecret string
	OSSBucket          string
	OSSBasePath        string
}

var AppConfig *Config

func LoadConfig() {
	// Load .env file if it exists (local development)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	jwtSecret := getEnv("JWT_SECRET", "super-secret-key-star-novel-2026")
	if os.Getenv("GIN_MODE") == "release" && (jwtSecret == "" || jwtSecret == "super-secret-key-star-novel-2026") {
		log.Fatal("[CRITICAL SECURITY ERROR] JWT_SECRET must be explicitly configured with a secure secret in release mode!")
	}

	AppConfig = &Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/novel_db?sslmode=disable"),
		RedisURL:           getEnv("REDIS_URL", "localhost:6379"),
		JWTSecret:          jwtSecret,
		StripeSecretKey:    getEnv("STRIPE_SECRET_KEY", ""),
		PayPalClientID:     getEnv("PAYPAL_CLIENT_ID", ""),
		PayPalClientSecret: getEnv("PAYPAL_CLIENT_SECRET", ""),
		PayPalMode:         getEnv("PAYPAL_MODE", "sandbox"),
		FbPixelID:          getEnv("FB_PIXEL_ID", ""),
		FbAccessToken:      getEnv("FB_ACCESS_TOKEN", ""),
		DefaultDomain:      getEnv("DEFAULT_DOMAIN", "https://h5.star-novel.com"),
		StorageType:        getEnv("STORAGE_TYPE", "postgres"),
		OSSEndpoint:        getEnv("OSS_ENDPOINT", ""),
		OSSAccessKeyID:     getEnv("OSS_ACCESS_KEY_ID", ""),
		OSSAccessKeySecret: getEnv("OSS_ACCESS_KEY_SECRET", ""),
		OSSBucket:          getEnv("OSS_BUCKET", ""),
		OSSBasePath:        getEnv("OSS_BASE_PATH", "novels"),
	}
	App = AppConfig
}

var App *Config

// Get returns the global application configuration in an idiomatic Go way.
func Get() *Config {
	return AppConfig
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
