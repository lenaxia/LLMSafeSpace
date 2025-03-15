package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockCacheService is a mock implementation of the CacheService interface
type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheService) GetObject(ctx context.Context, key string, value interface{}) error {
	args := m.Called(ctx, key, value)
	return args.Error(0)
}

func (m *MockCacheService) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	args := m.Called(ctx, sessionID, session, expiration)
	return args.Error(0)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockCacheService) TTL(ctx context.Context, key string) (time.Duration, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockCacheService) ZAdd(ctx context.Context, key string, score float64, member string) error {
	args := m.Called(ctx, key, score, member)
	return args.Error(0)
}

func (m *MockCacheService) ZRemRangeByScore(ctx context.Context, key string, min, max float64) error {
	args := m.Called(ctx, key, min, max)
	return args.Error(0)
}

func (m *MockCacheService) ZCount(ctx context.Context, key string, min, max interface{}) (int, error) {
	args := m.Called(ctx, key, min, max)
	return args.Int(0), args.Error(1)
}

func (m *MockCacheService) ZRange(ctx context.Context, key string, start, stop int) ([]string, error) {
	args := m.Called(ctx, key, start, stop)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockCacheService) Expire(ctx context.Context, key string, expiration time.Duration) error {
	args := m.Called(ctx, key, expiration)
	return args.Error(0)
}

func (m *MockCacheService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func TestRateLimitMiddleware_TokenBucket(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything).Maybe()
	
	mockCache := new(MockCacheService)
	
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
	router.Use(middleware.RateLimitMiddleware(mockCache, mockLogger, config))
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
}

func TestRateLimitMiddleware_FixedWindow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything).Maybe()
	
	mockCache := new(MockCacheService)
	// First request - no existing count
	mockCache.On("Get", mock.Anything, "ratelimit:test-key:default").Return("", nil).Once()
	mockCache.On("Set", mock.Anything, "ratelimit:test-key:default", "1", time.Minute).Return(nil).Once()
	mockCache.On("TTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
	// Second request - count = 1
	mockCache.On("Get", mock.Anything, "ratelimit:test-key:default").Return("1", nil).Once()
	mockCache.On("Set", mock.Anything, "ratelimit:test-key:default", "2", time.Duration(0)).Return(nil).Once()
	mockCache.On("TTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
	// Third request - count = 2, exceeds limit
	mockCache.On("Get", mock.Anything, "ratelimit:test-key:default").Return("2", nil).Once()
	mockCache.On("Set", mock.Anything, "ratelimit:test-key:default", "3", time.Duration(0)).Return(nil).Once()
	mockCache.On("TTL", mock.Anything, "ratelimit:test-key:default").Return(time.Minute, nil).Once()
	
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
	router.Use(middleware.RateLimitMiddleware(mockCache, mockLogger, config))
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
	
	mockCache.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitMiddleware_SlidingWindow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything).Maybe()
	
	mockCache := new(MockCacheService)
	now := time.Now().UnixNano()
	
	// First request
	mockCache.On("ZAdd", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", float64(now), mock.Anything).Return(nil).Once()
	mockCache.On("Expire", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockCache.On("ZRemRangeByScore", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", float64(0), mock.Anything).Return(nil).Once()
	mockCache.On("ZCount", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, "+inf").Return(1, nil).Once()
	mockCache.On("ZRange", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
	// Second request
	mockCache.On("ZAdd", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything).Return(nil).Once()
	mockCache.On("Expire", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockCache.On("ZRemRangeByScore", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", float64(0), mock.Anything).Return(nil).Once()
	mockCache.On("ZCount", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, "+inf").Return(2, nil).Once()
	mockCache.On("ZRange", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
	// Third request - exceeds limit
	mockCache.On("ZAdd", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, mock.Anything).Return(nil).Once()
	mockCache.On("Expire", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything).Return(nil).Once()
	mockCache.On("ZRemRangeByScore", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", float64(0), mock.Anything).Return(nil).Once()
	mockCache.On("ZCount", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", mock.Anything, "+inf").Return(3, nil).Once()
	mockCache.On("ZRange", mock.Anything, "ratelimit:sliding:test-key:default:timestamps", 0, 0).Return([]string{strconv.FormatInt(now, 10)}, nil).Once()
	
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
	router.Use(middleware.RateLimitMiddleware(mockCache, mockLogger, config))
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
	
	mockCache.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestRateLimitMiddleware_ExemptRoles(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockCache := new(MockCacheService)
	
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
	router.Use(middleware.RateLimitMiddleware(mockCache, mockLogger, config))
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
	
	mockCache.AssertNotCalled(t, "Get")
	mockCache.AssertNotCalled(t, "Set")
}
