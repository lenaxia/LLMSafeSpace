package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig defines configuration for CORS middleware
type CORSConfig struct {
	// AllowedOrigins is a list of origins a cross-domain request can be executed from
	AllowedOrigins []string
	
	// AllowedMethods is a list of methods the client is allowed to use
	AllowedMethods []string
	
	// AllowedHeaders is a list of headers the client is allowed to use
	AllowedHeaders []string
	
	// ExposedHeaders is a list of headers that are safe to expose
	ExposedHeaders []string
	
	// AllowCredentials indicates whether the request can include user credentials
	AllowCredentials bool
	
	// MaxAge indicates how long the results of a preflight request can be cached
	MaxAge int
	
	// OptionsPassthrough instructs preflight to let other handlers handle OPTIONS
	OptionsPassthrough bool
	
	// Debug enables debugging
	Debug bool
}

// DefaultCORSConfig returns the default CORS configuration
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins:     []string{"*"},
		AllowedMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-Request-ID"},
		ExposedHeaders:     []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
		AllowCredentials:   true,
		MaxAge:            86400,
		OptionsPassthrough: false,
		Debug:             false,
	}
}

// CORSMiddleware handles Cross-Origin Resource Sharing
func CORSMiddleware(config ...CORSConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultCORSConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(c *gin.Context) {
		// Handle preflight OPTIONS request
		if !cfg.OptionsPassthrough && c.Request.Method == "OPTIONS" {
			// Set CORS headers
			headers := c.Writer.Header()
			if origin := c.Request.Header.Get("Origin"); origin != "" && isAllowedOrigin(origin, cfg.AllowedOrigins) {
				headers.Set("Access-Control-Allow-Origin", origin)
				headers.Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
				headers.Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
				headers.Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))
				if cfg.AllowCredentials {
					headers.Set("Access-Control-Allow-Credentials", "true")
				}
				if cfg.MaxAge > 0 {
					headers.Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				}
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		// For actual requests, set CORS headers
		if origin := c.Request.Header.Get("Origin"); origin != "" && isAllowedOrigin(origin, cfg.AllowedOrigins) {
			headers := c.Writer.Header()
			headers.Set("Access-Control-Allow-Origin", origin)
			headers.Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))
			if cfg.AllowCredentials {
				headers.Set("Access-Control-Allow-Credentials", "true")
			}
		}

		c.Next()
	}
}

// isAllowedOrigin checks if the given origin is allowed
func isAllowedOrigin(origin string, allowedOrigins []string) bool {
	if len(allowedOrigins) == 0 {
		return true
	}
	for _, allowed := range allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}
