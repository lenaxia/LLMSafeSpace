package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	
	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// ErrorHandlerMiddleware returns a middleware that handles errors
func ErrorHandlerMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Create a copy of the request body for logging
		var requestBody []byte
		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			requestBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}
		
		// Create a response writer that captures the response
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw
		
		// Process request
		c.Next()
		
		// Check for errors
		if len(c.Errors) > 0 {
			// Get the last error
			err := c.Errors.Last().Err
			
			// Log the error with request details
			logError(log, c, err, requestBody, blw.body.String())
			
			// Handle the error
			handleError(c, err)
		}
	}
}

// bodyLogWriter is a gin.ResponseWriter that captures the response body
type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write captures the response body
func (w *bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// logError logs an error with request details
func logError(log *logger.Logger, c *gin.Context, err error, requestBody []byte, responseBody string) {
	// Determine log level based on error type
	var logLevel string
	if apiErr, ok := err.(*errors.APIError); ok {
		switch apiErr.Type {
		case errors.ErrorTypeValidation, errors.ErrorTypeBadRequest:
			logLevel = "warn"
		case errors.ErrorTypeNotFound:
			logLevel = "info"
		default:
			logLevel = "error"
		}
	} else {
		logLevel = "error"
	}
	
	// Create log fields
	fields := []interface{}{
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"status", c.Writer.Status(),
		"client_ip", c.ClientIP(),
		"request_id", c.GetString("request_id"),
		"user_id", c.GetString("userID"),
	}
	
	// Add request body if available (truncate if too large)
	if len(requestBody) > 0 {
		reqBodyStr := string(requestBody)
		if len(reqBodyStr) > 1000 {
			reqBodyStr = reqBodyStr[:1000] + "... (truncated)"
		}
		fields = append(fields, "request_body", reqBodyStr)
	}
	
	// Add response body if available (truncate if too large)
	if responseBody != "" {
		if len(responseBody) > 1000 {
			responseBody = responseBody[:1000] + "... (truncated)"
		}
		fields = append(fields, "response_body", responseBody)
	}
	
	// Add stack trace for internal errors
	if apiErr, ok := err.(*errors.APIError); ok && apiErr.Type == errors.ErrorTypeInternal {
		fields = append(fields, "stack_trace", string(debug.Stack()))
	}
	
	// Log the error with the appropriate level
	switch logLevel {
	case "info":
		log.Info(fmt.Sprintf("Request error: %v", err), fields...)
	case "warn":
		log.Warn(fmt.Sprintf("Request error: %v", err), fields...)
	default:
		log.Error("Request error", err, fields...)
	}
}

// handleError sends an appropriate error response
func handleError(c *gin.Context, err error) {
	// Check if it's an API error
	if apiErr, ok := err.(*errors.APIError); ok {
		// Get status code from API error
		statusCode := apiErr.StatusCode()
		
		// Add rate limit headers if applicable
		if apiErr.Type == errors.ErrorTypeRateLimit {
			if limit, ok := apiErr.Details["limit"].(int); ok {
				c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
			}
			if reset, ok := apiErr.Details["reset"].(int64); ok {
				c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", reset))
			}
		}
		
		// Send error response
		c.JSON(statusCode, gin.H{
			"error": gin.H{
				"code":    apiErr.Code,
				"message": apiErr.Message,
				"details": apiErr.Details,
			},
		})
		return
	}
	
	// Handle generic errors
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": gin.H{
			"code":    "internal_error",
			"message": "An unexpected error occurred",
		},
	})
}

// HandleAPIError handles an API error in a handler
func HandleAPIError(c *gin.Context, err error) {
	// Add error to gin context
	_ = c.Error(err)
	
	// Abort the request
	c.Abort()
}
