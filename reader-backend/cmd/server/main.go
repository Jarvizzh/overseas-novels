package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"reader-backend/internal/auth"
	"reader-backend/internal/config"
	"reader-backend/internal/db"
	"reader-backend/internal/novel"
	"reader-backend/internal/payment"
	"reader-backend/internal/redis"
	"reader-backend/internal/shelf"
	"reader-backend/internal/tracking"
	"reader-backend/internal/wallet"
	"reader-backend/internal/workerpool"
)

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

	redis.InitRedis()
	defer redis.CloseRedis()

	workerpool.InitPool(10, 1000)
	defer workerpool.Shutdown()

	// 4. Setup Gin engine
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// 5. Middleware
	r.Use(corsMiddleware(), requestIDMiddleware())

	r.GET("/healthz", func(c *gin.Context) {
		dbStatus := "UP"
		if db.DB == nil || db.DB.Ping(c.Request.Context()) != nil {
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
	api := r.Group("/api/v1")
	{
		authRepo := auth.NewUserRepository()
		authService := auth.NewAuthService(authRepo)
		authHandler := auth.NewAuthHandler(authService)
		authHandler.RegisterRoutes(api)

		novelRepo := novel.NewDBRepository()
		novelCache := novel.NewRedisCache()
		novelService := novel.NewService(novelRepo, novelCache)
		novelHandler := novel.NewHandler(novelService)
		novelHandler.RegisterRoutes(api)

		shelfRepo := shelf.NewDBRepository()
		shelfService := shelf.NewService(shelfRepo)
		shelfHandler := shelf.NewHandler(shelfService)
		shelfHandler.RegisterRoutes(api)

		stripeClient := payment.NewStripeClient()
		paypalClient := payment.NewPayPalClient()

		walletRepo := wallet.NewDBRepository()
		walletService := wallet.NewService(walletRepo, novelRepo, novelCache, stripeClient, paypalClient)
		walletHandler := wallet.NewHandler(walletService)
		walletHandler.RegisterRoutes(api)
	}

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "UP",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	// 7. Setup graceful shutdown server
	srv := &http.Server{
		Addr:    ":" + config.AppConfig.Port,
		Handler: r,
	}

	go func() {
		log.Printf("Star Novel backend starting on port %s", config.AppConfig.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with a timeout of 5 seconds.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	tracking.Shutdown(ctx)
	log.Println("Server exiting")
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-FB-FBP, X-FB-FBC, X-UTM-Source, X-UTM-Campaign, X-Event-Source-URL, X-FB-Pixel-ID, X-Recharge-Template-ID, X-Country, CF-IPCountry")
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
