package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
	kubernetes.Client
}

type MockLLMSafespaceV1Client struct {
	mock.Mock
	kubernetes.LLMSafespaceV1Interface
}

type MockSandboxInterface struct {
	mock.Mock
	kubernetes.SandboxInterface
}

func (m *MockK8sClient) LlmsafespaceV1() kubernetes.LLMSafespaceV1Interface {
	args := m.Called()
	return args.Get(0).(kubernetes.LLMSafespaceV1Interface)
}

func (m *MockLLMSafespaceV1Client) Sandboxes(namespace string) kubernetes.SandboxInterface {
	args := m.Called(namespace)
	return args.Get(0).(kubernetes.SandboxInterface)
}

func (m *MockSandboxInterface) Create(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) Update(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) UpdateStatus(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

func (m *MockSandboxInterface) Get(name string, options metav1.GetOptions) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) List(opts metav1.ListOptions) (*llmsafespacev1.SandboxList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.SandboxList), args.Error(1)
}

func (m *MockSandboxInterface) Watch(opts metav1.ListOptions) (interface{}, error) {
	args := m.Called(opts)
	return args.Get(0), args.Error(1)
}

type MockDatabaseService struct {
	mock.Mock
	database.Service
}

func (m *MockDatabaseService) CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error {
	args := m.Called(ctx, sandboxID, userID, runtime)
	return args.Error(0)
}

func (m *MockDatabaseService) GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
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

type MockWarmPoolService struct {
	mock.Mock
	warmpool.Service
}

func (m *MockWarmPoolService) CheckAvailability(ctx context.Context, runtime, securityLevel string) (bool, error) {
	args := m.Called(ctx, runtime, securityLevel)
	return args.Bool(0), args.Error(1)
}

type MockFileService struct {
	mock.Mock
	file.Service
}

func (m *MockFileService) ListFiles(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]file.FileInfo, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]file.FileInfo), args.Error(1)
}

func (m *MockFileService) DownloadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]byte, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockFileService) UploadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string, content []byte) (*file.FileInfo, error) {
	args := m.Called(ctx, sandbox, path, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*file.FileInfo), args.Error(1)
}

func (m *MockFileService) DeleteFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) error {
	args := m.Called(ctx, sandbox, path)
	return args.Error(0)
}

type MockExecutionService struct {
	mock.Mock
	execution.Service
}

