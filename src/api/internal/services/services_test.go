package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
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

func (m *MockAuthService) GetUserID(c *gin.Context) string {
	args := m.Called(c)
	return args.String(0)
}

func (m *MockAuthService) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0)
}

func (m *MockAuthService) GenerateToken(userID string) (string, error) {
	args := m.Called(userID)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) ValidateToken(token string) (string, error) {
	args := m.Called(token)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
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

func (m *MockDatabaseService) GetUserByID(ctx context.Context, userID string) (map[string]interface{}, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) GetSandboxByID(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) GetUserIDByAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
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

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheService) GetObject(ctx context.Context, key string, value interface{}) error {
	args := m.Called(ctx, key, value)
	return args.Error(0)
}

func (m *MockCacheService) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	args := m.Called(ctx, sessionID, session, expiration)
	return args.Error(0)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

type MockSandboxService struct {
	mock.Mock
}

func (m *MockSandboxService) CreateSandbox(ctx context.Context, req sandbox.CreateSandboxRequest) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxService) GetSandbox(ctx context.Context, sandboxID string) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockSandboxService) TerminateSandbox(ctx context.Context, sandboxID string) error {
	args := m.Called(ctx, sandboxID)
	return args.Error(0)
}

func (m *MockSandboxService) GetSandboxStatus(ctx context.Context, sandboxID string) (*llmsafespacev1.SandboxStatus, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.SandboxStatus), args.Error(1)
}

func (m *MockSandboxService) Execute(ctx context.Context, req sandbox.ExecuteRequest) (*execution.Result, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

func (m *MockSandboxService) ListFiles(ctx context.Context, sandboxID, path string) ([]interfaces.FileInfo, error) {
	args := m.Called(ctx, sandboxID, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]interfaces.FileInfo), args.Error(1)
}

func (m *MockSandboxService) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	args := m.Called(ctx, sandboxID, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockSandboxService) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*interfaces.FileInfo, error) {
	args := m.Called(ctx, sandboxID, path, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*interfaces.FileInfo), args.Error(1)
}

func (m *MockSandboxService) DeleteFile(ctx context.Context, sandboxID, path string) error {
	args := m.Called(ctx, sandboxID, path)
	return args.Error(0)
}

func (m *MockSandboxService) InstallPackages(ctx context.Context, req sandbox.InstallPackagesRequest) (*execution.Result, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

func (m *MockSandboxService) CreateSession(userID, sandboxID string, conn *websocket.Conn) (*sandbox.Session, error) {
	args := m.Called(userID, sandboxID, conn)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sandbox.Session), args.Error(1)
}

func (m *MockSandboxService) CloseSession(sessionID string) {
	m.Called(sessionID)
}

func (m *MockSandboxService) HandleSession(session *sandbox.Session) {
	m.Called(session)
}

func (m *MockSandboxService) GetMetrics() map[string]interface{} {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(map[string]interface{})
}

func (m *MockSandboxService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSandboxService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockWarmPoolService struct {
	mock.Mock
}

func (m *MockWarmPoolService) GetWarmSandbox(ctx context.Context, runtime string) (string, error) {
	args := m.Called(ctx, runtime)
	return args.String(0), args.Error(1)
}

func (m *MockWarmPoolService) AddToWarmPool(ctx context.Context, sandboxID, runtime string) error {
	args := m.Called(ctx, sandboxID, runtime)
	return args.Error(0)
}

func (m *MockWarmPoolService) RemoveFromWarmPool(ctx context.Context, sandboxID string) error {
	args := m.Called(ctx, sandboxID)
	return args.Error(0)
}

func (m *MockWarmPoolService) GetWarmPoolStatus(ctx context.Context, name, namespace string) (*llmsafespacev1.WarmPoolStatus, error) {
	args := m.Called(ctx, name, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPoolStatus), args.Error(1)
}

func (m *MockWarmPoolService) GetGlobalWarmPoolStatus(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockWarmPoolService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockWarmPoolService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockExecutionService struct {
	mock.Mock
}

func (m *MockExecutionService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockExecutionService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockExecutionService) ExecuteCode(ctx context.Context, sandboxID, code string, timeout int) (*execution.Result, error) {
	args := m.Called(ctx, sandboxID, code, timeout)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

func (m *MockExecutionService) ExecuteCommand(ctx context.Context, sandboxID, command string, timeout int) (*execution.Result, error) {
	args := m.Called(ctx, sandboxID, command, timeout)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

type MockFileService struct {
	mock.Mock
}

func (m *MockFileService) ListFiles(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]interfaces.FileInfo, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]interfaces.FileInfo), args.Error(1)
}

func (m *MockFileService) DownloadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]byte, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockFileService) UploadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string, content []byte) (*interfaces.FileInfo, error) {
	args := m.Called(ctx, sandbox, path, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*interfaces.FileInfo), args.Error(1)
}

