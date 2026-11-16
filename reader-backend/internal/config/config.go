package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                    string
	DatabaseURL             string
	RedisURL                string
	JWTSecret               string
	StripeSecretKey         string
	PayPalClientID          string
	PayPalClientSecret      string
	PayPalMode              string // "sandbox" or "live"
	DefaultDomain           string
	StorageType             string
	OSSEndpoint             string
	OSSAccessKeyID          string
	OSSAccessKeySecret      string
	OSSBucket               string
	OSSBasePath             string
	CoverOSSEnabled         bool
	CoverOSSRegion          string
	CoverOSSBucket          string
	CoverOSSPrefix          string
	CoverOSSPublicBaseURL   string
	CoverOSSAccessKeyID     string
	CoverOSSAccessKeySecret string
	CoverOSSSecurityToken   string
	CoverOSSSignedURLTTL    int
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
		Port:                    getEnv("PORT", "8080"),
		DatabaseURL:             getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/novel_db?sslmode=disable"),
		RedisURL:                getEnv("REDIS_URL", "localhost:6379"),
		JWTSecret:               jwtSecret,
		StripeSecretKey:         getEnv("STRIPE_SECRET_KEY", ""),
		PayPalClientID:          getEnv("PAYPAL_CLIENT_ID", ""),
		PayPalClientSecret:      getEnv("PAYPAL_CLIENT_SECRET", ""),
		PayPalMode:              getEnv("PAYPAL_MODE", "sandbox"),
		DefaultDomain:           getEnv("DEFAULT_DOMAIN", "https://h5.starsfic.com"),
		StorageType:             getEnv("STORAGE_TYPE", "postgres"),
		OSSEndpoint:             getEnv("OSS_ENDPOINT", ""),
		OSSAccessKeyID:          getEnv("OSS_ACCESS_KEY_ID", ""),
		OSSAccessKeySecret:      getEnv("OSS_ACCESS_KEY_SECRET", ""),
		OSSBucket:               getEnv("OSS_BUCKET", ""),
		OSSBasePath:             getEnv("OSS_BASE_PATH", "novels"),
		CoverOSSEnabled:         getEnvBool("COVER_OSS_ENABLED", false),
		CoverOSSRegion:          getEnv("COVER_OSS_REGION", "us-west-1"),
		CoverOSSBucket:          firstNonEmptyEnv("COVER_OSS_BUCKET", "OSS_BUCKET"),
		CoverOSSPrefix:          getEnv("COVER_OSS_PREFIX", "covers"),
		CoverOSSPublicBaseURL:   getEnv("COVER_OSS_PUBLIC_BASE_URL", ""),
		CoverOSSAccessKeyID:     firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_ID", "OSS_ACCESS_KEY_ID"),
		CoverOSSAccessKeySecret: firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_SECRET", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "OSS_ACCESS_KEY_SECRET"),
		CoverOSSSecurityToken:   firstNonEmptyEnv("COVER_OSS_SECURITY_TOKEN", "ALIBABA_CLOUD_SECURITY_TOKEN"),
		CoverOSSSignedURLTTL:    getEnvInt("COVER_OSS_SIGNED_URL_TTL_SECONDS", 3600),
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

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if val := strings.TrimSpace(os.Getenv(k)); val != "" {
			return val
		}
	}
	return ""
}

func getEnvBool(key string, fallback bool) bool {
	val, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(val) == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(val))
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	val, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(val) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return fallback
	}
	return parsed
}