func (m *MockExecutionService) Execute(ctx context.Context, sandbox *llmsafespacev1.Sandbox, execType, content string, timeout int) (*execution.Result, error) {
	args := m.Called(ctx, sandbox, execType, content, timeout)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

func (m *MockExecutionService) ExecuteStream(ctx context.Context, sandbox *llmsafespacev1.Sandbox, execType, content string, timeout int, outputCallback func(string, string)) (*execution.Result, error) {
	args := m.Called(ctx, sandbox, execType, content, timeout, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

func (m *MockExecutionService) InstallPackages(ctx context.Context, sandbox *llmsafespacev1.Sandbox, packages []string, manager string) (*execution.Result, error) {
	args := m.Called(ctx, sandbox, packages, manager)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
}

type MockMetricsService struct {
	mock.Mock
	metrics.Service
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

func (m *MockMetricsService) IncrementActiveConnections(connType string) {
	m.Called(connType)
}

func (m *MockMetricsService) DecrementActiveConnections(connType string) {
	m.Called(connType)
}

type MockCacheService struct {
	mock.Mock
	cache.Service
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	args := m.Called(ctx, sessionID, session, expiration)
	return args.Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func setupSandboxService(t *testing.T) (*Service, *MockK8sClient, *MockLLMSafespaceV1Client, *MockSandboxInterface, *MockDatabaseService, *MockWarmPoolService, *MockFileService, *MockExecutionService, *MockMetricsService, *MockCacheService) {
	mockLogger, _ := logger.New(true, "debug", "console")
	mockK8sClient := new(MockK8sClient)
	mockLLMSafespaceV1Client := new(MockLLMSafespaceV1Client)
	mockSandboxInterface := new(MockSandboxInterface)
	mockDbService := new(MockDatabaseService)
	mockWarmPoolService := new(MockWarmPoolService)
	mockFileService := new(MockFileService)
	mockExecutionService := new(MockExecutionService)
	mockMetricsService := new(MockMetricsService)
	mockCacheService := new(MockCacheService)

	mockK8sClient.On("LlmsafespaceV1").Return(mockLLMSafespaceV1Client)
	mockLLMSafespaceV1Client.On("Sandboxes", mock.Anything).Return(mockSandboxInterface)

	// Create real service instances
	k8sClient := &kubernetes.Client{}
	dbService := &database.Service{}
	warmPoolSvc := &warmpool.Service{}
	fileSvc := &file.Service{}
	executionSvc := &execution.Service{}
	metricsSvc := &metrics.Service{}
	cacheSvc := &cache.Service{}
	
	service, err := New(
		mockLogger,
		k8sClient,
		dbService,
		warmPoolSvc,
		fileSvc,
		executionSvc,
		metricsSvc,
		cacheSvc,
	)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	
	// Replace with our mocks
	var k8sClientInterface kubernetes.Client = mockK8sClient
	var dbServiceInterface database.Service = mockDbService
	var warmPoolServiceInterface warmpool.Service = mockWarmPoolService
	var fileServiceInterface file.Service = mockFileService
	var executionServiceInterface execution.Service = mockExecutionService
	var metricsServiceInterface metrics.Service = mockMetricsService
	var cacheServiceInterface cache.Service = mockCacheService

	service.k8sClient = &k8sClientInterface
	service.dbService = &dbServiceInterface
	service.warmPoolSvc = &warmPoolServiceInterface
	service.fileSvc = &fileServiceInterface
	service.executionSvc = &executionServiceInterface
	service.metricsSvc = &metricsServiceInterface
	
	if service.sessionMgr != nil {
		service.sessionMgr.cacheService = &cacheServiceInterface
	}

	return service, mockK8sClient, mockLLMSafespaceV1Client, mockSandboxInterface, mockDbService, mockWarmPoolService, mockFileService, mockExecutionService, mockMetricsService, mockCacheService
}

func TestCreateSandbox(t *testing.T) {
	service, _, _, mockSandboxInterface, mockDbService, mockWarmPoolService, _, _, mockMetricsService, _ := setupSandboxService(t)

	ctx := context.Background()
	req := CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
		Namespace:     "default",
		UseWarmPool:   true,
	}

	// Test case: Successful creation with warm pool
	mockWarmPoolService.On("CheckAvailability", ctx, "python:3.10", "standard").Return(true, nil).Once()
	mockSandboxInterface.On("Create", mock.MatchedBy(func(sandbox *llmsafespacev1.Sandbox) bool {
		return sandbox.Spec.Runtime == "python:3.10" && 
		       sandbox.Annotations["llmsafespace.dev/use-warm-pod"] == "true"
	})).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
	}, nil).Once()
	mockDbService.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(nil).Once()
	mockMetricsService.On("RecordSandboxCreation", "python:3.10", true).Once()

	result, err := service.CreateSandbox(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, "sb-12345", result.Name)

	// Test case: Successful creation without warm pool
	req.UseWarmPool = false
	mockSandboxInterface.On("Create", mock.MatchedBy(func(sandbox *llmsafespacev1.Sandbox) bool {
		return sandbox.Spec.Runtime == "python:3.10" && 
		       sandbox.Annotations["llmsafespace.dev/use-warm-pod"] == ""
	})).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-67890",
		},
	}, nil).Once()
	mockDbService.On("CreateSandboxMetadata", ctx, "sb-67890", "user123", "python:3.10").Return(nil).Once()
	mockMetricsService.On("RecordSandboxCreation", "python:3.10", false).Once()

	result, err = service.CreateSandbox(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, "sb-67890", result.Name)

	// Test case: Database error
	mockSandboxInterface.On("Create", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-error",
		},
	}, nil).Once()
	mockDbService.On("CreateSandboxMetadata", ctx, "sb-error", "user123", "python:3.10").Return(errors.New("database error")).Once()
	mockSandboxInterface.On("Delete", "sb-error", mock.Anything).Return(nil).Once()

	_, err = service.CreateSandbox(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to store sandbox metadata")

	mockWarmPoolService.AssertExpectations(t)
	mockSandboxInterface.AssertExpectations(t)
	mockDbService.AssertExpectations(t)
	mockMetricsService.AssertExpectations(t)
}

