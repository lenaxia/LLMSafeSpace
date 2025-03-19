package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	//"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
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
	//handlers   *handlers.Handlers
	shutdownCh chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates a new application instance
func New(cfg *config.Config, log *logger.Logger) (*App, error) {
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	
	// Set Gin mode
	if cfg.Logging.Development {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize Kubernetes client
	k8sClient, err := kubernetes.New(cfg, log)
	if err != nil {
		cancel() // Clean up context
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Initialize services
	svc, err := services.New(cfg, log, k8sClient)
	if err != nil {
		cancel() // Clean up context
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	// Create router
	router := gin.New()
	router.Use(gin.Recovery())
	
	// Add middleware
	//router.Use(handlers.LoggerMiddleware(log))
	//router.Use(handlers.MetricsMiddleware(svc.Metrics))
	
	// Initialize handlers
	//h := handlers.New(log, svc)
	
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
		//handlers:   h,
		shutdownCh: make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Run starts the application
func (a *App) Run() error {
	// Start services first
	if err := a.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Then start Kubernetes client
	if err := a.k8sClient.Start(); err != nil {
		a.services.Stop() // Clean up services if k8s fails
		return fmt.Errorf("failed to start Kubernetes client: %w", err)
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
	
	// Trigger context cancellation
	a.cancel()

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(a.ctx, a.config.Server.ShutdownTimeout)
	defer cancel()

	// Shutdown HTTP server first
	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("HTTP server shutdown error", err)
	}

	// Then stop Kubernetes client
	a.k8sClient.Stop()

	// Finally stop services
	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}
