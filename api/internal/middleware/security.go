package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/unrolled/secure"
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
	
	// PermissionsPolicy is the Permissions-Policy header value
	PermissionsPolicy string
	
	// RequireHTTPS indicates whether to require HTTPS
	RequireHTTPS bool
	
	// AllowHTTPSDowngrade indicates whether to allow HTTPS downgrade in development
	AllowHTTPSDowngrade bool
	
	// Development indicates whether the application is running in development mode
	Development bool
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
		PermissionsPolicy: "camera=(), microphone=(), geolocation=(), interest-cohort=()",
		RequireHTTPS:     true,
		AllowHTTPSDowngrade: false,
		Development:      false,
	}
}

// SecurityMiddleware returns a middleware that adds security headers
func SecurityMiddleware(log interfaces.LoggerInterface, config ...SecurityConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultSecurityConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	// Create secure middleware
	secureMiddleware := secure.New(secure.Options{
		AllowedHosts:          []string{}, // No host restriction by default
		SSLRedirect:           cfg.RequireHTTPS && !cfg.Development,
		SSLTemporaryRedirect:  false,
		SSLHost:               "",
		STSSeconds:            31536000,
		STSIncludeSubdomains:  true,
		STSPreload:            true,
		ForceSTSHeader:        false,
		FrameDeny:             true,
		ContentTypeNosniff:    true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: cfg.ContentSecurityPolicy,
		ReferrerPolicy:        cfg.ReferrerPolicy,
		PermissionsPolicy:     cfg.PermissionsPolicy,
		IsDevelopment:         cfg.Development,
	})
	
	return func(c *gin.Context) {
		// Skip security checks for OPTIONS requests
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		
		// Apply secure middleware
		err := secureMiddleware.Process(c.Writer, c.Request)
		if err != nil {
			// If there was an error, do not continue
			if cfg.Development && cfg.AllowHTTPSDowngrade {
				// Allow HTTP in development if configured
				c.Next()
				return
			}
			
			log.Warn("Security middleware blocked request",
				"error", err.Error(),
				"request_id", c.GetString("request_id"),
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"client_ip", c.ClientIP(),
			)
			
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		
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
		
		// Set trusted proxies - this needs to be done at the engine level, not here
		// We'll log a warning instead of trying to set it in middleware
		if len(cfg.TrustedProxies) > 0 {
			log.Info("Trusted proxies configuration detected in middleware",
				"trusted_proxies", cfg.TrustedProxies,
				"note", "This should be configured at the engine level, not in middleware",
			)
		}
		
		// Add additional security headers not covered by secure middleware
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Permitted-Cross-Domain-Policies", "none")
		c.Header("X-Download-Options", "noopen")
		
		c.Next()
	}
}

// WebSocketSecurityMiddleware returns a middleware that adds WebSocket security
func WebSocketSecurityMiddleware(log interfaces.LoggerInterface, allowedOrigins ...string) gin.HandlerFunc {
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
				c.AbortWithStatusJSON(apiErr.StatusCode(), gin.H{
					"error": gin.H{
						"code":    apiErr.Code,
						"message": apiErr.Message,
					},
				})
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
				c.AbortWithStatusJSON(apiErr.StatusCode(), gin.H{
					"error": gin.H{
						"code":    apiErr.Code,
						"message": apiErr.Message,
					},
				})
				return
			}
			
			// Add WebSocket specific security headers
			c.Header("Sec-WebSocket-Version", "13")
			
			// Check for WebSocket protocol
			protocol := c.GetHeader("Sec-WebSocket-Protocol")
			if protocol != "" {
				// Validate protocol (implement your validation logic here)
				// For now, we'll just echo it back
				c.Header("Sec-WebSocket-Protocol", protocol)
			}
		}
		
		c.Next()
	}
}

// CSPReportingMiddleware returns a middleware that handles CSP violation reports
func CSPReportingMiddleware(log interfaces.LoggerInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only process POST requests to the CSP report endpoint
		if c.Request.Method == "POST" && c.Request.URL.Path == "/api/v1/csp-report" {
			var report struct {
				CSPReport struct {
					DocumentURI        string `json:"document-uri"`
					Referrer           string `json:"referrer"`
					BlockedURI         string `json:"blocked-uri"`
					ViolatedDirective  string `json:"violated-directive"`
					OriginalPolicy     string `json:"original-policy"`
					Disposition        string `json:"disposition"`
					EffectiveDirective string `json:"effective-directive"`
				} `json:"csp-report"`
			}
			
			if err := c.ShouldBindJSON(&report); err == nil {
				log.Warn("CSP violation report",
					"document_uri", report.CSPReport.DocumentURI,
					"blocked_uri", report.CSPReport.BlockedURI,
					"violated_directive", report.CSPReport.ViolatedDirective,
					"effective_directive", report.CSPReport.EffectiveDirective,
					"referrer", report.CSPReport.Referrer,
					"client_ip", c.ClientIP(),
					"request_id", c.GetString("request_id"),
				)
			}
			
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		
		c.Next()
	}
}
