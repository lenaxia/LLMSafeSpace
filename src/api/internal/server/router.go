package server

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
)

// RouterConfig defines configuration for the router
type RouterConfig struct {
	// Debug enables debug mode
	Debug bool
	
	// LoggingConfig is the configuration for the logging middleware
	LoggingConfig middleware.LoggingConfig
	
	// RateLimitConfig is the configuration for the rate limiting middleware
	RateLimitConfig middleware.RateLimitConfig
	
	// SecurityConfig is the configuration for the security middleware
	SecurityConfig middleware.SecurityConfig
	
	// TracingConfig is the configuration for the tracing middleware
	TracingConfig middleware.TracingConfig
	
	// AllowedWebSocketOrigins is a list of allowed origins for WebSocket connections
	AllowedWebSocketOrigins []string
}

// DefaultRouterConfig returns the default router configuration
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		Debug:                 false,
		LoggingConfig:         middleware.DefaultLoggingConfig(),
		RateLimitConfig:       middleware.DefaultRateLimitConfig(),
		SecurityConfig:        middleware.DefaultSecurityConfig(),
		TracingConfig:         middleware.DefaultTracingConfig(),
		AllowedWebSocketOrigins: []string{"*"},
	}
}

// NewRouter creates a new Gin router with all routes configured
func NewRouter(services interfaces.Services, logger *logger.Logger, config ...RouterConfig) *gin.Engine {
	// Use default config if none provided
	cfg := DefaultRouterConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	
	// Set Gin mode
	if cfg.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	
	// Create router
	router := gin.New()
	
	// Add middleware in the correct order
	router.Use(middleware.RecoveryMiddleware(logger))
	router.Use(middleware.TracingMiddleware(logger, cfg.TracingConfig))
	router.Use(middleware.SecurityMiddleware(logger, cfg.SecurityConfig))
	router.Use(middleware.LoggingMiddleware(logger, cfg.LoggingConfig))
	router.Use(middleware.MetricsMiddleware(services.GetMetrics()))
	router.Use(middleware.ErrorHandlerMiddleware(logger))
	
	// Add rate limiting middleware if enabled
	if cfg.RateLimitConfig.Enabled {
		router.Use(middleware.RateLimitMiddleware(services.GetCache(), logger, cfg.RateLimitConfig))
	}
	
	// Create handlers
	h := handlers.New(logger, services)
	
	// Register routes
	h.RegisterRoutes(router)
	
	// Add WebSocket security middleware to WebSocket routes
	wsGroup := router.Group("/api/v1/sandboxes/:id/stream")
	wsGroup.Use(middleware.WebSocketSecurityMiddleware(logger, cfg.AllowedWebSocketOrigins...))
	wsGroup.Use(middleware.WebSocketMetricsMiddleware(services.GetMetrics()))
	
	// Metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	
	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	
	return router
}
