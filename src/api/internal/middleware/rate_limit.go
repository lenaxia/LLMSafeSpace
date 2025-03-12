package middleware

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// RateLimitConfig defines rate limit configuration
type RateLimitConfig struct {
	// Enabled indicates whether rate limiting is enabled
	Enabled bool
	
	// DefaultLimit is the default number of requests allowed per window
	DefaultLimit int
	
	// DefaultWindow is the default time window for rate limiting
	DefaultWindow time.Duration
	
	// Limits defines custom limits for specific endpoints
	Limits map[string]RateLimit
	
	// ExemptRoles are roles that are exempt from rate limiting
	ExemptRoles []string
}

// RateLimit defines a rate limit
type RateLimit struct {
	// Requests is the number of requests allowed per window
	Requests int
	
	// Window is the time window for rate limiting
	Window time.Duration
}

// DefaultRateLimitConfig returns the default rate limit configuration
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 1000,
		DefaultWindow: time.Hour,
		Limits: map[string]RateLimit{
			"create_sandbox": {100, time.Hour},
			"execute_code":   {500, time.Hour},
			"upload_file":    {300, time.Hour},
			"install_packages": {200, time.Hour},
		},
		ExemptRoles: []string{"admin", "system"},
	}
}

// RateLimitMiddleware returns a middleware that limits request rates
func RateLimitMiddleware(cacheService interfaces.CacheService, log *logger.Logger, config ...RateLimitConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultRateLimitConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Skip if rate limiting is disabled
		if !cfg.Enabled {
			c.Next()
			return
		}
		
		// Get API key from context
		apiKey, exists := c.Get("apiKey")
		if !exists {
			// No API key, skip rate limiting
			c.Next()
			return
		}
		
		// Check if user has an exempt role
		if userRole, exists := c.Get("userRole"); exists {
			for _, exemptRole := range cfg.ExemptRoles {
				if userRole == exemptRole {
					c.Next()
					return
				}
			}
		}
		
		// Determine limit type based on endpoint
		limitType := "default"
		path := c.FullPath()
		method := c.Request.Method
		
		if method == "POST" && strings.HasSuffix(path, "/sandboxes") {
			limitType = "create_sandbox"
		} else if method == "POST" && strings.Contains(path, "/execute") {
			limitType = "execute_code"
		} else if (method == "PUT" || method == "POST") && strings.Contains(path, "/files") {
			limitType = "upload_file"
		} else if method == "POST" && strings.Contains(path, "/packages") {
			limitType = "install_packages"
		}
		
		// Get limit for this type
		limit, window := cfg.DefaultLimit, cfg.DefaultWindow
		if customLimit, ok := cfg.Limits[limitType]; ok {
			limit, window = customLimit.Requests, customLimit.Window
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
			ttlDuration, err := getTTL(cacheService, ctx, key)
			if err == nil {
				ttl = ttlDuration
			}
		}
		
		// Set rate limit headers
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(limit-count))
		
		resetTime := time.Now().Add(ttl).Unix()
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))
		
		// Check if limit exceeded
		if count > limit {
			apiErr := errors.NewRateLimitError(
				fmt.Sprintf("Rate limit exceeded for %s. Try again later.", limitType),
				limit,
				resetTime,
				nil,
			)
			
			// Log rate limit exceeded
			log.Warn("Rate limit exceeded",
				"api_key", maskString(apiKey.(string)),
				"limit_type", limitType,
				"limit", limit,
				"count", count,
				"reset", resetTime,
				"path", path,
				"method", method,
				"request_id", c.GetString("request_id"),
			)
			
			HandleAPIError(c, apiErr)
			return
		}
		
		c.Next()
	}
}

// getTTL gets the TTL for a key
func getTTL(cacheService interfaces.CacheService, ctx interface{}, key string) (time.Duration, error) {
	// This is a simplified implementation
	// In a real implementation, you would use the cache service's TTL method
	ttlStr, err := cacheService.Get(ctx, key+":ttl")
	if err != nil {
		return time.Hour, err
	}
	
	ttlInt, err := strconv.ParseInt(ttlStr, 10, 64)
	if err != nil {
		return time.Hour, err
	}
	
	return time.Duration(ttlInt) * time.Second, nil
}
