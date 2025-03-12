package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// LoggingConfig defines configuration for the logging middleware
type LoggingConfig struct {
	// SkipPaths are paths that should not be logged
	SkipPaths []string
	
	// SkipPathPrefixes are path prefixes that should not be logged
	SkipPathPrefixes []string
	
	// LogRequestBody indicates whether to log request bodies
	LogRequestBody bool
	
	// LogResponseBody indicates whether to log response bodies
	LogResponseBody bool
	
	// MaxBodyLogSize is the maximum size of request/response bodies to log
	MaxBodyLogSize int
	
	// LogHeaders indicates whether to log request headers
	LogHeaders bool
	
	// SensitiveHeaders are headers that should be redacted
	SensitiveHeaders []string
}

// DefaultLoggingConfig returns the default logging configuration
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		SkipPaths:        []string{"/health", "/metrics"},
		SkipPathPrefixes: []string{},
		LogRequestBody:   true,
		LogResponseBody:  true,
		MaxBodyLogSize:   1024, // 1KB
		LogHeaders:       true,
		SensitiveHeaders: []string{"Authorization", "Cookie", "Set-Cookie"},
	}
}

// LoggingMiddleware returns a middleware that logs requests
func LoggingMiddleware(log *logger.Logger, config ...LoggingConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultLoggingConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Skip logging for certain paths
		path := c.Request.URL.Path
		if shouldSkipLogging(path, cfg.SkipPaths, cfg.SkipPathPrefixes) {
			c.Next()
			return
		}
		
		// Start timer
		start := time.Now()
		
		// Get request ID
		requestID := c.GetString("request_id")
		
		// Log request
		logRequest(log, c, requestID, cfg)
		
		// Create a response writer that captures the response
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw
		
		// Process request
		c.Next()
		
		// Calculate latency
		latency := time.Since(start)
		
		// Log response
		logResponse(log, c, requestID, latency, blw.body.String(), cfg)
	}
}

// shouldSkipLogging checks if logging should be skipped for a path
func shouldSkipLogging(path string, skipPaths, skipPathPrefixes []string) bool {
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

// logRequest logs the request details
func logRequest(log *logger.Logger, c *gin.Context, requestID string, cfg LoggingConfig) {
	// Create log fields
	fields := []interface{}{
		"request_id", requestID,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"query", c.Request.URL.RawQuery,
		"client_ip", c.ClientIP(),
		"user_agent", c.Request.UserAgent(),
	}
	
	// Add user ID if available
	if userID, exists := c.Get("userID"); exists {
		fields = append(fields, "user_id", userID)
	}
	
	// Add API key if available
	if apiKey, exists := c.Get("apiKey"); exists {
		fields = append(fields, "api_key", maskString(apiKey.(string)))
	}
	
	// Add headers if configured
	if cfg.LogHeaders {
		headers := make(map[string]string)
		for k, v := range c.Request.Header {
			// Skip sensitive headers or mask them
			if contains(cfg.SensitiveHeaders, k) {
				headers[k] = "********"
			} else {
				headers[k] = strings.Join(v, ", ")
			}
		}
		fields = append(fields, "headers", headers)
	}
	
	// Add request body if configured
	if cfg.LogRequestBody && c.Request.ContentLength > 0 {
		// Read request body
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			// Restore the body
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
		
		// Truncate if too large
		if len(bodyBytes) > cfg.MaxBodyLogSize {
			fields = append(fields, "request_body", string(bodyBytes[:cfg.MaxBodyLogSize])+"... (truncated)")
			fields = append(fields, "request_body_size", len(bodyBytes))
		} else if len(bodyBytes) > 0 {
			// Try to parse as JSON for prettier logging
			var prettyBody interface{}
			if json.Unmarshal(bodyBytes, &prettyBody) == nil {
				fields = append(fields, "request_body", prettyBody)
			} else {
				fields = append(fields, "request_body", string(bodyBytes))
			}
		}
	}
	
	// Log the request
	log.Info("Request received", fields...)
}

// logResponse logs the response details
func logResponse(log *logger.Logger, c *gin.Context, requestID string, latency time.Duration, responseBody string, cfg LoggingConfig) {
	// Create log fields
	fields := []interface{}{
		"request_id", requestID,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"status", c.Writer.Status(),
		"latency_ms", latency.Milliseconds(),
		"size", c.Writer.Size(),
	}
	
	// Add user ID if available
	if userID, exists := c.Get("userID"); exists {
		fields = append(fields, "user_id", userID)
	}
	
	// Add response body if configured
	if cfg.LogResponseBody && responseBody != "" {
		// Truncate if too large
		if len(responseBody) > cfg.MaxBodyLogSize {
			fields = append(fields, "response_body", responseBody[:cfg.MaxBodyLogSize]+"... (truncated)")
			fields = append(fields, "response_body_size", len(responseBody))
		} else {
			// Try to parse as JSON for prettier logging
			var prettyBody interface{}
			if json.Unmarshal([]byte(responseBody), &prettyBody) == nil {
				fields = append(fields, "response_body", prettyBody)
			} else {
				fields = append(fields, "response_body", responseBody)
			}
		}
	}
	
	// Log based on status code
	if c.Writer.Status() >= 500 {
		log.Error("Request failed", nil, fields...)
	} else if c.Writer.Status() >= 400 {
		log.Warn("Request error", fields...)
	} else {
		log.Info("Request processed", fields...)
	}
}

// Helper functions

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// maskString masks a string for logging
func maskString(s string) string {
	if len(s) <= 8 {
		return "********"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
