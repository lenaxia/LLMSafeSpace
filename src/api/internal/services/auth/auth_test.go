package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/utilities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockDatabaseService struct {
	mock.Mock
}

// Ensure MockDatabaseService implements the DatabaseService interface
var _ interfaces.DatabaseService = (*MockDatabaseService)(nil)

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

func (m *MockDatabaseService) CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error {
	args := m.Called(ctx, sandboxID, userID, runtime)
	return args.Error(0)
}

func (m *MockDatabaseService) GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
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

func (m *MockCacheService) GetObject(ctx context.Context, key string, value interface{}) error {
	args := m.Called(ctx, key, value)
	return args.Error(0)
}

func (m *MockCacheService) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	args := m.Called(ctx, sessionID, session, expiration)
	return args.Error(0)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
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
	
	service, err := New(cfg, log, mockDb, mockCache)
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
	service, err = New(cfg, log, mockDb, mockCache)
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
	service, _ := New(cfg, log, mockDbService, mockCacheService)

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
}

func TestValidateToken(t *testing.T) {
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
	service, err := New(cfg, log, mockDbService, mockCacheService)
	assert.NoError(t, err)

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
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "token:invalid-token").
		Return("", errors.New("not found")).Once()
	
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

	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "token:"+tokenString).
		Return("", errors.New("not found")).Once()

	extractedUserID, err = service.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token is expired", "should detect expired token")

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
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	service, err := New(cfg, log, mockDbService, mockCacheService)
	assert.NoError(t, err)

	// Generate a valid token
	token, err := service.GenerateToken("user123")

	// Parse the token to get the jti claim
	parsedToken, _ := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return service.jwtSecret, nil
	})
	claims := parsedToken.Claims.(jwt.MapClaims)
	
	// Get token ID (jti) or use subject as fallback
	jti, _ := claims["jti"].(string)
	if jti == "" {
		jti = fmt.Sprintf("%v", claims["sub"])
	}
	
	// Get and validate expiration time
	expClaim, ok := claims["exp"]
	if !ok {
		t.Fatal("token missing expiration claim")
	}
	
	if _, ok := expClaim.(float64); !ok {
		t.Fatal("invalid expiration time format in token")
	}

	// Test token revocation
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), 
		mock.MatchedBy(func(key string) bool { return key[:6] == "token:" }), 
		"revoked", mock.Anything).Return(nil).Once()
	
	err = service.RevokeToken(token)
	assert.NoError(t, err)

	mockCacheService.AssertExpectations(t)
}

func TestCheckResourceAccess(t *testing.T) {
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
	service, err := New(cfg, log, mockDbService, mockCacheService)
	assert.NoError(t, err)

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
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Test case: User ID in context
	c := &gin.Context{}
	c.Set("userID", "user123")

	userID := service.GetUserID(c)
	assert.Equal(t, "user123", userID)

	// Test case: No user ID in context
	c, _ = gin.CreateTestContext(nil)

	userID = service.GetUserID(c)
	assert.Equal(t, "", userID)
}

func TestValidateAPIKey(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour
	cfg.Auth.APIKeyPrefix = "api_"
	
	// Create mock service instances
	mockDbService := new(MockDatabaseService)
	mockCacheService := new(MockCacheService)
	
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Test case: Valid API key (cached)
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_valid").Return("user123", nil).Once()
	
	userID, err := service.validateAPIKey("api_valid")
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: Valid API key (not cached)
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_new").Return("", errors.New("not found")).Once()
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_new").Return("user456", nil).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_new", "user456", mock.Anything).Return(nil).Once()
	
	userID, err = service.validateAPIKey("api_new")
	assert.NoError(t, err)
	assert.Equal(t, "user456", userID)

	// Test case: Invalid API key
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_invalid").Return("", errors.New("not found")).Once()
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_invalid").Return("", nil).Once()
	
	userID, err = service.validateAPIKey("api_invalid")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "invalid API key")

	// Test case: Database error
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_error").Return("", errors.New("not found")).Once()
	mockDbService.On("GetUserIDByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_error").Return("", errors.New("database error")).Once()
	
	userID, err = service.validateAPIKey("api_error")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "database error")

	mockCacheService.AssertExpectations(t)
	mockDbService.AssertExpectations(t)
}

func TestIsAPIKey(t *testing.T) {
	// Test cases
	testCases := []struct {
		name     string
		token    string
		prefix   string
		expected bool
	}{
		{"Valid API key", "api_12345", "api_", true},
		{"Not an API key", "jwt_token", "api_", false},
		{"Empty token", "", "api_", false},
		{"Empty prefix", "api_12345", "", false},
		{"Prefix only", "api_", "api_", true},
		{"Case sensitive", "API_12345", "api_", false},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := utilities.IsAPIKey(tc.token, tc.prefix)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestExtractToken(t *testing.T) {
	// Test cases
	testCases := []struct {
		name     string
		setup    func(*gin.Context)
		expected string
	}{
		{
			"Bearer token",
			func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "Bearer token123")
			},
			"token123",
		},
		{
			"Plain token",
			func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "token123")
			},
			"token123",
		},
		{
			"Query parameter",
			func(c *gin.Context) {
				c.Request.URL.RawQuery = "token=token123"
			},
			"token123",
		},
		{
			"No token",
			func(c *gin.Context) {},
			"",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request, _ = http.NewRequest("GET", "/", nil)
			tc.setup(c)
			
			result := utilities.ExtractToken(c)
			assert.Equal(t, tc.expected, result)
		})
	}
}
