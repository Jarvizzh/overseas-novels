package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func sessionCookieName() string {
	if gin.Mode() == gin.ReleaseMode {
		return "__Host-reader-session"
	}
	return "reader-session"
}

func setSessionCookie(c *gin.Context, raw string, age int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookieName(), raw, age, "/", "", gin.Mode() == gin.ReleaseMode, true)
}
func (h *AuthHandler) respondAuth(c *gin.Context, response AuthResponse) {
	c.Header("Cache-Control", "no-store")
	raw, err := (sessionService{}).create(c.Request.Context(), response.User.ID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Session unavailable"})
		return
	}
	token, err := generateToken(response.User.ID, sessionHash(raw))
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	response.Token = token
	setSessionCookie(c, raw, 180*86400)
	c.JSON(http.StatusOK, response)
}
func (h *AuthHandler) RefreshSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Sec-Fetch-Site")
	if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	raw, err := c.Cookie(sessionCookieName())
	if err != nil || len(raw) != 64 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	id, err := (sessionService{}).refresh(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, ErrInvalidAuth) {
			setSessionCookie(c, "", -1)
			c.AbortWithStatus(http.StatusUnauthorized)
		} else {
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
		return
	}
	u, err := h.service.GetProfile(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	token, err := generateToken(id, sessionHash(raw))
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	setSessionCookie(c, raw, 180*86400)
	c.JSON(http.StatusOK, AuthResponse{Token: token, User: *u})
}
func (h *AuthHandler) Logout(c *gin.Context) {
	if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if raw, err := c.Cookie(sessionCookieName()); err == nil {
		if err := (sessionService{}).logout(c.Request.Context(), raw); err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
	}
	setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}
