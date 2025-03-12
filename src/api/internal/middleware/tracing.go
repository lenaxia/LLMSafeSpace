package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// TracingConfig defines configuration for the tracing middleware
type TracingConfig struct {
	// HeaderName is the name of the header to use for the request ID
	HeaderName string
	
	// PropagateHeader indicates whether to propagate the request ID in the response header
	PropagateHeader bool
	
	// GenerateIfMissing indicates whether to generate a request ID if one is not provided
	GenerateIfMissing bool
	
	// UseUUID indicates whether to use UUID for generated request IDs
	UseUUID bool
}

// DefaultTracingConfig returns the default tracing configuration
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		HeaderName:       "X-Request-ID",
		PropagateHeader:  true,
		GenerateIfMissing: true,
		UseUUID:          true,
	}
}

// TracingMiddleware returns a middleware that adds request tracing
func TracingMiddleware(log *logger.Logger, config ...TracingConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultTracingConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Get request ID from header
		requestID := c.GetHeader(cfg.HeaderName)
		
		// Generate request ID if missing and configured to do so
		if requestID == "" && cfg.GenerateIfMissing {
			if cfg.UseUUID {
				requestID = uuid.New().String()
			} else {
				requestID = fmt.Sprintf("%d", time.Now().UnixNano())
			}
		}
		
		// Store request ID in context
		if requestID != "" {
			c.Set("request_id", requestID)
			
			// Propagate request ID in response header if configured to do so
			if cfg.PropagateHeader {
				c.Header(cfg.HeaderName, requestID)
			}
			
			// Add request ID to logger context
			requestLogger := log.With("request_id", requestID)
			c.Set("logger", requestLogger)
		}
		
		// Add start time to context for latency calculation
		c.Set("start_time", time.Now())
		
		c.Next()
	}
}
