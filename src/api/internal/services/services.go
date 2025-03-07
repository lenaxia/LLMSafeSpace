package services

import (
	"fmt"

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
)

// Service interfaces
type (
	// AuthService defines the interface for authentication services
	AuthService interface {
		GetUserID(c *gin.Context) string
		CheckResourceAccess(userID, resourceType, resourceID, action string) bool
		GenerateToken(userID string) (string, error)
		ValidateToken(token string) (string, error)
		AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error)
		AuthMiddleware() gin.HandlerFunc
		Start() error
		Stop() error
	}

	// DatabaseService defines the interface for database services
	DatabaseService interface {
		GetUserByID(ctx context.Context, userID string) (map[string]interface{}, error)
		GetSandboxByID(ctx context.Context, sandboxID string) (map[string]interface{}, error)
		ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error)
		CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error)
		Start() error
		Stop() error
	}

	// ExecutionService defines the interface for execution services
	ExecutionService interface {
		ExecuteCode(ctx context.Context, sandboxID, code string, timeout int) (*execution.Result, error)
		ExecuteCommand(ctx context.Context, sandboxID, command string, timeout int) (*execution.Result, error)
		Start() error
		Stop() error
	}

	// FileService defines the interface for file services
	FileService interface {
		ListFiles(ctx context.Context, sandboxID, path string) ([]file.FileInfo, error)
		ReadFile(ctx context.Context, sandboxID, path string) ([]byte, error)
		WriteFile(ctx context.Context, sandboxID, path string, content []byte) error
		DeleteFile(ctx context.Context, sandboxID, path string) error
		Start() error
		Stop() error
	}

	// MetricsService defines the interface for metrics services
	MetricsService interface {
		RecordRequest(method, path string, status int, duration time.Duration, size int)
		RecordSandboxCreation()
		RecordSandboxTermination()
		RecordExecution(duration time.Duration)
		IncActiveConnections()
		DecActiveConnections()
		RecordWarmPoolHit()
		Start() error
		Stop() error
	}

	// SandboxService defines the interface for sandbox services
	SandboxService interface {
		CreateSandbox(ctx context.Context, req sandbox.CreateSandboxRequest) (*llmsafespacev1.Sandbox, error)
		GetSandbox(ctx context.Context, sandboxID string) (*llmsafespacev1.Sandbox, error)
		ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error)
		TerminateSandbox(ctx context.Context, sandboxID string) error
		GetSandboxStatus(ctx context.Context, sandboxID string) (*llmsafespacev1.SandboxStatus, error)
		Execute(ctx context.Context, req sandbox.ExecuteRequest) (*execution.Result, error)
		ListFiles(ctx context.Context, sandboxID, path string) ([]file.FileInfo, error)
		DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error)
		UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*file.FileInfo, error)
		DeleteFile(ctx context.Context, sandboxID, path string) error
		InstallPackages(ctx context.Context, req sandbox.InstallPackagesRequest) (*execution.Result, error)
		CreateSession(userID, sandboxID string, conn *websocket.Conn) (*sandbox.Session, error)
		CloseSession(sessionID string)
		HandleSession(session *sandbox.Session)
		GetMetrics() map[string]interface{}
		Start() error
		Stop() error
	}

	// WarmPoolService defines the interface for warm pool services
	WarmPoolService interface {
		GetWarmSandbox(ctx context.Context, runtime string) (string, error)
		AddToWarmPool(ctx context.Context, sandboxID, runtime string) error
		RemoveFromWarmPool(ctx context.Context, sandboxID string) error
		GetWarmPoolStatus(ctx context.Context) (map[string]interface{}, error)
		Start() error
		Stop() error
	}
)

// Services holds all application services
type Services struct {
	Auth      AuthService
	Database  DatabaseService
	Execution ExecutionService
	File      FileService
	Metrics   MetricsService
	Sandbox   SandboxService
	WarmPool  WarmPoolService
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

	// Initialize cache service
	cacheService, err := cache.New(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache service: %w", err)
	}

	// Initialize auth service
	authService, err := auth.New(cfg, log, dbService.(database.Service), cacheService.(cache.Service))
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
