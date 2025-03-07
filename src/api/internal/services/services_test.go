package services

import (
	"errors"
	"testing"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations
type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockDatabaseService struct {
	mock.Mock
}

func (m *MockDatabaseService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

// Mock service factory functions
func mockNewAuth(cfg *config.Config, log *logger.Logger, dbService *database.Service, cacheService *cache.Service) (*auth.Service, error) {
	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT secret is required")
	}
	return &auth.Service{}, nil
}

func mockNewDatabase(cfg *config.Config, log *logger.Logger) (*database.Service, error) {
	if cfg.Database.Host == "" {
		return nil, errors.New("database host is required")
	}
	return &database.Service{}, nil
}

func mockNewCache(cfg *config.Config, log *logger.Logger) (*cache.Service, error) {
	if cfg.Redis.Host == "" {
		return nil, errors.New("redis host is required")
	}
	return &cache.Service{}, nil
}

func mockNewWarmPool(log *logger.Logger, k8sClient *kubernetes.Client, dbService *database.Service, metricsService *metrics.Service) (*warmpool.Service, error) {
	if k8sClient == nil {
		return nil, errors.New("k8s client is required")
	}
	return &warmpool.Service{}, nil
}

func mockNewFile(log *logger.Logger, k8sClient *kubernetes.Client) (*file.Service, error) {
	if k8sClient == nil {
		return nil, errors.New("k8s client is required")
	}
	return &file.Service{}, nil
}

func mockNewExecution(log *logger.Logger, k8sClient *kubernetes.Client) (*execution.Service, error) {
	if k8sClient == nil {
		return nil, errors.New("k8s client is required")
	}
	return &execution.Service{}, nil
}

func mockNewSandbox(log *logger.Logger, k8sClient *kubernetes.Client, dbService *database.Service, warmPoolService *warmpool.Service, fileService *file.Service, executionService *execution.Service, metricsService *metrics.Service, cacheService *cache.Service) (*sandbox.Service, error) {
	if k8sClient == nil {
		return nil, errors.New("k8s client is required")
	}
	return &sandbox.Service{}, nil
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Database.Host = "localhost"
	cfg.Redis.Host = "localhost"
	k8sClient := &kubernetes.Client{}

	// Test successful initialization
	services, err := New(cfg, log, k8sClient)
	assert.NoError(t, err)
	assert.NotNil(t, services)
	assert.NotNil(t, services.Auth)
	assert.NotNil(t, services.Database)
	assert.NotNil(t, services.Execution)
	assert.NotNil(t, services.File)
	assert.NotNil(t, services.Metrics)
	assert.NotNil(t, services.Sandbox)
	assert.NotNil(t, services.WarmPool)

	// Test database service initialization failure
	cfg.Database.Host = ""
	services, err = New(cfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize database service")

	// Restore database host for subsequent tests
	cfg.Database.Host = "localhost"

	// Test cache service initialization failure
	cfg.Redis.Host = ""
	services, err = New(cfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize cache service")

	// Restore redis host for subsequent tests
	cfg.Redis.Host = "localhost"

	// Test auth service initialization failure
	cfg.Auth.JWTSecret = ""
	services, err = New(cfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize auth service")
}

func TestStartStop(t *testing.T) {
	// Create mock services
	mockDb := new(MockDatabaseService)
	
	// Create services struct with mocks
	services := &Services{
		Database: mockDb,
	}

	// Test successful start
	mockDb.On("Start").Return(nil).Once()
	err := services.Start()
	assert.NoError(t, err)
	mockDb.AssertExpectations(t)

	// Test start failure
	mockDb.On("Start").Return(errors.New("start error")).Once()
	err = services.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start database service")
	mockDb.AssertExpectations(t)

	// Test successful stop
	mockDb.On("Stop").Return(nil).Once()
	err = services.Stop()
	assert.NoError(t, err)
	mockDb.AssertExpectations(t)

	// Test stop failure
	mockDb.On("Stop").Return(errors.New("stop error")).Once()
	err = services.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stop database service")
	mockDb.AssertExpectations(t)
}
