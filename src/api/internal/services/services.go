package services

import (
	"fmt"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
)

// Services holds all application services
type Services struct {
	Auth      interfaces.AuthService
	Database  interfaces.DatabaseService
	Cache     interfaces.CacheService
	Execution interfaces.ExecutionService
	File      interfaces.FileService
	Metrics   interfaces.MetricsService
	Sandbox   interfaces.SandboxService
	WarmPool  interfaces.WarmPoolService
}

// Ensure Services implements the required interfaces
var _ interfaces.Services = (*Services)(nil)

// New creates and initializes all services
func New(cfg *config.Config, log *logger.Logger, k8sClient interfaces.KubernetesClient) (*Services, error) {
	// Initialize metrics service first as other services may use it
	metricsService := metrics.New()

	// Initialize database service
	dbService, err := database.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database service: %w", err)
	}

	// Initialize cache service
	cacheService, err := cache.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache service: %w", err)
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
		Cache:     cacheService,
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
	if err := s.Metrics.Start(); err != nil {
		return fmt.Errorf("failed to start metrics service: %w", err)
	}
	
	if err := s.Database.Start(); err != nil {
		s.Metrics.Stop() // Clean up metrics if database fails
		return fmt.Errorf("failed to start database service: %w", err)
	}
	
	if err := s.Cache.Start(); err != nil {
		s.Database.Stop() // Clean up database if cache fails
		s.Metrics.Stop()
		return fmt.Errorf("failed to start cache service: %w", err)
	}
	
	if err := s.Auth.Start(); err != nil {
		s.Cache.Stop() // Clean up cache if auth fails
		s.Database.Stop()
		s.Metrics.Stop()
		return fmt.Errorf("failed to start auth service: %w", err)
	}
	
	if err := s.File.Start(); err != nil {
		s.Auth.Stop() // Clean up previous services
		s.Database.Stop()
		s.Metrics.Stop()
		return fmt.Errorf("failed to start file service: %w", err)
	}
	
	if err := s.Execution.Start(); err != nil {
		s.File.Stop() // Clean up previous services
		s.Auth.Stop()
		s.Database.Stop()
		s.Metrics.Stop()
		return fmt.Errorf("failed to start execution service: %w", err)
	}
	
	if err := s.WarmPool.Start(); err != nil {
		s.Execution.Stop() // Clean up previous services
		s.File.Stop()
		s.Auth.Stop()
		s.Database.Stop()
		s.Metrics.Stop()
		return fmt.Errorf("failed to start warm pool service: %w", err)
	}
	
	if err := s.Sandbox.Start(); err != nil {
		s.WarmPool.Stop() // Clean up previous services
		s.Execution.Stop()
		s.File.Stop()
		s.Auth.Stop()
		s.Database.Stop()
		s.Metrics.Stop()
		return fmt.Errorf("failed to start sandbox service: %w", err)
	}

	return nil
}

// Stop stops all services
func (s *Services) Stop() error {
	var errs []error
	
	// Stop services in reverse order of initialization
	if err := s.Sandbox.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop sandbox service: %w", err))
	}
	
	if err := s.WarmPool.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop warm pool service: %w", err))
	}
	
	if err := s.Execution.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop execution service: %w", err))
	}
	
	if err := s.File.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop file service: %w", err))
	}
	
	if err := s.Auth.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop auth service: %w", err))
	}
	
	if err := s.Cache.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop cache service: %w", err))
	}
	
	if err := s.Database.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop database service: %w", err))
	}
	
	if err := s.Metrics.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop metrics service: %w", err))
	}
	
	// If we have errors, return the first one
	if len(errs) > 0 {
		return errs[0]
	}
	
	return nil
}
