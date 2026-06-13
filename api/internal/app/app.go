// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/server"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sessionindex"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	agentoc "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/settings"
)

type App struct {
	config             *config.Config
	logger             *logger.Logger
	router             *gin.Engine
	server             *http.Server
	k8sClient          *kubernetes.Client
	services           *services.Services
	proxyHandler       *handlers.ProxyHandler
	agentReloadHandler *handlers.AgentReloadHandler
	bulkReloadHandler  *handlers.BulkReloadHandler
	sessionIndexSvc    *sessionindex.Service
	instanceSettings   *settings.InstanceService
	userSettings       *settings.UserService
	asyncAudit         *secrets.AsyncAuditLogger // nil if pgxpool path not used
	secretsPool        *pgxpool.Pool             // pgx pool for secrets store; closed on shutdown
	dekCacheClient     *redis.Client             // redis client for DEK cache; closed on shutdown
	shutdownCh         chan struct{}
	ctx                context.Context
	cancel             context.CancelFunc
}

func New(cfg *config.Config, log *logger.Logger) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// validateMasterSecret is the very first check — before any infrastructure
	// is constructed. This ensures startup fails fast with a clear error rather
	// than a misleading K8s/DB error, and makes the enforcement unit-testable
	// without a live cluster (see TestApp_New_FailsWithoutMasterSecret).
	if err := validateMasterSecret(log); err != nil {
		cancel()
		return nil, err
	}

	k8sClient, err := kubernetes.New(&cfg.Kubernetes, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	svc, err := services.New(cfg, log, k8sClient)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	proxyHandler, err := handlers.NewProxyHandler(k8sClient, log, cfg.Kubernetes.Namespace, nil, &agentoc.Dialect{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create proxy handler: %w", err)
	}

	// Resolve subagent (subtask) sessions back to their root user-visible
	// session, so permission/question events from child sessions bubble up
	// to the chat view of the active parent session.
	proxyHandler.EnableSessionParentResolution()

	// Wire session index so sessions are tracked and listable.
	sessionIndexSvc := sessionindex.New(svc.Database, log)
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		wsSvc.SetSessionIndex(sessionIndexSvc)
	}
	proxyHandler.SetSessionIndex(sessionIndexSvc)

	// Initialize settings services (backed by the same DB service).
	dbSvc := svc.Database.(*database.Service)
	instanceSettings := settings.NewInstanceService(dbSvc, log)
	userSettings := settings.NewUserService(dbSvc, log)

	// Inject instance settings into workspace service for enforcement.
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		wsSvc.SetInstanceSettings(instanceSettings)
	}

	// Create settings handler for API routes.
	settingsHandler := handlers.NewSettingsHandler(instanceSettings, userSettings)

	// Wire secret management (Epic 10).
	var secretsHandler *handlers.SecretsHandler
	var rotateKeyHandler *handlers.RotateKeyHandler
	var adminProvCredHandler *handlers.AdminProviderCredentialsHandler
	var userProvCredHandler *handlers.UserProviderCredentialsHandler
	var orgsHandler *handlers.OrgsHandler
	var asyncAudit *secrets.AsyncAuditLogger // populated when secrets are enabled; drained on Shutdown
	var secretsPool *pgxpool.Pool            // closed on Shutdown
	var dekCacheClient *redis.Client         // closed on Shutdown
	{
		mk := dekMasterKey()
		if mk == nil {
			// Unreachable after validateMasterSecret passed — env var is
			// immutable for the process lifetime. Guards against future
			// refactors that move validateMasterSecret.
			cancel()
			return nil, errors.New("internal: dekMasterKey returned nil after validateMasterSecret passed")
		}
		dekCacheClient = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		dekCache := secrets.NewRedisDEKCache(dekCacheClient, mk)

		// Create pgxpool for secret stores (same DB, separate pool for pgx native queries).
		pgxDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.User,
			cfg.Database.Password, cfg.Database.Database, cfg.Database.SSLMode)
		var pgxErr error
		secretsPool, pgxErr = pgxpool.New(context.Background(), pgxDSN)

		var keyService *secrets.KeyService
		var secretService *secrets.SecretService
		var auditStore secrets.SecretStore
		if pgxErr != nil {
			// Refusing to start is the only correct response: the
			// in-memory adapter fallback (dbSecretStoreAdapter,
			// dbKeyStoreAdapter) is racy, unbounded in audit log
			// growth, and loses every secret + key on restart. It
			// existed for dev-environment convenience but in any
			// shape resembling production it is silent data loss
			// disguised as graceful degradation. Tests use the
			// in-memory adapters directly via NewSecretService;
			// production must always have pgxpool.
			cancel()
			return nil, fmt.Errorf("create pgxpool for secrets store: %w (refusing to fall back to in-memory; the in-memory secret/key adapters lose data on restart and are not safe for any environment that handles real user secrets)", pgxErr)
		}
		pgStore := secrets.NewPgSecretStore(secretsPool)
		// Wrap the secret store in an async audit logger so audit
		// writes do not block the request goroutine. The wrapper is
		// itself a SecretStore (CRUD methods delegate; LogAudit goes
		// through a 4096-entry buffered channel). Operators see drop
		// counts via Stats() and Warn-level logs.
		asyncAudit = secrets.NewAsyncAuditLogger(pgStore, 4096, log)
		keyService = secrets.NewKeyService(secrets.NewPgKeyStore(secretsPool), dekCache)
		keyService.SetLogger(log)
		secretService = secrets.NewSecretService(keyService, asyncAudit)
		auditStore = asyncAudit

		secretsHandler = handlers.NewSecretsHandler(secretService)
		// Wire billing/metering metrics recorder.
		if metricsSvc, ok := svc.GetMetrics().(*metrics.Service); ok {
			secretsHandler.SetMetricsRecorder(metricsSvc)
		}
		// Epic 26: mark relay active when LLMSAFESPACE_INFERENCE_RELAY_URL is
		// configured. This causes ListModels to remap free-tier opencode model
		// providerIDs to "opencode-relay" so clients route inference through
		// the CF Worker (which the phase-2 relay injector configures in agentd).
		if inferenceRelayURL := cfg.Server.InferenceRelayURL; inferenceRelayURL != "" {
			secretsHandler.SetRelayActive(true)
		}
		adminProvCredHandler = handlers.NewAdminProviderCredentialsHandler(pgStore, deriveServerKey)
		adminProvCredHandler.SetAutoApplyStore(pgStore)
		userProvCredHandler = handlers.NewUserProviderCredentialsHandler(pgStore, keyService, secrets.NewPgKeyStore(secretsPool))
		userProvCredHandler.SetWorkspaceOwnerChecker(func(ctx context.Context, userID, wsID string) error {
			return (&workspaceOwnerVerifierAdapter{db: dbSvc, logger: log}).VerifyWorkspaceOwner(ctx, userID, wsID)
		})
		userProvCredHandler.SetCredentialStateWriter(dbSvc)

		// Seed the free-tier opencode credential (Epic 30 US-30.4).
		if err := ensureFreeTierCredential(context.Background(), pgStore, log); err != nil {
			log.Warn("free-tier credential seeding skipped", "error", err.Error())
		}
		// Wire pod-IP resolver so reload-secrets can reach in-pod agentd.
		// Without this the SecretsHandler returns 503 for every reload
		// request and the SetBindings auto-push silently no-ops; see
		// Bug 1 + Bug 2 in worklog 0085.
		secretsHandler.SetPodIPResolver(newSecretsPodIPResolver(
			&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
			dbSvc,
			log,
		))
		secretsHandler.SetLogger(log)
		secretsHandler.SetCredentialStateWriter(dbSvc)
		secretsHandler.SetWorkspaceMetadataUpdater(dbSvc)
		// Wire password getter so ListModels/SetModel can authenticate
		// to opencode. Uses the same K8s-secret-backed getter as ProxyHandler.
		// Wired after proxyHandler construction (see below).
		// Wire the manifest writer so SetBindings persists a K8s Secret
		// (`workspace-secrets-<id>`) read by the pod init container on
		// every start. The live HTTP push alone is not durable; see
		// Bug 3 in worklog 0085.
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			secretsHandler.SetSecretsManifestWriter(wsSvc)
		}
		// Wire the password verifier so RevealSecret enforces a real
		// re-authentication gate. Without this the field is theater
		// (validator finding on RevealSecret in worklog 0094 audit).
		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			secretsHandler.SetPasswordVerifier(authSvc)
		}
		// Wire workspace-ownership verification into the secret
		// service so SetBindings/AddBindings/GetBindings/
		// PrepareSecretsForInjection refuse to operate on another
		// user's workspace (validator pass-3+4 findings SO-1 and
		// PARTIAL-1). RequireOwnerVerification flips the service
		// into fail-closed mode so a future wiring regression
		// produces a uniform 404 rather than silently re-enabling
		// cross-tenant pollution (NEW-1).
		secretService.SetWorkspaceOwnerVerifier(&workspaceOwnerVerifierAdapter{db: dbSvc, logger: log})
		secretService.RequireOwnerVerification()
		secretService.SetAdminKeyDeriver(deriveServerKey)
		rotateKeyHandler = handlers.NewRotateKeyHandler(keyService)
		rotateKeyHandler.SetPasswordUpdater(&bcryptPasswordUpdater{db: svc.Database})
		rotateKeyHandler.SetAuditFunc(func(userID, action string) {
			entry := &secrets.AuditEntry{
				UserID:    userID,
				Action:    action,
				Metadata:  []byte(`{}`),
				Timestamp: time.Now(),
			}
			_ = auditStore.LogAudit(context.Background(), entry)
		})

		rkp := newRootKeyProvider(cfg, log)

		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			authSvc.SetKeyService(keyService)
			authSvc.SetInstanceSettings(instanceSettings)

			if rkp != nil {
				authSvc.SetRootKeyProvider(rkp)
			} else {
				authSvc.SetMasterKey(dekMasterKey())
			}
		}

		pgOrgKeyStore := secrets.NewPgOrgKeyStore(secretsPool)
		orgKeyService := secrets.NewOrgKeyService(pgOrgKeyStore, dekCache)
		orgKeyService.SetLogger(log)
		orgAwareKS := secrets.NewOrgAwareKeyService(keyService, orgKeyService)
		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			authSvc.SetKeyService(orgAwareKS)
		}

		pgOrgStore := database.NewPgOrgStore(dbSvc.DB)
		orgsHandler = handlers.NewOrgsHandler(pgOrgStore, orgKeyService, dekCache, svc.GetAuth())

		if rkp != nil {
			keyService.SetAPIKeyStore(&apiKeyStoreAdapter{db: dbSvc}, rkp)
		} else {
			mk := dekMasterKey()
			if mk != nil {
				sp, _ := secrets.NewStaticKeyProvider(mk)
				keyService.SetAPIKeyStore(&apiKeyStoreAdapter{db: dbSvc}, sp)
			}
		}
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			wsSvc.SetSecretInjector(secretService)
			wsSvc.SetCredentialProvisioner(pgStore)
		}
	}

	// In development mode, disable RequireHTTPS so the API works over plain
	// HTTP via port-forward / local tooling. In production, set
	// logging.development=false and front the API with an Ingress that
	// terminates TLS and sets X-Forwarded-Proto=https.
	securityCfg := server.DefaultRouterConfig().SecurityConfig
	if cfg.Logging.Development {
		securityCfg.Development = true
		securityCfg.RequireHTTPS = false
		securityCfg.AllowHTTPSDowngrade = true
	}
	if len(cfg.Security.AllowedOrigins) > 0 {
		securityCfg.AllowedOrigins = cfg.Security.AllowedOrigins
	}
	securityCfg.AllowCredentials = cfg.Security.AllowCredentials

	rateLimitCfg := server.DefaultRouterConfig().RateLimitConfig
	rateLimitCfg.Enabled = cfg.RateLimiting.Enabled
	if cfg.RateLimiting.DefaultLimit > 0 {
		rateLimitCfg.DefaultLimit = cfg.RateLimiting.DefaultLimit
	}
	if cfg.RateLimiting.DefaultWindow > 0 {
		rateLimitCfg.DefaultWindow = cfg.RateLimiting.DefaultWindow
	}
	if cfg.RateLimiting.BurstSize > 0 {
		rateLimitCfg.BurstSize = cfg.RateLimiting.BurstSize
	}
	if cfg.RateLimiting.Strategy != "" {
		rateLimitCfg.Strategy = cfg.RateLimiting.Strategy
	}

	wsOrigins := server.DefaultRouterConfig().AllowedWebSocketOrigins
	if len(cfg.Security.AllowedOrigins) > 0 && cfg.Security.AllowedOrigins[0] != "*" {
		wsOrigins = cfg.Security.AllowedOrigins
	}

	// Create terminal handler (Epic 14 — WebSocket terminal proxy).
	terminalHandler := handlers.NewTerminalHandler(svc.Cache, &k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace}, cfg.Kubernetes.Namespace, log)

	// Epic 27a: Agent reload handler.
	var agentReloadHandler *handlers.AgentReloadHandler
	var bulkReloadHandler *handlers.BulkReloadHandler
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		agentReloadHandler = handlers.NewAgentReloadHandler(
			wsSvc,
			dbSvc,
			newSecretsPodIPResolver(
				&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
				dbSvc,
				log,
			),
			&http.Client{Timeout: 15 * time.Second},
			log,
		)
		bulkReloadHandler = handlers.NewBulkReloadHandler(
			dbSvc,
			wsSvc,
			dbSvc,
			newSecretsPodIPResolver(
				&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
				dbSvc,
				log,
			),
			&http.Client{Timeout: 15 * time.Second},
			log,
		)
	}

	// Epic 27b: Drain mode SSETracker wiring is deferred to Run() — the tracker
	// is nil until proxyHandler.Start() runs. Wire password getter + metrics here
	// (these are available at construction time).
	if agentReloadHandler != nil {
		if pwGetter := proxyHandler.GetPasswordGetter(); pwGetter != nil {
			agentReloadHandler.SetPasswordGetter(pwGetter)
			bulkReloadHandler.SetPasswordGetter(pwGetter)
			if secretsHandler != nil {
				secretsHandler.SetPasswordGetter(pwGetter)
			}
		}
	}
	// Wire metrics into reload handlers (guarded: handlers are nil when workspace
	// service type assertion fails, e.g. in tests or future refactors).
	if agentReloadHandler != nil {
		if metricsSvc, ok := svc.Metrics.(*metrics.Service); ok {
			agentReloadHandler.SetMetrics(metricsSvc)
			bulkReloadHandler.SetMetrics(metricsSvc)
		}
	}

	router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
		Debug:                           cfg.Logging.Development,
		LoggingConfig:                   server.DefaultRouterConfig().LoggingConfig,
		RateLimitConfig:                 rateLimitCfg,
		SecurityConfig:                  securityCfg,
		TracingConfig:                   server.DefaultRouterConfig().TracingConfig,
		AllowedWebSocketOrigins:         wsOrigins,
		SettingsHandler:                 settingsHandler,
		InstanceSettings:                instanceSettings,
		AdminProviderCredentialsHandler: adminProvCredHandler,
		UserProviderCredentialsHandler:  userProvCredHandler,
		SecretsHandler:                  secretsHandler,
		RotateKeyHandler:                rotateKeyHandler,
		OrgsHandler:                     orgsHandler,
		TerminalHandler:                 terminalHandler,
		AgentReloadHandler:              agentReloadHandler,
		BulkReloadHandler:               bulkReloadHandler,
		CookieName:                      cfg.Auth.CookieName,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
		// Slowloris hardening: cap header read time. Body read +
		// response write are bounded by per-handler logic; the API has
		// long-lived SSE endpoints so we deliberately do NOT set
		// ReadTimeout/WriteTimeout at the server level.
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &App{
		config:             cfg,
		logger:             log,
		router:             router,
		server:             httpServer,
		k8sClient:          k8sClient,
		services:           svc,
		proxyHandler:       proxyHandler,
		agentReloadHandler: agentReloadHandler,
		bulkReloadHandler:  bulkReloadHandler,
		sessionIndexSvc:    sessionIndexSvc,
		instanceSettings:   instanceSettings,
		userSettings:       userSettings,
		asyncAudit:         asyncAudit,
		secretsPool:        secretsPool,
		dekCacheClient:     dekCacheClient,
		shutdownCh:         make(chan struct{}),
		ctx:                ctx,
		cancel:             cancel,
	}, nil
}

