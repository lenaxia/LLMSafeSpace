package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/pkg/types"
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
		Debug:                   false,
		LoggingConfig:           middleware.DefaultLoggingConfig(),
		RateLimitConfig:         middleware.DefaultRateLimitConfig(),
		SecurityConfig:          middleware.DefaultSecurityConfig(),
		TracingConfig:           middleware.DefaultTracingConfig(),
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

	// Rate limiting is wired when a RateLimiterService is available
	// if cfg.RateLimitConfig.Enabled {
	// 	router.Use(middleware.RateLimitMiddleware(rateLimiter, logger, cfg.RateLimitConfig))
	// }

	// Create handlers
	//h := handlers.New(logger, services)

	// Register routes
	//h.RegisterRoutes(router)

	// Add WebSocket security middleware to WebSocket routes
	wsGroup := router.Group("/api/v1/sandboxes/:id/stream")
	wsGroup.Use(middleware.WebSocketSecurityMiddleware(logger, cfg.AllowedWebSocketOrigins...))
	wsGroup.Use(middleware.WebSocketMetricsMiddleware(services.GetMetrics()))

	// Authenticated workspace routes
	workspaceGroup := router.Group("/api/v1/workspaces")
	workspaceGroup.Use(services.GetAuth().AuthMiddleware())
	registerWorkspaceRoutes(workspaceGroup, services)

	// Metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	return router
}

// registerWorkspaceRoutes adds all /api/v1/workspaces routes to the given group.
// All routes require authentication (the group already has auth middleware applied).
func registerWorkspaceRoutes(rg *gin.RouterGroup, services interfaces.Services) {
	wsSvc := services.GetWorkspace()
	authSvc := services.GetAuth()

	rg.GET("", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		limit := 20
		offset := 0
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := c.Query("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}
		result, err := wsSvc.ListWorkspaces(c.Request.Context(), userID, types.ListOptions{Limit: limit, Offset: offset})
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})

	rg.POST("", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		var req types.CreateWorkspaceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ws, err := wsSvc.CreateWorkspace(c.Request.Context(), userID, req)
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusCreated, ws)
	})

	rg.GET("/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		ws, err := wsSvc.GetWorkspace(c.Request.Context(), userID, c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, ws)
	})

	rg.DELETE("/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := wsSvc.DeleteWorkspace(c.Request.Context(), userID, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	rg.POST("/:id/suspend", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := wsSvc.SuspendWorkspace(c.Request.Context(), userID, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusAccepted)
	})

	rg.POST("/:id/resume", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := wsSvc.ResumeWorkspace(c.Request.Context(), userID, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusAccepted)
	})

	rg.GET("/:id/status", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		status, err := wsSvc.GetWorkspaceStatus(c.Request.Context(), userID, c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, status)
	})

	rg.PUT("/:id/credentials", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		var req types.SetCredentialsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := wsSvc.SetCredentials(c.Request.Context(), userID, c.Param("id"), req); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	rg.DELETE("/:id/credentials", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := wsSvc.DeleteCredentials(c.Request.Context(), userID, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// respondWithError maps API errors to HTTP responses.
func respondWithError(c *gin.Context, err error) {
	type apiError interface {
		StatusCode() int
		Error() string
	}
	if ae, ok := err.(apiError); ok {
		c.JSON(ae.StatusCode(), gin.H{"error": ae.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
