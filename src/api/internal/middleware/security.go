package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// SecurityConfig defines configuration for the security middleware
type SecurityConfig struct {
	// AllowedOrigins is a list of allowed origins for CORS
	AllowedOrigins []string
	
	// AllowedMethods is a list of allowed HTTP methods for CORS
	AllowedMethods []string
	
	// AllowedHeaders is a list of allowed HTTP headers for CORS
	AllowedHeaders []string
	
	// ExposedHeaders is a list of headers that can be exposed to the client
	ExposedHeaders []string
	
	// AllowCredentials indicates whether the request can include user credentials
	AllowCredentials bool
	
	// MaxAge indicates how long the results of a preflight request can be cached
	MaxAge int
	
	// TrustedProxies is a list of trusted proxy IP addresses
	TrustedProxies []string
	
	// ContentSecurityPolicy is the Content-Security-Policy header value
	ContentSecurityPolicy string
	
	// ReferrerPolicy is the Referrer-Policy header value
	ReferrerPolicy string
}

// DefaultSecurityConfig returns the default security configuration
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
		AllowCredentials: true,
		MaxAge:           86400,
		TrustedProxies:   []string{"127.0.0.1", "::1"},
		ContentSecurityPolicy: "default-src 'self'; connect-src 'self' wss:; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'self'; block-all-mixed-content",
		ReferrerPolicy:   "strict-origin-when-cross-origin",
	}
}

// SecurityMiddleware returns a middleware that adds security headers
func SecurityMiddleware(log *logger.Logger, config ...SecurityConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultSecurityConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Set security headers
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Content-Security-Policy", cfg.ContentSecurityPolicy)
		c.Header("Referrer-Policy", cfg.ReferrerPolicy)
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		
		// Handle CORS
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			// Check if origin is allowed
			allowed := false
			for _, allowedOrigin := range cfg.AllowedOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowed = true
					break
				}
			}
			
			if allowed {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
				c.Header("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
				c.Header("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))
				
				if cfg.AllowCredentials {
					c.Header("Access-Control-Allow-Credentials", "true")
				}
				
				if cfg.MaxAge > 0 {
					c.Header("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				}
			} else {
				log.Warn("CORS origin not allowed",
					"origin", origin,
					"request_id", c.GetString("request_id"),
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
			}
		}
		
		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		
		// Set trusted proxies
		if len(cfg.TrustedProxies) > 0 {
			err := c.Request.ParseForm()
			if err != nil {
				log.Error("Failed to parse form", err,
					"request_id", c.GetString("request_id"),
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
			}
		}
		
		c.Next()
	}
}

// WebSocketSecurityMiddleware returns a middleware that adds WebSocket security
func WebSocketSecurityMiddleware(log *logger.Logger, allowedOrigins ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check origin header for WebSocket connections
		if strings.Contains(c.GetHeader("Connection"), "Upgrade") && 
		   strings.Contains(c.GetHeader("Upgrade"), "websocket") {
			
			origin := c.GetHeader("Origin")
			if origin == "" {
				log.Warn("WebSocket connection attempt without Origin header",
					"request_id", c.GetString("request_id"),
					"path", c.Request.URL.Path,
					"remote_addr", c.ClientIP(),
				)
				
				apiErr := errors.NewForbiddenError("Origin header is required for WebSocket connections", nil)
				HandleAPIError(c, apiErr)
				return
			}
			
			// Check if origin is allowed
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowed = true
					break
				}
			}
			
			if !allowed {
				log.Warn("WebSocket connection attempt from unauthorized origin",
					"origin", origin,
					"request_id", c.GetString("request_id"),
					"path", c.Request.URL.Path,
					"remote_addr", c.ClientIP(),
				)
				
				apiErr := errors.NewForbiddenError("Origin not allowed", nil)
				HandleAPIError(c, apiErr)
				return
			}
		}
		
		c.Next()
	}
}
