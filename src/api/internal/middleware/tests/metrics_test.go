package tests

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMetricsService is a mock implementation of the MetricsService interface
type MockMetricsService struct {
	mock.Mock
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

func (m *MockMetricsService) RecordSandboxCreation(runtime string, warmPodUsed bool, userID string) {
	m.Called(runtime, warmPodUsed, userID)
}

func (m *MockMetricsService) RecordSandboxTermination(runtime, reason string) {
	m.Called(runtime, reason)
}

func (m *MockMetricsService) RecordExecution(execType, runtime, status, userID string, duration time.Duration) {
	m.Called(execType, runtime, status, userID, duration)
}

func (m *MockMetricsService) RecordError(errorType, endpoint, code string) {
	m.Called(errorType, endpoint, code)
}

func (m *MockMetricsService) RecordPackageInstallation(runtime, manager, status string) {
	m.Called(runtime, manager, status)
}

func (m *MockMetricsService) RecordFileOperation(operation, status string) {
	m.Called(operation, status)
}

func (m *MockMetricsService) RecordResourceUsage(sandboxID string, cpu float64, memoryBytes int64) {
	m.Called(sandboxID, cpu, memoryBytes)
}

func (m *MockMetricsService) RecordWarmPoolMetrics(runtime, poolName string, utilization float64) {
	m.Called(runtime, poolName, utilization)
}

func (m *MockMetricsService) RecordWarmPoolScaling(runtime, operation, reason string) {
	m.Called(runtime, operation, reason)
}

func (m *MockMetricsService) IncrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *MockMetricsService) DecrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *MockMetricsService) UpdateWarmPoolHitRatio(runtime string, ratio float64) {
	m.Called(runtime, ratio)
}

func TestMetricsMiddleware_RecordRequest(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockMetrics := new(MockMetricsService)
	mockMetrics.On("RecordRequest", "GET", "/api/v1/test", http.StatusOK, mock.Anything, mock.Anything).Once()
	
	router := gin.New()
	router.Use(middleware.MetricsMiddleware(mockMetrics))
	router.GET("/api/v1/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/test", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockMetrics.AssertExpectations(t)
}

func TestMetricsMiddleware_SkipMetricsEndpoints(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockMetrics := new(MockMetricsService)
	// No calls to RecordRequest expected for /metrics endpoint
	
	router := gin.New()
	router.Use(middleware.MetricsMiddleware(mockMetrics))
	router.GET("/metrics", func(c *gin.Context) {
		c.String(http.StatusOK, "metrics data")
	})
	router.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "healthy")
	})
	
	// Execute requests to skipped paths
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	
	mockMetrics.AssertNotCalled(t, "RecordRequest")
}

func TestWebSocketMetricsMiddleware(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockMetrics := new(MockMetricsService)
	mockMetrics.On("IncrementActiveConnections", "chat", mock.Anything).Once()
	mockMetrics.On("DecrementActiveConnections", "chat", mock.Anything).Once()
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "type", Value: "chat"})
		c.Set("userID", "user123") // Add userID to context
		c.Next()
	})
	router.Use(middleware.WebSocketMetricsMiddleware(mockMetrics))
	router.GET("/ws/:type", func(c *gin.Context) {
		c.String(http.StatusOK, "websocket connection")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ws/chat", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockMetrics.AssertExpectations(t)
}

func TestExecutionMetricsMiddleware(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockMetrics := new(MockMetricsService)
	mockMetrics.On("RecordExecution", "code", "python", "200", mock.Anything, mock.Anything).Once()
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "runtime", Value: "python"})
		c.Next()
	})
	router.Use(middleware.ExecutionMetricsMiddleware(mockMetrics))
	router.POST("/execute", func(c *gin.Context) {
		c.String(http.StatusOK, "execution result")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/execute", nil)
	req.Header.Set("X-Execution-Type", "code")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	mockMetrics.AssertExpectations(t)
}
