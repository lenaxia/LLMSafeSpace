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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockLogger is a mock implementation of the Logger interface
type MockLogger struct {
	mock.Mock
}

func (m *MockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Error(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

func (m *MockLogger) Fatal(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

func (m *MockLogger) With(keysAndValues ...interface{}) *MockLogger {
	args := m.Called(keysAndValues)
	return args.Get(0).(*MockLogger)
}

func (m *MockLogger) Sync() error {
	args := m.Called()
	return args.Error(0)
}

// MockRateLimiterService is a mock implementation of the RateLimiterService interface
type MockRateLimiterService struct {
	mock.Mock
}

func (m *MockRateLimiterService) Increment(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error) {
	args := m.Called(ctx, key, value, expiration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRateLimiterService) AddToWindow(ctx context.Context, key string, timestamp int64, member string, expiration time.Duration) error {
	args := m.Called(ctx, key, timestamp, member, expiration)
	return args.Error(0)
}

func (m *MockRateLimiterService) RemoveFromWindow(ctx context.Context, key string, cutoff int64) error {
	args := m.Called(ctx, key, cutoff)
	return args.Error(0)
}

func (m *MockRateLimiterService) CountInWindow(ctx context.Context, key string, min, max int64) (int, error) {
	args := m.Called(ctx, key, min, max)
	return args.Int(0), args.Error(1)
}

func (m *MockRateLimiterService) GetWindowEntries(ctx context.Context, key string, start, stop int) ([]string, error) {
	args := m.Called(ctx, key, start, stop)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRateLimiterService) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockRateLimiterService) Allow(key string, rate float64, burst int) bool {
	args := m.Called(key, rate, burst)
	return args.Bool(0)
}

func (m *MockRateLimiterService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRateLimiterService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func TestRateLimitMiddleware_TokenBucket(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(MockRateLimiterService)
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
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(MockRateLimiterService)
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
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	mockRateLimiter := new(MockRateLimiterService)
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
	mockLogger := new(MockLogger)
	mockRateLimiter := new(MockRateLimiterService)
	
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