func TestGetSandbox(t *testing.T) {
	service, _, _, mockSandboxInterface, mockDbService, _, _, _, _, _ := setupSandboxService(t)

	ctx := context.Background()
	sandboxID := "sb-12345"

	// Test case: Successful get
	mockDbService.On("GetSandboxMetadata", ctx, sandboxID).Return(map[string]interface{}{
		"id":      sandboxID,
		"user_id": "user123",
		"runtime": "python:3.10",
	}, nil).Once()
	mockSandboxInterface.On("Get", sandboxID, mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxID,
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime: "python:3.10",
		},
	}, nil).Once()

	result, err := service.GetSandbox(ctx, sandboxID)
	assert.NoError(t, err)
	assert.Equal(t, sandboxID, result.Name)
	assert.Equal(t, "python:3.10", result.Spec.Runtime)

	// Test case: Sandbox not found in database
	mockDbService.On("GetSandboxMetadata", ctx, "nonexistent").Return(nil, nil).Once()

	_, err = service.GetSandbox(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not found")

	// Test case: Database error
	mockDbService.On("GetSandboxMetadata", ctx, "error").Return(nil, errors.New("database error")).Once()

	_, err = service.GetSandbox(ctx, "error")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get sandbox metadata")

	mockDbService.AssertExpectations(t)
	mockSandboxInterface.AssertExpectations(t)
}

func TestListSandboxes(t *testing.T) {
	service, _, _, mockSandboxInterface, mockDbService, _, _, _, _, _ := setupSandboxService(t)

	ctx := context.Background()
	userID := "user123"
	limit := 10
	offset := 0

	// Test case: Successful list
	mockDbService.On("ListSandboxes", ctx, userID, limit, offset).Return([]map[string]interface{}{
		{
			"id":      "sb-12345",
			"runtime": "python:3.10",
		},
		{
			"id":      "sb-67890",
			"runtime": "nodejs:16",
		},
	}, nil).Once()

	mockSandboxInterface.On("Get", "sb-12345", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase:    "Running",
			Endpoint: "sb-12345.default.svc.cluster.local",
		},
	}, nil).Once()

	mockSandboxInterface.On("Get", "sb-67890", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-67890",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase:    "Running",
			Endpoint: "sb-67890.default.svc.cluster.local",
		},
	}, nil).Once()

	result, err := service.ListSandboxes(ctx, userID, limit, offset)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "sb-12345", result[0]["id"])
	assert.Equal(t, "Running", result[0]["status"])
	assert.Equal(t, "sb-67890", result[1]["id"])
	assert.Equal(t, "Running", result[1]["status"])

	// Test case: Database error
	mockDbService.On("ListSandboxes", ctx, userID, limit, offset).Return(nil, errors.New("database error")).Once()

	_, err = service.ListSandboxes(ctx, userID, limit, offset)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list sandboxes")

	mockDbService.AssertExpectations(t)
	mockSandboxInterface.AssertExpectations(t)
}

