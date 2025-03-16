package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
)

type RateLimitConfig struct {
	Enabled        bool
	DefaultLimit   int
	DefaultWindow  time.Duration
	BurstSize      int
	Strategy       string
	ExemptRoles    []string
	CustomLimits   map[string]int
	CustomBursts   map[string]int
}

func RateLimitMiddleware(rl interfaces.RateLimiterService, log pkginterfaces.LoggerInterface, config RateLimitConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		// Check for exempt roles
		if role, exists := c.Get("userRole"); exists {
			for _, exemptRole := range config.ExemptRoles {
				if role == exemptRole {
					c.Next()
					return
				}
			}
		}

		apiKey, exists := c.Get("apiKey")
		if !exists {
			c.Next()
			return
		}

		keyStr := apiKey.(string)
		hashedKey := utilities.HashString(keyStr)
		limit := config.DefaultLimit
		burst := config.BurstSize

		// Check for custom limits
		if customLimit, ok := config.CustomLimits[keyStr]; ok {
			limit = customLimit
		}
		if customBurst, ok := config.CustomBursts[keyStr]; ok {
			burst = customBurst
		}

		var err error
		switch config.Strategy {
		case "token_bucket":
			err = applyTokenBucketRateLimit(c, rl, hashedKey, limit, burst, log)
		case "fixed_window":
			err = applyFixedWindowRateLimit(c, rl, config, hashedKey, limit, log)
		case "sliding_window":
			err = applySlidingWindowRateLimit(c, rl, config, hashedKey, limit, log)
		case "":
			// Default to token bucket if no strategy specified
			err = applyTokenBucketRateLimit(c, rl, hashedKey, limit, burst, log)
		default:
			err = fmt.Errorf("unsupported rate limit strategy: %s", config.Strategy)
		}

		if err != nil {
			if apiErr, ok := err.(*errors.APIError); ok && apiErr.Type == errors.ErrorTypeRateLimit {
				c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
				c.Header("X-RateLimit-Remaining", "0")
				c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(config.DefaultWindow).Unix(), 10))
				c.AbortWithStatusJSON(apiErr.StatusCode(), gin.H{
					"error": gin.H{
						"code":    apiErr.Code,
						"message": apiErr.Message,
						"details": apiErr.Details,
					},
				})
				return
			}
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		c.Next()
	}
}

func applyTokenBucketRateLimit(c *gin.Context, rl interfaces.RateLimiterService, key string, limit, burst int, log pkginterfaces.LoggerInterface) error {
	// Calculate rate from limit (requests per second)
	rate := float64(limit)
	
	// Use the RateLimiterService.Allow method
	if !rl.Allow(key, rate, burst) {
		log.Warn("Rate limit exceeded",
			"hashed_key", key,
			"limit", strconv.Itoa(limit),
			"burst", strconv.Itoa(burst),
			"path", c.FullPath(),
		)
		resetTime := time.Now().Add(time.Second).Unix() // Approximate reset time
		return errors.NewRateLimitError("Too many requests", limit, resetTime, nil)
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	// Since we're not tracking tokens directly anymore, we can approximate remaining
	remaining := burst - 1 // Assume one token used
	if remaining < 0 {
		remaining = 0
	}
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	return nil
}

func applyFixedWindowRateLimit(c *gin.Context, rl interfaces.RateLimiterService, config RateLimitConfig, key string, limit int, log pkginterfaces.LoggerInterface) error {
	counterKey := fmt.Sprintf("ratelimit:%s:fixed_window", key)

	count, err := rl.Increment(c.Request.Context(), counterKey, 1, config.DefaultWindow)
	if err != nil {
		log.Error("Failed to increment rate limit counter", err,
			"hashed_key", key,
			"key", counterKey,
		)
		return errors.NewInternalError("Rate limit service unavailable", err)
	}

	ttl, err := rl.GetTTL(c.Request.Context(), counterKey)
	if err != nil {
		log.Error("Failed to get rate limit TTL", err,
			"hashed_key", key,
			"key", counterKey,
		)
	}

	if count > int64(limit) {
		log.Warn("Rate limit exceeded",
			"hashed_key", key,
			"count", count,
			"limit", limit,
			"window", config.DefaultWindow.String(),
		)
		resetTime := time.Now().Add(ttl).Unix()
		return errors.NewRateLimitError("Too many requests", limit, resetTime, nil)
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit - int(count)))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
	return nil
}

func applySlidingWindowRateLimit(c *gin.Context, rl interfaces.RateLimiterService, config RateLimitConfig, key string, limit int, log pkginterfaces.LoggerInterface) error {
	now := time.Now().UnixNano()
	windowKey := fmt.Sprintf("ratelimit:%s:sliding_window", key)

	// Add current timestamp to the window
	err := rl.AddToWindow(c.Request.Context(), windowKey, now, strconv.FormatInt(now, 10), config.DefaultWindow)
	if err != nil {
		log.Error("Failed to add timestamp to rate limit window", err,
			"hashed_key", key,
			"key", windowKey,
		)
		return errors.NewInternalError("Rate limit service unavailable", err)
	}

	// Remove old timestamps
	cutoff := time.Now().Add(-config.DefaultWindow).UnixNano()
	err = rl.RemoveFromWindow(c.Request.Context(), windowKey, cutoff)
	if err != nil {
		log.Error("Failed to clean up rate limit window", err,
			"hashed_key", key,
			"key", windowKey,
		)
	}

	// Count remaining requests
	count, err := rl.CountInWindow(c.Request.Context(), windowKey, cutoff, now)
	if err != nil {
		log.Error("Failed to count rate limit window entries", err,
			"hashed_key", key,
			"key", windowKey,
		)
		return errors.NewInternalError("Rate limit service unavailable", err)
	}

	if count > limit {
		log.Warn("Rate limit exceeded",
			"hashed_key", key,
			"count", count,
			"limit", limit,
			"window", config.DefaultWindow.String(),
		)
		resetTime := time.Now().Add(config.DefaultWindow).Unix()
		return errors.NewRateLimitError("Too many requests", limit, resetTime, nil)
	}

	// Get remaining TTL for the window
	ttl, err := rl.GetTTL(c.Request.Context(), windowKey)
	if err != nil {
		log.Error("Failed to get rate limit window TTL", err,
			"hashed_key", key,
			"key", windowKey,
		)
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit - count))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
	return nil
}
