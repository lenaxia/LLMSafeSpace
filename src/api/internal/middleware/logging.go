package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
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

func LoggingMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		requestID := generateRequestID()

		// Log request details
		logRequest(c, log, requestID)

		// Capture response
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		// Process request
		c.Next()

		// Log response details
		logResponse(c, log, requestID, start, blw.body.String())
	}
}

func logRequest(c *gin.Context, log *logger.Logger, requestID string) {
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

	// Log request body if present
	if c.Request.Body != nil && c.Request.ContentLength > 0 {
		body, err := readAndReplaceBody(c)
		if err == nil {
			var jsonBody map[string]interface{}
			if err := json.Unmarshal(body, &jsonBody); err == nil {
				maskSensitiveFields(jsonBody)
				fields = append(fields, "body", jsonBody)
			} else {
				fields = append(fields, "body", string(body))
			}
		}
	}

	log.Info("Request received", fields...)
}

func logResponse(c *gin.Context, log *logger.Logger, requestID string, start time.Time, responseBody string) {
	duration := time.Since(start)
	fields := []interface{}{
		"status", c.Writer.Status(),
		"duration", duration.String(),
		"response_size", c.Writer.Size(),
		"request_id", requestID,
	}

	if c.Writer.Status() >= 400 && responseBody != "" {
		var jsonBody map[string]interface{}
		if err := json.Unmarshal([]byte(responseBody), &jsonBody); err == nil {
			maskSensitiveFields(jsonBody)
			fields = append(fields, "response_body", jsonBody)
		} else {
			fields = append(fields, "response_body", truncateString(responseBody, maxBodyLogSize))
		}
	}

	log.Info("Request completed", fields...)
}

func maskSensitiveFields(data map[string]interface{}) {
	sensitiveKeys := []string{"password", "api_key", "token", "secret"}
	for _, k := range sensitiveKeys {
		if v, exists := data[k]; exists {
			data[k] = utilities.MaskString(fmt.Sprint(v))
		}
	}
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

type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}