func (a *App) Run() error {
	if err := a.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Start instance settings (loads cache from DB).
	if err := a.instanceSettings.Start(); err != nil {
		a.logger.Warn("Instance settings failed to start (will use defaults)", "error", err.Error())
		// Non-fatal: settings will fall back to schema defaults.
	}

	// Seed instance settings defaults (idempotent).
	if result, err := settings.Seed(a.ctx, a.services.Database.(*database.Service), a.logger); err != nil {
		a.logger.Warn("Settings seed failed", "error", err.Error())
	} else {
		a.logger.Info("Settings seed complete", "inserted", result.Inserted, "skipped", result.Skipped, "orphaned", len(result.Orphaned))
	}

	if err := a.k8sClient.Start(); err != nil {
		_ = a.services.Stop()
		return fmt.Errorf("failed to start Kubernetes client: %w", err)
	}

	if err := a.proxyHandler.Start(); err != nil {
		a.k8sClient.Stop()
		_ = a.services.Stop()
		return fmt.Errorf("failed to start proxy handler: %w", err)
	}

	// Epic 27a/27b: Wire drain mode dependencies now that proxyHandler.Start()
	// has initialized the SSETracker.
	if a.agentReloadHandler != nil {
		if tracker := a.proxyHandler.GetSSETracker(); tracker != nil {
			a.agentReloadHandler.SetSSETracker(tracker)
			if a.bulkReloadHandler != nil {
				a.bulkReloadHandler.SetSSETracker(tracker)
			}
		}
	}

	// Epic 26 / billing: wire inference callback and session metrics unconditionally.
	// Previously nested inside the agentReloadHandler guard, which meant if the
	// workspace service type assertion failed (or the handler wasn't created),
	// SetOnInference was never called and inference metrics remained permanently zero.
	if tracker := a.proxyHandler.GetSSETracker(); tracker != nil {
		if metricsSvc, ok := a.services.Metrics.(*metrics.Service); ok {
			tracker.SetOnInference(func(workspaceID, modelID, providerID string, inputTokens, outputTokens int64, costDollars float64) {
				metricsSvc.RecordInference(modelID, providerID, inputTokens, outputTokens, costDollars)
			})
			tracker.SetSessionMetrics(metricsSvc)
		}
	}
	// Epic 27b US-27b.5: Wire agent state checker into proxy for chat error enrichment.
	// dbSvc is referenced via services; use a type assertion to get the concrete type
	// which implements AgentStateChecker (GetLastCredentialChangedAt).
	if dbSvc, ok := a.services.Database.(*database.Service); ok {
		a.proxyHandler.SetAgentStateChecker(dbSvc)
	}

	if err := a.sessionIndexSvc.Start(); err != nil {
		_ = a.proxyHandler.Stop()
		a.k8sClient.Stop()
		_ = a.services.Stop()
		return fmt.Errorf("failed to start session index: %w", err)
	}

	a.logger.Info("Starting HTTP server", "address", a.server.Addr)

	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func (a *App) Shutdown() error {
	a.logger.Info("Shutting down application")

	a.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), a.config.Server.ShutdownTimeout)
	defer cancel()

	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("HTTP server shutdown error", err)
	}

	if err := a.proxyHandler.Stop(); err != nil {
		a.logger.Error("Proxy handler shutdown error", err)
	}

	if err := a.sessionIndexSvc.Stop(); err != nil {
		a.logger.Error("Session index shutdown error", err)
	}

	// Drain pending audit entries before tearing down the DB pool so
	// pending writes get a fair chance to land.
	if a.asyncAudit != nil {
		a.asyncAudit.Stop()
		stats := a.asyncAudit.Stats()
		a.logger.Info("Async audit logger drained",
			"written", stats.Written, "dropped", stats.Dropped, "failed", stats.Failed)
	}

	// Close the secrets pgxpool and Redis DEK cache last so any
	// last-millisecond audit write through asyncAudit.run() above
	// could complete. Both are nil-safe; we still nil-check for
	// belt-and-braces against future "secrets disabled" config paths.
	if a.secretsPool != nil {
		a.secretsPool.Close()
	}
	if a.dekCacheClient != nil {
		if err := a.dekCacheClient.Close(); err != nil {
			a.logger.Error("Redis DEK cache close error", err)
		}
	}

	a.k8sClient.Stop()

	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}

