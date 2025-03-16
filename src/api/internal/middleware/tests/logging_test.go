package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	logmock "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestLoggingMiddleware_RequestResponse(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Info", "Request received", mock.Anything).Once()
	mockLogger.On("Info", "Request completed", mock.Anything).Once()
	
	config := middleware.LoggingConfig{
		LogRequestBody:  true,
		LogResponseBody: true,
		MaxBodyLogSize:  1024,
	}
	
	router := gin.New()
	router.Use(middleware.LoggingMiddleware(mockLogger, config))
	router.POST("/test", func(c *gin.Context) {
		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err == nil {
			c.JSON(http.StatusOK, gin.H{"message": "success", "data": data})
		}
	})
	
	// Execute
	w := httptest.NewRecorder()
	reqBody := `{"name": "test", "value": 123}`
	req, _ := http.NewRequest("POST", "/test", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "success")
	
	mockLogger.AssertExpectations(t)
}

func TestLoggingMiddleware_SensitiveDataRedaction(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	
	// Capture the log fields for inspection
	var requestFields []interface{}
	var responseFields []interface{}
	
	mockLogger.On("Info", "Request received", mock.Anything).Run(func(args mock.Arguments) {
		requestFields = args.Get(1).([]interface{})
	}).Once()
	
	mockLogger.On("Info", "Request completed", mock.Anything).Run(func(args mock.Arguments) {
		responseFields = args.Get(1).([]interface{})
	}).Once()
	
	config := middleware.LoggingConfig{
		LogRequestBody:  true,
		LogResponseBody: true,
		SensitiveFields: []string{"password", "token"},
	}
	
	router := gin.New()
	router.Use(middleware.LoggingMiddleware(mockLogger, config))
	router.POST("/login", func(c *gin.Context) {
		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err == nil {
			c.JSON(http.StatusOK, gin.H{
				"message": "logged in",
				"token": "secret-jwt-token",
				"user": data["username"],
			})
		}
	})
	
	// Execute
	w := httptest.NewRecorder()
	reqBody := `{"username": "testuser", "password": "secret123"}`
	req, _ := http.NewRequest("POST", "/login", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Find request body in log fields
	var requestBody map[string]interface{}
	for i := 0; i < len(requestFields); i += 2 {
		if requestFields[i] == "request_body" {
			if body, ok := requestFields[i+1].(map[string]interface{}); ok {
				requestBody = body
			}
		}
	}
	
	// Find response body in log fields
	var responseBody map[string]interface{}
	for i := 0; i < len(responseFields); i += 2 {
		if responseFields[i] == "response_body" {
			if body, ok := responseFields[i+1].(map[string]interface{}); ok {
				responseBody = body
			}
		}
	}
	
	// Check that sensitive fields are masked
	assert.NotNil(t, requestBody)
	assert.Equal(t, "********", requestBody["password"])
	assert.Equal(t, "testuser", requestBody["username"])
	
	assert.NotNil(t, responseBody)
	assert.Equal(t, "********", responseBody["token"])
	assert.Equal(t, "logged in", responseBody["message"])
	
	mockLogger.AssertExpectations(t)
}

func TestLoggingMiddleware_BodySizeTruncation(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	
	// Capture the log fields for inspection
	var requestFields []interface{}
	
	mockLogger.On("Info", "Request received", mock.Anything).Run(func(args mock.Arguments) {
		requestFields = args.Get(1).([]interface{})
	}).Once()
	
	mockLogger.On("Info", "Request processed", mock.Anything).Once()
	
	config := middleware.LoggingConfig{
		LogRequestBody: true,
		MaxBodyLogSize: 20, // Very small to force truncation
	}
	
	router := gin.New()
	router.Use(middleware.LoggingMiddleware(mockLogger, config))
	router.POST("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	
	// Execute with large body
	w := httptest.NewRecorder()
	largeBody := strings.Repeat("abcdefghij", 10) // 100 characters
	req, _ := http.NewRequest("POST", "/test", bytes.NewBufferString(largeBody))
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Find request body in log fields
	var requestBodyStr string
	var requestBodySize int
	for i := 0; i < len(requestFields); i += 2 {
		if requestFields[i] == "request_body" {
			if body, ok := requestFields[i+1].(string); ok {
				requestBodyStr = body
			}
		}
		if requestFields[i] == "request_body_size" {
			if size, ok := requestFields[i+1].(int); ok {
				requestBodySize = size
			}
		}
	}
	
	// Check that body was truncated
	assert.Contains(t, requestBodyStr, "... (truncated)")
	assert.Equal(t, 100, requestBodySize)
	assert.True(t, len(requestBodyStr) < 100)
	
	mockLogger.AssertExpectations(t)
}

func TestLoggingMiddleware_SkipPaths(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	// No log calls expected for skipped paths
	
	config := middleware.LoggingConfig{
		SkipPaths: []string{"/health", "/metrics"},
	}
	
	router := gin.New()
	router.Use(middleware.LoggingMiddleware(mockLogger, config))
	router.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "healthy")
	})
	router.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "api")
	})
	
	// Execute request to skipped path
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Execute request to non-skipped path
	mockLogger.On("Info", "Request received", mock.Anything).Once()
	mockLogger.On("Info", "Request completed", mock.Anything).Once()
	
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	
	mockLogger.AssertExpectations(t)
}
