package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
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

// NewRouter creates a new Gin router with all routes configured.
// proxyHandler may be nil — proxy routes are not registered in that case.
func NewRouter(services interfaces.Services, logger *logger.Logger, proxyHandler *handlers.ProxyHandler, config ...RouterConfig) *gin.Engine {
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
	router.Use(middleware.RateLimitMiddleware(services.GetRateLimiter(), logger, cfg.RateLimitConfig))
	router.Use(middleware.ErrorHandlerMiddleware(logger))

	// Add WebSocket security middleware to WebSocket routes
	wsGroup := router.Group("/api/v1/sandboxes/:id/stream")
	wsGroup.Use(middleware.WebSocketSecurityMiddleware(logger, cfg.AllowedWebSocketOrigins...))
	wsGroup.Use(middleware.WebSocketMetricsMiddleware(services.GetMetrics()))

	// Auth routes (public — no auth middleware)
	authGroup := router.Group("/api/v1/auth")
	registerAuthRoutes(authGroup, services)

	// Authenticated workspace routes
	workspaceGroup := router.Group("/api/v1/workspaces")
	workspaceGroup.Use(services.GetAuth().AuthMiddleware())
	registerWorkspaceRoutes(workspaceGroup, services)

	// Authenticated sandbox CRUD routes (Create, List, Get, Delete, Status).
	// These do NOT use the proxy ownership middleware because:
	//   - List/Create have no :id yet
	//   - Service-level methods perform their own ownership/permission checks
	// The path prefix is shared with the proxy group; Gin dispatches by full
	// path (e.g. GET /api/v1/sandboxes/:id/sessions still routes to the proxy
	// group below).
	if services.GetSandbox() != nil {
		sandboxCRUDGroup := router.Group("/api/v1/sandboxes")
		sandboxCRUDGroup.Use(services.GetAuth().AuthMiddleware())
		registerSandboxCRUDRoutes(sandboxCRUDGroup, services)
	}

	// Proxy routes — only registered when a ProxyHandler is provided
	if proxyHandler != nil {
		proxyGroup := router.Group("/api/v1/sandboxes")
		proxyGroup.Use(services.GetAuth().AuthMiddleware())
		proxyGroup.Use(sandboxOwnershipMiddleware(services, proxyHandler))
		registerProxyRoutes(proxyGroup, proxyHandler)
	}

	// Metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Liveness probe — always returns 200 if the process is responding.
	// Use this for Kubernetes livenessProbe.
	livenessHandler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/livez", livenessHandler)

	// Legacy alias retained for backwards compatibility with deployments
	// that already point at /health. Equivalent to /livez.
	router.GET("/health", livenessHandler)

	// Readiness probe — verifies that all upstream dependencies (Postgres,
	// Redis) are reachable. Returns 503 if any dependency is down. Use this
	// for Kubernetes readinessProbe so the pod is removed from Service
	// endpoints when its dependencies are unavailable.
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		var failures []string

		db := services.GetDatabase()
		if db == nil {
			failures = append(failures, "database: not configured")
		} else if err := db.Ping(ctx); err != nil {
			failures = append(failures, "database: "+err.Error())
		}

		cache := services.GetCache()
		if cache == nil {
			failures = append(failures, "cache: not configured")
		} else if err := cache.Ping(ctx); err != nil {
			failures = append(failures, "cache: "+err.Error())
		}

		if len(failures) > 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "unhealthy",
				"failures": failures,
				"detail":   strings.Join(failures, "; "),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	return router
}

const (
	maxAuthBodyBytes  = 1 << 20 // 1 MiB max for auth request bodies
	authRatePerMinute = 20
	authRateBurst     = 5
)

// sanitizeBindError returns a user-safe error message for binding failures
// without leaking internal struct details.
func sanitizeBindError(err error) string {
	return "invalid request body"
}

