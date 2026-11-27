package config

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                                 string
	DatabaseURL                          string
	ForegroundDBMax                      int32
	BackgroundDBMax                      int32
	ReadRequestMax                       int32
	ReadLoadMax                          int32
	RedisURL                             string
	JWTSecret                            string
	PayPalClientID                       string
	PayPalClientSecret                   string
	PayPalWebhookID                      string
	PayPalMerchantID                     string
	PayPalMode                           string // "sandbox" or "live"
	PaymentTaskMetrics                   bool
	StripeSingleCheckoutEnabled          bool
	StripeVIPCheckoutEnabled             bool
	StripeBalanceChangeReportType        string
	StripePayoutReconciliationReportType string
	StripeReportMoRColumnsEnabled        bool
	PaymentAccounts                      []PaymentAccount
	DefaultDomain                        string
	StorageType                          string
	OSSEndpoint                          string
	OSSAccessKeyID                       string
	OSSAccessKeySecret                   string
	OSSBucket                            string
	OSSBasePath                          string
	CoverOSSEnabled                      bool
	CoverOSSPublicRead                   bool
	CoverOSSRegion                       string
	CoverOSSBucket                       string
	CoverOSSPrefix                       string
	CoverOSSPublicBaseURL                string
	CoverOSSAccessKeyID                  string
	CoverOSSAccessKeySecret              string
	CoverOSSSecurityToken                string
	CoverOSSSignedURLTTL                 int
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
		Port:                                 getEnv("PORT", "8080"),
		DatabaseURL:                          getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/novel_db?sslmode=disable"),
		ForegroundDBMax:                      positiveLimit("READER_DB_FOREGROUND_MAX_CONNS", 21),
		BackgroundDBMax:                      positiveLimit("READER_DB_BACKGROUND_MAX_CONNS", 4),
		ReadRequestMax:                       positiveLimit("READER_MAX_IN_FLIGHT_READS", 48),
		ReadLoadMax:                          positiveLimit("READER_MAX_CONTENT_LOADS", 12),
		RedisURL:                             getEnv("REDIS_URL", "localhost:6379"),
		JWTSecret:                            jwtSecret,
		PayPalClientID:                       getEnv("PAYPAL_CLIENT_ID", ""),
		PayPalClientSecret:                   getEnv("PAYPAL_CLIENT_SECRET", ""),
		PayPalWebhookID:                      getEnv("PAYPAL_WEBHOOK_ID", ""),
		PayPalMerchantID:                     getEnv("PAYPAL_MERCHANT_ID", ""),
		PayPalMode:                           getEnv("PAYPAL_MODE", "sandbox"),
		PaymentTaskMetrics:                   getEnvBool("PAYMENT_TASK_METRICS_ENABLED", true),
		StripeSingleCheckoutEnabled:          getEnvBool("STRIPE_SINGLE_CHECKOUT_ENABLED", false),
		StripeVIPCheckoutEnabled:             getEnvBool("STRIPE_VIP_CHECKOUT_ENABLED", false),
		StripeBalanceChangeReportType:        strings.TrimSpace(getEnv("STRIPE_REPORT_TYPE_BALANCE_CHANGE", "")),
		StripePayoutReconciliationReportType: strings.TrimSpace(getEnv("STRIPE_REPORT_TYPE_PAYOUT_RECONCILIATION", "")),
		StripeReportMoRColumnsEnabled:        getEnvBool("STRIPE_REPORT_MOR_COLUMNS_ENABLED", true),
		DefaultDomain:                        getEnv("DEFAULT_DOMAIN", "https://h5.starsfic.com"),
		StorageType:                          getEnv("STORAGE_TYPE", "postgres"),
		OSSEndpoint:                          getEnv("OSS_ENDPOINT", ""),
		OSSAccessKeyID:                       getEnv("OSS_ACCESS_KEY_ID", ""),
		OSSAccessKeySecret:                   getEnv("OSS_ACCESS_KEY_SECRET", ""),
		OSSBucket:                            getEnv("OSS_BUCKET", ""),
		OSSBasePath:                          getEnv("OSS_BASE_PATH", "novels"),
		CoverOSSEnabled:                      getEnvBool("COVER_OSS_ENABLED", false),
		CoverOSSPublicRead:                   getEnvBool("COVER_OSS_PUBLIC_READ", false),
		CoverOSSRegion:                       getEnv("COVER_OSS_REGION", "us-west-1"),
		CoverOSSBucket:                       firstNonEmptyEnv("COVER_OSS_BUCKET", "OSS_BUCKET"),
		CoverOSSPrefix:                       getEnv("COVER_OSS_PREFIX", "covers"),
		CoverOSSPublicBaseURL:                getEnv("COVER_OSS_PUBLIC_BASE_URL", ""),
		CoverOSSAccessKeyID:                  firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_ID", "OSS_ACCESS_KEY_ID"),
		CoverOSSAccessKeySecret:              firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_SECRET", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "OSS_ACCESS_KEY_SECRET"),
		CoverOSSSecurityToken:                firstNonEmptyEnv("COVER_OSS_SECURITY_TOKEN", "ALIBABA_CLOUD_SECURITY_TOKEN"),
		CoverOSSSignedURLTTL:                 getEnvInt("COVER_OSS_SIGNED_URL_TTL_SECONDS", 3600),
	}
	if raw := os.Getenv("PAYMENT_ACCOUNT_REFERENCES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &AppConfig.PaymentAccounts); err != nil {
			log.Fatal("PAYMENT_ACCOUNT_REFERENCES must contain a JSON array of account references")
		}
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

func positiveLimit(key string, fallback int32) int32 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		log.Fatalf("%s must be a positive limit", key)
	}
	return int32(value)
}
