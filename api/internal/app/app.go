package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/server"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/sessionindex"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

type App struct {
	config          *config.Config
	logger          *logger.Logger
	router          *gin.Engine
	server          *http.Server
	k8sClient       *kubernetes.Client
	services        *services.Services
	proxyHandler    *handlers.ProxyHandler
	sessionIndexSvc *sessionindex.Service
	shutdownCh      chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
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

	proxyHandler, err := handlers.NewProxyHandler(k8sClient, log, cfg.Kubernetes.Namespace, nil)
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

	// Wire secret management (Epic 10).
	var secretsHandler *handlers.SecretsHandler
	dekCacheClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	dekCache := secrets.NewRedisDEKCache(dekCacheClient)
	// PgKeyStore and PgSecretStore require a pgxpool — for now use a nil-safe
	// approach: if database is available, wire secrets; otherwise skip.
	// The database service manages its own pool, so we create lightweight
	// in-memory key/secret stores that delegate to the DB service interface.
	// For the initial wiring, we use the KeyService with the DEK cache and
	// a placeholder key store that will be replaced when pgxpool is exposed.
	keyService := secrets.NewKeyService(&dbKeyStoreAdapter{db: svc.Database}, dekCache)
	secretStore := &dbSecretStoreAdapter{db: svc.Database}
	secretService := secrets.NewSecretService(keyService, secretStore)
	secretsHandler = handlers.NewSecretsHandler(secretService)

	// Connect key service to auth for DEK unlock on login
	if authSvc, ok := svc.Auth.(*auth.Service); ok {
		authSvc.SetKeyService(keyService)
	}

	rotateKeyHandler := handlers.NewRotateKeyHandler(keyService)

	// Wire secret injector into workspace service for pod activation
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		wsSvc.SetSecretInjector(secretService)
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

	router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
		Debug:                   cfg.Logging.Development,
		LoggingConfig:           server.DefaultRouterConfig().LoggingConfig,
		RateLimitConfig:         rateLimitCfg,
		SecurityConfig:          securityCfg,
		TracingConfig:           server.DefaultRouterConfig().TracingConfig,
		AllowedWebSocketOrigins: wsOrigins,
		SecretsHandler:          secretsHandler,
		RotateKeyHandler:        rotateKeyHandler,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
	}

	return &App{
		config:          cfg,
		logger:          log,
		router:          router,
		server:          httpServer,
		k8sClient:       k8sClient,
		services:        svc,
		proxyHandler:    proxyHandler,
		sessionIndexSvc: sessionIndexSvc,
		shutdownCh:      make(chan struct{}),
		ctx:             ctx,
		cancel:          cancel,
	}, nil
}

func (a *App) Run() error {
	if err := a.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
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
