package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
)

func TestRequestIDMiddleware_Generation(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	router := gin.New()
	router.Use(middleware.RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		requestID, exists := c.Get("request_id")
		if exists {
			c.String(http.StatusOK, "Request ID: %s", requestID)
		} else {
			c.String(http.StatusInternalServerError, "No request ID found")
		}
	})
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Request ID:")
	
	// Check that request ID is in response header
	requestID := w.Header().Get("X-Request-ID")
	assert.NotEmpty(t, requestID)
	
	// Check that request ID is a valid UUID
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, requestID)
}

func TestRequestIDMiddleware_ExistingID(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	router := gin.New()
	router.Use(middleware.RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		requestID, exists := c.Get("request_id")
		if exists {
			c.String(http.StatusOK, "Request ID: %s", requestID)
		} else {
			c.String(http.StatusInternalServerError, "No request ID found")
		}
	})
	
	// Execute with existing request ID
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "existing-id-12345")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	
	// Since the existing ID is not a valid UUID, it should be replaced
	requestID := w.Header().Get("X-Request-ID")
	assert.NotEqual(t, "existing-id-12345", requestID)
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, requestID)
	
	// Test with valid UUID
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	req.Header.Set("X-Request-ID", validUUID)
	router.ServeHTTP(w, req)
	
	// Valid UUID should be preserved
	assert.Equal(t, validUUID, w.Header().Get("X-Request-ID"))
}
