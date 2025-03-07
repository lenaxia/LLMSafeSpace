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
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockDatabaseService struct {
	mock.Mock
	database.Service
}

func (m *MockDatabaseService) GetUserIDByAPIKey(apiKey string) (string, error) {
	args := m.Called(apiKey)
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
	cache.Service
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
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Test successful creation
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, err := New(cfg, log, (*database.Service)(mockDbService), (*cache.Service)(mockCacheService))
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, cfg, service.config)
	assert.Equal(t, mockDbService, service.dbService)
	assert.Equal(t, mockCacheService, service.cacheService)
	assert.Equal(t, []byte("test-secret"), service.jwtSecret)
	assert.Equal(t, 24*time.Hour, service.tokenDuration)

	// Test missing JWT secret
	cfg.Auth.JWTSecret = ""
	service, err = New(cfg, log, mockDbService, mockCacheService)
	assert.Error(t, err)
	assert.Nil(t, service)
	assert.Contains(t, err.Error(), "JWT secret is required")
}

func TestAuthenticateAPIKey(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, (*database.Service)(nil), (*cache.Service)(nil))
	// Replace the nil services with our mocks
	service.dbService = mockDbService
	service.cacheService = mockCacheService

	// Test case: Valid API key
	mockDbService.On("GetUserIDByAPIKey", "valid-key").Return("user123", nil).Once()
	mockCacheService.On("Get", mock.Anything, "apikey:valid-key").Return("", errors.New("not found")).Once()
	mockCacheService.On("Set", mock.Anything, "apikey:valid-key", "user123", mock.Anything).Return(nil).Once()

	userID, err := service.AuthenticateAPIKey("valid-key")
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: Invalid API key
	mockDbService.On("GetUserIDByAPIKey", "invalid-key").Return("", nil).Once()
	mockCacheService.On("Get", mock.Anything, "apikey:invalid-key").Return("", errors.New("not found")).Once()

	userID, err = service.AuthenticateAPIKey("invalid-key")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "invalid API key")

	// Test case: Database error
	mockDbService.On("GetUserIDByAPIKey", "error-key").Return("", errors.New("database error")).Once()
	mockCacheService.On("Get", mock.Anything, "apikey:error-key").Return("", errors.New("not found")).Once()

	userID, err = service.AuthenticateAPIKey("error-key")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "database error")

	// Test case: Cached API key
	mockCacheService.On("Get", mock.Anything, "apikey:cached-key").Return("cached-user", nil).Once()

	userID, err = service.AuthenticateAPIKey("cached-key")
	assert.NoError(t, err)
	assert.Equal(t, "cached-user", userID)

	mockDbService.AssertExpectations(t)
	mockCacheService.AssertExpectations(t)
}

func TestGenerateToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

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
	assert.NotEmpty(t, claims["jti"])
}

func TestValidateToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Generate a valid token
	userID := "user123"
	token, _ := service.GenerateToken(userID)

	// Test case: Valid token
	mockCacheService.On("Get", mock.Anything, mock.Anything).Return("", errors.New("not found")).Once()

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
		"jti": "test-id",
	})
	tokenString, _ := expiredToken.SignedString(service.jwtSecret)

	extractedUserID, err = service.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token is expired")

	// Test case: Revoked token
	mockCacheService.On("Get", mock.Anything, mock.Anything).Return("revoked", nil).Once()

	extractedUserID, err = service.ValidateToken(token)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token has been revoked")

	mockCacheService.AssertExpectations(t)
}

func TestRevokeToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Generate a token
	token, _ := service.GenerateToken("user123")

	// Parse the token to get the jti claim
	parsedToken, _ := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return service.jwtSecret, nil
	})
	claims := parsedToken.Claims.(jwt.MapClaims)
	jti := claims["jti"].(string)
	exp := time.Unix(int64(claims["exp"].(float64)), 0)

	// Test case: Successful revocation
	mockCacheService.On("Set", mock.Anything, "token:"+jti, "revoked", exp.Sub(time.Now())).Return(nil).Once()

	err := service.RevokeToken(token)
	assert.NoError(t, err)

	// Test case: Invalid token
	err = service.RevokeToken("invalid-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse token")

	// Test case: Cache error
	mockCacheService.On("Set", mock.Anything, "token:"+jti, "revoked", mock.Anything).Return(errors.New("cache error")).Once()

	err = service.RevokeToken(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to revoke token")

	mockCacheService.AssertExpectations(t)
}

func TestCheckResourceAccess(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Create a mock gin context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", "user123")

	// Test case: User owns the resource
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-12345").Return(true, nil).Once()

	hasAccess := service.CheckResourceAccess(c, "sandbox", "sb-12345", "read")
	assert.True(t, hasAccess)

	// Test case: User doesn't own the resource but has permission
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-67890").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-67890", "read").Return(true, nil).Once()

	hasAccess = service.CheckResourceAccess(c, "sandbox", "sb-67890", "read")
	assert.True(t, hasAccess)

	// Test case: User doesn't own the resource and doesn't have permission
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-noaccess").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-noaccess", "read").Return(false, nil).Once()

	hasAccess = service.CheckResourceAccess(c, "sandbox", "sb-noaccess", "read")
	assert.False(t, hasAccess)

	// Test case: No user ID in context
	c, _ = gin.CreateTestContext(nil)

	hasAccess = service.CheckResourceAccess(c, "sandbox", "sb-12345", "read")
	assert.False(t, hasAccess)

	// Test case: Database error during ownership check
	c, _ = gin.CreateTestContext(nil)
	c.Set("user_id", "user123")
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-error").Return(false, errors.New("database error")).Once()

	hasAccess = service.CheckResourceAccess(c, "sandbox", "sb-error", "read")
	assert.False(t, hasAccess)

	// Test case: Database error during permission check
	mockDbService.On("CheckResourceOwnership", "user123", "sandbox", "sb-permerror").Return(false, nil).Once()
	mockDbService.On("CheckPermission", "user123", "sandbox", "sb-permerror", "read").Return(false, errors.New("database error")).Once()

	hasAccess = service.CheckResourceAccess(c, "sandbox", "sb-permerror", "read")
	assert.False(t, hasAccess)

	mockDbService.AssertExpectations(t)
}

func TestGetUserFromContext(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Test case: User ID in context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", "user123")

	userID, err := service.GetUserFromContext(c)
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: No user ID in context
	c, _ = gin.CreateTestContext(nil)

	userID, err = service.GetUserFromContext(c)
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "user not authenticated")
}
