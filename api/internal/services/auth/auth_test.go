package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/api/internal/utilities"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")

	// Test successful creation
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour

	mockDb := new(mocks.MockDatabaseService)
	mockCache := new(mocks.MockCacheService)

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

	// Create service with mocks
	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Test case: Valid API key
	user := &types.User{
		ID: "user123",
	}
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "valid-key").Return(user, nil).Once()
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:valid-key").Return("", errors.New("not found")).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:valid-key", "user123", mock.Anything).Return(nil).Once()

	userID, err := service.AuthenticateAPIKey(context.Background(), "valid-key")
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: Invalid API key
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "invalid-key").Return((*types.User)(nil), nil).Once()
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:invalid-key").Return("", errors.New("not found")).Once()

	userID, err = service.AuthenticateAPIKey(context.Background(), "invalid-key")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "invalid API key")

	// Test case: Database error
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "error-key").Return((*types.User)(nil), errors.New("database error")).Once()
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
	// Test case: API key validation
	apiKey := "api_test_key"
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:"+apiKey).
		Return("", errors.New("not found")).Once()

	user = &types.User{
		ID: "api_user",
	}
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), apiKey).
		Return(user, nil).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }),
		"apikey:"+apiKey, "api_user", mock.Anything).Return(nil).Once()

	// Configure the service to recognize API keys
	service.config.Auth.APIKeyPrefix = "api_"

	userID, err = service.ValidateToken(apiKey)
	assert.NoError(t, err)
	assert.Equal(t, "api_user", userID)

	mockCacheService.AssertExpectations(t)
	mockDbService.AssertExpectations(t)
}

func TestGenerateToken(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")

	// Create service
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.TokenDuration = 24 * time.Hour

	// Create mock service instances
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

	// Create service with mocks
	service, err := New(cfg, log, mockDbService, mockCacheService)
	assert.NoError(t, err)

	// Generate a valid token
	userID := "user123"
	token, _ := service.GenerateToken(userID)

	// Test case: Valid token
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "token:")
	})).Return("", errors.New("not found")).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "token:")
	}), userID, mock.Anything).Return(nil).Once()

	// Configure the service to recognize API keys
	service.config.Auth.APIKeyPrefix = "api_"

	extractedUserID, err := service.ValidateToken(token)
	assert.NoError(t, err)
	assert.Equal(t, userID, extractedUserID)

	// Test case: Invalid token
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "token:")
	})).Return("", errors.New("not found")).Once()

	// For API key format tokens, we need to mock the database call
	user := (*types.User)(nil)
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "invalid-token").
		Return(user, errors.New("invalid API key")).Maybe()

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

	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "token:")
	})).Return("", errors.New("not found")).Once()

	// For API key format tokens, we need to mock the database call
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), tokenString).
		Return((*types.User)(nil), errors.New("invalid API key")).Maybe()

	extractedUserID, err = service.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Equal(t, "", extractedUserID)
	assert.Contains(t, err.Error(), "token is expired", "should detect expired token")

	// Test case: Revoked token
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "token:")
	})).Return("revoked", nil).Once()

	// For API key format tokens, we need to mock the database call
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), token).
		Return((*types.User)(nil), errors.New("invalid API key")).Maybe()

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

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
	mockDbService := new(mocks.MockDatabaseService)
	mockCacheService := new(mocks.MockCacheService)

	service, _ := New(cfg, log, mockDbService, mockCacheService)

	// Test case: Valid API key (cached)
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_valid").Return("user123", nil).Once()

	userID, err := service.validateAPIKey("api_valid")
	assert.NoError(t, err)
	assert.Equal(t, "user123", userID)

	// Test case: Valid API key (not cached)
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_new").Return("", errors.New("not found")).Once()

	user := &types.User{
		ID: "user456",
	}
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_new").Return(user, nil).Once()
	mockCacheService.On("Set", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_new", "user456", mock.Anything).Return(nil).Once()

	userID, err = service.validateAPIKey("api_new")
	assert.NoError(t, err)
	assert.Equal(t, "user456", userID)

	// Test case: Invalid API key
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_invalid").Return("", errors.New("not found")).Once()
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_invalid").Return((*types.User)(nil), nil).Once()

	userID, err = service.validateAPIKey("api_invalid")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "invalid API key")

	// Test case: Database error
	mockCacheService.On("Get", mock.MatchedBy(func(ctx context.Context) bool { return true }), "apikey:api_error").Return("", errors.New("not found")).Once()
	mockDbService.On("GetUserByAPIKey", mock.MatchedBy(func(ctx context.Context) bool { return true }), "api_error").Return((*types.User)(nil), errors.New("database error")).Once()

	userID, err = service.validateAPIKey("api_error")
	assert.Error(t, err)
	assert.Equal(t, "", userID)
	assert.Contains(t, err.Error(), "database error")

	mockCacheService.AssertExpectations(t)
	mockDbService.AssertExpectations(t)
}

