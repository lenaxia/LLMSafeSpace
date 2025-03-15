package middleware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"golang.org/x/time/rate"
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
	
	// BurstSize is the maximum burst size allowed
	BurstSize int
	
	// Strategy defines the rate limiting strategy to use
	Strategy string // "token_bucket", "fixed_window", "sliding_window"
}

// RateLimit defines a rate limit
type RateLimit struct {
	// Requests is the number of requests allowed per window
	Requests int
	
	// Window is the time window for rate limiting
	Window time.Duration
	
	// BurstSize is the maximum burst size allowed for this limit
	BurstSize int
}

// DefaultRateLimitConfig returns the default rate limit configuration
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 1000,
		DefaultWindow: time.Hour,
		BurstSize:    50,
		Strategy:     "token_bucket",
		Limits: map[string]RateLimit{
			"create_sandbox": {100, time.Hour, 10},
			"execute_code":   {500, time.Hour, 20},
			"upload_file":    {300, time.Hour, 15},
			"install_packages": {200, time.Hour, 10},
		},
		ExemptRoles: []string{"admin", "system"},
	}
}

// In-memory limiters for token bucket algorithm
var (
	limiters     = make(map[string]*rate.Limiter)
	limiterMutex sync.RWMutex
)

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
		burstSize := cfg.BurstSize
		
		if customLimit, ok := cfg.Limits[limitType]; ok {
			limit, window = customLimit.Requests, customLimit.Window
			if customLimit.BurstSize > 0 {
				burstSize = customLimit.BurstSize
			}
		}
		
		// Apply rate limiting based on strategy
		switch cfg.Strategy {
		case "token_bucket":
			if !applyTokenBucketRateLimit(c, apiKey.(string), limitType, limit, window, burstSize, log) {
				return
			}
		case "sliding_window":
			if !applySlidingWindowRateLimit(c, apiKey.(string), limitType, limit, window, cacheService, log) {
				return
			}
		default: // "fixed_window"
			if !applyFixedWindowRateLimit(c, apiKey.(string), limitType, limit, window, cacheService, log) {
				return
			}
		}
		
		c.Next()
	}
}

// applyTokenBucketRateLimit applies rate limiting using the token bucket algorithm
func applyTokenBucketRateLimit(c *gin.Context, apiKey, limitType string, limit int, window time.Duration, burstSize int, log *logger.Logger) bool {
	// Create a unique key for this API key and limit type
	key := fmt.Sprintf("%s:%s", apiKey, limitType)
	
	// Get or create limiter
	limiterMutex.RLock()
	limiter, exists := limiters[key]
	limiterMutex.RUnlock()
	
	if !exists {
		// Calculate rate from limit and window
		rate := rate.Limit(float64(limit) / window.Seconds())
		
		// Create new limiter
		limiter = rate.NewLimiter(rate, burstSize)
		
		limiterMutex.Lock()
		limiters[key] = limiter
		limiterMutex.Unlock()
	}
	
	// Try to allow request
	if !limiter.Allow() {
		// Calculate reset time
		resetTime := time.Now().Add(time.Second) // Approximate reset time
		
		// Set rate limit headers
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", "0")
		c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
		
		// Create API error
		apiErr := errors.NewRateLimitError(
			fmt.Sprintf("Rate limit exceeded for %s. Try again later.", limitType),
			limit,
			resetTime.Unix(),
			nil,
		)
		
		// Log rate limit exceeded
		log.Warn("Rate limit exceeded",
			"api_key", maskString(apiKey),
			"limit_type", limitType,
			"limit", limit,
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"request_id", c.GetString("request_id"),
		)
		
		HandleAPIError(c, apiErr)
		return false
	}
	
	// Set rate limit headers
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	
	// Tokens are not directly accessible, so we can't set X-RateLimit-Remaining accurately
	// This is an approximation
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit/2))
	
	return true
}

// applyFixedWindowRateLimit applies rate limiting using the fixed window algorithm
func applyFixedWindowRateLimit(c *gin.Context, apiKey, limitType string, limit int, window time.Duration, cacheService interfaces.CacheService, log *logger.Logger) bool {
	// Create a unique key for this API key and limit type
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
			"api_key", maskString(apiKey),
			"limit_type", limitType,
			"limit", limit,
			"count", count,
			"reset", resetTime,
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"request_id", c.GetString("request_id"),
		)
		
		HandleAPIError(c, apiErr)
		return false
	}
	
	return true
}

// applySlidingWindowRateLimit applies rate limiting using the sliding window algorithm
func applySlidingWindowRateLimit(c *gin.Context, apiKey, limitType string, limit int, window time.Duration, cacheService interfaces.CacheService, log *logger.Logger) bool {
	// Create a unique key for this API key and limit type
	baseKey := fmt.Sprintf("ratelimit:sliding:%s:%s", apiKey, limitType)
	ctx := c.Request.Context()
	
	// Current timestamp
	now := time.Now().UnixNano()
	
	// Add current timestamp to sorted set
	timestampKey := fmt.Sprintf("%s:timestamps", baseKey)
	err := cacheService.ZAdd(ctx, timestampKey, now, strconv.FormatInt(now, 10))
	if err != nil {
		// If we can't track rate limiting, allow the request but log the error
		log.Error("Failed to add timestamp to rate limit set", err,
			"api_key", maskString(apiKey),
			"limit_type", limitType,
		)
		c.Next()
		return true
	}
	
	// Set expiry for the sorted set
	cacheService.Expire(ctx, timestampKey, window*2) // Double the window to ensure we keep history
	
	// Remove timestamps outside the window
	cutoff := now - window.Nanoseconds()
	cacheService.ZRemRangeByScore(ctx, timestampKey, 0, cutoff)
	
	// Count requests in the current window
	count, err := cacheService.ZCount(ctx, timestampKey, cutoff, "+inf")
	if err != nil {
		// If we can't count, allow the request but log the error
		log.Error("Failed to count rate limit timestamps", err,
			"api_key", maskString(apiKey),
			"limit_type", limitType,
		)
		c.Next()
		return true
	}
	
	// Set rate limit headers
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit-count))
	
	// Calculate reset time (when the oldest request falls out of the window)
	oldestTimestamp, err := cacheService.ZRange(ctx, timestampKey, 0, 0)
	resetTime := now + window.Nanoseconds()
	if err == nil && len(oldestTimestamp) > 0 {
		oldest, err := strconv.ParseInt(oldestTimestamp[0], 10, 64)
		if err == nil {
			resetTime = oldest + window.Nanoseconds()
		}
	}
	c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime/1e9, 10)) // Convert to seconds
	
	// Check if limit exceeded
	if count > limit {
		apiErr := errors.NewRateLimitError(
			fmt.Sprintf("Rate limit exceeded for %s. Try again later.", limitType),
			limit,
			resetTime/1e9, // Convert to seconds
			nil,
		)
		
		// Log rate limit exceeded
		log.Warn("Rate limit exceeded",
			"api_key", maskString(apiKey),
			"limit_type", limitType,
			"limit", limit,
			"count", count,
			"reset", resetTime/1e9,
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"request_id", c.GetString("request_id"),
		)
		
		HandleAPIError(c, apiErr)
		return false
	}
	
	return true
}

// getTTL gets the TTL for a key
func getTTL(cacheService interfaces.CacheService, ctx context.Context, key string) (time.Duration, error) {
	ttl, err := cacheService.TTL(ctx, key)
	if err != nil {
		return time.Hour, err
	}
	
	return ttl, nil
}
