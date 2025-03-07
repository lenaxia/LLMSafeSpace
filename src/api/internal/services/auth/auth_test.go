package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockDatabaseService struct {
	mock.Mock
}

func (m *MockDatabaseService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) GetUserByID(ctx context.Context, userID string) (map[string]interface{}, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) GetSandboxByID(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) GetUserIDByAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Test successful creation
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	mockDb := new(MockDatabaseService)
	mockCache := new(MockCacheService)

	var dbService database.Service = mockDb
	var cacheService cache.Service = mockCache
	
	service, err := New(cfg, log, dbService, cacheService)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, cfg, service.config)
	assert.Equal(t, mockDb, service.dbService)
	assert.Equal(t, mockCache, service.cacheService)
	assert.Equal(t, []byte("test-secret"), service.jwtSecret)
	assert.Equal(t, 24*time.Hour, service.tokenDuration)

	// Test missing JWT secret
	cfg.Auth.JWTSecret = ""
	service, err = New(cfg, log, dbService, cacheService)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JWT secret is required")
	assert.Nil(t, service)
}

func TestAuthenticateAPIKey(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service with mocks
	var dbService services.DatabaseService = mockDbService
	var cacheService services.CacheService = mockCacheService
	
	service, _ := New(cfg, log, dbService, cacheService)

	// Test case: Valid API key
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "valid-key").Return("user123", nil).Once()
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:valid-key").Return("", errors.New("not found")).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:valid-key", "user123", mock.Anything).Return(nil).Once()

	userID, err := service.AuthenticateAPIKey(context.Background(), "valid-key")
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: Invalid API key
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "invalid-key").Return("", nil).Once()
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:invalid-key").Return("", errors.New("not found")).Once()

	userID, err = service.AuthenticateAPIKey(context.Background(), "invalid-key")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "invalid API key")

	// Test case: Database error
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "error-key").Return("", errors.New("database error")).Once()
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:error-key").Return("", errors.New("not found")).Once()

	userID, err = service.AuthenticateAPIKey(context.Background(), "error-key")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "database error")

	// Test case: Cached API key
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:cached-key").Return("cached-user", nil).Once()

	userID, err = service.AuthenticateAPIKey(context.Background(), "cached-key")
	assert.NoError(t, err)
	assert.Equal(t, "cached-user", userID)

	mockDbService.AssertExpectations(t)
	mockCacheService.AssertExpectations(t)
}

func TestGenerateToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service with mocks
	var dbService services.DatabaseService = mockDbService
	var cacheService services.CacheService = mockCacheService
	
	service, _ := New(cfg, log, dbService, cacheService)

	// Test token generation
	userID := "user123"
	token, err := service.GenerateToken(userID)
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	// Verify token
	parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return service.jwtSecret, nil
	})
	assert.NoError(t, err)
	assert.True(t, parsedToken.Valid)

	// Check claims
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	assert.True(t, ok)
	assert.Equal(t, userID, claims["sub"])
	assert.NotEmpty(t, claims["exp"])
	assert.NotEmpty(t, claims["iat"])
}

func TestValidateToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service with mocks
	var dbService services.DatabaseService = mockDbService
	var cacheService services.CacheService = mockCacheService
	
	service, _ := New(cfg, log, dbService, cacheService)

	// Generate a valid token
	userID := "user123"
	token, _ := service.GenerateToken(userID)

	// Test case: Valid token
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.Anything).Return("", errors.New("not found")).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	extractedUserID, err := service.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, userID, extractedUserID)

	// Test case: Invalid token
	extractedUserID, err = service.ValidateToken("invalid-token")
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)

	// Test case: Expired token
	// Create a token that's already expired
	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	tokenString, _ := expiredToken.SignedString(service.jwtSecret)

	extractedUserID, err = service.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token has expired")

	// Test case: Revoked token
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.Anything).Return("revoked", nil).Once()

	extractedUserID, err = service.ValidateToken(token)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token has been revoked")

	mockCacheService.AssertExpectations(t)
}

func TestRevokeToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Generate a token
	token, _ := service.GenerateToken("user123")

	// Parse the token to get the jti claim
	parsedToken, _ := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return service.jwtSecret, nil
	})
	claims := parsedToken.Claims.(jwt.MapClaims)
	jti, exists := claims["jti"].(string)
	if !exists {
		// If jti doesn't exist, we'll use a mock value for testing
		jti = "mock-jti"
	}
	exp := time.Unix(int64(claims["exp"].(float64)), 0)

	// Test token revocation
	err := service.RevokeToken(token)
	// Since we haven't set up the mock expectations, this should fail
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to revoke token")

	mockCacheService.AssertExpectations(t)
}

func TestCheckResourceAccess(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	
	// Create service with mocks
	service, _ := New(cfg, log, mockDbService, &cache.Service{})

	// Create a mock gin context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("userID", "user123")

	// Test case: User owns the resource
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-12345").Return(true, nil).Once()

	hasAccess := service.CheckResourceAccess("user123", "sandbox", "sb-12345", "read")
	assert.True(t, hasAccess)

	// Test case: User doesn't own the resource but has permission
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-67890").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-67890", "read").Return(true, nil).Once()

	hasAccess = service.CheckResourceAccess("user123", "sandbox", "sb-67890", "read")
	assert.True(t, hasAccess)

	// Test case: User doesn't own the resource and doesn't have permission
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-noaccess").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-noaccess", "read").Return(false, nil).Once()

	hasAccess = service.CheckResourceAccess("user123", "sandbox", "sb-noaccess", "read")
	assert.False(t, hasAccess)

	// Test case: Database error during ownership check
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-error").Return(false, errors.New("database error")).Once()

	hasAccess = service.CheckResourceAccess("user123", "sandbox", "sb-error", "read")
	assert.False(t, hasAccess)

	// Test case: Database error during permission check
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-permerror").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-permerror", "read").Return(false, errors.New("database error")).Once()

	hasAccess = service.CheckResourceAccess("user123", "sandbox", "sb-permerror", "read")
	assert.False(t, hasAccess)

	mockDbService.AssertExpectations(t)
}

func TestGetUserFromContext(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create service instances
	var dbService services.DatabaseService = &database.Service{}
	var cacheService services.CacheService = &cache.Service{}
	
	service, _ := New(cfg, log, dbService, cacheService)

	// Test case: User ID in context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("userID", "user123")

	userID := service.GetUserID(c)
	assert.Equal(t, "user123", userID)

	// Test case: No user ID in context
	c, _ = gin.CreateTestContext(nil)

	userID = service.GetUserID(c)
	assert.Equal(t, "", userID)
}
