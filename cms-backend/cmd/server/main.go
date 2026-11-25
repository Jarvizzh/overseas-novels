package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"star-novel-cms/internal/auth"
	"star-novel-cms/internal/billing"
	"star-novel-cms/internal/config"
	"star-novel-cms/internal/db"
	"star-novel-cms/internal/domain"
	"star-novel-cms/internal/feedback"
	"star-novel-cms/internal/novel"
	"star-novel-cms/internal/operations"
	"star-novel-cms/internal/redis"
	"star-novel-cms/internal/storage"
	"star-novel-cms/internal/tracking"
	"star-novel-cms/internal/user"
)

var buildVersion = "dev"

func main() {
	migrate := flag.Bool("migrate", false, "apply pending database migrations and exit")
	backfill := flag.Bool("backfill-chapter-previews", false, "generate missing historical OSS chapter previews and exit")
	flag.Parse()
	if *migrate && *backfill {
		log.Fatal("run migration and preview backfill separately")
	}
	// 1. Load config
	config.LoadConfig()

	// 2. Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 3. Connect to DB and Redis
	db.InitDB(*migrate)
	defer db.CloseDB()
	if *migrate {
		return
	}

	if *backfill {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		store, err := storage.NewContentStorage(config.AppConfig, db.DB)
		if err != nil {
			log.Fatal(err)
		}
		ossStore, ok := store.(*storage.AliyunOSSStorage)
		if !ok {
			log.Println("PostgreSQL previews are available inline")
			return
		}
		if err := ossStore.BackfillChapterPreviews(ctx, db.DB); err != nil {
			log.Fatal(err)
		}
		return
	}

	redis.InitRedis()
	defer redis.CloseRedis()

	// 4. Setup Gin engine
	r := gin.New()
	r.MaxMultipartMemory = 50 << 20 // 50MB max upload memory limit
	r.Use(gin.Logger(), gin.Recovery())

	// 5. Middleware
	r.Use(corsMiddleware(), requestIDMiddleware())

	r.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		dbStatus := "UP"
		if db.DB == nil || db.CheckSchema(ctx) != nil {
			dbStatus = "DOWN"
		}
		redisStatus := "UP"
		if redis.RDB == nil || redis.RDB.Ping(ctx).Err() != nil {
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

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"version": buildVersion,
			"time":    time.Now().Format(time.RFC3339),
		})
	})

	api := r.Group("/api/v1/admin")
	{
		authRepo := auth.NewAdminRepository()
		authService := auth.NewAdminService(authRepo)
		authHandler := auth.NewAuthHandler(authService)
		authHandler.RegisterRoutes(api)

		roleRepo := auth.NewRoleRepository()
		roleService := auth.NewRoleService(roleRepo)
		roleHandler := auth.NewRoleHandler(roleService)
		roleHandler.RegisterRoutes(api)

		contentStorage, err := storage.NewContentStorage(config.AppConfig, db.DB)
		if err != nil {
			log.Fatalf("Failed to initialize content storage: %v", err)
		}
		log.Printf("CMS Content Storage initialized with driver: %s", contentStorage.StorageType())

		if ossStorage, ok := contentStorage.(*storage.AliyunOSSStorage); ok {
			cleanupCtx, stopCleanup := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				ossStorage.RunChapterCleanup(cleanupCtx, db.DB)
			}()
			defer func() {
				stopCleanup()
				<-done
			}()
		}

		coverStore, err := storage.NewCoverOSSStore(config.AppConfig)
		if err != nil {
			log.Fatalf("Failed to initialize cover OSS storage: %v", err)
		}
		log.Printf("CMS Cover OSS Storage enabled=%t", coverStore.IsEnabled())

		novelRepo := novel.NewNovelRepository()
		novelService := novel.NewNovelService(novelRepo, contentStorage, coverStore)
		novelHandler := novel.NewHandler(novelService)
		novelHandler.RegisterRoutes(api)

		billingRepo := billing.NewBillingRepository()
		billingService := billing.NewBillingService(billingRepo)
		billingHandler := billing.NewHandler(billingService)
		billingHandler.RegisterRoutes(api)

		userRepo := user.NewUserRepository()
		userService := user.NewUserService(userRepo, coverStore)
		userHandler := user.NewHandler(userService)
		userHandler.RegisterRoutes(api)

		if err := tracking.RegisterConversionOrders(api, authRepo, os.Getenv("CONVERSION_PRIVATE_URL"), os.Getenv("CONVERSION_PRIVATE_TOKEN")); err != nil {
			log.Fatal(err)
		}

		domainRepo := domain.NewRepository()
		domainService := domain.NewService(domainRepo)
		domainHandler := domain.NewHandler(domainService)
		domainHandler.RegisterRoutes(api)

		feedbackRepo := feedback.NewRepository()
		feedbackService := feedback.NewService(feedbackRepo)
		feedbackHandler := feedback.NewHandler(feedbackService)
		feedbackHandler.RegisterRoutes(api)

		operationsRepo := operations.NewRepository()
		operationsService := operations.NewService(operationsRepo, coverStore)
		operationsHandler := operations.NewHandler(operationsService)
		operationsHandler.RegisterRoutes(api)
	}

	// 7. Setup graceful shutdown server
	srv := &http.Server{
		Addr:    ":" + config.AppConfig.Port,
		Handler: r,
	}

	go func() {
		log.Printf("Star Novel CMS Backend starting on port %s (version=%s)", config.AppConfig.Port, buildVersion)
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
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-FB-FBP, X-FB-FBC, X-UTM-Source, X-UTM-Campaign, X-Event-Source-URL, X-FB-Pixel-ID")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

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