func TestTerminateSandbox(t *testing.T) {
	service, _, _, mockSandboxInterface, mockDbService, _, _, _, mockMetricsService, _ := setupSandboxService(t)

	ctx := context.Background()
	sandboxID := "sb-12345"

	// Test case: Successful termination
	mockDbService.On("GetSandboxMetadata", ctx, sandboxID).Return(map[string]interface{}{
		"id":      sandboxID,
		"user_id": "user123",
		"runtime": "python:3.10",
	}, nil).Once()
	mockSandboxInterface.On("Delete", sandboxID, mock.Anything).Return(nil).Once()
	mockMetricsService.On("RecordSandboxTermination", "python:3.10").Once()

	err := service.TerminateSandbox(ctx, sandboxID)
	assert.NoError(t, err)

	// Test case: Sandbox not found
	mockDbService.On("GetSandboxMetadata", ctx, "nonexistent").Return(nil, nil).Once()

	err = service.TerminateSandbox(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox not found")

	// Test case: Database error
	mockDbService.On("GetSandboxMetadata", ctx, "error").Return(nil, errors.New("database error")).Once()

	err = service.TerminateSandbox(ctx, "error")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get sandbox metadata")

	mockDbService.AssertExpectations(t)
	mockSandboxInterface.AssertExpectations(t)
	mockMetricsService.AssertExpectations(t)
}

func TestGetSandboxStatus(t *testing.T) {
	service, _, _, mockSandboxInterface, _, _, _, _, _, _ := setupSandboxService(t)

	ctx := context.Background()
	sandboxID := "sb-12345"

	// Test case: Successful get status
	mockSandboxInterface.On("Get", sandboxID, mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxID,
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase:    "Running",
			Endpoint: "sb-12345.default.svc.cluster.local",
		},
	}, nil).Once()

	status, err := service.GetSandboxStatus(ctx, sandboxID)
	assert.NoError(t, err)
	assert.Equal(t, "Running", status.Phase)
	assert.Equal(t, "sb-12345.default.svc.cluster.local", status.Endpoint)

	// Test case: Kubernetes error
	mockSandboxInterface.On("Get", "error", mock.Anything).Return(nil, errors.New("kubernetes error")).Once()

	_, err = service.GetSandboxStatus(ctx, "error")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get sandbox")

	mockSandboxInterface.AssertExpectations(t)
}