func (m *MockFileService) DeleteFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) error {
	args := m.Called(ctx, sandbox, path)
	return args.Error(0)
}

func (m *MockFileService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockFileService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	m.Called(method, path, status, duration, size)
}

func (m *MockMetricsService) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	m.Called(runtime, warmPodUsed)
}

func (m *MockMetricsService) RecordSandboxTermination(runtime string) {
	m.Called(runtime)
}

func (m *MockMetricsService) RecordExecution(execType, runtime, status string, duration time.Duration) {
	m.Called(execType, runtime, status, duration)
}

func (m *MockMetricsService) IncActiveConnections() {
	m.Called()
}

func (m *MockMetricsService) DecActiveConnections() {
	m.Called()
}

func (m *MockMetricsService) RecordWarmPoolHit() {
	m.Called()
}

func (m *MockMetricsService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

// Helper function to create a valid test config
func createTestConfig() *config.Config {
	return &config.Config{
		Auth: config.Auth{
			JWTSecret: "test-secret",
		},
		Database: config.Database{
			Host: "localhost",
			Port: 5432,
		},
		Redis: config.Redis{
			Host: "localhost",
		},
		Kubernetes: config.Kubernetes{
			ConfigPath: "test-config-path",
		},
	}
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	cfg := createTestConfig()
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
	badCfg := createTestConfig()
	badCfg.Database.Host = ""
	services, err = New(badCfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize database service")

	// Test cache service initialization failure
	badCfg = createTestConfig()
	badCfg.Redis.Host = ""
	services, err = New(badCfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize cache service")

	// Test auth service initialization failure
	badCfg = createTestConfig()
	badCfg.Auth.JWTSecret = ""
	services, err = New(badCfg, log, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Contains(t, err.Error(), "failed to initialize auth service")
}

func TestStartStop(t *testing.T) {
	// Create mock services
	mockDb := new(MockDatabaseService)
	
	// Create mock cache service
	mockCache := new(MockCacheService)
	
	// Create services struct with mocks
	services := &Services{
		Database: mockDb,
		Cache: mockCache,
	}
	
	// Test successful start
	mockDb.On("Start").Return(nil).Once()
	mockCache.On("Start").Return(nil).Once()
	err := services.Start()
	assert.NoError(t, err)
	mockDb.AssertExpectations(t)
	mockCache.AssertExpectations(t)

	// Test start failure
	mockDb.On("Start").Return(errors.New("start error")).Once()
	err = services.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start database service")
	mockDb.AssertExpectations(t)

	// Test successful stop
	mockDb.On("Stop").Return(nil).Once()
	mockCache.On("Stop").Return(nil).Once()
	err = services.Stop()
	assert.NoError(t, err)
	mockDb.AssertExpectations(t)
	mockCache.AssertExpectations(t)

	// Test stop failure
	mockDb.On("Stop").Return(errors.New("stop error")).Once()
	err = services.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stop database service")
	mockDb.AssertExpectations(t)
}
