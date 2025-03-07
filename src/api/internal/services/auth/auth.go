package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
)

// Service handles authentication and authorization
type Service struct {
	logger        *logger.Logger
	config        *config.Config
	dbService     *database.Service
	cacheService  *cache.Service
	jwtSecret     []byte
	tokenDuration time.Duration
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	// Check if API key is cached
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)
	
	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		return cachedUserID, nil
	}

	// Get user ID from database
	userID, err := s.dbService.GetUserIDByAPIKey(apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate API key: %w", err)
	}

	if userID == "" {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, userID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", userID)
		// Continue even if caching fails
	}

	return userID, nil
}

// New creates a new auth service
func New(cfg *config.Config, log *logger.Logger, dbService *database.Service, cacheService *cache.Service) (*Service, error) {
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

// AuthMiddleware returns a middleware function for authentication
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from Authorization header
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(401, gin.H{
				"error": "Authentication required",
			})
			return
		}

		// Check if token is an API key
		if isAPIKey(token, s.config.Auth.APIKeyPrefix) {
			userID, err := s.validateAPIKey(token)
			if err != nil {
				c.AbortWithStatusJSON(401, gin.H{
					"error": "Invalid API key",
				})
				return
			}

			// Store user ID in context
			c.Set("userID", userID)
			c.Set("apiKey", token)
			c.Next()
			return
		}

		// Validate JWT token
		userID, err := s.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{
				"error": "Invalid or expired token",
			})
			return
		}

		// Store user ID in context
		c.Set("userID", userID)
		c.Next()
	}
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

	// Get token ID
	jti, ok := claims["jti"].(string)
	if !ok {
		jti = fmt.Sprintf("%v", claims["sub"]) // Use subject as fallback
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
	ctx := context.Background()
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
	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(s.tokenDuration).Unix(),
		"iat": time.Now().Unix(),
	})

	// Sign token
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// validateToken validates a JWT token
func (s *Service) validateToken(tokenString string) (string, error) {
	// Check if token is cached
	ctx := context.Background()
	cacheKey := fmt.Sprintf("token:%s", tokenString)
	
	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
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
	ctx := context.Background()
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)
	
	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		return cachedUserID, nil
	}

	// Get user ID from database
	userID, err := s.dbService.GetUserIDByAPIKey(apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to get user ID by API key: %w", err)
	}

	if userID == "" {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, userID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", userID)
		// Continue even if caching fails
	}

	return userID, nil
}

// isAPIKey checks if a token is an API key
func isAPIKey(token, prefix string) bool {
	return len(token) > len(prefix) && token[:len(prefix)] == prefix
}

// extractToken extracts the token from the Authorization header
func extractToken(c *gin.Context) string {
	// Check Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			return authHeader[7:]
		}
		return authHeader
	}

	// Check query parameter
	token := c.Query("token")
	if token != "" {
		return token
	}

	return ""
}
