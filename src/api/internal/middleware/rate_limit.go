package middleware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
	"golang.org/x/time/rate"
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
	StoragePrefix  string
}

type rateLimitContext struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func RateLimitMiddleware(rl interfaces.RateLimiterService, log *logger.Logger, config RateLimitConfig) gin.HandlerFunc {
	ctx := &rateLimitContext{
		limiters: make(map[string]*rate.Limiter),
	}

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
			err = applyTokenBucketRateLimit(c, ctx, keyStr, limit, burst, log)
		case "fixed_window":
			err = applyFixedWindowRateLimit(c, rl, config, keyStr, limit, log)
		case "sliding_window":
			err = applySlidingWindowRateLimit(c, rl, config, keyStr, limit, log)
		default:
			err = fmt.Errorf("unsupported rate limit strategy: %s", config.Strategy)
		}

		if err != nil {
			if rlErr, ok := err.(*errors.APIError); ok && rlErr.Type == errors.RateLimitExceeded {
				c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
				c.Header("X-RateLimit-Remaining", "0")
				c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(config.DefaultWindow).Unix(), 10))
			}
			c.AbortWithError(rlErr.StatusCode, rlErr)
			return
		}

		c.Next()
	}
}

func applyTokenBucketRateLimit(c *gin.Context, ctx *rateLimitContext, key string, limit, burst int, log *logger.Logger) error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	limiter, exists := ctx.limiters[key]
	if !exists {
		limiter = rate.NewLimiter(rate.Limit(limit), burst)
		ctx.limiters[key] = limiter
	}

	if !limiter.Allow() {
		log.Warn("Rate limit exceeded",
			"api_key", utilities.MaskString(key),
			"limit", strconv.Itoa(limit),
			"burst", strconv.Itoa(burst),
			"path", c.FullPath(),
		)
		return errors.NewRateLimitError("Too many requests")
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limiter.Burst() - limiter.Limit()))
	return nil
}

func applyFixedWindowRateLimit(c *gin.Context, rl interfaces.RateLimiterService, config RateLimitConfig, key string, limit int, log *logger.Logger) error {
	counterKey := fmt.Sprintf("%s:%s:%s", config.StoragePrefix, key, c.FullPath())

	count, err := rl.Increment(c.Request.Context(), counterKey, 1, config.DefaultWindow)
	if err != nil {
		log.Error("Failed to increment rate limit counter", err,
			"api_key", utilities.MaskString(key),
			"key", counterKey,
		)
		return errors.NewInternalServerError("Rate limit service unavailable")
	}

	ttl, err := rl.GetTTL(c.Request.Context(), counterKey)
	if err != nil {
		log.Error("Failed to get rate limit TTL", err,
			"api_key", utilities.MaskString(key),
			"key", counterKey,
		)
	}

	if count > int64(limit) {
		log.Warn("Rate limit exceeded",
			"api_key", utilities.MaskString(key),
			"count", count,
			"limit", limit,
			"window", config.DefaultWindow.String(),
		)
		return errors.NewRateLimitError("Too many requests")
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit - int(count)))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
	return nil
}

func applySlidingWindowRateLimit(c *gin.Context, rl interfaces.RateLimiterService, config RateLimitConfig, key string, limit int, log *logger.Logger) error {
	now := time.Now().UnixNano()
	windowKey := fmt.Sprintf("%s:%s:%s:timestamps", config.StoragePrefix, key, c.FullPath())

	// Add current timestamp to the window
	err := rl.AddToWindow(c.Request.Context(), windowKey, now, strconv.FormatInt(now, 10), config.DefaultWindow)
	if err != nil {
		log.Error("Failed to add timestamp to rate limit window", err,
			"api_key", utilities.MaskString(key),
			"key", windowKey,
		)
		return errors.NewInternalServerError("Rate limit service unavailable")
	}

	// Remove old timestamps
	cutoff := time.Now().Add(-config.DefaultWindow).UnixNano()
	err = rl.RemoveFromWindow(c.Request.Context(), windowKey, cutoff)
	if err != nil {
		log.Error("Failed to clean up rate limit window", err,
			"api_key", utilities.MaskString(key),
			"key", windowKey,
		)
	}

	// Count remaining requests
	count, err := rl.CountInWindow(c.Request.Context(), windowKey, cutoff, now)
	if err != nil {
		log.Error("Failed to count rate limit window entries", err,
			"api_key", utilities.MaskString(key),
			"key", windowKey,
		)
		return errors.NewInternalServerError("Rate limit service unavailable")
	}

	if count > limit {
		log.Warn("Rate limit exceeded",
			"api_key", utilities.MaskString(key),
			"count", count,
			"limit", limit,
			"window", config.DefaultWindow.String(),
		)
		return errors.NewRateLimitError("Too many requests")
	}

	// Get remaining TTL for the window
	ttl, err := rl.GetTTL(c.Request.Context(), windowKey)
	if err != nil {
		log.Error("Failed to get rate limit window TTL", err,
			"api_key", utilities.MaskString(key),
			"key", windowKey,
		)
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(limit - count))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
	return nil
}
