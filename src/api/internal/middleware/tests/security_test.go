package tests

import (
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

func TestSecurityMiddleware_Headers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything).Maybe()
	
	router := gin.New()
	config := middleware.SecurityConfig{
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		RequireHTTPS:          true,
		Development:           false,
	}
	
	router.Use(middleware.SecurityMiddleware(mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "default-src 'self'", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "none", w.Header().Get("X-Permitted-Cross-Domain-Policies"))
	
	mockLogger.AssertExpectations(t)
}

func TestSecurityMiddleware_CORS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything).Maybe()
	
	router := gin.New()
	config := middleware.SecurityConfig{
		AllowedOrigins: []string{"https://example.com"},
		Development:    false,
	}
	
	router.Use(middleware.SecurityMiddleware(mockLogger, config))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})
	
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	
	mockLogger.AssertExpectations(t)
}

func TestWebSocketSecurityMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	router := gin.New()
	router.Use(middleware.WebSocketSecurityMiddleware(mockLogger, "https://example.com"))
	router.GET("/ws", func(c *gin.Context) {
		c.String(http.StatusOK, "connected")
	})
	
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "13", w.Header().Get("Sec-WebSocket-Version"))
	
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusForbidden, w.Code)
	
	mockLogger.AssertExpectations(t)
}

func TestCSPReportingMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", "CSP violation report", mock.Anything).Once()
	
	router := gin.New()
	router.Use(middleware.CSPReportingMiddleware(mockLogger))
	
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/csp-report", strings.NewReader(`{
		"csp-report": {
			"document-uri": "https://example.com",
			"blocked-uri": "https://evil.com/script.js",
			"violated-directive": "script-src",
			"effective-directive": "script-src",
			"original-policy": "default-src 'self'"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusNoContent, w.Code)
	mockLogger.AssertExpectations(t)
}
