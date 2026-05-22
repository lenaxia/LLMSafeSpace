package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/server"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
)

type App struct {
	config       *config.Config
	logger       *logger.Logger
	router       *gin.Engine
	server       *http.Server
	k8sClient    *kubernetes.Client
	services     *services.Services
	proxyHandler *handlers.ProxyHandler
	shutdownCh   chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
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

	router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
		Debug:                   cfg.Logging.Development,
		LoggingConfig:           server.DefaultRouterConfig().LoggingConfig,
		RateLimitConfig:         server.DefaultRouterConfig().RateLimitConfig,
		SecurityConfig:          server.DefaultRouterConfig().SecurityConfig,
		TracingConfig:           server.DefaultRouterConfig().TracingConfig,
		AllowedWebSocketOrigins: server.DefaultRouterConfig().AllowedWebSocketOrigins,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
	}

	return &App{
		config:       cfg,
		logger:       log,
		router:       router,
		server:       httpServer,
		k8sClient:    k8sClient,
		services:     svc,
		proxyHandler: proxyHandler,
		shutdownCh:   make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
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

	a.k8sClient.Stop()

	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}
