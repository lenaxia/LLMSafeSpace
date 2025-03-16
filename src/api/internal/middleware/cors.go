package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/cors"
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
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
		AllowCredentials: true,
		MaxAge:           86400,
		OptionsPassthrough: false,
		Debug:            false,
	}
}

// CORSMiddleware handles Cross-Origin Resource Sharing
func CORSMiddleware(config ...CORSConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultCORSConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	// Create cors.Options from config
	options := cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   cfg.AllowedMethods,
		AllowedHeaders:   cfg.AllowedHeaders,
		ExposedHeaders:   cfg.ExposedHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
		Debug:            cfg.Debug,
	}
	
	// Create cors handler
	corsHandler := cors.New(options).Handler
	
	return func(c *gin.Context) {
		// Handle preflight OPTIONS request directly if not using passthrough
		if !cfg.OptionsPassthrough && c.Request.Method == "OPTIONS" {
			// Create a response recorder
			w := &responseRecorder{
				ResponseWriter: c.Writer,
				statusCode:     http.StatusOK,
			}
			
			// Process with cors handler
			corsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Do nothing, just let CORS headers be added
			})).ServeHTTP(w, c.Request)
			
			// Set CORS headers
			for key, values := range w.Header() {
				for _, value := range values {
					c.Header(key, value)
				}
			}
			
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		
		// For all other requests, process with cors handler then continue
		corsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Copy headers from response writer to gin context
			for key, values := range w.Header() {
				for _, value := range values {
					c.Header(key, value)
				}
			}
			
			// Continue processing
			c.Next()
		})).ServeHTTP(c.Writer, c.Request)
	}
}

// responseRecorder is a wrapper for http.ResponseWriter that captures status code
type responseRecorder struct {
	gin.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code
func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Status returns the status code
func (r *responseRecorder) Status() int {
	return r.statusCode
}
