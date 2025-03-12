package middleware

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
)

// RateLimitMiddleware returns a middleware that limits request rates
func RateLimitMiddleware(cacheService interfaces.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get API key from context
		apiKey, exists := c.Get("apiKey")
		if !exists {
			// No API key, skip rate limiting
			c.Next()
			return
		}
		
		// Determine limit type based on endpoint
		limitType := "default"
		path := c.FullPath()
		method := c.Request.Method
		
		if method == "POST" && strings.HasSuffix(path, "/sandboxes") {
			limitType = "create_sandbox"
		} else if method == "POST" && strings.Contains(path, "/execute") {
			limitType = "execute_code"
		}
		
		// Get limit for this type
		var limit int
		var window time.Duration
		
		switch limitType {
		case "create_sandbox":
			limit = 100
			window = time.Hour
		case "execute_code":
			limit = 500
			window = time.Hour
		default:
			limit = 1000
			window = time.Hour
		}
		
		// Check rate limit
		key := fmt.Sprintf("ratelimit:%s:%s", apiKey, limitType)
		ctx := c.Request.Context()
		
		// Get current count
		countStr, err := cacheService.Get(ctx, key)
		var count int
		
		if err == nil && countStr != "" {
			count, _ = strconv.Atoi(countStr)
		}
		
		// Increment count
		count++
		
		// Set expiry on first request
		var ttl time.Duration
		if count == 1 {
			err = cacheService.Set(ctx, key, strconv.Itoa(count), window)
			ttl = window
		} else {
			err = cacheService.Set(ctx, key, strconv.Itoa(count), 0) // Don't reset TTL
			
			// Get TTL for reset time
			ttlStr, err := cacheService.Get(ctx, key+":ttl")
			if err == nil && ttlStr != "" {
				ttlInt, _ := strconv.ParseInt(ttlStr, 10, 64)
				ttl = time.Duration(ttlInt) * time.Second
			}
		}
		
		// Set rate limit headers
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(limit-count))
		
		resetTime := time.Now().Add(ttl).Unix()
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))
		
		// Check if limit exceeded
		if count > limit {
			c.AbortWithStatusJSON(429, gin.H{
				"error": gin.H{
					"code":    "rate_limited",
					"message": fmt.Sprintf("Rate limit exceeded. Try again in %v", ttl),
				},
			})
			return
		}
		
		c.Next()
	}
}
