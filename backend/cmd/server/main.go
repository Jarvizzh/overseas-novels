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
	"novel-backend/internal/auth"
	"novel-backend/internal/config"
	"novel-backend/internal/db"
	"novel-backend/internal/novel"
	"novel-backend/internal/payment"
	"novel-backend/internal/redis"
	"novel-backend/internal/shelf"
	"novel-backend/internal/wallet"
)

func main() {
	// 1. Load config
	config.LoadConfig()

	// 2. Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 3. Connect to DB and Redis
	// Note: During local build/testing, make sure Docker containers are running
	db.InitDB()
	defer db.CloseDB()

	redis.InitRedis()
	defer redis.CloseRedis()

	// 4. Setup Gin engine
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// 5. CORS Middleware
	r.Use(corsMiddleware())

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

	log.Println("Server exiting")
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-FB-FBP, X-FB-FBC, X-UTM-Source, X-UTM-Campaign, X-Event-Source-URL, X-FB-Pixel-ID, X-Recharge-Template-ID")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
