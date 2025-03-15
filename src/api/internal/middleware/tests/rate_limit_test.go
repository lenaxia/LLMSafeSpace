package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	logmock "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestRateLimitMiddleware_TokenBucket(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(mocks.MockRateLimiterService)
	// First request - allowed
	mockRateLimiter.On("Allow", "test-key:default", mock.Anything, 2).Return(true).Once()
	// Second request - allowed
	mockRateLimiter.On("Allow", "test-key:default", mock.Anything, 2).Return(true).Once()
	// Third request - denied
	mockRateLimiter.On("Allow", "test-key:default", mock.Anything, 2).Return(false).Once()
	
	config := middleware.RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 2,
		DefaultWindow: time.Minute,
		Strategy:     "token_bucket",
		BurstSize:    2,
	}
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Set API key in context
		c.Set("apiKey", "test-key")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(mockRateLimiter, mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute first request - should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
	
	// Execute second request - should succeed
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Execute third request - should be rate limited
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
	
	mockLogger.AssertExpectations(t)
	mockRateLimiter.AssertExpectations(t)
}

func TestRateLimitMiddleware_FixedWindow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(mocks.MockRateLimiterService)
	// First request - count = 1
	mockRateLimiter.On("Increment", mock.Anything, "ratelimit:test-key:default", int64(1), time.Minute).Return(int64(1), nil).Once()
	mockRateLimiter.On("GetTTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
	// Second request - count = 2
	mockRateLimiter.On("Increment", mock.Anything, "ratelimit:test-key:default", int64(1), time.Minute).Return(int64(2), nil).Once()
	mockRateLimiter.On("GetTTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
	// Third request - count = 3, exceeds limit
	mockRateLimiter.On("Increment", mock.Anything, "ratelimit:test-key:default", int64(1), time.Minute).Return(int64(3), nil).Once()
	mockRateLimiter.On("GetTTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
	config := middleware.RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 2,
		DefaultWindow: time.Minute,
		Strategy:     "fixed_window",
	}
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Set API key in context
		c.Set("apiKey", "test-key")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(mockRateLimiter, mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute first request - should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "1", w.Header().Get("X-RateLimit-Remaining"))
	
	// Execute second request - should succeed
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
	
	// Execute third request - should be rate limited
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	
	mockRateLimiter.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitMiddleware_SlidingWindow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(mocks.MockRateLimiterService)
	now := time.Now().UnixNano()
	
	// First request
	mockRateLimiter.On("AddToWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockRateLimiter.On("RemoveFromWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockRateLimiter.On("CountInWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything).Return(1, nil).Once()
	mockRateLimiter.On("GetWindowEntries", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
	// Second request
	mockRateLimiter.On("AddToWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockRateLimiter.On("RemoveFromWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockRateLimiter.On("CountInWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything).Return(2, nil).Once()
	mockRateLimiter.On("GetWindowEntries", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
	// Third request - exceeds limit
	mockRateLimiter.On("AddToWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockRateLimiter.On("RemoveFromWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockRateLimiter.On("CountInWindow", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything).Return(3, nil).Once()
	mockRateLimiter.On("GetWindowEntries", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
	config := middleware.RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 2,
		DefaultWindow: time.Minute,
		Strategy:     "sliding_window",
	}
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Set API key in context
		c.Set("apiKey", "test-key")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(mockRateLimiter, mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute first request - should succeed
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "1", w.Header().Get("X-RateLimit-Remaining"))
	
	// Execute second request - should succeed
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
	
	// Execute third request - should be rate limited
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	
	mockRateLimiter.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitMiddleware_ExemptRoles(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockRateLimiter := new(mocks.MockRateLimiterService)
	
	config := middleware.RateLimitConfig{
		Enabled:      true,
		DefaultLimit: 1,
		ExemptRoles:  []string{"admin"},
	}
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Set API key and role in context
		c.Set("apiKey", "test-key")
		c.Set("userRole", "admin")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(mockRateLimiter, mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute multiple requests - all should succeed due to exempt role
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		
		assert.Equal(t, http.StatusOK, w.Code)
	}
	
	mockRateLimiter.AssertNotCalled(t, "Increment")
	mockRateLimiter.AssertNotCalled(t, "Allow")
}
