package auth

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/utilities"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

func hashToken(token string) string {
	h := md5.Sum([]byte(token))
	return hex.EncodeToString(h[:])
}

// Service handles authentication and authorization
type Service struct {
	logger        *logger.Logger
	config        *config.Config
	dbService     interfaces.DatabaseService
	cacheService  interfaces.CacheService
	jwtSecret     []byte
	tokenDuration time.Duration
}

// Start initializes the auth service
func (s *Service) Start() error {
	return nil
}

// Stop cleans up the auth service
func (s *Service) Stop() error {
	return nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	// Check if API key is cached
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)

	// Try to get from cache first
	cachedStatus, err := s.cacheService.Get(ctx, cacheKey)
	if err == nil && cachedStatus != "" {
		if cachedStatus == "revoked" {
			return "", errors.New("token has been revoked")
		}
		return cachedStatus, nil
	}

	// Get user from database
	user, err := s.dbService.GetUserByAPIKey(ctx, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate API key: %w", err)
	}

	if user == nil {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, user.ID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", user.ID)
		// Continue even if caching fails
	}

	return user.ID, nil
}

// Note: The redundant AuthMiddleware method has been removed as it duplicates
// functionality in the middleware package

// New creates a new auth service
func New(cfg *config.Config, log *logger.Logger, dbService interfaces.DatabaseService, cacheService interfaces.CacheService) (*Service, error) {
	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT secret is required")
	}

	return &Service{
		logger:        log,
		config:        cfg,
		dbService:     dbService,
		cacheService:  cacheService,
		jwtSecret:     []byte(cfg.Auth.JWTSecret),
		tokenDuration: cfg.Auth.TokenDuration,
	}, nil
}

// GetUserID gets the user ID from the context
func (s *Service) GetUserID(c *gin.Context) string {
	userID, exists := c.Get("userID")
	if !exists {
		return ""
	}
	return userID.(string)
}

// RevokeToken revokes a JWT token
func (s *Service) RevokeToken(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Parse token
	parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		return fmt.Errorf("failed to parse token: %w", err)
	}

	// Get claims
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return errors.New("invalid token claims")
	}

	// Get token ID with proper validation
	jti, _ := claims["jti"].(string)
	if jti == "" {
		if sub, ok := claims["sub"].(string); ok && sub != "" {
			jti = sub
		} else {
			return errors.New("token missing valid jti or sub claim")
		}
	}

	// Get expiration time
	exp, ok := claims["exp"].(float64)
	if !ok {
		return errors.New("invalid expiration time in token")
	}

	// Calculate remaining time until expiration
	expTime := time.Unix(int64(exp), 0)
	remainingTime := time.Until(expTime)

	if remainingTime <= 0 {
		return errors.New("token has already expired")
	}

	// Add token to blacklist
	err = s.cacheService.Set(ctx, "token:"+jti, "revoked", remainingTime)
	if err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}

	return nil
}

// CheckResourceAccess checks if a user has access to a resource
func (s *Service) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	// Check resource ownership
	isOwner, err := s.dbService.CheckResourceOwnership(userID, resourceType, resourceID)
	if err != nil {
		s.logger.Error("Failed to check resource ownership", err,
			"user_id", userID,
			"resource_type", resourceType,
			"resource_id", resourceID,
		)
		return false
	}

	if isOwner {
		return true
	}

	// Check RBAC permissions
	hasPermission, err := s.dbService.CheckPermission(userID, resourceType, resourceID, action)
	if err != nil {
		s.logger.Error("Failed to check permission", err,
			"user_id", userID,
			"resource_type", resourceType,
			"resource_id", resourceID,
			"action", action,
		)
		return false
	}

	return hasPermission
}

// GenerateToken generates a JWT token for a user
func (s *Service) GenerateToken(userID string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"jti": uuid.New().String(),
		"exp": time.Now().Add(s.tokenDuration).Unix(),
		"iat": time.Now().Unix(),
	})

	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ValidateToken validates a JWT token or API key
