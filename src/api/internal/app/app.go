package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// App represents the main application
type App struct {
	config     *config.Config
	logger     *logger.Logger
	router     *gin.Engine
	server     *http.Server
	k8sClient  *kubernetes.Client
	services   *services.Services
	handlers   *handlers.Handlers
	shutdownCh chan struct{}
}

// New creates a new application instance
func New(cfg *config.Config, log *logger.Logger) (*App, error) {
	// Set Gin mode
	if cfg.Logging.Development {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize Kubernetes client
	k8sClient, err := kubernetes.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Initialize services
	svc, err := services.New(cfg, log, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	// Create router
	router := gin.New()
	router.Use(gin.Recovery())
	
	// Add middleware
	router.Use(handlers.LoggerMiddleware(log))
	router.Use(handlers.MetricsMiddleware(svc.Metrics))
	
	// Initialize handlers
	h := handlers.New(log, svc)
	
	// Register routes
	h.RegisterRoutes(router)
	
	// Add metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	
	// Add health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Create HTTP server
	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
	}

	return &App{
		config:     cfg,
		logger:     log,
		router:     router,
		server:     server,
		k8sClient:  k8sClient,
		services:   svc,
		handlers:   h,
		shutdownCh: make(chan struct{}),
	}, nil
}

// Run starts the application
func (a *App) Run() error {
	// Start Kubernetes client
	if err := a.k8sClient.Start(); err != nil {
		return fmt.Errorf("failed to start Kubernetes client: %w", err)
	}

	// Start services
	if err := a.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Log server start
	a.logger.Info("Starting HTTP server", "address", a.server.Addr)

	// Start HTTP server
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the application
func (a *App) Shutdown() error {
	a.logger.Info("Shutting down application")

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), a.config.Server.ShutdownTimeout)
	defer cancel()

	// Shutdown HTTP server
	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("HTTP server shutdown error", err)
	}

	// Stop Kubernetes client
	a.k8sClient.Stop()

	// Stop services
	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}
