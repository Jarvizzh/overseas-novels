package auth

import (
	"errors"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"reader-backend/internal/model"
)

var guestLimiters = sync.Map{}

func GuestRateLimitMiddleware(rps float64, burst int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiterRaw, _ := guestLimiters.LoadOrStore(ip, rate.NewLimiter(rate.Limit(rps), burst))
		limiter := limiterRaw.(*rate.Limiter)

		if !limiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many guest registration attempts. Please try again later."})
			c.Abort()
			return
		}
		c.Next()
	}
}

type AuthHandler struct {
	service AuthService
}

type AuthResponse struct {
	Token string     `json:"token"`
	User  model.User `json:"user"`
}

func NewAuthHandler(service AuthService) *AuthHandler {
	return &AuthHandler{
		service: service,
	}
}

func (h *AuthHandler) RegisterRoutes(r *gin.RouterGroup) {
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/guest", GuestRateLimitMiddleware(0.1, 5), h.GuestLogin)
		authGroup.POST("/register", h.Register)
		authGroup.POST("/login", h.Login)
		authGroup.GET("/profile", AuthMiddleware(), h.GetProfile)
	}
}

func (h *AuthHandler) GuestLogin(c *gin.Context) {
	ctx := c.Request.Context()

	device := c.GetHeader("User-Agent")
	ipAddress := c.ClientIP()
	utmSource := c.GetHeader("X-UTM-Source")
	utmCampaign := c.GetHeader("X-UTM-Campaign")
	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	userAgent := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	user, token, err := h.service.GuestLogin(ctx, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to login as guest: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		Token: token,
		User:  *user,
	})
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Nickname string `json:"nickname"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	device := c.GetHeader("User-Agent")
	ipAddress := c.ClientIP()
	utmSource := c.GetHeader("X-UTM-Source")
	utmCampaign := c.GetHeader("X-UTM-Campaign")
	fbp := c.GetHeader("X-FB-FBP")
	fbc := c.GetHeader("X-FB-FBC")
	pixelID := c.GetHeader("X-FB-Pixel-ID")
	userAgent := c.Request.UserAgent()
	sourceURL := c.GetHeader("X-Event-Source-URL")
	country := c.GetHeader("X-Country")
	if country == "" {
		country = c.GetHeader("CF-IPCountry")
	}

	user, token, err := h.service.Register(ctx, req.Email, req.Password, req.Nickname, device, ipAddress, utmSource, utmCampaign, fbp, fbc, pixelID, userAgent, sourceURL, country)
	if err != nil {
		if errors.Is(err, ErrEmailExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Registration failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		Token: token,
		User:  *user,
	})
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	user, token, err := h.service.Login(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidAuth) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrAccountBanned) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Login failed"})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		Token: token,
		User:  *user,
	})
}

func (h *AuthHandler) GetProfile(c *gin.Context) {
	userIDVal, _ := c.Get("user_id")
	userID, ok := userIDVal.(int64)
	if !ok {
		// Middleware puts it as int64, but let's be safe
		if floatVal, ok := userIDVal.(float64); ok {
			userID = int64(floatVal)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID type in context"})
			return
		}
	}
	ctx := c.Request.Context()

	user, err := h.service.GetProfile(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}