// validateMasterSecret verifies LLMSAFESPACE_MASTER_SECRET is present and
// decodes to at least 32 bytes (the AES-256-GCM key size minimum).
//
// Returns nil on success. Logs a structured Warn when the secret is present
// but too short so operators can distinguish "forgot to set it" from "set it
// to the wrong value." The secret value itself is never logged.
//
// This function reads the env var independently of deriveServerKey to keep
// deriveServerKey a pure, side-effect-free function compatible with the
// secrets.AdminKeyDeriver type (func(string) []byte).
func validateMasterSecret(log *logger.Logger) error {
	masterRaw := os.Getenv("LLMSAFESPACE_MASTER_SECRET")
	if masterRaw == "" {
		masterRaw = os.Getenv("LLMSAFESPACE_DEK_MASTER_KEY")
	}
	if masterRaw == "" {
		return errors.New(
			"LLMSAFESPACE_MASTER_SECRET is required but not set; " +
				"refusing to start without DEK encryption at rest in Redis. " +
				"Generate one with: openssl rand -hex 32")
	}

	var master []byte
	if decoded, err := hex.DecodeString(masterRaw); err == nil {
		master = decoded
	} else {
		master = []byte(masterRaw)
	}

	if len(master) < 32 {
		log.Warn("LLMSAFESPACE_MASTER_SECRET is set but too short for AES-256-GCM",
			"decoded_bytes", len(master), "required_bytes", 32)
		// masterRaw is intentionally NOT included in the error message or log.
		return fmt.Errorf(
			"LLMSAFESPACE_MASTER_SECRET decodes to %d bytes; minimum is 32 (AES-256-GCM key size). "+
				"Use at least 32 bytes (e.g. 64 hex chars, or 32+ alphanumeric chars)",
			len(master))
	}
	return nil
}
