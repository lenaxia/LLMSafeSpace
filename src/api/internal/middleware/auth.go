package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	apiinterfaces "github.com/lenaxia/llmsafespace/api/internal/interfaces"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// AuthConfig defines configuration for the authentication middleware
type AuthConfig struct {
	// HeaderName is the name of the header containing the authentication token
	HeaderName string
	
	// QueryParamName is the name of the query parameter containing the authentication token
	QueryParamName string
	
	// CookieName is the name of the cookie containing the authentication token
	CookieName string
	
	// TokenType is the type of token (e.g., "Bearer")
	TokenType string
	
	// SkipPaths are paths that should not be authenticated
	SkipPaths []string
	
	// SkipPathPrefixes are path prefixes that should not be authenticated
	SkipPathPrefixes []string
}

// DefaultAuthConfig returns the default authentication configuration
func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		HeaderName:       "Authorization",
		QueryParamName:   "token",
		CookieName:       "auth_token",
		TokenType:        "Bearer",
		SkipPaths:        []string{"/health", "/metrics", "/api/v1/auth/login", "/api/v1/auth/register"},
		SkipPathPrefixes: []string{"/static/", "/docs/"},
	}
}

// AuthMiddleware returns a middleware that handles authentication
func AuthMiddleware(authService apiinterfaces.AuthService, log pkginterfaces.LoggerInterface, config ...AuthConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultAuthConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Skip authentication for certain paths
		path := c.Request.URL.Path
		if shouldSkipAuth(path, cfg.SkipPaths, cfg.SkipPathPrefixes) {
			c.Next()
			return
		}
		
		// Extract token from request
		token := extractToken(c, cfg)
		if token == "" {
			if log != nil {
				log.Warn("Authentication failed: no token provided",
				"path", path,
				"method", c.Request.Method,
				"client_ip", c.ClientIP(),
				"request_id", c.GetString("request_id"),
				)
			}
			
			apiErr := errors.NewAuthenticationError("Authentication required", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Validate token
		userID, err := authService.ValidateToken(token)
		if err != nil {
			if log != nil {
				log.Warn("Authentication failed: invalid token",
				"path", path,
				"method", c.Request.Method,
				"client_ip", c.ClientIP(),
				"request_id", c.GetString("request_id"),
				"error", err.Error(),
				)
			}
			
			apiErr := errors.NewAuthenticationError("Invalid or expired token", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Store authentication result in context
		c.Set("userID", userID)
		
		// Add user ID to logger context
		if requestLogger, exists := c.Get("logger"); exists {
			if typedLogger, ok := requestLogger.(pkginterfaces.LoggerInterface); ok {
				newLogger := typedLogger.With("user_id", userID)
				c.Set("logger", newLogger)
			}
		}
		
		// Add user ID to span if tracing is enabled - commented out until we properly import trace packages
		/*
		if span := trace.SpanFromContext(c.Request.Context()); span != nil {
			span.SetAttributes(attribute.String("user.id", authResult.UserID))
			span.SetAttributes(attribute.String("user.role", authResult.Role))
		}
		*/
		
		c.Next()
	}
}

// AuthorizationMiddleware returns a middleware that handles authorization
func AuthorizationMiddleware(authService apiinterfaces.AuthService, log pkginterfaces.LoggerInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip authorization for certain paths
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		
		// Get required permissions from context
		requiredPermissions, exists := c.Get("requiredPermissions")
		if !exists {
			c.Next()
			return
		}
		
		// Get user permissions from context
		userPermissions, exists := c.Get("permissions")
		if !exists {
			log.Warn("Authorization failed: no permissions in context",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"client_ip", c.ClientIP(),
				"request_id", c.GetString("request_id"),
			)
			
			apiErr := errors.NewForbiddenError("Authorization required", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Check if user has required permissions
		hasPermission := false
		userPerms := userPermissions.([]string)
		requiredPerms := requiredPermissions.([]string)
		
		// Simple permission check - user must have all required permissions
		hasAll := true
		for _, required := range requiredPerms {
			found := false
			for _, userPerm := range userPerms {
				if required == userPerm {
					found = true
					break
				}
			}
			if !found {
				hasAll = false
				break
			}
		}
		hasPermission = hasAll
		
		if !hasPermission {
			log.Warn("Authorization failed: insufficient permissions",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"client_ip", c.ClientIP(),
				"request_id", c.GetString("request_id"),
				"user_id", c.GetString("userID"),
				"required_permissions", requiredPermissions,
			)
			
			apiErr := errors.NewForbiddenError("Insufficient permissions", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		c.Next()
	}
}

// RequirePermissions returns a middleware that requires specific permissions
func RequirePermissions(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("requiredPermissions", permissions)
		c.Next()
	}
}

// RequireRoles returns a middleware that requires specific roles
func RequireRoles(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("userRole")
		if !exists {
			apiErr := errors.NewForbiddenError("Authorization required", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Check if user has one of the required roles
		hasRole := false
		for _, role := range roles {
			if userRole == role {
				hasRole = true
				break
			}
		}
		
		if !hasRole {
			apiErr := errors.NewForbiddenError("Insufficient permissions", nil)
			HandleAPIError(c, apiErr)
			return
		}
		
		c.Next()
	}
}

// shouldSkipAuth checks if authentication should be skipped for a path
func shouldSkipAuth(path string, skipPaths, skipPathPrefixes []string) bool {
	// Check exact paths
	for _, skipPath := range skipPaths {
		if path == skipPath {
			return true
		}
	}
	
	// Check path prefixes
	for _, prefix := range skipPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	
	return false
}

// extractToken extracts the authentication token from the request
func extractToken(c *gin.Context, cfg AuthConfig) string {
	// Check header
	authHeader := c.GetHeader(cfg.HeaderName)
	if authHeader != "" {
		// If token type is specified, check for it
		if cfg.TokenType != "" {
			if strings.HasPrefix(authHeader, cfg.TokenType+" ") {
				return strings.TrimPrefix(authHeader, cfg.TokenType+" ")
			}
		} else {
			return authHeader
		}
	}
	
	// Check query parameter
	if cfg.QueryParamName != "" {
		token := c.Query(cfg.QueryParamName)
		if token != "" {
			return token
		}
	}
	
	// Check cookie
	if cfg.CookieName != "" {
		token, err := c.Cookie(cfg.CookieName)
		if err == nil && token != "" {
			return token
		}
	}
	
	return ""
}