func (s *Service) ValidateToken(tokenString string) (string, error) {
	// Check if token is an API key
	if utilities.IsAPIKey(tokenString, s.config.Auth.APIKeyPrefix) {
		return s.validateAPIKey(tokenString)
	}

	// Check if token is cached
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("token:%s", hashToken(tokenString))

	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		if cachedUserID == "revoked" {
			return "", errors.New("token has been revoked")
		}
		return cachedUserID, nil
	}

	// Parse token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	// Validate token
	if !token.Valid {
		return "", errors.New("invalid token")
	}

	// Get claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid token claims")
	}

	// Get user ID
	userID, ok := claims["sub"].(string)
	if !ok {
		return "", errors.New("invalid user ID in token")
	}

	// Get expiration time
	exp, ok := claims["exp"].(float64)
	if !ok {
		return "", errors.New("invalid expiration time in token")
	}

	// Calculate remaining time until expiration
	expTime := time.Unix(int64(exp), 0)
	remainingTime := time.Until(expTime)

	// Cache the token if it's valid
	if remainingTime > 0 {
		// Cache for the remaining time of the token, but not more than 1 hour
		cacheDuration := remainingTime
		if cacheDuration > time.Hour {
			cacheDuration = time.Hour
		}

		err = s.cacheService.Set(ctx, cacheKey, userID, cacheDuration)
		if err != nil {
			s.logger.Error("Failed to cache token", err, "user_id", userID)
			// Continue even if caching fails
		}
	}

	return userID, nil
}

// validateAPIKey validates an API key (internal method)
func (s *Service) validateAPIKey(apiKey string) (string, error) {
	// Check if API key is cached
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)

	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		return cachedUserID, nil
	}

	// Get user from database
	user, err := s.dbService.GetUserByAPIKey(ctx, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to get user by API key: %w", err)
	}

	if user == nil {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, user.ID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", user.ID)
		// Continue even if caching fails
	}

	return user.ID, nil
}

const bcryptCost = 12

func (s *Service) Register(ctx context.Context, req types.RegisterRequest) (*types.AuthResponse, error) {
	existing, err := s.dbService.GetUserByEmail(ctx, req.Email)
	if err != nil {
		s.logger.Error("Register: failed to check existing user", err)
		return nil, errors.New("registration failed")
	}
	if existing != nil {
		s.logger.Warn("Register: duplicate email attempt", "email", req.Email)
		return nil, apierrors.NewConflictError("user", "email", fmt.Errorf("registration failed"))
	}

	// First user in a fresh installation is auto-promoted to admin so the
	// system has at least one administrator. CountUsers must succeed; on
	// error we fail closed (do not silently default to admin).
	userCount, err := s.dbService.CountUsers(ctx)
	if err != nil {
		s.logger.Error("Register: failed to count users", err)
		return nil, errors.New("registration failed")
	}
	role := "user"
	if userCount == 0 {
		role = "admin"
		s.logger.Info("Register: first user in fresh installation, promoting to admin",
			"email", req.Email)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, errors.New("registration failed")
	}

	userID := uuid.New().String()
	user := &types.User{
		ID:           userID,
		Username:     strings.TrimSpace(req.Username),
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		PasswordHash: string(hash),
		Active:       true,
		Role:         role,
	}

	if err := s.dbService.CreateUser(ctx, user); err != nil {
		s.logger.Error("Register: failed to create user", err)
		return nil, errors.New("registration failed")
	}

	token, err := s.GenerateToken(userID)
	if err != nil {
		return nil, errors.New("registration failed")
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user}, nil
}

