package middleware

import (
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Request metrics
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	httpResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "path"},
	)

	// WebSocket metrics
	wsConnectionsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ws_connections_active",
			Help: "Number of active WebSocket connections",
		},
		[]string{"type"},
	)

	wsConnectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ws_connections_total",
			Help: "Total number of WebSocket connections",
		},
		[]string{"type"},
	)

	// Execution metrics
	codeExecutionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "code_executions_total",
			Help: "Total number of code executions",
		},
		[]string{"type", "runtime", "status"},
	)

	codeExecutionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "code_execution_duration_seconds",
			Help:    "Code execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
		},
		[]string{"type", "runtime"},
	)
)

func init() {
	// Register metrics with Prometheus
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(httpResponseSize)
	prometheus.MustRegister(wsConnectionsActive)
	prometheus.MustRegister(wsConnectionsTotal)
	prometheus.MustRegister(codeExecutionsTotal)
	prometheus.MustRegister(codeExecutionDuration)
}

// MetricsMiddleware returns a middleware that collects metrics
func MetricsMiddleware(metricsService interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip metrics for certain paths
		path := c.Request.URL.Path
		if path == "/metrics" || path == "/health" {
			c.Next()
			return
		}

		// Use normalized path to reduce cardinality
		normalizedPath := getNormalizedPath(c.FullPath())
		
		// Start timer
		start := time.Now()
		
		// Process request
		c.Next()
		
		// Calculate request duration
		duration := time.Since(start)
		
		// Record metrics
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		
		// Update Prometheus metrics
		httpRequestsTotal.WithLabelValues(method, normalizedPath, status).Inc()
		httpRequestDuration.WithLabelValues(method, normalizedPath).Observe(duration.Seconds())
		httpResponseSize.WithLabelValues(method, normalizedPath).Observe(float64(c.Writer.Size()))
		
		// Record metrics using service
		metricsService.RecordRequest(
			method,
			normalizedPath,
			c.Writer.Status(),
			duration,
			c.Writer.Size(),
		)
	}
}

// WebSocketMetricsMiddleware returns a middleware that tracks WebSocket connections
func WebSocketMetricsMiddleware(metricsService interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		connType := c.Param("type")
		if connType == "" {
			connType = "websocket"
		}
		
		// Increment active connections before processing
		wsConnectionsActive.WithLabelValues(connType).Inc()
		wsConnectionsTotal.WithLabelValues(connType).Inc()
		
		// Get user ID if available
		if id, exists := c.Get("userID"); exists {
			userID = id.(string)
		}
		
		metricsService.IncrementActiveConnections(connType)
		
		// Process request
		c.Next()
		
		// Decrement active connections after processing
		wsConnectionsActive.WithLabelValues(connType).Dec()
		
		// Get user ID if available
		var userID string
		if id, exists := c.Get("userID"); exists {
			userID = id.(string)
		}
		
		metricsService.DecrementActiveConnections(connType)
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
			execType = c.GetHeader("X-Execution-Type")
			if execType == "" {
				execType = "unknown"
			}
		}
		
		runtime := c.Param("runtime")
		if runtime == "" {
			runtime = c.GetHeader("X-Runtime")
			if runtime == "" {
				runtime = "unknown"
			}
		}
		
		// Calculate execution duration
		duration := time.Since(start)
		
		// Record metrics
		status := strconv.Itoa(c.Writer.Status())
		
		// Update Prometheus metrics
		codeExecutionsTotal.WithLabelValues(execType, runtime, status).Inc()
		codeExecutionDuration.WithLabelValues(execType, runtime).Observe(duration.Seconds())
		
		// Get user ID if available
		userID = ""
		if id, exists := c.Get("userID"); exists {
			userID = id.(string)
		}
		
		// Record metrics using service
		userID = ""
		if id, exists := c.Get("userID"); exists {
			userID = id.(string)
		}
		metricsService.RecordExecution(execType, runtime, status, duration)
	}
}

// getNormalizedPath returns a normalized path to reduce cardinality
func getNormalizedPath(path string) string {
	// Replace path parameters with placeholders
	// Example: /api/v1/sandboxes/:id/execute -> /api/v1/sandboxes/{id}/execute
	re := regexp.MustCompile(`:[^/]+`)
	return re.ReplaceAllString(path, "{id}")
}