// API key management routes.
func registerAuthRoutes(rg *gin.RouterGroup, services interfaces.Services) {
	authSvc := services.GetAuth()

	rg.POST("/register", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthBodyBytes)
		var req types.RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeBindError(err)})
			return
		}
		resp, err := authSvc.Register(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, resp)
	})

	rg.POST("/login", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthBodyBytes)
		var req types.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeBindError(err)})
			return
		}
		resp, err := authSvc.Login(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, resp)
	})

	apiKeyGroup := rg.Group("")
	apiKeyGroup.Use(authSvc.AuthMiddleware())
	apiKeyGroup.POST("/api-keys", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthBodyBytes)
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		var req types.CreateAPIKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeBindError(err)})
			return
		}
		apiKey, err := authSvc.CreateAPIKey(c.Request.Context(), userID, req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, apiKey)
	})
	apiKeyGroup.GET("/api-keys", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		keys, err := authSvc.ListAPIKeys(c.Request.Context(), userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if keys == nil {
			keys = []*types.APIKey{}
		}
		c.JSON(http.StatusOK, keys)
	})
	apiKeyGroup.DELETE("/api-keys/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := authSvc.DeleteAPIKey(c.Request.Context(), userID, c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
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

// sandboxOwnershipMiddleware verifies that the authenticated user owns the
// sandbox identified by the :id route parameter. It reads the sandbox CRD's
// user-id label and compares it to the authenticated user ID. Returns 404 if
// the sandbox does not exist, 403 if the user is not the owner.
func sandboxOwnershipMiddleware(services interfaces.Services, proxyHandler *handlers.ProxyHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		sandboxID := c.Param("id")
		userID := services.GetAuth().GetUserID(c)

		sb, err := proxyHandler.GetSandboxCRD(sandboxID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "sandbox not found"})
			return
		}

		ownerID, hasLabel := sb.Labels["user-id"]
		if !hasLabel || ownerID == "" || ownerID != userID {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}

		c.Set("sandbox", sb)
		c.Next()
	}
}

// registerProxyRoutes adds all /api/v1/sandboxes/:id proxy routes.
// All routes require authentication and ownership check (applied on the group).
func registerProxyRoutes(rg *gin.RouterGroup, proxyHandler *handlers.ProxyHandler) {
	rg.POST("/:id/sessions", proxyHandler.CreateSession)
	rg.GET("/:id/sessions", proxyHandler.ListSessions)
	rg.POST("/:id/sessions/:sessionId/message", proxyHandler.SendMessage)
	rg.POST("/:id/sessions/:sessionId/prompt", proxyHandler.SendPromptAsync)
	rg.GET("/:id/sessions/:sessionId/message", proxyHandler.GetHistory)
	rg.POST("/:id/sessions/:sessionId/abort", proxyHandler.AbortSession)
	rg.GET("/:id/events", proxyHandler.StreamEvents)
}

// registerSandboxCRUDRoutes adds /api/v1/sandboxes CRUD endpoints.
// Authentication is applied at the group level. Authorization (ownership and
// permission checks) is performed inside the SandboxService methods — the
// ownership middleware used by the proxy group is intentionally NOT applied
// here because List has no :id and the service performs its own checks.
func registerSandboxCRUDRoutes(rg *gin.RouterGroup, services interfaces.Services) {
	sbSvc := services.GetSandbox()
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
		result, err := sbSvc.ListSandboxes(c.Request.Context(), userID, limit, offset)
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
		var req types.CreateSandboxRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeBindError(err)})
			return
		}
		// Always trust the authenticated user ID over anything in the body.
		req.UserID = userID
		// Inject userID into context so service-level ownership checks (which
		// read from context via types.ContextKeyUserID) function correctly.
		ctx := context.WithValue(c.Request.Context(), types.ContextKeyUserID, userID)
		sb, err := sbSvc.CreateSandbox(ctx, &req)
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusCreated, sb)
	})

	rg.GET("/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		sb, err := sbSvc.GetSandbox(c.Request.Context(), c.Param("id"))
		if err != nil {
			if _, ok := err.(*types.SandboxNotFoundError); ok {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			respondWithError(c, err)
			return
		}
		// Authorization: only return sandboxes the user owns.
		// Sandbox CRDs carry the owner in the user-id label.
		if owner := sb.Labels["user-id"]; owner != "" && owner != userID {
			c.JSON(http.StatusNotFound, gin.H{"error": "sandbox not found"})
			return
		}
		c.JSON(http.StatusOK, sb)
	})

	rg.DELETE("/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		ctx := context.WithValue(c.Request.Context(), types.ContextKeyUserID, userID)
		if err := sbSvc.TerminateSandbox(ctx, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	rg.GET("/:id/status", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		status, err := sbSvc.GetSandboxStatus(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, status)
	})

	rg.POST("/:id/restart", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		ctx := context.WithValue(c.Request.Context(), types.ContextKeyUserID, userID)
		if err := sbSvc.RestartSandbox(ctx, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"message": "restart initiated"})
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
