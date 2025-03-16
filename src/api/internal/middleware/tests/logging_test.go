package tests

import (
	"bytes"
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
		// Get the variadic arguments
		requestFields = args.Get(1).([]interface{})
		t.Logf("Request fields captured: %+v", requestFields)
	}).Once()
	
	mockLogger.On("Info", "Request completed", mock.Anything).Run(func(args mock.Arguments) {
		// Get the variadic arguments
		responseFields = args.Get(1).([]interface{})
		t.Logf("Response fields captured: %+v", responseFields)
	}).Once()

	config := middleware.LoggingConfig{
		LogRequestBody:  true,
		LogResponseBody: true,
		SensitiveFields: []string{"password", "token", "email", "api_key", "credit_card"},
		MaxBodyLogSize:  4096, // Ensure bodies aren't truncated
	}
	
	router := gin.New()
	router.Use(middleware.LoggingMiddleware(mockLogger, config))
	router.POST("/login", func(c *gin.Context) {
		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err == nil {
			c.JSON(http.StatusOK, gin.H{
				"message": "logged in",
				"token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ",
				"user": data["username"],
				"email": data["email"],
				"api_key": "sk_live_51NXxbTLxmNAjIcThJV9PmvWR9ybXlPfVzBkgJqhcRnWM5ujZEAiLwwrgvgUgtGgQXqnPwGKpK1R",
			})
		}
	})
	
	// Execute
	w := httptest.NewRecorder()
	reqBody := `{
		"username": "testuser", 
		"password": "secret123", 
		"email": "user@example.com", 
		"credit_card": "4242-4242-4242-4242",
		"api_key": "pk_test_51NXxbTLxmNAjIcThJV9PmvWR9ybXlPfVzBkgJqhcRnWM5ujZEAiLwwrgvgUgtGgQXqnPwGKpK1R"
	}`
	req, _ := http.NewRequest("POST", "/login", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Find request body in log fields
	var requestBody map[string]interface{}
	for i := 0; i < len(requestFields); i += 2 {
		t.Logf("Request field %d: %v = %v", i/2, requestFields[i], requestFields[i+1])
		if requestFields[i] == "request_body" {
			t.Logf("Found request_body at index %d", i)
			if body, ok := requestFields[i+1].(map[string]interface{}); ok {
				requestBody = body
				t.Logf("Successfully cast request_body to map: %+v", requestBody)
			} else {
				t.Logf("Failed to cast request_body to map, type: %T, value: %v", requestFields[i+1], requestFields[i+1])
			}
		}
	}
	
	// Find response body in log fields
	var responseBody map[string]interface{}
	for i := 0; i < len(responseFields); i += 2 {
		t.Logf("Response field %d: %v = %v", i/2, responseFields[i], responseFields[i+1])
		if responseFields[i] == "response_body" {
			t.Logf("Found response_body at index %d", i)
			if body, ok := responseFields[i+1].(map[string]interface{}); ok {
				responseBody = body
				t.Logf("Successfully cast response_body to map: %+v", responseBody)
			} else {
				t.Logf("Failed to cast response_body to map, type: %T, value: %v", responseFields[i+1], responseFields[i+1])
			}
		}
	}
	
	// Check that sensitive fields are masked
	assert.NotNil(t, requestBody, "Request body should not be nil")
	if requestBody != nil {
		assert.NotEqual(t, "secret123", requestBody["password"], "Password should be masked")
		assert.Contains(t, requestBody["password"].(string), "...", "Password should use MaskString format")
		assert.Equal(t, "testuser", requestBody["username"], "Username should be preserved")
		assert.NotEqual(t, "user@example.com", requestBody["email"], "Email should be masked")
		assert.Contains(t, requestBody["email"].(string), "...", "Email should use MaskString format")
		assert.NotEqual(t, "4242-4242-4242-4242", requestBody["credit_card"], "Credit card should be masked")
		assert.Contains(t, requestBody["credit_card"].(string), "...", "Credit card should use MaskString format")
		assert.NotEqual(t, "pk_test_51NXxbTLxmNAjIcThJV9PmvWR9ybXlPfVzBkgJqhcRnWM5ujZEAiLwwrgvgUgtGgQXqnPwGKpK1R", requestBody["api_key"], "API key should be masked")
		assert.Contains(t, requestBody["api_key"].(string), "...", "API key should use MaskString format")
	}
	
	assert.NotNil(t, responseBody, "Response body should not be nil")
	if responseBody != nil {
		assert.NotEqual(t, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ", responseBody["token"], "Token should be masked")
		assert.Contains(t, responseBody["token"].(string), "...", "Token should use MaskString format")
		assert.Equal(t, "logged in", responseBody["message"], "Message should be preserved")
		assert.NotEqual(t, "user@example.com", responseBody["email"], "Email should be masked")
		assert.Contains(t, responseBody["email"].(string), "...", "Email should use MaskString format")
		assert.NotEqual(t, "sk_live_51NXxbTLxmNAjIcThJV9PmvWR9ybXlPfVzBkgJqhcRnWM5ujZEAiLwwrgvgUgtGgQXqnPwGKpK1R", responseBody["api_key"], "API key should be masked")
		assert.Contains(t, responseBody["api_key"].(string), "...", "API key should use MaskString format")
	}
	
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
	
	mockLogger.On("Info", "Request completed", mock.Anything).Once()
	
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
