// Package httpx defines Reader transport resource and error contracts.
package httpx

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	MaxPageSize     = 100
	MaxOffset       = 100000
	MaxBodyBytes    = 64 << 10
	MaxWebhookBytes = 1 << 20
)

// Policy bounds body reads (including chunked requests) before binding and gives
// downstream DB, cache and provider calls one shared request deadline.
func Policy() gin.HandlerFunc {
	return func(c *gin.Context) {
		budget := 10 * time.Second
		maxBody := int64(MaxBodyBytes)
		path := c.FullPath()
		if strings.Contains(path, "/wallet/webhook/") {
			budget, maxBody = 30*time.Second, MaxWebhookBytes
		} else if strings.Contains(path, "/wallet/recharge/") || strings.Contains(path, "/wallet/purchase-intents") || strings.HasSuffix(path, "/subscription/cancel") {
			budget = 60 * time.Second
		} else if path == "/healthz" {
			budget = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), budget)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Body != nil {
			body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
			_ = c.Request.Body.Close()
			if int64(len(body)) > maxBody {
				Fail(c, http.StatusRequestEntityTooLarge, "body_too_large", "Request body exceeds the size limit", nil)
				return
			}
			if err != nil {
				Fail(c, http.StatusBadRequest, "invalid_body", "Unable to read request body", err)
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}
		c.Next()
	}
}

// Pagination rejects excessive offsets before multiplication can overflow.
func Pagination(c *gin.Context) (limit, offset int, ok bool) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limitErr != nil || page < 1 || limit < 1 || limit > MaxPageSize || page-1 > MaxOffset/limit {
		Fail(c, http.StatusBadRequest, "invalid_pagination", "Page size must be 1–100 and offset at most 100000", nil)
		return 0, 0, false
	}
	return limit, (page - 1) * limit, true
}

// Fail keeps diagnostic details in logs; clients retain the existing error field.
func Fail(c *gin.Context, status int, code, message string, cause error) {
	if cause != nil {
		log.Printf("request_id=%s route=%s code=%s error=%v", c.GetString("RequestID"), c.FullPath(), code, cause)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": message, "code": code})
}
