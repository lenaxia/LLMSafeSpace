package tests

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	logmock "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("With", mock.Anything).Return(mockLogger).Maybe()
	
	mockAuth := new(mocks.MockAuthMiddlewareService)
	mockAuth.On("ValidateToken", "valid-token").Return("user123", nil)
	mockAuth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Next()
	}))
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, nil))
	router.GET("/protected", func(c *gin.Context) {
		userID, _ := c.Get("userID")
		c.String(http.StatusOK, "user: %s", userID)
	})
	
	// Execute with valid token
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user: user123")
	
	mockAuth.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("Warn", mock.Anything, mock.Anything).Once()
	
	mockAuth := new(mocks.MockAuthMiddlewareService)
	mockAuth.On("ValidateToken", "invalid-token").Return("", errors.New("invalid token"))
	mockAuth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Next()
	}))
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, nil))
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "should not reach here")
	})
	
	// Execute with invalid token
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NotContains(t, w.Body.String(), "should not reach here")
	
	mockAuth.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestAuthMiddleware_SkipPaths(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockAuth := new(mocks.MockAuthMiddlewareService)
	mockAuth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Next()
	}))
	
	config := middleware.AuthConfig{
		SkipPaths: []string{"/public", "/health"},
	}
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, nil, config))
	router.GET("/public", func(c *gin.Context) {
		c.String(http.StatusOK, "public endpoint")
	})
	router.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "protected endpoint")
	})
	
	// Execute public endpoint
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/public", nil)
	router.ServeHTTP(w, req)
	
	// Assert public endpoint works without token
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "public endpoint")
	
	// Execute protected endpoint
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	router.ServeHTTP(w, req)
	
	// Assert protected endpoint requires auth
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	
	mockAuth.AssertExpectations(t)
}

func TestRequirePermissions(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("With", mock.Anything).Return(mockLogger).Maybe()
	
	mockAuth := new(mocks.MockAuthMiddlewareService)
	mockAuth.On("ValidateToken", "valid-token").Return("user123", nil)
	mockAuth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Next()
	}))
	mockAuth.On("CheckResourceAccess", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false)
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, nil))
	router.GET("/write-access", middleware.RequirePermissions("write"), func(c *gin.Context) {
		c.String(http.StatusOK, "write access granted")
	})
	
	// Execute with insufficient permissions
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/write-access", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusForbidden, w.Code)
	
	mockAuth.AssertExpectations(t)
}

func TestRequireRoles(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := logmock.NewMockLogger()
	mockLogger.On("With", mock.Anything).Return(mockLogger).Maybe()
	
	mockAuth := new(mocks.MockAuthMiddlewareService)
	mockAuth.On("ValidateToken", "valid-token").Return("user123", nil)
	mockAuth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Next()
	}))
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, nil))
	router.GET("/admin-only", middleware.RequireRoles("admin"), func(c *gin.Context) {
		c.String(http.StatusOK, "admin access granted")
	})
	
	// Execute with insufficient role
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin-only", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	router.ServeHTTP(w, req)
	
	// Assert
	assert.Equal(t, http.StatusForbidden, w.Code)
	
	mockAuth.AssertExpectations(t)
}
