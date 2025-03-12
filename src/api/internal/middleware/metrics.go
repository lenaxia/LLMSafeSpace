package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
)

// MetricsMiddleware returns a middleware that collects metrics
func MetricsMiddleware(metricsService interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		
		// Process request
		c.Next()
		
		// Calculate request duration
		duration := time.Since(start)
		
		// Record metrics
		metricsService.RecordRequest(
			c.Request.Method,
			c.FullPath(),
			c.Writer.Status(),
			duration,
			c.Writer.Size(),
		)
	}
}

// WebSocketMetricsMiddleware returns a middleware that tracks WebSocket connections
func WebSocketMetricsMiddleware(metricsService interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Increment active connections before processing
		metricsService.IncrementActiveConnections("websocket")
		
		// Process request
		c.Next()
		
		// Decrement active connections after processing
		metricsService.DecrementActiveConnections("websocket")
	}
}

// ExecutionMetricsMiddleware returns a middleware that tracks code execution
func ExecutionMetricsMiddleware(metricsService interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		
		// Process request
		c.Next()
		
		// Get execution type and runtime from request
		execType := c.PostForm("type")
		if execType == "" {
			execType = "unknown"
		}
		
		runtime := c.Param("runtime")
		if runtime == "" {
			runtime = "unknown"
		}
		
		// Calculate execution duration
		duration := time.Since(start)
		
		// Record metrics
		status := strconv.Itoa(c.Writer.Status())
		metricsService.RecordExecution(execType, runtime, status, duration)
	}
}