func TestIsAPIKey(t *testing.T) {
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
		{"Prefix with separator", "api_12345", "api_", true},
		{"Case sensitive", "API_12345", "api_", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := utilities.IsAPIKey(tc.token, tc.prefix)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func newTestService(t *testing.T) (*Service, *mocks.MockDatabaseService, *mocks.MockCacheService) {
	t.Helper()
	log, _ := logger.New(true, "debug", "console")
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret-1234567890"
	cfg.Auth.TokenDuration = 24 * time.Hour
	cfg.Auth.APIKeyPrefix = "lsp_"
	mockDb := new(mocks.MockDatabaseService)
	mockCache := new(mocks.MockCacheService)
	svc, err := New(cfg, log, mockDb, mockCache)
	require.NoError(t, err)
	return svc, mockDb, mockCache
}

func TestRegister_Success(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "new@example.com").Return(nil, nil)
	mockDb.On("CountUsers", ctx).Return(5, nil) // existing users → role=user
	mockDb.On("CreateUser", ctx, mock.MatchedBy(func(u *types.User) bool {
		return u.Email == "new@example.com" && u.Username == "newuser" && u.PasswordHash != "" && u.Active && u.Role == "user"
	})).Return(nil)

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "newuser",
		Email:    "new@example.com",
		Password: "securepassword123",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Token, "response must include a JWT")
	assert.Equal(t, "newuser", resp.User.Username)
	assert.Equal(t, "new@example.com", resp.User.Email)
	assert.Empty(t, resp.User.PasswordHash, "password hash must not be in response")
	mockDb.AssertExpectations(t)
}

// TestRegister_FirstUserBecomesAdmin verifies that the first user registered
// in a fresh installation is auto-promoted to admin. Otherwise the system
// has no admin and is effectively inert (no way to grant admin powers).
func TestRegister_FirstUserBecomesAdmin(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "first@example.com").Return(nil, nil)
	mockDb.On("CountUsers", ctx).Return(0, nil) // empty system → first user is admin
	mockDb.On("CreateUser", ctx, mock.MatchedBy(func(u *types.User) bool {
		return u.Email == "first@example.com" && u.Role == "admin"
	})).Return(nil)

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "founder",
		Email:    "first@example.com",
		Password: "securepassword123",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "admin", resp.User.Role, "first user must be promoted to admin")
	mockDb.AssertExpectations(t)
}

// TestRegister_SubsequentUsersAreNotAdmin verifies that after the first user
// exists, subsequent registrations get role=user. Only the very first user
// (CountUsers == 0 at registration time) is auto-promoted.
func TestRegister_SubsequentUsersAreNotAdmin(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "second@example.com").Return(nil, nil)
	mockDb.On("CountUsers", ctx).Return(1, nil) // one existing user
	mockDb.On("CreateUser", ctx, mock.MatchedBy(func(u *types.User) bool {
		return u.Email == "second@example.com" && u.Role == "user"
	})).Return(nil)

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "regular",
		Email:    "second@example.com",
		Password: "securepassword123",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "user", resp.User.Role)
	mockDb.AssertExpectations(t)
}

// TestRegister_CountUsersError_FailsClosed verifies that a CountUsers failure
// blocks registration rather than silently defaulting to admin. We must not
// accidentally promote on transient DB errors.
func TestRegister_CountUsersError_FailsClosed(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "any@example.com").Return(nil, nil)
	mockDb.On("CountUsers", ctx).Return(0, errors.New("db down"))

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "any",
		Email:    "any@example.com",
		Password: "securepassword123",
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
	mockDb.AssertExpectations(t)
	mockDb.AssertNotCalled(t, "CreateUser")
}

func TestRegister_DuplicateEmail(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "taken@example.com").Return(&types.User{ID: "existing"}, nil)

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "newuser",
		Email:    "taken@example.com",
		Password: "securepassword123",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "registration failed")
	assert.Nil(t, resp)
	mockDb.AssertExpectations(t)
}

