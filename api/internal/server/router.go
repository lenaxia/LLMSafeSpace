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
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
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

	// SecretsHandler is the handler for secret management endpoints (optional)
	SecretsHandler *handlers.SecretsHandler

	// RotateKeyHandler is the handler for key rotation (optional)
	RotateKeyHandler *handlers.RotateKeyHandler
}

// DefaultRouterConfig returns the default router configuration
func DefaultRouterConfig() RouterConfig {
	rlCfg := middleware.DefaultRateLimitConfig()
	// The /events SSE endpoint is a long-lived connection, not a per-request
	// API call. Exempt it from the token-bucket rate limiter so reconnects
	// after network drops don't trigger 429s.
	rlCfg.ExemptPaths = []string{"/events"}
	return RouterConfig{
		Debug:                   false,
		LoggingConfig:           middleware.DefaultLoggingConfig(),
		RateLimitConfig:         rlCfg,
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
	wsGroup := router.Group("/api/v1/workspaces/:id/stream")
	wsGroup.Use(middleware.WebSocketSecurityMiddleware(logger, cfg.AllowedWebSocketOrigins...))
	wsGroup.Use(middleware.WebSocketMetricsMiddleware(services.GetMetrics()))

	// Auth routes (public — no auth middleware)
	authGroup := router.Group("/api/v1/auth")
	registerAuthRoutes(authGroup, services)

	// Authenticated workspace routes
	workspaceGroup := router.Group("/api/v1/workspaces")
	workspaceGroup.Use(services.GetAuth().AuthMiddleware())
	registerWorkspaceRoutes(workspaceGroup, services)

	// Sessions/active endpoint — needs proxyHandler for active session data
	if proxyHandler != nil {
		workspaceGroup.GET("/:id/sessions/active", func(c *gin.Context) {
			userID := services.GetAuth().GetUserID(c)
			if userID == "" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
				return
			}
			workspaceID := c.Param("id")
			// Get active sessions keyed by workspace ID directly.
			active := proxyHandler.GetActiveSessions(workspaceID)
			if active == nil {
				active = []string{}
			}
			c.JSON(http.StatusOK, types.ActiveSessionsResponse{
				Active:    active,
				MaxActive: 5, // default; could read from workspace CRD
			})
		})
	}

	// Authenticated sandbox CRUD routes (Create, List, Get, Delete, Status).
	// These do NOT use the proxy ownership middleware because:
	//   - List/Create have no :id yet
	//   - Service-level methods perform their own ownership/permission checks
	// The path prefix is shared with the proxy group; Gin dispatches by full
	// Proxy routes — registered within workspace group when a ProxyHandler is provided
	if proxyHandler != nil {
		registerProxyRoutes(workspaceGroup, proxyHandler)
	}

	// Secret management routes (Epic 10)
	if cfg.SecretsHandler != nil {
		secretsGroup := router.Group("/api/v1/secrets")
		secretsGroup.Use(services.GetAuth().AuthMiddleware())
		secretsGroup.POST("", cfg.SecretsHandler.CreateSecret)
		secretsGroup.GET("", cfg.SecretsHandler.ListSecrets)
		secretsGroup.GET("/audit", cfg.SecretsHandler.GetAuditLog)
		secretsGroup.GET("/:id", cfg.SecretsHandler.GetSecret)
		secretsGroup.PUT("/:id", cfg.SecretsHandler.UpdateSecret)
		secretsGroup.DELETE("/:id", cfg.SecretsHandler.DeleteSecret)

		// Bindings are under the workspace group (already authenticated)
		workspaceGroup.PUT("/:id/bindings", cfg.SecretsHandler.SetBindings)
		workspaceGroup.GET("/:id/bindings", cfg.SecretsHandler.GetBindings)
	}

	// Key rotation endpoint (Epic 10)
	if cfg.RotateKeyHandler != nil {
		accountGroup := router.Group("/api/v1/account")
		accountGroup.Use(services.GetAuth().AuthMiddleware())
		accountGroup.POST("/rotate-key", cfg.RotateKeyHandler.RotateKey)
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

// setSessionCookie sets the HttpOnly session cookie on the response.
func setSessionCookie(c *gin.Context, token string) {
	c.SetCookie("lsp_session", token, 86400, "/", "", true, true)
}

// API key management routes.
func registerAuthRoutes(rg *gin.RouterGroup, services interfaces.Services) {
	authSvc := services.GetAuth()

	// Public: feature flag discovery
	rg.GET("/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, types.AuthConfig{
			RegistrationEnabled: false, // TODO: read from config
			OIDCEnabled:         false,
		})
	})

	rg.POST("/register", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthBodyBytes)
		var req types.RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeBindError(err)})
			return
		}
		resp, err := authSvc.Register(c.Request.Context(), req)
		if err != nil {
			respondWithError(c, err)
			return
		}
		setSessionCookie(c, resp.Token)
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
		setSessionCookie(c, resp.Token)
		c.JSON(http.StatusOK, resp)
	})

	// Public: logout (clears cookie)
	rg.POST("/logout", func(c *gin.Context) {
		c.SetCookie("lsp_session", "", -1, "/", "", true, true)
		c.Status(http.StatusNoContent)
	})

	// Authenticated: current user info
	meGroup := rg.Group("")
	meGroup.Use(authSvc.AuthMiddleware())
	meGroup.GET("/me", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		user, err := services.GetDatabase().GetUser(c.Request.Context(), userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
			return
		}
		c.JSON(http.StatusOK, user)
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

	rg.PUT("/:id", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		var body struct {
			Name string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}
		if err := wsSvc.RenameWorkspace(c.Request.Context(), userID, c.Param("id"), body.Name); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
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
		c.Header("Deprecation", "true")
		c.Header("Sunset", "2027-01-01")
		c.Header("Link", `</api/v1/secrets>; rel="successor-version"`)
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
		c.Header("Deprecation", "true")
		c.Header("Sunset", "2027-01-01")
		c.Header("Link", `</api/v1/secrets>; rel="successor-version"`)
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

	// --- Frontend routes (Phase A) ---

	rg.POST("/:id/activate", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		// Pass sessionID through context for secret injection during activation.
		ctx := c.Request.Context()
		if sid, exists := c.Get("sessionID"); exists {
			ctx = workspace.ContextWithSessionID(ctx, sid.(string))
		}
		resp, err := wsSvc.ActivateWorkspace(ctx, userID, c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, resp)
	})

	rg.GET("/:id/sessions", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		sessions, err := wsSvc.ListWorkspaceSessions(c.Request.Context(), userID, c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, sessions)
	})

	rg.POST("/:id/sessions/new", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		resp, err := wsSvc.EnsureSession(c.Request.Context(), userID, c.Param("id"))
		if err != nil {
			respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, resp)
	})

	rg.PUT("/:id/sessions/:sessionId/title", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		var body struct {
			Title string `json:"title" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		if err := wsSvc.RenameSession(c.Request.Context(), userID, c.Param("id"), c.Param("sessionId"), body.Title); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// registerProxyRoutes adds all /api/v1/sandboxes/:id proxy routes.
// All routes require authentication and ownership check (applied on the group).
func registerProxyRoutes(rg *gin.RouterGroup, proxyHandler *handlers.ProxyHandler) {
	rg.POST("/:id/sessions/:sessionId/message", proxyHandler.SendMessage)
	rg.POST("/:id/sessions/:sessionId/prompt", proxyHandler.SendPromptAsync)
	rg.GET("/:id/sessions/:sessionId/message", proxyHandler.GetHistory)
	rg.GET("/:id/sessions/:sessionId", proxyHandler.GetSession)
	rg.POST("/:id/sessions/:sessionId/abort", proxyHandler.AbortSession)
	rg.GET("/:id/events", proxyHandler.StreamEvents)
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
