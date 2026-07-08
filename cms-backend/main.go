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
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/billing"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
	"star-novel-cms/internal/novel"
	"star-novel-cms/internal/redis"
	"star-novel-cms/internal/tracking"
	"star-novel-cms/internal/user"
)

func main() {
	// 1. Load config
	config.LoadConfig()

	// 2. Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 3. Connect to DB and Redis
	db.InitDB()
	defer db.CloseDB()

	redis.InitRedis()
	defer redis.CloseRedis()

	// 4. Setup Gin engine
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// 5. CORS Middleware
	r.Use(corsMiddleware())

	// 6. Basic Health check and routing groups
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "UP",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	api := r.Group("/api/v1/admin")
	{
		authRepo := auth.NewAdminRepository()
		authService := auth.NewAdminService(authRepo)
		authHandler := auth.NewAuthHandler(authService)
		authHandler.RegisterRoutes(api)

		novelRepo := novel.NewNovelRepository()
		novelService := novel.NewNovelService(novelRepo)
		novelHandler := novel.NewHandler(novelService)
		novelHandler.RegisterRoutes(api)

		billingRepo := billing.NewBillingRepository()
		billingService := billing.NewBillingService(billingRepo)
		billingHandler := billing.NewHandler(billingService)
		billingHandler.RegisterRoutes(api)

		userRepo := user.NewUserRepository()
		userService := user.NewUserService(userRepo)
		userHandler := user.NewHandler(userService)
		userHandler.RegisterRoutes(api)

		trackingRepo := tracking.NewRepository()
		trackingService := tracking.NewService(trackingRepo)
		trackingHandler := tracking.NewHandler(trackingService)
		trackingHandler.RegisterRoutes(api)
	}

	// 7. Setup graceful shutdown server
	srv := &http.Server{
		Addr:    ":" + config.AppConfig.Port,
		Handler: r,
	}

	go func() {
		log.Printf("Star Novel CMS Backend starting on port %s", config.AppConfig.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down CMS server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("CMS Server forced to shutdown:", err)
	}

	log.Println("CMS Server exiting")
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-FB-FBP, X-FB-FBC, X-UTM-Source, X-UTM-Campaign, X-Event-Source-URL, X-FB-Pixel-ID, X-Recharge-Template-ID")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
