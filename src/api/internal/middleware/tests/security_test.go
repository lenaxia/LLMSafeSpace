package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockLogger is a mock implementation of the logger
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

func (m *MockLogger) With(keysAndValues ...interface{}) logger.LoggerInterface {
	args := m.Called(keysAndValues)
	return args.Get(0).(logger.LoggerInterface)
}

func (m *MockLogger) Sync() error {
	args := m.Called()
	return args.Error(0)
}

func TestSecurityMiddleware_Headers(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
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
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "default-src 'self'", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "none", w.Header().Get("X-Permitted-Cross-Domain-Policies"))
	
	mockLogger.AssertExpectations(t)
}

func TestSecurityMiddleware_CORS(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
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
	
	// Execute
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	
	// Test disallowed origin
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)
	
	// Origin should not be allowed
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	
	mockLogger.AssertExpectations(t)
}

func TestWebSocketSecurityMiddleware(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	router := gin.New()
	router.Use(middleware.WebSocketSecurityMiddleware(mockLogger, "https://example.com"))
	router.GET("/ws", func(c *gin.Context) {
		c.String(http.StatusOK, "connected")
	})
	
	// Test valid WebSocket connection
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)
	
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "13", w.Header().Get("Sec-WebSocket-Version"))
	
	// Test invalid origin
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)
	
	// Should be forbidden
	assert.Equal(t, http.StatusForbidden, w.Code)
	
	mockLogger.AssertExpectations(t)
}

func TestCSPReportingMiddleware(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", "CSP violation report", mock.Anything).Once()
	
	router := gin.New()
	router.Use(middleware.CSPReportingMiddleware(mockLogger))
	
	// Test CSP violation report
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
