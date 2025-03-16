package tests

import (
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

func TestRecoveryMiddleware_PanicRecovery(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Error", "Recovery from panic", mock.Anything, mock.Anything).Once()
	
	config := middleware.RecoveryConfig{
		IncludeStackTrace: false,
		LogStackTrace:     true,
	}
	
	router := gin.New()
	router.Use(middleware.RecoveryMiddleware(mockLogger, config))
	
	// Handler that panics
	router.GET("/panic", func(c *gin.Context) {
		panic("something went terribly wrong")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/panic", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal_error")
	assert.Contains(t, w.Body.String(), "Internal server error")
	
	mockLogger.AssertExpectations(t)
}

func TestRecoveryMiddleware_CustomHandler(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Error", "Recovery from panic", mock.Anything, mock.Anything).Once()
	
	customHandlerCalled := false
	
	config := middleware.RecoveryConfig{
		CustomRecoveryHandler: func(c *gin.Context, err interface{}) {
			customHandlerCalled = true
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"custom_error": "Service temporarily unavailable",
				"reason":       err,
			})
		},
	}
	
	router := gin.New()
	router.Use(middleware.RecoveryMiddleware(mockLogger, config))
	
	// Handler that panics
	router.GET("/panic", func(c *gin.Context) {
		panic("custom panic")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/panic", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "Service temporarily unavailable")
	assert.Contains(t, w.Body.String(), "custom panic")
	assert.True(t, customHandlerCalled)
	
	mockLogger.AssertExpectations(t)
}

func TestRecoveryMiddleware_StackTraceInResponse(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Error", "Recovery from panic", mock.Anything, mock.Anything).Once()
	
	config := middleware.RecoveryConfig{
		IncludeStackTrace: true,
		LogStackTrace:     true,
	}
	
	router := gin.New()
	router.Use(middleware.RecoveryMiddleware(mockLogger, config))
	
	// Handler that panics
	router.GET("/panic", func(c *gin.Context) {
		panic("stack trace test")
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/panic", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "stack")
	
	// Parse response to check for stack trace
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	
	errorDetails := response["error"].(map[string]interface{})["details"].(map[string]interface{})
	stackTrace := errorDetails["stack"].([]interface{})
	assert.NotEmpty(t, stackTrace)
	
	// Check if any line in the stack trace contains the test function name
	foundTestFunction := false
	for _, line := range stackTrace {
		if strings.Contains(line.(string), "TestRecoveryMiddleware_StackTraceInResponse") {
			foundTestFunction = true
			break
		}
	}
	assert.True(t, foundTestFunction, "Stack trace should contain the test function name")
	
	mockLogger.AssertExpectations(t)
}
