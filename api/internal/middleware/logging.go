package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	httputil "github.com/lenaxia/llmsafespace/pkg/http"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
)

const (
	logRequestIDLength = 8
	maxBodyLogSize     = 1024 // 1KB
)

var (
	bodyLogPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

// LoggingConfig defines configuration for the logging middleware
type LoggingConfig struct {
	// LogRequestBody indicates whether to log request bodies
	LogRequestBody bool
	
	// LogResponseBody indicates whether to log response bodies
	LogResponseBody bool
	
	// MaxBodyLogSize is the maximum size of request/response bodies to log
	MaxBodyLogSize int
	
	// SensitiveFields are JSON fields that should be redacted in request/response bodies
	SensitiveFields []string
	
	// SkipPaths are paths that should not be logged
	SkipPaths []string
}

// DefaultLoggingConfig returns the default logging configuration
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		LogRequestBody:  true,
		LogResponseBody: true,
		MaxBodyLogSize:  1024, // 1KB
		SensitiveFields: []string{"password", "token", "secret", "key", "apiKey", "credit_card"},
		SkipPaths:       []string{"/health", "/metrics"},
	}
}

func LoggingMiddleware(log interfaces.LoggerInterface, config ...LoggingConfig) gin.HandlerFunc {
	// Use default config if none provided
	cfg := DefaultLoggingConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	return func(c *gin.Context) {
		// Skip logging for certain paths
		path := c.Request.URL.Path
		for _, skipPath := range cfg.SkipPaths {
			if path == skipPath {
				c.Next()
				return
			}
		}
		
		start := time.Now()
		requestID := generateRequestID()

		// Log request details
		logRequest(c, log, requestID, cfg)

		// Capture response
		writer := httputil.NewBodyCaptureWriter(c)
		c.Writer = writer

		// Process request
		c.Next()

		// Log response details
		logResponse(c, log, requestID, start, writer.GetBody(), cfg)
	}
}

func logRequest(c *gin.Context, log interfaces.LoggerInterface, requestID string, cfg LoggingConfig) {
	fields := []interface{}{
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"remote_addr", c.Request.RemoteAddr,
		"user_agent", c.Request.UserAgent(),
		"request_id", requestID,
	}

	if apiKey, exists := c.Get("apiKey"); exists {
		fields = append(fields, "api_key", utilities.MaskString(apiKey.(string)))
	}

	// Log request body if present and configured to do so
	if cfg.LogRequestBody && c.Request.Body != nil && c.Request.ContentLength > 0 {
		body, err := readAndReplaceBody(c)
		if err == nil {
			// Add content length
			fields = append(fields, "request_body_size", len(body))
			
			// Try to parse as JSON first
			var jsonBody map[string]interface{}
			if err := json.Unmarshal(body, &jsonBody); err == nil {
				// Create a copy of the map to avoid modifying the original
				maskedBody := make(map[string]interface{})
				for k, v := range jsonBody {
					maskedBody[k] = v
				}
				// Use the utilities.MaskSensitiveFieldsWithList function to mask sensitive fields
				utilities.MaskSensitiveFieldsWithList(maskedBody, cfg.SensitiveFields)
				fields = append(fields, "request_body", maskedBody)
			} else {
				// If not JSON or too large, truncate it
				if len(body) > cfg.MaxBodyLogSize {
					truncatedBody := string(body[:cfg.MaxBodyLogSize]) + "... (truncated)"
					fields = append(fields, "request_body", truncatedBody)
				} else {
					fields = append(fields, "request_body", string(body))
				}
			}
		}
	}

	log.Info("Request received", fields...)
}

func logResponse(c *gin.Context, log interfaces.LoggerInterface, requestID string, start time.Time, responseBody string, cfg LoggingConfig) {
	duration := time.Since(start)
	fields := []interface{}{
		"status", c.Writer.Status(),
		"duration", duration.String(),
		"response_size", c.Writer.Size(),
		"request_id", requestID,
	}

	// Log response body if configured to do so and either:
	// 1. It's an error response (status >= 400)
	// 2. LogResponseBody is true for all responses
	if (cfg.LogResponseBody || c.Writer.Status() >= 400) && responseBody != "" {
		// Try to parse as JSON first
		var jsonBody map[string]interface{}
		if err := json.Unmarshal([]byte(responseBody), &jsonBody); err == nil {
			// Create a copy of the map to avoid modifying the original
			maskedBody := make(map[string]interface{})
			for k, v := range jsonBody {
				maskedBody[k] = v
			}
			// Use the utilities.MaskSensitiveFieldsWithList function to mask sensitive fields
			utilities.MaskSensitiveFieldsWithList(maskedBody, cfg.SensitiveFields)
			fields = append(fields, "response_body", maskedBody)
		} else {
			// If not JSON or too large, truncate it
			if len(responseBody) > cfg.MaxBodyLogSize {
				truncatedBody := responseBody[:cfg.MaxBodyLogSize] + "... (truncated)"
				fields = append(fields, "response_body", truncatedBody)
			} else {
				fields = append(fields, "response_body", responseBody)
			}
		}
	}

	log.Info("Request completed", fields...)
}


func readAndReplaceBody(c *gin.Context) ([]byte, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body.Close()

	// Replace body with a new reader
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}

func generateRequestID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, logRequestIDLength)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
