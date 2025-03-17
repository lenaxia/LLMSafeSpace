package utilities

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// TokenExtractorConfig defines configuration for token extraction
type TokenExtractorConfig struct {
	// HeaderName is the name of the header containing the authentication token
	HeaderName string
	
	// TokenType is the type of token (e.g., "Bearer")
	TokenType string
	
	// QueryParamName is the name of the query parameter containing the authentication token
	QueryParamName string
	
	// CookieName is the name of the cookie containing the authentication token
	CookieName string
}

// DefaultTokenExtractorConfig returns the default token extraction configuration
func DefaultTokenExtractorConfig() TokenExtractorConfig {
	return TokenExtractorConfig{
		HeaderName:     "Authorization",
		TokenType:      "Bearer",
		QueryParamName: "token",
		CookieName:     "auth_token",
	}
}

// ExtractToken extracts the authentication token from the request
func ExtractToken(c *gin.Context, config ...TokenExtractorConfig) string {
	// Use default config if none provided
	cfg := DefaultTokenExtractorConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
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

// IsAPIKey checks if a token is an API key based on its prefix
func IsAPIKey(token, prefix string) bool {
	return len(token) > len(prefix) && token[:len(prefix)] == prefix
}
