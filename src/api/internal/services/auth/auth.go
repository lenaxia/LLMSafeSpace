package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
)

// Service handles authentication and authorization
type Service struct {
	logger        *logger.Logger
	config        *config.Config
	dbService     *database.Service
	jwtSecret     []byte
	tokenDuration time.Duration
}

// New creates a new auth service
func New(cfg *config.Config, log *logger.Logger, dbService *database.Service) (*Service, error) {
	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT secret is required")
	}

	return &Service{
		logger:        log,
		config:        cfg,
		dbService:     dbService,
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
		userID, err := s.validateToken(token)
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

	return userID, nil
}

// validateAPIKey validates an API key
func (s *Service) validateAPIKey(apiKey string) (string, error) {
	// Get user ID from database
	userID, err := s.dbService.GetUserIDByAPIKey(apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to get user ID by API key: %w", err)
	}

	if userID == "" {
		return "", errors.New("invalid API key")
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
