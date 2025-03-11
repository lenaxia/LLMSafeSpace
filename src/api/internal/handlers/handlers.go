package handlers

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Handlers contains all API handlers
type Handlers struct {
	logger   *logger.Logger
	services interfaces.Services
	sandbox  *SandboxHandler
	warmPool *WarmPoolHandler
	runtime  *RuntimeHandler
	profile  *ProfileHandler
	user     *UserHandler
}

// New creates a new Handlers instance
func New(log *logger.Logger, svc interfaces.Services) *Handlers {
	return &Handlers{
		logger:   log,
		services: svc,
		sandbox:  NewSandboxHandler(log, svc.GetSandbox(), svc.GetAuth()),
		warmPool: NewWarmPoolHandler(log, svc.GetWarmPool(), svc.GetAuth()),
		runtime:  NewRuntimeHandler(log, svc.GetAuth()),
		profile:  NewProfileHandler(log, svc.GetAuth()),
		user:     NewUserHandler(log, svc.GetAuth()),
	}
}

// RegisterRoutes registers all API routes
func (h *Handlers) RegisterRoutes(router *gin.Engine) {
	// API version group
	v1 := router.Group("/api/v1")
	
	// Register routes for each handler
	h.sandbox.RegisterRoutes(v1)
	h.warmPool.RegisterRoutes(v1)
	h.runtime.RegisterRoutes(v1)
	h.profile.RegisterRoutes(v1)
	h.user.RegisterRoutes(v1)
	
	// WebSocket route
	v1.GET("/sandboxes/:id/stream", h.sandbox.HandleWebSocket)
}

// LoggerMiddleware returns a middleware for logging requests
func LoggerMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		// Process request
		c.Next()

		// Log request
		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		log.Info("API Request",
			"method", method,
			"path", path,
			"status", statusCode,
			"latency", latency,
			"client_ip", clientIP,
		)
	}
}

// MetricsMiddleware returns a middleware for collecting metrics
func MetricsMiddleware(metrics interfaces.MetricsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		// Process request
		c.Next()

		// Record metrics
		latency := time.Since(start)
		statusCode := c.Writer.Status()
		
		metrics.RecordRequest(method, path, statusCode, latency, c.Writer.Size())
	}
}
