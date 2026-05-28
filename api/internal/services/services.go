package services

import (
	"fmt"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/ratelimit"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
)

type Services struct {
	Auth        interfaces.AuthService
	Database    interfaces.DatabaseService
	Cache       interfaces.CacheService
	Metrics     interfaces.MetricsService
	Workspace   interfaces.WorkspaceService
	RateLimiter interfaces.RateLimiterService
}

var _ interfaces.Services = &Services{}

func (s *Services) GetAuth() interfaces.AuthService {
	return s.Auth
}

func (s *Services) GetDatabase() interfaces.DatabaseService {
	return s.Database
}

func (s *Services) GetCache() interfaces.CacheService {
	return s.Cache
}

func (s *Services) GetMetrics() interfaces.MetricsService {
	return s.Metrics
}

func (s *Services) GetWorkspace() interfaces.WorkspaceService {
	return s.Workspace
}

func (s *Services) GetRateLimiter() interfaces.RateLimiterService {
	return s.RateLimiter
}

func New(cfg *config.Config, log *logger.Logger, k8sClient interfaces.KubernetesClient) (*Services, error) {
	metricsService := metrics.New(log)

	dbService, err := database.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database service: %w", err)
	}

	cacheService, err := cache.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache service: %w", err)
	}

	authService, err := auth.New(cfg, log, dbService, cacheService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth service: %w", err)
	}

	workspaceConfig := &workspace.Config{
		Namespace: cfg.Kubernetes.Namespace,
	}

	workspaceService, err := workspace.New(
		log,
		k8sClient,
		dbService,
		cacheService,
		metricsService,
		workspaceConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize workspace service: %w", err)
	}

	rateLimiterService := ratelimit.NewWithCache(log, cacheService)

	return &Services{
		Auth:        authService,
		Database:    dbService,
		Cache:       cacheService,
		Metrics:     metricsService,
		Workspace:   workspaceService,
		RateLimiter: rateLimiterService,
	}, nil
}

func (s *Services) Start() error {
	if err := s.Metrics.Start(); err != nil {
		return fmt.Errorf("failed to start metrics service: %w", err)
	}
	if err := s.Database.Start(); err != nil {
		return fmt.Errorf("failed to start database service: %w", err)
	}
	if err := s.Cache.Start(); err != nil {
		return fmt.Errorf("failed to start cache service: %w", err)
	}
	if err := s.Auth.Start(); err != nil {
		return fmt.Errorf("failed to start auth service: %w", err)
	}
	if err := s.Workspace.Start(); err != nil {
		return fmt.Errorf("failed to start workspace service: %w", err)
	}
	return nil
}

func (s *Services) Stop() error {
	var errs []error
	if err := s.Workspace.Stop(); err != nil {
		errs = append(errs, err)
	}
	if err := s.Auth.Stop(); err != nil {
		errs = append(errs, err)
	}
	if err := s.Cache.Stop(); err != nil {
		errs = append(errs, err)
	}
	if err := s.Database.Stop(); err != nil {
		errs = append(errs, err)
	}
	if err := s.Metrics.Stop(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
