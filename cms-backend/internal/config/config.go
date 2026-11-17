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
	DefaultDomain           string
	StorageType             string
	OSSEndpoint             string
	OSSAccessKeyID          string
	OSSAccessKeySecret      string
	OSSBucket               string
	OSSBasePath             string
	CoverOSSEnabled         bool
	CoverOSSPublicRead      bool
	CoverOSSRegion          string
	CoverOSSEndpoint        string
	CoverOSSBucket          string
	CoverOSSPrefix          string
	CoverOSSPublicBaseURL   string
	CoverOSSAccessKeyID     string
	CoverOSSAccessKeySecret string
	CoverOSSSecurityToken   string
	CoverOSSSignedURLTTL    int
	CoverMaxUploadBytes     int64
}

var AppConfig *Config

func LoadConfig() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	adminJWTSecret := strings.TrimSpace(os.Getenv("CMS_JWT_SECRET"))
	if os.Getenv("GIN_MODE") == "release" {
		if !validAdminJWTSecret(adminJWTSecret, os.Getenv("JWT_SECRET")) {
			log.Fatal("CMS_JWT_SECRET must be distinct from Reader JWT_SECRET and at least 32 characters")
		}
	} else if adminJWTSecret == "" {
		adminJWTSecret = "star-novel-cms-local-development-only"
	}

	AppConfig = &Config{
		Port:                    getEnv("PORT", "8081"),
		DatabaseURL:             getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/novel_db?sslmode=disable"),
		RedisURL:                getEnv("REDIS_URL", "localhost:6379"),
		JWTSecret:               adminJWTSecret,
		StripeSecretKey:         getEnv("STRIPE_SECRET_KEY", ""),
		PayPalClientID:          getEnv("PAYPAL_CLIENT_ID", ""),
		PayPalClientSecret:      getEnv("PAYPAL_CLIENT_SECRET", ""),
		DefaultDomain:           getEnv("DEFAULT_DOMAIN", "https://h5.starsfic.com"),
		StorageType:             getEnv("STORAGE_TYPE", "postgres"),
		OSSEndpoint:             getEnv("OSS_ENDPOINT", ""),
		OSSAccessKeyID:          getEnv("OSS_ACCESS_KEY_ID", ""),
		OSSAccessKeySecret:      getEnv("OSS_ACCESS_KEY_SECRET", ""),
		OSSBucket:               getEnv("OSS_BUCKET", ""),
		OSSBasePath:             getEnv("OSS_BASE_PATH", "novels"),
		CoverOSSEnabled:         getEnvBool("COVER_OSS_ENABLED", false),
		CoverOSSPublicRead:      getEnvBool("COVER_OSS_PUBLIC_READ", false),
		CoverOSSRegion:          getEnv("COVER_OSS_REGION", "us-west-1"),
		CoverOSSEndpoint:        firstNonEmptyEnv("COVER_OSS_ENDPOINT", "OSS_ENDPOINT"),
		CoverOSSBucket:          firstNonEmptyEnv("COVER_OSS_BUCKET", "OSS_BUCKET"),
		CoverOSSPrefix:          getEnv("COVER_OSS_PREFIX", "covers"),
		CoverOSSPublicBaseURL:   getEnv("COVER_OSS_PUBLIC_BASE_URL", ""),
		CoverOSSAccessKeyID:     firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_ID", "OSS_ACCESS_KEY_ID"),
		CoverOSSAccessKeySecret: firstNonEmptyEnv("COVER_OSS_ACCESS_KEY_SECRET", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "OSS_ACCESS_KEY_SECRET"),
		CoverOSSSecurityToken:   firstNonEmptyEnv("COVER_OSS_SECURITY_TOKEN", "ALIBABA_CLOUD_SECURITY_TOKEN"),
		CoverOSSSignedURLTTL:    getEnvInt("COVER_OSS_SIGNED_URL_TTL_SECONDS", 3600),
		CoverMaxUploadBytes:     getEnvInt64("COVER_MAX_UPLOAD_BYTES", 5*1024*1024),
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

func getEnvInt64(key string, fallback int64) int64 {
	val, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(val) == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func validAdminJWTSecret(secret, readerSecret string) bool {
	secret = strings.TrimSpace(secret)
	lower := strings.ToLower(secret)
	return len(secret) >= 32 && secret != strings.TrimSpace(readerSecret) &&
		!strings.Contains(lower, "change_me") && !strings.Contains(lower, "changeme") &&
		!strings.Contains(lower, "random_string") && !strings.Contains(lower, "development-only") &&
		!strings.HasPrefix(secret, "<")
}
