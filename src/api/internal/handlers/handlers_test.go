package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockAuthService struct {
	mock.Mock
}

type MockSandboxService struct {
	mock.Mock
}

type MockWarmPoolService struct {
	mock.Mock
}

type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	m.Called(method, path, status, duration, size)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockServices := &services.Services{
		Auth:     &auth.Service{},
		Sandbox:  &sandbox.Service{},
		WarmPool: &warmpool.Service{},
	}

	// Test handler creation
	handlers := New(log, mockServices)
	assert.NotNil(t, handlers)
	assert.NotNil(t, handlers.logger)
	assert.NotNil(t, handlers.services)
	assert.NotNil(t, handlers.sandbox)
	assert.NotNil(t, handlers.warmPool)
	assert.NotNil(t, handlers.runtime)
	assert.NotNil(t, handlers.profile)
	assert.NotNil(t, handlers.user)
}

func TestRegisterRoutes(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockServices := &services.Services{
		Auth:     &auth.Service{},
		Sandbox:  &sandbox.Service{},
		WarmPool: &warmpool.Service{},
	}

	// Create handlers
	handlers := New(log, mockServices)

	// Create a test router
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Register routes
	handlers.RegisterRoutes(router)

	// Test that routes were registered by checking a few key endpoints
	routes := router.Routes()
	
	// Helper function to check if a route exists
	hasRoute := func(method, path string) bool {
		for _, route := range routes {
			if route.Method == method && route.Path == path {
				return true
			}
		}
		return false
	}

	// Check for some expected routes
	assert.True(t, hasRoute("GET", "/api/v1/sandboxes"))
	assert.True(t, hasRoute("POST", "/api/v1/sandboxes"))
	assert.True(t, hasRoute("GET", "/api/v1/warmpools"))
	assert.True(t, hasRoute("GET", "/api/v1/runtimes"))
	assert.True(t, hasRoute("GET", "/api/v1/profiles"))
	assert.True(t, hasRoute("GET", "/api/v1/user"))
	assert.True(t, hasRoute("GET", "/api/v1/sandboxes/:id/stream"))
}

func TestLoggerMiddleware(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")

	// Create a test router with the middleware
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(LoggerMiddleware(log))

	// Add a test route
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check the response
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMetricsMiddleware(t *testing.T) {
	// Create mock metrics service
	mockMetrics := new(MockMetricsService)

	// Create a test router with the middleware
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(MetricsMiddleware(mockMetrics))

	// Add a test route
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Set up expectations
	mockMetrics.On("RecordRequest", "GET", "/test", http.StatusOK, mock.Anything, mock.Anything).Once()

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check the response
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify metrics were recorded
	mockMetrics.AssertExpectations(t)
}
