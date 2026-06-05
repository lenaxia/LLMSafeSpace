// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	"github.com/lenaxia/llmsafespace/api/internal/utilities"
	"github.com/lenaxia/llmsafespace/pkg/settings"
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

	// SettingsHandler is the optional settings handler for admin/user settings routes
	SettingsHandler *handlers.SettingsHandler

	// InstanceSettings provides access to instance settings for feature flags
	InstanceSettings *settings.InstanceService

	// SecretsHandler is the handler for secret management endpoints (optional)
	SecretsHandler *handlers.SecretsHandler

	// CredentialsHandler is the handler for credential set CRUD (optional)
	CredentialsHandler *handlers.CredentialsHandler

	// RotateKeyHandler is the handler for key rotation (optional)
	RotateKeyHandler *handlers.RotateKeyHandler

	// TerminalHandler is the handler for WebSocket terminal proxy (optional)
	TerminalHandler *handlers.TerminalHandler

	// AgentReloadHandler handles POST /api/v1/workspaces/:id/agent/reload (optional)
	AgentReloadHandler *handlers.AgentReloadHandler

	// BulkReloadHandler handles POST /api/v1/users/me/agents/reload (optional)
	BulkReloadHandler *handlers.BulkReloadHandler
}

// DefaultRouterConfig returns the default router configuration
func DefaultRouterConfig() RouterConfig {
	rlCfg := middleware.DefaultRateLimitConfig()
	// The /events SSE endpoint is a long-lived connection, not a per-request
	// API call. Exempt it from the token-bucket rate limiter so reconnects
	// after network drops don't trigger 429s.
	rlCfg.ExemptPaths = []string{"/events", "/session-events"}
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
func NewRouter(services interfaces.Services, logger *apilogger.Logger, proxyHandler *handlers.ProxyHandler, config ...RouterConfig) *gin.Engine {
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
	router.Use(middleware.RateLimitMiddleware(services.GetRateLimiter(), logger, cfg.RateLimitConfig, cfg.InstanceSettings))
	router.Use(middleware.ErrorHandlerMiddleware(logger))

	// F1.1.4 (Epic 17): the previous `/api/v1/workspaces/:id/stream`
	// group had middleware attached but no handlers — dead code that
	// existed only because an earlier API design wired SSE here. The
	// current session SSE endpoint is `/api/v1/workspaces/:id/session-events`
	// registered below; the user-scoped event stream is at `/api/v1/events`.
	// The actual WebSocket terminal endpoint is
	// `/api/v1/workspaces/:id/terminal/...` which gets the WebSocket
	// security middleware via its own router group.
	_ = router // wsGroup removal — kept the var to avoid unused-import warnings if a future commit re-adds /stream.

	// Auth routes (public — no auth middleware)
	authGroup := router.Group("/api/v1/auth")
	registerAuthRoutes(authGroup, services, cfg.InstanceSettings, logger)

	// Authenticated workspace routes
	workspaceGroup := router.Group("/api/v1/workspaces")
	workspaceGroup.Use(services.GetAuth().AuthMiddleware())
	registerWorkspaceRoutes(workspaceGroup, services, proxyHandler, cfg)

	// Epic 27b: Bulk agent reload across all pending workspaces.
	if cfg.BulkReloadHandler != nil {
		userGroup := router.Group("/api/v1/users/me")
		userGroup.Use(services.GetAuth().AuthMiddleware())
		userGroup.POST("/agents/reload", cfg.BulkReloadHandler.BulkReload)
	}

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
				MaxActive: getMaxActiveSessions(c.Request.Context(), cfg.InstanceSettings),
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

		// S28.3: User-scoped SSE stream (authenticated, rate-limit exempt)
		eventsGroup := router.Group("/api/v1")
		eventsGroup.Use(services.GetAuth().AuthMiddleware())
		eventsGroup.GET("/events", proxyHandler.StreamUserEvents)
	}

	// Terminal proxy routes (WebSocket terminal to sandbox pod)
	if cfg.TerminalHandler != nil {
		// Ticket endpoint — on the authenticated workspace group (requires JWT/API key)
		workspaceGroup.POST("/:id/terminal/ticket", cfg.TerminalHandler.HandleTicket)
		// WebSocket endpoint — on the ROOT router (auth via one-time ticket, not JWT)
		router.GET("/api/v1/workspaces/:id/terminal", cfg.TerminalHandler.HandleTerminal)
	}

	// Settings routes (admin + user)
	if cfg.SettingsHandler != nil {
		registerSettingsRoutes(router, services, cfg.SettingsHandler)
	}

	// Credential set CRUD routes (admin only)
	if cfg.CredentialsHandler != nil {
		registerCredentialRoutes(router, services, cfg.CredentialsHandler)
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
		secretsGroup.POST("/:id/reveal", cfg.SecretsHandler.RevealSecret)
		secretsGroup.GET("/:id/bindings", cfg.SecretsHandler.GetSecretBindings)

		workspaceGroup.PUT("/:id/bindings", cfg.SecretsHandler.SetBindings)
		workspaceGroup.GET("/:id/bindings", cfg.SecretsHandler.GetBindings)
		workspaceGroup.POST("/:id/reload-secrets", cfg.SecretsHandler.ReloadSecrets)
		workspaceGroup.PUT("/:id/env", cfg.SecretsHandler.SetWorkspaceEnv)
		workspaceGroup.GET("/:id/env", cfg.SecretsHandler.GetWorkspaceEnv)
		workspaceGroup.DELETE("/:id/env/:name", cfg.SecretsHandler.DeleteWorkspaceEnv)
		workspaceGroup.GET("/:id/models", cfg.SecretsHandler.ListModels)
		workspaceGroup.PUT("/:id/model", cfg.SecretsHandler.SetModel)
	}

	// Key rotation endpoint (Epic 10)
	if cfg.RotateKeyHandler != nil {
		accountGroup := router.Group("/api/v1/account")
		accountGroup.Use(services.GetAuth().AuthMiddleware())
		accountGroup.POST("/rotate-key", cfg.RotateKeyHandler.RotateKey)
		accountGroup.POST("/change-password", cfg.RotateKeyHandler.ChangePassword)
		router.POST("/api/v1/account/recover", cfg.RotateKeyHandler.RecoverAccount)
	}

	// Metrics endpoint.
	//
	// F1.1.3 (Epic 17): pre-fix /metrics was unauthenticated, leaking
	// internal counters (request rates per route, error rates, etc.)
	// to any pod that could route to the API service. We now require
	// `Authorization: Bearer <token>` if the env var
	// LLMSAFESPACE_METRICS_TOKEN is set. Operators who want
	// Prometheus to scrape unauthenticated should leave the env unset
	// (matching the pre-fix behavior with explicit opt-in).
	router.GET("/metrics", func(c *gin.Context) {
		token := os.Getenv("LLMSAFESPACE_METRICS_TOKEN")
		if token != "" && c.GetHeader("Authorization") != "Bearer "+token {
			c.Header("WWW-Authenticate", `Bearer realm="metrics"`)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		promhttp.Handler().ServeHTTP(c.Writer, c.Request)
	})

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

		// F1.1.1 (Epic 17): pre-fix the failure list contained the
		// raw `err.Error()` from the driver, which can include the
		// connection string, hostname, port, and sometimes the
		// password depending on the driver. We now log the detailed
		// error server-side and return only a generic component
		// status to the client.
		var failures []string

		db := services.GetDatabase()
		if db == nil {
			failures = append(failures, "database: not configured")
		} else if err := db.Ping(ctx); err != nil {
			logger.Warn("/readyz: database ping failed",
				"error", err.Error())
			failures = append(failures, "database: unreachable")
		}

		cache := services.GetCache()
		if cache == nil {
			failures = append(failures, "cache: not configured")
		} else if err := cache.Ping(ctx); err != nil {
			logger.Warn("/readyz: cache ping failed",
				"error", err.Error())
			failures = append(failures, "cache: unreachable")
		}

		if len(failures) > 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "unhealthy",
				"failures": failures,
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
func registerAuthRoutes(rg *gin.RouterGroup, services interfaces.Services, instanceSettings *settings.InstanceService, logger *apilogger.Logger) {
	authSvc := services.GetAuth()

	// Public: feature flag discovery
	rg.GET("/config", func(c *gin.Context) {
		regEnabled := true // default
		instanceName := "LLMSafeSpace"
		motd := ""
		if instanceSettings != nil {
			if v, err := instanceSettings.GetBool(c.Request.Context(), "auth.registrationEnabled"); err == nil {
				regEnabled = v
			}
			if v, err := instanceSettings.GetString(c.Request.Context(), "instance.name"); err == nil && v != "" {
				instanceName = v
			}
			if v, err := instanceSettings.GetString(c.Request.Context(), "instance.motd"); err == nil {
				motd = v
			}
		}
		c.JSON(http.StatusOK, types.AuthConfig{
			RegistrationEnabled: regEnabled,
			OIDCEnabled:         false,
			InstanceName:        instanceName,
			MOTD:                motd,
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

	// Public: logout
	//
	// G18 (Epic 17 Phase 4 RT-4.13): the JWT must be added to the
	// revocation cache so subsequent ValidateToken calls reject it.
	// Pre-fix this handler only cleared the cookie, leaving the token
	// replayable by anyone who captured it (including via Authorization
	// header re-supply).
	//
	// Token sources, in priority order:
	//   1. Authorization: Bearer <jwt> header
	//   2. lsp_session cookie
	//
	// Filtering: API keys (lsp_ prefix) are NOT revoked here. Their
	// lifecycle is /api-keys/:id DELETE; calling RevokeToken on them
	// would return a JWT-parse error which we'd then have to ignore.
	// The router uses the literal "lsp_" prefix to match the chart's
	// default Auth.APIKeyPrefix; operators who change the prefix get
	// best-effort revoke-and-log on API keys, which is harmless.
	//
	// Failure semantics: RevokeToken errors do NOT propagate. Logout
	// must always succeed from the user's perspective; the cookie is
	// cleared and 204 returned regardless. Any revocation failure is
	// logged at Warn for observability.
	rg.POST("/logout", func(c *gin.Context) {
		token := utilities.ExtractToken(c, utilities.TokenExtractorConfig{
			HeaderName: "Authorization",
			TokenType:  "Bearer",
			CookieName: "lsp_session",
		})
		if token != "" && !utilities.IsAPIKey(token, "lsp_") {
			if err := authSvc.RevokeToken(token); err != nil {
				logger.Warn("auth.logout: RevokeToken failed (proceeding with cookie clear)",
					"error", err.Error())
			}
		}
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
//
// proxyHandler may be nil; it is only used to trigger the optional
// session-parent backfill on the /sessions endpoint and is otherwise unused.
func registerWorkspaceRoutes(rg *gin.RouterGroup, services interfaces.Services, proxyHandler *handlers.ProxyHandler, cfg RouterConfig) {
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

		// G32 (Epic 17): per-user workspace quota. When the env var
		// LLMSAFESPACE_MAX_WORKSPACES_PER_USER is set to a positive
		// integer, count the user's existing non-deleted workspaces
		// and reject CreateWorkspace if at or above the limit.
		// Default unset = unbounded (single-tenant deployments).
		if maxWS := os.Getenv("LLMSAFESPACE_MAX_WORKSPACES_PER_USER"); maxWS != "" {
			if cap, parseErr := strconv.Atoi(maxWS); parseErr == nil && cap > 0 {
				_, page, err := services.GetDatabase().ListWorkspaces(c.Request.Context(), userID, 1, 0)
				if err == nil && page != nil && page.Total >= cap {
					c.JSON(http.StatusTooManyRequests, gin.H{
						"error": "workspace quota exceeded",
						"limit": cap,
					})
					return
				}
			}
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

	// Epic 21 Change A — declarative recovery from Failed (and force-restart
	// from Active). Bumps spec.restartGeneration; controller observes and
	// transitions back through Pending. Idempotent at the spec layer.
	rg.POST("/:id/restart", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if err := wsSvc.RestartWorkspace(c.Request.Context(), userID, c.Param("id")); err != nil {
			respondWithError(c, err)
			return
		}
		c.Status(http.StatusAccepted)
	})

	// Epic 27a: explicit agent reload (disposes opencode without pod restart).
	if cfg.AgentReloadHandler != nil {
		rg.POST("/:id/agent/reload", cfg.AgentReloadHandler.Reload)
	}

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

	rg.POST("/:id/activate", func(c *gin.Context) {
		userID := authSvc.GetUserID(c)
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
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
		workspaceID := c.Param("id")
		sessions, err := wsSvc.ListWorkspaceSessions(c.Request.Context(), userID, workspaceID)
		if err != nil {
			respondWithError(c, err)
			return
		}
		// Trigger a one-shot async backfill of parent_session_id from the
		// authoritative opencode /session list. No-op when the workspace
		// has already been backfilled this process lifetime, so the steady-
		// state cost is a single map lookup per request. Useful for
		// sessions that pre-date the parent_session_id migration.
		// Skipped when proxyHandler is nil (router built without proxy).
		if proxyHandler != nil {
			proxyHandler.BackfillSessionParents(workspaceID)
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
	rg.GET("/:id/session-events", proxyHandler.StreamEvents)

	// Question/Permission input request routes (Epic 16)
	rg.GET("/:id/question", proxyHandler.ListQuestions)
	rg.POST("/:id/question/:requestID/reply", proxyHandler.QuestionReply)
	rg.POST("/:id/question/:requestID/reject", proxyHandler.QuestionReject)
	rg.GET("/:id/permission", proxyHandler.ListPermissions)
	rg.POST("/:id/permission/:requestID/reply", proxyHandler.PermissionReply)
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

// registerSettingsRoutes adds admin and user settings routes.
func registerSettingsRoutes(router *gin.Engine, services interfaces.Services, h *handlers.SettingsHandler) {
	authMW := services.GetAuth().AuthMiddleware()

	// Admin settings (authenticated + admin guard)
	admin := router.Group("/api/v1/admin/settings")
	admin.Use(authMW)
	admin.Use(middleware.AdminGuard())
	admin.GET("", h.GetAdminSettings)
	admin.GET("/schema", h.GetAdminSettingsSchema)
	admin.PUT("/:key", h.SetAdminSetting)

	// User settings (authenticated)
	user := router.Group("/api/v1/users/me/settings")
	user.Use(authMW)
	user.GET("", h.GetUserSettings)
	user.GET("/schema", h.GetUserSettingsSchema)
	user.PUT("/:key", h.SetUserSetting)
}

// registerCredentialRoutes adds admin credential set CRUD routes.
func registerCredentialRoutes(router *gin.Engine, services interfaces.Services, h *handlers.CredentialsHandler) {
	authMW := services.GetAuth().AuthMiddleware()

	creds := router.Group("/api/v1/admin/credentials")
	creds.Use(authMW)
	creds.Use(middleware.AdminGuard())
	creds.POST("", h.CreateCredentialSet)
	creds.GET("", h.ListCredentialSets)
	creds.GET("/:id", h.GetCredentialSet)
	creds.PUT("/:id", h.UpdateCredentialSet)
	creds.DELETE("/:id", h.DeleteCredentialSet)
	creds.PUT("/:id/default", h.SetDefaultCredentialSet)
	creds.POST("/rotate-key", h.RotateCredentialKey)
}

// getMaxActiveSessions reads the max active sessions setting, falling back to 5.
func getMaxActiveSessions(ctx context.Context, instanceSettings *settings.InstanceService) int {
	if instanceSettings != nil {
		if v, err := instanceSettings.GetInt(ctx, "workspace.defaultMaxActiveSessions"); err == nil && v > 0 {
			return v
		}
	}
	return 5
}
