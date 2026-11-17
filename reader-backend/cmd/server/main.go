package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"reader-backend/internal/auth"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/feedback"
	"reader-backend/internal/httpx"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/redis"
	"reader-backend/internal/shelf"
	"reader-backend/internal/storage"
	"reader-backend/internal/tracking"
	"reader-backend/internal/wallet"
	"reader-backend/internal/workerpool"
)

var buildVersion = "dev"

func main() {
	// 1. Load config
	config.LoadConfig()

	// 2. Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 3. Connect to DB and Redis, Initialize WorkerPool
	db.InitDB()
	defer db.CloseDB()
	tokenCtx, tokenCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := auth.InitTokenPolicy(tokenCtx); err != nil {
		log.Fatal(err)
	}
	tokenCancel()

	redis.InitRedis()
	defer redis.CloseRedis()

	workerpool.InitPool(10, 1000)

	// 4. Setup Gin engine
	r := gin.New()
	// Trust only the local gateway, never arbitrary forwarded chains.
	proxies := []string{"127.0.0.1", "::1", "172.30.87.1"}
	if raw := os.Getenv("TRUSTED_PROXIES"); raw != "" {
		proxies = strings.Split(raw, ",")
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		log.Fatal(err)
	}
	r.Use(gin.Logger(), gin.Recovery())

	// 5. Middleware
	r.Use(corsMiddleware(), requestIDMiddleware(), httpx.Policy())

	r.GET("/healthz", func(c *gin.Context) {
		dbStatus := "UP"
		if db.DB == nil || db.CheckSchema(c.Request.Context()) != nil {
			dbStatus = "DOWN"
		}
		redisStatus := "UP"
		if redis.RDB == nil || redis.RDB.Ping(c.Request.Context()).Err() != nil {
			redisStatus = "DOWN"
		}

		statusCode := http.StatusOK
		if dbStatus != "UP" || redisStatus != "UP" {
			statusCode = http.StatusServiceUnavailable
		}

		c.JSON(statusCode, gin.H{
			"status": gin.H{
				"database": dbStatus,
				"redis":    redisStatus,
			},
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// 6. Basic Health check and routes
	reconcileCtx, stopReconciliation := context.WithCancel(context.Background())
	reconciliationDone := make(chan struct{})
	defer stopReconciliation()
	api := r.Group("/api/v1")
	{
		authRepo := auth.NewUserRepository()
		authService := auth.NewAuthService(authRepo)
		authHandler := auth.NewAuthHandler(authService)
		authHandler.RegisterRoutes(api)

		contentStorage, err := storage.NewContentStorage(config.AppConfig, db.DB)
		if err != nil {
			log.Fatalf("Failed to initialize content storage: %v", err)
		}
		log.Printf("Reader Content Storage initialized with driver: %s", contentStorage.StorageType())

		coverSigner, err := storage.NewCoverOSSSigner(config.AppConfig)
		if err != nil {
			log.Fatalf("Failed to initialize reader cover OSS signer: %v", err)
		}
		log.Printf("Reader Cover OSS Signer enabled=%t", coverSigner.IsEnabled())

		novelRepo := novel.NewDBRepository()
		novelCache := novel.NewRedisCache()
		novelService := novel.NewService(novelRepo, novelCache, contentStorage, coverSigner)
		novelHandler := novel.NewHandler(novelService)
		novelHandler.RegisterRoutes(api)

		shelfRepo := shelf.NewDBRepository()
		shelfService := shelf.NewService(shelfRepo, coverSigner)
		shelfHandler := shelf.NewHandler(shelfService)
		shelfHandler.RegisterRoutes(api)

		stripeClient := payment.NewStripeClient()
		paypalClient := payment.NewPayPalClient()

		walletRepo := wallet.NewDBRepository()
		walletService := wallet.NewService(walletRepo, novelRepo, novelCache, stripeClient, paypalClient)
		go func() { defer close(reconciliationDone); walletService.RunReconciliation(reconcileCtx) }()
		walletHandler := wallet.NewHandler(walletService)
		walletHandler.RegisterRoutes(api)

		feedbackRepo := feedback.NewRepository()
		feedbackService := feedback.NewService(feedbackRepo)
		feedbackHandler := feedback.NewHandler(feedbackService)
		feedbackHandler.RegisterRoutes(api)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"version": buildVersion,
			"time":    time.Now().Format(time.RFC3339),
		})
	})

	// 7. Setup graceful shutdown server
	srv := &http.Server{
		Addr:              ":" + config.AppConfig.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      70 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	go func() {
		log.Printf("Star Novel backend starting on port %s (version=%s)", config.AppConfig.Port, buildVersion)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Drain producers and consumers in dependency order within the container shutdown budget.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	httpCtx, httpCancel := context.WithTimeout(ctx, 8*time.Second)
	if err := srv.Shutdown(httpCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
		_ = srv.Close()
	}
	httpCancel()
	stopReconciliation()
	select {
	case <-reconciliationDone:
	case <-ctx.Done():
		log.Print("Background shutdown budget exhausted; durable work will resume")
		os.Exit(1)
	}
	if err := workerpool.ShutdownContext(ctx); err != nil {
		log.Print("Worker shutdown budget exhausted; durable work will resume")
		os.Exit(1)
	}
	tracking.Shutdown(ctx)

	log.Println("Server exiting")
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			parsed, err := url.Parse(origin)
			allowed := err == nil && parsed.Host == c.Request.Host
			if gin.Mode() != gin.ReleaseMode && (origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173") {
				allowed = true
			}
			for _, candidate := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
				if origin == strings.TrimSpace(candidate) {
					allowed = true
				}
			}
			if origin == config.AppConfig.DefaultDomain {
				allowed = true
			}
			if !allowed {
				c.AbortWithStatus(403)
				return
			}
			c.Header("Vary", "Origin")
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-FB-FBP, X-FB-FBC, X-UTM-Source, X-UTM-Campaign, X-Event-Source-URL, X-FB-Pixel-ID, X-Recharge-Template-ID, X-Promo-ID, X-Country, CF-IPCountry")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.Request.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		c.Writer.Header().Set("X-Request-ID", reqID)
		c.Set("RequestID", reqID)
		c.Next()
	}
}