func TestRegister_GetUserByEmailError(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "error@example.com").Return(nil, errors.New("db down"))

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "user",
		Email:    "error@example.com",
		Password: "securepassword123",
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRegister_CreateUserError(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "fail@example.com").Return(nil, nil)
	mockDb.On("CountUsers", ctx).Return(2, nil)
	mockDb.On("CreateUser", ctx, mock.Anything).Return(errors.New("insert failed"))

	resp, err := svc.Register(ctx, types.RegisterRequest{
		Username: "user",
		Email:    "fail@example.com",
		Password: "securepassword123",
	})

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestLogin_Success(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.DefaultCost)
	mockDb.On("GetUserByEmail", ctx, "user@example.com").Return(&types.User{
		ID:           "u1",
		Username:     "user",
		Email:        "user@example.com",
		PasswordHash: string(hash),
		Active:       true,
	}, nil)

	resp, err := svc.Login(ctx, types.LoginRequest{
		Email:    "user@example.com",
		Password: "mypassword",
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "u1", resp.User.ID)
	assert.Empty(t, resp.User.PasswordHash)
	mockDb.AssertExpectations(t)
}

func TestLogin_UserNotFound(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "nobody@example.com").Return(nil, nil)

	resp, err := svc.Login(ctx, types.LoginRequest{
		Email:    "nobody@example.com",
		Password: "whatever",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email or password")
	assert.Nil(t, resp)
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.DefaultCost)
	mockDb.On("GetUserByEmail", ctx, "user@example.com").Return(&types.User{
		ID:           "u1",
		PasswordHash: string(hash),
		Active:       true,
	}, nil)

	resp, err := svc.Login(ctx, types.LoginRequest{
		Email:    "user@example.com",
		Password: "wrong",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email or password")
	assert.Nil(t, resp)
}

func TestLogin_InactiveUser(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("pass"), bcrypt.DefaultCost)
	mockDb.On("GetUserByEmail", ctx, "disabled@example.com").Return(&types.User{
		ID:           "u1",
		PasswordHash: string(hash),
		Active:       false,
	}, nil)

	resp, err := svc.Login(ctx, types.LoginRequest{
		Email:    "disabled@example.com",
		Password: "pass",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email or password")
	assert.Nil(t, resp)
}

func TestCreateAPIKey_Success(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("CreateAPIKey", ctx, mock.MatchedBy(func(k *types.APIKey) bool {
		return k.UserID == "user-1" && k.Name == "my-key" && k.Active && len(k.Key) > 4
	})).Return(nil)

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{Name: "my-key"})

	assert.NoError(t, err)
	assert.NotNil(t, apiKey)
	assert.Equal(t, "my-key", apiKey.Name)
	assert.True(t, len(apiKey.Key) > 32, "API key must be long enough")
	assert.True(t, len(apiKey.Key) > 4 && apiKey.Key[:4] == "lsp_", "API key must have lsp_ prefix")
	mockDb.AssertExpectations(t)
}

func TestCreateAPIKey_DBError(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("CreateAPIKey", ctx, mock.Anything).Return(errors.New("db error"))

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{Name: "my-key"})

	assert.Error(t, err)
	assert.Nil(t, apiKey)
}

func TestListAPIKeys_Success(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("ListAPIKeys", ctx, "user-1").Return([]*types.APIKey{
		{ID: "k1", Name: "key-one", Prefix: "lsp_", Active: true, Key: "lsp_secret"},
		{ID: "k2", Name: "key-two", Prefix: "lsp_", Active: true, Key: "lsp_secret2"},
	}, nil)

	keys, err := svc.ListAPIKeys(ctx, "user-1")

	assert.NoError(t, err)
	assert.Len(t, keys, 2)
	assert.Empty(t, keys[0].Key, "listed keys must not expose the secret")
	assert.Empty(t, keys[1].Key, "listed keys must not expose the secret")
	mockDb.AssertExpectations(t)
}

func TestListAPIKeys_DBError(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("ListAPIKeys", ctx, "user-1").Return(nil, errors.New("db error"))

	keys, err := svc.ListAPIKeys(ctx, "user-1")

	assert.Error(t, err)
	assert.Nil(t, keys)
}

func TestDeleteAPIKey_Success(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetAPIKey", ctx, "user-1", "key-1").Return(&types.APIKey{ID: "key-1"}, nil)
	mockDb.On("DeleteAPIKey", ctx, "user-1", "key-1").Return(nil)

	err := svc.DeleteAPIKey(ctx, "user-1", "key-1")

	assert.NoError(t, err)
	mockDb.AssertExpectations(t)
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetAPIKey", ctx, "user-1", "nonexistent").Return(nil, nil)

	err := svc.DeleteAPIKey(ctx, "user-1", "nonexistent")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	mockDb.AssertExpectations(t)
}

func TestDeleteAPIKey_DBError(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetAPIKey", ctx, "user-1", "key-1").Return(nil, errors.New("db error"))

	err := svc.DeleteAPIKey(ctx, "user-1", "key-1")

	assert.Error(t, err)
}

func TestDeleteAPIKey_DeleteFails(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetAPIKey", ctx, "user-1", "key-1").Return(&types.APIKey{ID: "key-1"}, nil)
	mockDb.On("DeleteAPIKey", ctx, "user-1", "key-1").Return(errors.New("delete failed"))

	err := svc.DeleteAPIKey(ctx, "user-1", "key-1")

	assert.Error(t, err)
	mockDb.AssertExpectations(t)
}

// --- Account Lockout ---

func newLockoutService(t *testing.T) (*Service, *mocks.MockDatabaseService, *mocks.MockCacheService) {
	t.Helper()
	log, _ := logger.New(true, "debug", "console")
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret-1234567890"
	cfg.Auth.TokenDuration = 24 * time.Hour
	cfg.Auth.APIKeyPrefix = "lsp_"
	cfg.Auth.LockoutEnabled = true
	cfg.Auth.LockoutAttempts = 3
	cfg.Auth.LockoutDuration = 15 * time.Minute
	mockDb := new(mocks.MockDatabaseService)
	mockCache := new(mocks.MockCacheService)
	svc, err := New(cfg, log, mockDb, mockCache)
	require.NoError(t, err)
	return svc, mockDb, mockCache
}

func TestLogin_LockoutAfterFailedAttempts(t *testing.T) {
	svc, mockDb, mockCache := newLockoutService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("pass"), bcrypt.DefaultCost)
	user := &types.User{
		ID: "u1", Email: "lock@e.com", PasswordHash: string(hash), Active: true,
	}

	attemptCount := 0
	mockCache.On("Get", ctx, "lockout:lock@e.com").Return("", errors.New("not found"))
	mockCache.On("Set", ctx, "lockout:lock@e.com", mock.MatchedBy(func(v string) bool {
		attemptCount++
		return true
	}), mock.Anything).Return(nil)

	for i := 0; i < 3; i++ {
		mockDb.On("GetUserByEmail", ctx, "lock@e.com").Return(user, nil).Once()
		_, err := svc.Login(ctx, types.LoginRequest{Email: "lock@e.com", Password: "wrong"})
		assert.Error(t, err, "attempt %d should fail", i+1)
	}
	assert.Equal(t, 3, attemptCount, "should have recorded 3 failed attempts")

	mockDb.AssertExpectations(t)
}

