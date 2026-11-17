package auth

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"reader-backend/internal/httpx"
	"reader-backend/internal/model"
)

func GuestRateLimitMiddleware(rps float64, burst int) gin.HandlerFunc {
	type entry struct {
		limiter *rate.Limiter
		seen    time.Time
	}
	var mu sync.Mutex
	entries := make(map[string]entry)
	return func(c *gin.Context) {
		now := time.Now()
		ip := c.ClientIP()
		mu.Lock()
		if len(entries) >= 10000 {
			for key, v := range entries {
				if now.Sub(v.seen) > time.Hour {
					delete(entries, key)
				}
			}
		}
		v, exists := entries[ip]
		if !exists {
			if len(entries) >= 10000 {
				mu.Unlock()
				c.AbortWithStatus(429)
				return
			}
			v.limiter = rate.NewLimiter(rate.Limit(rps), burst)
		}
		v.seen = now
		entries[ip] = v
		allowed := v.limiter.Allow()
		mu.Unlock()
		if !allowed {
			c.AbortWithStatusJSON(429, gin.H{"error": "Too many authentication attempts"})
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
		authGroup.POST("/refresh", h.RefreshSession)
		authGroup.POST("/logout", h.Logout)
		authGroup.POST("/register", GuestRateLimitMiddleware(0.1, 5), h.Register)
		authGroup.POST("/login", GuestRateLimitMiddleware(0.2, 10), h.Login)
		authGroup.POST("/bind", AuthMiddleware(), h.BindEmail)
		authGroup.GET("/profile", AuthMiddleware(), h.GetProfile)
	}
}

func (h *AuthHandler) GuestLogin(c *gin.Context) {
	if raw, err := c.Cookie(sessionCookieName()); err == nil && raw != "" {
		h.RefreshSession(c)
		return
	}
	ctx := promotionContext(c.Request.Context(), c.GetHeader("X-Promo-ID"))

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
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to login as guest", err)
		return
	}

	h.respondAuth(c, AuthResponse{
		Token: token,
		User:  *user,
	})
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=6,max=72"`
	Nickname string `json:"nickname" binding:"max=100"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := promotionContext(c.Request.Context(), c.GetHeader("X-Promo-ID"))

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
		if errors.Is(err, ErrInvalidPassword) {
			httpx.Fail(c, 400, "invalid_password", ErrInvalidPassword.Error(), nil)
			return
		}
		if errors.Is(err, ErrEmailExists) || errors.Is(err, ErrAlreadyBound) {
			if errors.Is(err, ErrEmailExists) {
				httpx.Fail(c, 400, "email_exists", ErrEmailExists.Error(), nil)
			} else {
				httpx.Fail(c, 400, "already_bound", ErrAlreadyBound.Error(), nil)
			}
			return
		}
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Registration failed", err)
		return
	}

	h.respondAuth(c, AuthResponse{
		Token: token,
		User:  *user,
	})
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,max=72"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := promotionContext(c.Request.Context(), c.GetHeader("X-Promo-ID"))

	user, token, err := h.service.Login(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidAuth) {
			httpx.Fail(c, 401, "invalid_auth", ErrInvalidAuth.Error(), nil)
			return
		}
		if errors.Is(err, ErrAccountBanned) {
			httpx.Fail(c, 403, "account_banned", ErrAccountBanned.Error(), nil)
			return
		}
		httpx.Fail(c, 500, "internal_error", "Login failed", err)
		return
	}

	h.respondAuth(c, AuthResponse{
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
	ctx := promotionContext(c.Request.Context(), c.GetHeader("X-Promo-ID"))

	user, err := h.service.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httpx.Fail(c, 404, "user_not_found", ErrUserNotFound.Error(), nil)
		} else {
			httpx.Fail(c, 503, "profile_unavailable", "Profile unavailable", err)
		}
		return
	}

	if _, err := c.Cookie(sessionCookieName()); err != nil {
		raw, err := (sessionService{}).create(ctx, userID)
		if err != nil {
			c.AbortWithStatus(503)
			return
		}
		setSessionCookie(c, raw, 180*86400)
	}
	c.JSON(http.StatusOK, user)
}

type BindEmailRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=6,max=72"`
	Nickname string `json:"nickname" binding:"max=100"`
}

func (h *AuthHandler) BindEmail(c *gin.Context) {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID, ok := userIDVal.(int64)
	if !ok {
		if floatVal, ok := userIDVal.(float64); ok {
			userID = int64(floatVal)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID type in context"})
			return
		}
	}

	var req BindEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, "invalid_request", "Invalid request fields", nil)
		return
	}

	ctx := promotionContext(c.Request.Context(), c.GetHeader("X-Promo-ID"))
	user, token, err := h.service.BindEmail(ctx, userID, req.Email, req.Password, req.Nickname)
	if err != nil {
		if errors.Is(err, ErrInvalidPassword) {
			httpx.Fail(c, 400, "invalid_password", ErrInvalidPassword.Error(), nil)
			return
		}
		if errors.Is(err, ErrEmailExists) || errors.Is(err, ErrAlreadyBound) {
			if errors.Is(err, ErrEmailExists) {
				httpx.Fail(c, 400, "email_exists", ErrEmailExists.Error(), nil)
			} else {
				httpx.Fail(c, 400, "already_bound", ErrAlreadyBound.Error(), nil)
			}
			return
		}
		httpx.Fail(c, http.StatusInternalServerError, "internal_error", "Failed to bind email", err)
		return
	}

	h.respondAuth(c, AuthResponse{
		Token: token,
		User:  *user,
	})
}
