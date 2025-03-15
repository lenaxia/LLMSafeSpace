package tests

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAuthService is a mock implementation of the AuthService interface
type MockAuthService struct {
	mock.Mock
}

type AuthResult struct {
	UserID      string
	Role        string
	APIKey      string
	Permissions []string
}

func (m *MockAuthService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) ValidateToken(ctx context.Context, token string) (AuthResult, error) {
	args := m.Called(ctx, token)
	return args.Get(0).(AuthResult), args.Error(1)
}

func (m *MockAuthService) CheckPermissions(ctx context.Context, userPermissions []string, requiredPermissions []string) bool {
	args := m.Called(ctx, userPermissions, requiredPermissions)
	return args.Bool(0)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockLogger := new(MockLogger)
	mockLogger.On("With", mock.Anything).Return(mockLogger)
	
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateToken", mock.Anything, "valid-token").Return(
		AuthResult{
			UserID:      "user123",
			Role:        "admin",
			APIKey:      "api-key-123",
			Permissions: []string{"read", "write"},
		}, nil)
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, mockLogger))
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
	mockLogger := new(MockLogger)
	mockLogger.On("Warn", mock.Anything, mock.Anything).Once()
	
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateToken", mock.Anything, "invalid-token").Return(
		AuthResult{}, errors.New("invalid token"))
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, mockLogger))
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
	mockLogger := new(MockLogger)
	mockAuth := new(MockAuthService)
	
	config := middleware.AuthConfig{
		SkipPaths: []string{"/public", "/health"},
	}
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, mockLogger, config))
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
	mockLogger := new(MockLogger)
	mockLogger.On("With", mock.Anything).Return(mockLogger)
	
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateToken", mock.Anything, "valid-token").Return(
		AuthResult{
			UserID:      "user123",
			Role:        "user",
			Permissions: []string{"read"},
		}, nil)
	mockAuth.On("CheckPermissions", mock.Anything, []string{"read"}, []string{"write"}).Return(false)
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, mockLogger))
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
	mockLogger := new(MockLogger)
	mockLogger.On("With", mock.Anything).Return(mockLogger)
	
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateToken", mock.Anything, "valid-token").Return(
		AuthResult{
			UserID: "user123",
			Role:   "user",
		}, nil)
	
	router := gin.New()
	router.Use(middleware.AuthMiddleware(mockAuth, mockLogger))
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