func TestLogin_LockoutBlocksAfterMaxAttempts(t *testing.T) {
	svc, _, mockCache := newLockoutService(t)
	ctx := context.Background()

	mockCache.On("Get", ctx, "lockout:locked@e.com").Return("3", nil)

	_, err := svc.Login(ctx, types.LoginRequest{Email: "locked@e.com", Password: "pass"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "temporarily locked")

	mockCache.AssertExpectations(t)
}

func TestLogin_SuccessResetsLockout(t *testing.T) {
	svc, mockDb, mockCache := newLockoutService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("pass"), bcrypt.DefaultCost)
	user := &types.User{
		ID: "u1", Email: "reset@e.com", PasswordHash: string(hash), Active: true,
	}
	mockDb.On("GetUserByEmail", ctx, "reset@e.com").Return(user, nil)
	mockCache.On("Get", ctx, mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "lockout:")
	})).Return("2", nil).Once()
	mockCache.On("Delete", ctx, mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "lockout:")
	})).Return(nil).Once()

	resp, err := svc.Login(ctx, types.LoginRequest{Email: "reset@e.com", Password: "pass"})
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	mockCache.AssertExpectations(t)
}

func TestLogin_LockoutDisabled(t *testing.T) {
	svc, _, _ := newTestService(t)
	_ = context.Background()

	assert.False(t, svc.config.Auth.LockoutEnabled, "default service should have lockout disabled")
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
			"Query parameter disabled by default",
			func(c *gin.Context) {
				c.Request.URL.RawQuery = "token=token123"
			},
			"",
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
