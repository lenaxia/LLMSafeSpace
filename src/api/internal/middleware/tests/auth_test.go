package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) ValidateToken(token string) (string, error) {
	args := m.Called(token)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) GetUserID(c *gin.Context) string {
	args := m.Called(c)
	return args.String(0)
}

func (m *MockAuthService) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0)
}

func (m *MockAuthService) GenerateToken(userID string) (string, error) {
	args := m.Called(userID)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthenticateAPIKey(ctx interface{}, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
}

func TestAuthMiddleware(t *testing.T) {
	mockAuthService := new(MockAuthService)
	mockLogger := pkginterfaces.LoggerInterface(nil) // Use a no-op logger

	// Test case: Valid token
	mockAuthService.On("ValidateToken", "valid_token").Return("user_id", nil)
	r := setupTestRouter(mockAuthService, mockLogger)
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer valid_token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user_id", w.Header().Get("X-User-ID"))

	// Test case: Invalid token
	mockAuthService.On("ValidateToken", "invalid_token").Return("", errors.NewAuthenticationError("Invalid token", nil))
	r = setupTestRouter(mockAuthService, mockLogger)
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid_token")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: No token provided
	r = setupTestRouter(mockAuthService, mockLogger)
	req, _ = http.NewRequest("GET", "/test", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Clean up
	mockAuthService.AssertExpectations(t)
}

func setupTestRouter(authService interfaces.AuthService, logger pkginterfaces.LoggerInterface) *gin.Engine {
	r := gin.New()
	r.Use(middleware.AuthMiddleware(authService, logger))
	r.GET("/test", func(c *gin.Context) {
		userID, _ := c.Get("userID")
		c.Writer.Header().Set("X-User-ID", userID.(string))
		c.Status(http.StatusOK)
	})
	return r
}
