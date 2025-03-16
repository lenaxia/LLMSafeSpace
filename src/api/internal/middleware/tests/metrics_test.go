package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestMetricsMiddleware_RecordRequest(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockMetrics := new(mocks.MockMetricsService)
	mockMetrics.On("RecordRequest", "GET", mock.Anything, http.StatusOK, mock.Anything, mock.Anything).Once()
	
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
	mockMetrics := new(mocks.MockMetricsService)
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
	mockMetrics := new(mocks.MockMetricsService)
	mockMetrics.On("IncrementActiveConnections", "chat", mock.Anything).Once()
	mockMetrics.On("DecrementActiveConnections", "chat", mock.Anything).Once()
	
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "type", Value: "chat"})
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
	mockMetrics := new(mocks.MockMetricsService)
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
