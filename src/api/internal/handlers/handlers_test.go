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
type (
	MockWarmPoolService struct {
		mock.Mock
	}
	
	MockMetricsService struct {
		mock.Mock
	}
)

// Ensure mock implements the interface
var _ MetricsService = (*MockMetricsService)(nil)
var _ WarmPoolService = (*MockWarmPoolService)(nil)

func (m *MockWarmPoolService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockWarmPoolService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	m.Called(method, path, status, duration, size)
}

func (m *MockMetricsService) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	m.Called(runtime, warmPodUsed)
}

func (m *MockMetricsService) RecordSandboxTermination(runtime string) {
	m.Called(runtime)
}

func (m *MockMetricsService) RecordExecution(execType, runtime, status string, duration time.Duration) {
	m.Called(execType, runtime, status, duration)
}

func (m *MockMetricsService) IncActiveConnections() {
	m.Called()
}

func (m *MockMetricsService) DecActiveConnections() {
	m.Called()
}

func (m *MockMetricsService) RecordWarmPoolHit() {
	m.Called()
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
	mockMetrics := &MockMetricsService{}
	router := gin.New()
	
	router.Use(MetricsMiddleware(mockMetrics))

	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	mockMetrics.On("RecordRequest", "GET", "/test", http.StatusOK, mock.Anything, mock.Anything).Once()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockMetrics.AssertExpectations(t)
}
