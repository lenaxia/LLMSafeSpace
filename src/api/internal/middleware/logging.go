package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// LoggingMiddleware returns a middleware that logs requests
func LoggingMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		
		// Process request
		c.Next()
		
		// Log request
		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		
		if query != "" {
			path = path + "?" + query
		}
		
		// Log based on status code
		if statusCode >= 500 {
			log.Error("Request failed", nil,
				"status", statusCode,
				"method", method,
				"path", path,
				"ip", clientIP,
				"latency_ms", latency.Milliseconds(),
				"request_id", c.GetString("request_id"),
			)
		} else if statusCode >= 400 {
			log.Warn("Request error",
				"status", statusCode,
				"method", method,
				"path", path,
				"ip", clientIP,
				"latency_ms", latency.Milliseconds(),
				"request_id", c.GetString("request_id"),
			)
		} else {
			log.Info("Request processed",
				"status", statusCode,
				"method", method,
				"path", path,
				"ip", clientIP,
				"latency_ms", latency.Milliseconds(),
				"request_id", c.GetString("request_id"),
			)
		}
	}
}
