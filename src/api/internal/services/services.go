package services

import (
	"fmt"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
)

// Services holds all application services
type Services struct {
	Auth      *auth.Service
	Database  *database.Service
	Execution *execution.Service
	File      *file.Service
	Metrics   *metrics.Service
	Sandbox   *sandbox.Service
	WarmPool  *warmpool.Service
}

// New creates and initializes all services
func New(cfg *config.Config, log *logger.Logger, k8sClient *kubernetes.Client) (*Services, error) {
	// Initialize metrics service first as other services may use it
	metricsService := metrics.New()

	// Initialize database service
	dbService, err := database.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database service: %w", err)
	}

	// Initialize auth service
	authService, err := auth.New(cfg, log, dbService, cacheService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth service: %w", err)
	}

	// Initialize warm pool service
	warmPoolService, err := warmpool.New(log, k8sClient, dbService, metricsService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize warm pool service: %w", err)
	}

	// Initialize file service
	fileService, err := file.New(log, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize file service: %w", err)
	}

	// Initialize execution service
	executionService, err := execution.New(log, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize execution service: %w", err)
	}

	// Initialize sandbox service
	sandboxService, err := sandbox.New(log, k8sClient, dbService, warmPoolService, fileService, executionService, metricsService, cacheService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sandbox service: %w", err)
	}

	return &Services{
		Auth:      authService,
		Database:  dbService,
		Execution: executionService,
		File:      fileService,
		Metrics:   metricsService,
		Sandbox:   sandboxService,
		WarmPool:  warmPoolService,
	}, nil
}

// Start starts all services
func (s *Services) Start() error {
	// Start services in appropriate order
	if err := s.Database.Start(); err != nil {
		return fmt.Errorf("failed to start database service: %w", err)
	}

	return nil
}

// Stop stops all services
func (s *Services) Stop() error {
	// Stop services in reverse order
	if err := s.Database.Stop(); err != nil {
		return fmt.Errorf("failed to stop database service: %w", err)
	}

	return nil
}