func (s *Service) Login(ctx context.Context, req types.LoginRequest) (*types.AuthResponse, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if s.config.Auth.LockoutEnabled {
		lockoutKey := fmt.Sprintf("lockout:%s", email)
		if countStr, err := s.cacheService.Get(ctx, lockoutKey); err == nil && countStr != "" {
			var count int
			if _, err := fmt.Sscanf(countStr, "%d", &count); err == nil && count >= s.config.Auth.LockoutAttempts {
				return nil, errors.New("account temporarily locked due to too many failed attempts")
			}
		}
	}

	user, err := s.dbService.GetUserByEmail(ctx, email)
	if err != nil {
		s.logger.Error("Login: db error", err)
		return nil, errors.New("invalid email or password")
	}
	if user == nil {
		s.recordFailedAttempt(ctx, email)
		return nil, errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		s.recordFailedAttempt(ctx, email)
		return nil, errors.New("invalid email or password")
	}

	if !user.Active {
		s.recordFailedAttempt(ctx, email)
		return nil, errors.New("invalid email or password")
	}

	s.clearFailedAttempts(ctx, email)

	token, err := s.GenerateToken(user.ID)
	if err != nil {
		return nil, errors.New("login failed")
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user}, nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, email string) {
	if !s.config.Auth.LockoutEnabled {
		return
	}
	lockoutKey := fmt.Sprintf("lockout:%s", email)
	countStr, _ := s.cacheService.Get(ctx, lockoutKey)
	count := 0
	if countStr != "" {
		fmt.Sscanf(countStr, "%d", &count)
	}
	count++
	duration := s.config.Auth.LockoutDuration
	if duration == 0 {
		duration = 15 * time.Minute
	}
	if err := s.cacheService.Set(ctx, lockoutKey, fmt.Sprintf("%d", count), duration); err != nil {
		s.logger.Error("Failed to record lockout attempt", err, "email", email)
	}
}

func (s *Service) clearFailedAttempts(ctx context.Context, email string) {
	if !s.config.Auth.LockoutEnabled {
		return
	}
	lockoutKey := fmt.Sprintf("lockout:%s", email)
	if err := s.cacheService.Delete(ctx, lockoutKey); err != nil {
		s.logger.Error("Failed to clear lockout", err, "email", email)
	}
}

func (s *Service) CreateAPIKey(ctx context.Context, userID string, req types.CreateAPIKeyRequest) (*types.APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("failed to generate api key: %w", err)
	}
	keyStr := s.config.Auth.APIKeyPrefix + hex.EncodeToString(raw)

	apiKey := &types.APIKey{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      req.Name,
		Key:       keyStr,
		Prefix:    s.config.Auth.APIKeyPrefix,
		Active:    true,
		CreatedAt: time.Now(),
	}

	if err := s.dbService.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to store api key: %w", err)
	}

	return apiKey, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error) {
	keys, err := s.dbService.ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	for _, k := range keys {
		k.Key = ""
	}
	return keys, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	existing, err := s.dbService.GetAPIKey(ctx, userID, keyID)
	if err != nil {
		return fmt.Errorf("failed to get api key: %w", err)
	}
	if existing == nil {
		return errors.New("api key not found")
	}
	return s.dbService.DeleteAPIKey(ctx, userID, keyID)
}

// extractToken extracts the token from the Authorization header or cookie
func extractToken(c *gin.Context) string {
	return utilities.ExtractToken(c, utilities.TokenExtractorConfig{
		HeaderName: "Authorization",
		TokenType:  "Bearer",
		CookieName: "lsp_session",
	})
}

// AuthMiddleware returns a middleware that validates JWT tokens
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from request
		tokenString := extractToken(c)
		if tokenString == "" {
			c.JSON(401, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Validate token
		userID, err := s.ValidateToken(tokenString)
		if err != nil {
			c.JSON(401, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Set user ID in context
		c.Set("userID", userID)

		// Load user role into context for AdminGuard and authorization checks.
		if s.dbService != nil {
			if user, err := s.dbService.GetUser(c.Request.Context(), userID); err == nil && user != nil {
				c.Set("userRole", user.Role)
			}
		}

		c.Next()
	}
}