func TestExecute(t *testing.T) {
	service, _, _, mockSandboxInterface, _, _, _, mockExecutionService, mockMetricsService, _ := setupSandboxService(t)

	ctx := context.Background()
	req := ExecuteRequest{
		Type:      "code",
		Content:   "print('Hello, World!')",
		Timeout:   30,
		SandboxID: "sb-12345",
	}

	// Test case: Successful execution
	mockSandboxInterface.On("Get", "sb-12345", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	mockExecutionService.On("Execute", ctx, mock.Anything, "code", "print('Hello, World!')", 30).Return(&execution.Result{
		ExitCode: 0,
		Stdout:   "Hello, World!\n",
		Stderr:   "",
	}, nil).Once()

	mockMetricsService.On("RecordExecution", "code", "python:3.10", "success", mock.Anything).Once()

	result, err := service.Execute(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "Hello, World!\n", result.Stdout)

	// Test case: Sandbox not running
	mockSandboxInterface.On("Get", "sb-notrunning", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-notrunning",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Creating",
		},
	}, nil).Once()

	req.SandboxID = "sb-notrunning"
	_, err = service.Execute(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox is not running")

	// Test case: Execution error
	mockSandboxInterface.On("Get", "sb-execerror", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-execerror",
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	mockExecutionService.On("Execute", ctx, mock.Anything, "code", "print('Hello, World!')", 30).Return(nil, errors.New("execution error")).Once()

	req.SandboxID = "sb-execerror"
	_, err = service.Execute(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute")

	mockSandboxInterface.AssertExpectations(t)
	mockExecutionService.AssertExpectations(t)
	mockMetricsService.AssertExpectations(t)
}

func TestFileOperations(t *testing.T) {
	service, _, _, mockSandboxInterface, _, _, mockFileService, _, _, _ := setupSandboxService(t)

	ctx := context.Background()
	sandboxID := "sb-12345"
	path := "/workspace/file.txt"
	content := []byte("Hello, World!")

	// Setup sandbox mock
	mockSandboxInterface.On("Get", sandboxID, mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxID,
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Times(4)

	// Test case: List files
	mockFileService.On("ListFiles", ctx, mock.Anything, "/workspace").Return([]file.FileInfo{
		{
			Path:      "/workspace/file.txt",
			Size:      13,
			IsDir:     false,
		},
	}, nil).Once()

	files, err := service.ListFiles(ctx, sandboxID, "/workspace")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "/workspace/file.txt", files[0].Path)

	// Test case: Download file
	mockFileService.On("DownloadFile", ctx, mock.Anything, path).Return(content, nil).Once()

	downloadedContent, err := service.DownloadFile(ctx, sandboxID, path)
	assert.NoError(t, err)
	assert.Equal(t, content, downloadedContent)

	// Test case: Upload file
	mockFileService.On("UploadFile", ctx, mock.Anything, path, content).Return(&file.FileInfo{
		Path:      path,
		Size:      13,
		IsDir:     false,
	}, nil).Once()

	fileInfo, err := service.UploadFile(ctx, sandboxID, path, content)
	assert.NoError(t, err)
	assert.Equal(t, path, fileInfo.Path)
	assert.Equal(t, int64(13), fileInfo.Size)

	// Test case: Delete file
	mockFileService.On("DeleteFile", ctx, mock.Anything, path).Return(nil).Once()

	err = service.DeleteFile(ctx, sandboxID, path)
	assert.NoError(t, err)

	mockSandboxInterface.AssertExpectations(t)
	mockFileService.AssertExpectations(t)
}

func TestInstallPackages(t *testing.T) {
	service, _, _, mockSandboxInterface, _, _, _, mockExecutionService, _, _ := setupSandboxService(t)

	ctx := context.Background()
	req := InstallPackagesRequest{
		Packages:  []string{"numpy", "pandas"},
		Manager:   "pip",
		SandboxID: "sb-12345",
	}

	// Test case: Successful installation
	mockSandboxInterface.On("Get", "sb-12345", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	mockExecutionService.On("InstallPackages", ctx, mock.Anything, []string{"numpy", "pandas"}, "pip").Return(&execution.Result{
		ExitCode: 0,
		Stdout:   "Successfully installed numpy pandas",
		Stderr:   "",
	}, nil).Once()

	result, err := service.InstallPackages(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Successfully installed")

	// Test case: Sandbox not running
	mockSandboxInterface.On("Get", "sb-notrunning", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-notrunning",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Creating",
		},
	}, nil).Once()

	req.SandboxID = "sb-notrunning"
	_, err = service.InstallPackages(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox is not running")

	// Test case: Installation error
	mockSandboxInterface.On("Get", "sb-installerror", mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-installerror",
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	mockExecutionService.On("InstallPackages", ctx, mock.Anything, []string{"numpy", "pandas"}, "pip").Return(nil, errors.New("installation error")).Once()

	req.SandboxID = "sb-installerror"
	_, err = service.InstallPackages(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to install packages")

	mockSandboxInterface.AssertExpectations(t)
	mockExecutionService.AssertExpectations(t)
}

func TestWebSocketSession(t *testing.T) {
	service, _, _, mockSandboxInterface, _, _, _, _, mockMetricsService, mockCacheService := setupSandboxService(t)

	// Mock websocket connection
	mockConn := &websocket.Conn{}
	userID := "user123"
	sandboxID := "sb-12345"

	// Setup sandbox mock
	mockSandboxInterface.On("Get", sandboxID, mock.Anything).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxID,
		},
		Status: llmsafespacev1.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	// Test case: Create session
	mockCacheService.On("SetSession", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockMetricsService.On("IncrementActiveConnections", "websocket").Once()

	session, err := service.CreateSession(userID, sandboxID, mockConn)
	assert.NoError(t, err)
	assert.NotNil(t, session)
	assert.Equal(t, userID, session.UserID)
	assert.Equal(t, sandboxID, session.SandboxID)

	// Test case: Close session
	mockCacheService.On("DeleteSession", mock.Anything, mock.Anything).Return(nil).Once()
	mockMetricsService.On("DecrementActiveConnections", "websocket").Once()

	service.CloseSession(session.ID)

	mockMetricsService.AssertExpectations(t)
	mockCacheService.AssertExpectations(t)
}
