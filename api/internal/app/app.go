package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/lenaxia/llmsafespace/api/internal/services/sessionindex"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	agentoc "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/credentials"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/settings"
)

type App struct {
	config           *config.Config
	logger           *logger.Logger
	router           *gin.Engine
	server           *http.Server
	k8sClient        *kubernetes.Client
	services         *services.Services
	proxyHandler     *handlers.ProxyHandler
	sessionIndexSvc  *sessionindex.Service
	instanceSettings *settings.InstanceService
	userSettings     *settings.UserService
	shutdownCh       chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
}

func New(cfg *config.Config, log *logger.Logger) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

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

	// Create credential sets handler (Epic 9 Phase C).
	credKeySet := loadCredentialKeySet(cfg)
	credSvc := credentials.NewService(dbSvc, credKeySet, log)
	credentialsHandler := handlers.NewCredentialsHandler(credSvc)

	// Wire secret management (Epic 10).
	var secretsHandler *handlers.SecretsHandler
	var rotateKeyHandler *handlers.RotateKeyHandler
	{
		dekCacheClient := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		dekCache := secrets.NewRedisDEKCache(dekCacheClient, dekMasterKey())

		// Create pgxpool for secret stores (same DB, separate pool for pgx native queries).
		pgxDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.User,
			cfg.Database.Password, cfg.Database.Database, cfg.Database.SSLMode)
		secretsPool, pgxErr := pgxpool.New(context.Background(), pgxDSN)

		var keyService *secrets.KeyService
		var secretService *secrets.SecretService
		var auditStore secrets.SecretStore
		if pgxErr != nil {
			log.Warn("Failed to create pgxpool for secrets; using in-memory adapters", "error", pgxErr.Error())
			memStore := &dbSecretStoreAdapter{}
			keyService = secrets.NewKeyService(&dbKeyStoreAdapter{}, dekCache)
			secretService = secrets.NewSecretService(keyService, memStore)
			auditStore = memStore
		} else {
			pgStore := secrets.NewPgSecretStore(secretsPool)
			keyService = secrets.NewKeyService(secrets.NewPgKeyStore(secretsPool), dekCache)
			secretService = secrets.NewSecretService(keyService, pgStore)
			auditStore = pgStore
		}

		secretsHandler = handlers.NewSecretsHandler(secretService)
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
		// Wire the manifest writer so SetBindings persists a K8s Secret
		// (`workspace-secrets-<id>`) read by the pod init container on
		// every start. The live HTTP push alone is not durable; see
		// Bug 3 in worklog 0085.
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			secretsHandler.SetSecretsManifestWriter(wsSvc)
		}
		rotateKeyHandler = handlers.NewRotateKeyHandler(keyService)
		rotateKeyHandler.SetPasswordUpdater(&bcryptPasswordUpdater{db: svc.Database})
		rotateKeyHandler.SetAuditFunc(func(userID, action string) {
			entry := &secrets.AuditEntry{
				UserID:    userID,
				Action:    action,
				Metadata:  []byte(`{}`),
				Timestamp: time.Now(),
			}
			auditStore.LogAudit(context.Background(), entry)
		})

		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			authSvc.SetKeyService(keyService)
			authSvc.SetInstanceSettings(instanceSettings)
		}
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			wsSvc.SetSecretInjector(secretService)
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

	router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
		Debug:                   cfg.Logging.Development,
		LoggingConfig:           server.DefaultRouterConfig().LoggingConfig,
		RateLimitConfig:         rateLimitCfg,
		SecurityConfig:          securityCfg,
		TracingConfig:           server.DefaultRouterConfig().TracingConfig,
		AllowedWebSocketOrigins: wsOrigins,
		SettingsHandler:         settingsHandler,
		InstanceSettings:        instanceSettings,
		CredentialsHandler:      credentialsHandler,
		SecretsHandler:          secretsHandler,
		RotateKeyHandler:        rotateKeyHandler,
		TerminalHandler:         terminalHandler,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
	}

	return &App{
		config:           cfg,
		logger:           log,
		router:           router,
		server:           httpServer,
		k8sClient:        k8sClient,
		services:         svc,
		proxyHandler:     proxyHandler,
		sessionIndexSvc:  sessionIndexSvc,
		instanceSettings: instanceSettings,
		userSettings:     userSettings,
		shutdownCh:       make(chan struct{}),
		ctx:              ctx,
		cancel:           cancel,
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
		a.services.Stop()
		return fmt.Errorf("failed to start Kubernetes client: %w", err)
	}

	if err := a.proxyHandler.Start(); err != nil {
		a.k8sClient.Stop()
		a.services.Stop()
		return fmt.Errorf("failed to start proxy handler: %w", err)
	}

	if err := a.sessionIndexSvc.Start(); err != nil {
		a.proxyHandler.Stop()
		a.k8sClient.Stop()
		a.services.Stop()
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

	a.k8sClient.Stop()

	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}

// loadCredentialKeySet loads the credential encryption key set from the
// LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY environment variable (hex-encoded 32 bytes).
// If not set, generates a random key (suitable for development only).
func loadCredentialKeySet(cfg *config.Config) *credentials.EncryptionKeySet {
	keyHex := os.Getenv("LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY")
	if keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err == nil && len(key) == 32 {
			return &credentials.EncryptionKeySet{
				Keys: []credentials.EncryptionKey{{Version: 1, Key: key}},
			}
		}
	}
	// Fallback: generate a random key (development only — not persisted across restarts).
	key := make([]byte, 32)
	rand.Read(key)
	return &credentials.EncryptionKeySet{
		Keys: []credentials.EncryptionKey{{Version: 1, Key: key}},
	}
}
