package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Mock implementations
type mockKubernetesClient struct {
	mock.Mock
}

func (m *mockKubernetesClient) Clientset() kubernetes.Interface {
	return fake.NewSimpleClientset()
}

func (m *mockKubernetesClient) LlmsafespaceV1() mockLLMSafespaceV1Interface {
	args := m.Called()
	return args.Get(0).(mockLLMSafespaceV1Interface)
}

func (m *mockKubernetesClient) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockKubernetesClient) Stop() {
	m.Called()
}

type mockLLMSafespaceV1Interface struct {
	mock.Mock
}

func (m *mockLLMSafespaceV1Interface) Sandboxes(namespace string) mockSandboxInterface {
	args := m.Called(namespace)
	return args.Get(0).(mockSandboxInterface)
}

func (m *mockLLMSafespaceV1Interface) WarmPods(namespace string) interface{} {
	args := m.Called(namespace)
	return args.Get(0)
}

type mockSandboxInterface struct {
	mock.Mock
}

func (m *mockSandboxInterface) Create(sandbox *types.Sandbox, opts metav1.CreateOptions) (*types.Sandbox, error) {
	args := m.Called(sandbox, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *mockSandboxInterface) Delete(name string, opts metav1.DeleteOptions) error {
	args := m.Called(name, opts)
	return args.Error(0)
}

func (m *mockSandboxInterface) Get(name string, opts metav1.GetOptions) (*types.Sandbox, error) {
	args := m.Called(name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *mockSandboxInterface) List(opts metav1.ListOptions) (*types.SandboxList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxList), args.Error(1)
}

type mockDatabaseService struct {
	mock.Mock
}

func (m *mockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

func (m *mockDatabaseService) CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error {
	args := m.Called(ctx, sandboxID, userID, runtime)
	return args.Error(0)
}

func (m *mockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *mockDatabaseService) GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *mockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

type mockWarmPoolService struct {
	mock.Mock
}

func (m *mockWarmPoolService) AddToWarmPool(ctx context.Context, sandboxID, runtime string) error {
	args := m.Called(ctx, sandboxID, runtime)
	return args.Error(0)
}

func (m *mockWarmPoolService) GetWarmSandbox(ctx context.Context, runtime string) (string, error) {
	args := m.Called(ctx, runtime)
	return args.String(0), args.Error(1)
}

type mockMetricsService struct {
	mock.Mock
}

func (m *mockMetricsService) DecrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *mockMetricsService) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	m.Called(method, path, status, duration, size)
}

func (m *mockMetricsService) RecordSandboxCreation(runtime string, warmPodUsed bool, userID string) {
	m.Called(runtime, warmPodUsed, userID)
}

func (m *mockMetricsService) RecordSandboxTermination(runtime, reason string) {
	m.Called(runtime, reason)
}

type mockLogger struct {
	mock.Mock
}

func (m *mockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *mockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *mockLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *mockLogger) Error(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

func (m *mockLogger) Fatal(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

func (m *mockLogger) With(keysAndValues ...interface{}) interfaces.LoggerInterface {
	args := m.Called(keysAndValues)
	return m
}

func (m *mockLogger) Sync() error {
	args := m.Called()
	return args.Error(0)
}

// Test setup helper
func setupTestService() (*Service, *mockKubernetesClient, *mockLLMSafespaceV1Interface, *mockSandboxInterface, *mockDatabaseService, *mockWarmPoolService, *mockMetricsService, *mockLogger) {
	mockK8s := new(mockKubernetesClient)
	mockLLMSafespaceV1 := new(mockLLMSafespaceV1Interface)
	mockSandbox := new(mockSandboxInterface)
	mockDB := new(mockDatabaseService)
	mockWarmPool := new(mockWarmPoolService)
	mockMetrics := new(mockMetricsService)
	mockLog := new(mockLogger)

	// Setup default expectations
	mockK8s.On("LlmsafespaceV1").Return(mockLLMSafespaceV1)
	mockLLMSafespaceV1.On("Sandboxes", mock.Anything).Return(mockSandbox)
	mockLog.On("Info", mock.Anything, mock.Anything).Return()
	mockLog.On("Warn", mock.Anything, mock.Anything).Return()
	mockLog.On("Error", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	
	// Add required method implementations for the interfaces
	mockK8s.On("Clientset").Return(nil)
	mockK8s.On("DynamicClient").Return(nil)
	mockK8s.On("RESTConfig").Return((*rest.Config)(nil))
	mockK8s.On("InformerFactory").Return(nil)
	
	mockLog.On("With", mock.Anything).Return(mockLog)
	mockLog.On("Sync").Return(nil)
	
	mockDB.On("Start").Return(nil)
	mockDB.On("Stop").Return(nil)
	mockDB.On("GetUserByID", mock.Anything, mock.Anything).Return(map[string]interface{}{}, nil)
	mockDB.On("GetSandboxByID", mock.Anything, mock.Anything).Return(map[string]interface{}{}, nil)
	mockDB.On("GetUserIDByAPIKey", mock.Anything, mock.Anything).Return("", nil)
	mockDB.On("CheckPermission", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	
	mockMetrics.On("Start").Return(nil)
	mockMetrics.On("Stop").Return(nil)
	mockMetrics.On("RecordExecution", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("IncrementActiveConnections", mock.Anything, mock.Anything).Return()
	mockMetrics.On("DecrementActiveConnections", mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordWarmPoolHit").Return()
	mockMetrics.On("RecordError", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordPackageInstallation", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordFileOperation", mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordResourceUsage", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordWarmPoolMetrics", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordWarmPoolScaling", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("UpdateWarmPoolHitRatio", mock.Anything, mock.Anything).Return()
	
	mockWarmPool.On("Start").Return(nil)
	mockWarmPool.On("Stop").Return(nil)
	mockWarmPool.On("AddToWarmPool", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockWarmPool.On("RemoveFromWarmPool", mock.Anything, mock.Anything).Return(nil)
	mockWarmPool.On("GetWarmPoolStatus", mock.Anything, mock.Anything, mock.Anything).Return(map[string]interface{}{}, nil)
	mockWarmPool.On("GetGlobalWarmPoolStatus", mock.Anything).Return(map[string]interface{}{}, nil)
	mockWarmPool.On("CheckAvailability", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	mockWarmPool.On("CreateWarmPool", mock.Anything, mock.Anything).Return((*types.WarmPool)(nil), nil)
	mockWarmPool.On("GetWarmPool", mock.Anything, mock.Anything, mock.Anything).Return((*types.WarmPool)(nil), nil)
	mockWarmPool.On("ListWarmPools", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]map[string]interface{}{}, nil)
	mockWarmPool.On("UpdateWarmPool", mock.Anything, mock.Anything).Return((*types.WarmPool)(nil), nil)
	mockWarmPool.On("DeleteWarmPool", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	service, _ := New(
		mockLog,
		mockK8s,
		mockDB,
		nil, // cache service
		mockMetrics,
		mockWarmPool,
		nil, // file service
		nil, // exec service
		&Config{Namespace: "default"},
	)

	return service, mockK8s, mockLLMSafespaceV1, mockSandbox, mockDB, mockWarmPool, mockMetrics, mockLog
}

// Test cases
func TestCreateSandbox_Success(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, mockWarmPool, mockMetrics, _ := setupTestService()
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
	}

	// Mock expectations
	mockWarmPool.On("GetWarmSandbox", ctx, "python:3.10").Return("", errors.New("no warm pod available"))
	
	createdSandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
		},
	}
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox"), mock.Anything).Return(createdSandbox, nil)
	mockDB.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(nil)
	mockMetrics.On("RecordSandboxCreation", "python:3.10", false, "user123").Return()

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "sb-12345", result.Name)
	mockSandbox.AssertExpectations(t)
	mockDB.AssertExpectations(t)
	mockWarmPool.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

func TestCreateSandbox_WithWarmPod(t *testing.T) {
	// Setup
	service, _, mockLLMSafespaceV1, mockSandbox, mockDB, mockWarmPool, mockMetrics, _ := setupTestService()
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
		UseWarmPool:   true,
	}

	// Mock expectations
	mockWarmPool.On("GetWarmSandbox", ctx, "python:3.10").Return("warm-pod-123", nil)
	
	mockWarmPodInterface := new(mockSandboxInterface)
	mockLLMSafespaceV1.On("WarmPods", "default").Return(mockWarmPodInterface)
	
	warmPod := &types.WarmPod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "warm-pod-123",
			Namespace: "default",
		},
	}
	
	mockWarmPodInterface.On("Get", "warm-pod-123", mock.Anything).Return(warmPod, nil)
	
	createdSandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
		},
		Status: types.SandboxStatus{
			WarmPodRef: &types.WarmPodReference{
				Name:      "warm-pod-123",
				Namespace: "default",
			},
		},
	}
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox"), mock.Anything).Return(createdSandbox, nil)
	mockDB.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(nil)
	mockMetrics.On("RecordSandboxCreation", "python:3.10", true, "user123").Return()

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "sb-12345", result.Name)
	assert.Equal(t, "warm-pod-123", result.Status.WarmPodRef.Name)
	mockSandbox.AssertExpectations(t)
	mockDB.AssertExpectations(t)
	mockWarmPool.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

func TestCreateSandbox_ValidationFailure(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, _, _, _ := setupTestService()
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Runtime:       "",  // Empty runtime should fail validation
		SecurityLevel: "invalid-level",
		Timeout:       9999999,  // Exceeds max timeout
		UserID:        "user123",
	}

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid_request")
	
	// Ensure no Kubernetes or DB calls were made
	mockSandbox.AssertNotCalled(t, "Create")
	mockDB.AssertNotCalled(t, "CreateSandboxMetadata")
}

func TestCreateSandbox_KubernetesFailure(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, mockWarmPool, _, mockLog := setupTestService()
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
	}

	// Mock expectations
	mockWarmPool.On("GetWarmSandbox", ctx, "python:3.10").Return("", errors.New("no warm pod available"))
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox"), mock.Anything).Return(nil, errors.New("kubernetes error"))
	mockLog.On("Error", "Failed to create sandbox in Kubernetes", mock.Anything, "runtime", "python:3.10", "userID", "user123").Return()

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "sandbox_creation_failed")
	
	// Ensure no DB calls were made
	mockDB.AssertNotCalled(t, "CreateSandboxMetadata")
}

func TestCreateSandbox_DatabaseFailure(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, mockWarmPool, _, mockLog := setupTestService()
	ctx := context.Background()

	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
	}

	// Mock expectations
	mockWarmPool.On("GetWarmSandbox", ctx, "python:3.10").Return("", errors.New("no warm pod available"))
	
	createdSandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
		},
	}
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox"), mock.Anything).Return(createdSandbox, nil)
	mockDB.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(errors.New("database error"))
	mockLog.On("Error", "Failed to store sandbox metadata", mock.Anything, "sandboxID", "sb-12345", "userID", "user123").Return()
	
	// Expect cleanup call
	mockSandbox.On("Delete", "sb-12345", mock.Anything).Return(nil)

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "metadata_creation_failed")
	
	// Ensure cleanup was called
	mockSandbox.AssertCalled(t, "Delete", "sb-12345", mock.Anything)
}

func TestGetSandbox_Success(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
		},
	}

	// Mock expectations
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(sandbox, nil)

	// Execute
	result, err := service.GetSandbox(ctx, "sb-12345")

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "sb-12345", result.Name)
	mockSandbox.AssertExpectations(t)
}

func TestGetSandbox_NamespaceFallback(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock expectations - first call fails, list succeeds
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(nil, errors.New("not found"))
	
	sandboxList := &types.SandboxList{
		Items: []types.Sandbox{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sb-12345",
					Namespace: "other-namespace",
				},
				Spec: types.SandboxSpec{
					Runtime:       "python:3.10",
					SecurityLevel: "standard",
				},
			},
		},
	}
	
	mockSandbox.On("List", mock.Anything).Return(sandboxList, nil)

	// Execute
	result, err := service.GetSandbox(ctx, "sb-12345")

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "sb-12345", result.Name)
	assert.Equal(t, "other-namespace", result.Namespace)
	mockSandbox.AssertExpectations(t)
}

func TestGetSandbox_NotFound(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock expectations - both direct get and list return empty
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(nil, errors.New("not found"))
	mockSandbox.On("List", mock.Anything).Return(&types.SandboxList{Items: []types.Sandbox{}}, nil)

	// Execute
	result, err := service.GetSandbox(ctx, "sb-12345")

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.IsType(t, &types.SandboxNotFoundError{}, err)
	mockSandbox.AssertExpectations(t)
}

func TestListSandboxes_Success(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock database response
	dbSandboxes := []map[string]interface{}{
		{
			"id":      "sb-12345",
			"runtime": "python:3.10",
			"created": time.Now(),
		},
		{
			"id":      "sb-67890",
			"runtime": "nodejs:16",
			"created": time.Now(),
		},
	}
	
	mockDB.On("ListSandboxes", ctx, "user123", 10, 0).Return(dbSandboxes, nil)

	// Mock Kubernetes responses
	sandbox1 := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Status: types.SandboxStatus{
			Phase:     "Running",
			StartTime: &metav1.Time{Time: time.Now()},
			Resources: &types.ResourceStatus{
				CPUUsage:    "100m",
				MemoryUsage: "256Mi",
			},
		},
	}
	
	sandbox2 := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-67890",
			Namespace: "default",
		},
		Status: types.SandboxStatus{
			Phase:     "Pending",
			StartTime: &metav1.Time{Time: time.Now()},
		},
	}
	
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(sandbox1, nil)
	mockSandbox.On("Get", "sb-67890", mock.Anything).Return(sandbox2, nil)

	// Execute
	result, err := service.ListSandboxes(ctx, "user123", 10, 0)

	// Assert
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "Running", result[0]["status"])
	assert.Equal(t, "Pending", result[1]["status"])
	assert.Equal(t, "100m", result[0]["cpuUsage"])
	mockDB.AssertExpectations(t)
	mockSandbox.AssertExpectations(t)
}

func TestListSandboxes_DatabaseFailure(t *testing.T) {
	// Setup
	service, _, _, _, mockDB, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock database error
	mockDB.On("ListSandboxes", ctx, "user123", 10, 0).Return([]map[string]interface{}{}, errors.New("database error"))

	// Execute
	result, err := service.ListSandboxes(ctx, "user123", 10, 0)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "sandbox_list_failed")
	mockDB.AssertExpectations(t)
}

func TestTerminateSandbox_Success(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, mockMetrics, _ := setupTestService()
	ctx := context.Background()

	// Mock sandbox retrieval
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
	}
	
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(sandbox, nil)
	mockSandbox.On("Delete", "sb-12345", mock.Anything).Return(nil)
	mockMetrics.On("RecordSandboxTermination", "python:3.10", "user_requested").Return()

	// Execute
	err := service.TerminateSandbox(ctx, "sb-12345")

	// Assert
	assert.NoError(t, err)
	mockSandbox.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

func TestTerminateSandbox_NotFound(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock sandbox not found
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(nil, errors.New("not found"))
	mockSandbox.On("List", mock.Anything).Return(&types.SandboxList{Items: []types.Sandbox{}}, nil)

	// Execute
	err := service.TerminateSandbox(ctx, "sb-12345")

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_not_found")
	mockSandbox.AssertExpectations(t)
}

func TestTerminateSandbox_DeleteFailure(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, mockLog := setupTestService()
	ctx := context.Background()

	// Mock sandbox retrieval success but delete failure
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
	}
	
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(sandbox, nil)
	mockSandbox.On("Delete", "sb-12345", mock.Anything).Return(errors.New("delete error"))
	mockLog.On("Error", "Failed to delete sandbox", mock.Anything, "sandboxID", "sb-12345").Return()

	// Execute
	err := service.TerminateSandbox(ctx, "sb-12345")

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_termination_failed")
	mockSandbox.AssertExpectations(t)
}

func TestGetSandboxStatus_Success(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock sandbox retrieval
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Status: types.SandboxStatus{
			Phase:     "Running",
			StartTime: &metav1.Time{Time: time.Now()},
			Resources: &types.ResourceStatus{
				CPUUsage:    "100m",
				MemoryUsage: "256Mi",
			},
		},
	}
	
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(sandbox, nil)

	// Execute
	status, err := service.GetSandboxStatus(ctx, "sb-12345")

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, status)
	assert.Equal(t, "Running", status.Phase)
	assert.Equal(t, "100m", status.Resources.CPUUsage)
	mockSandbox.AssertExpectations(t)
}

func TestGetSandboxStatus_NotFound(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, _, _, _, _ := setupTestService()
	ctx := context.Background()

	// Mock sandbox not found
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(nil, errors.New("not found"))
	mockSandbox.On("List", mock.Anything).Return(&types.SandboxList{Items: []types.Sandbox{}}, nil)

	// Execute
	status, err := service.GetSandboxStatus(ctx, "sb-12345")

	// Assert
	assert.Error(t, err)
	assert.Nil(t, status)
	assert.Contains(t, err.Error(), "sandbox_not_found")
	mockSandbox.AssertExpectations(t)
}

func TestSandboxLifecycle(t *testing.T) {
	// Setup
	service, _, _, mockSandbox, mockDB, mockWarmPool, mockMetrics, _ := setupTestService()
	ctx := context.Background()

	// Step 1: Create sandbox
	createReq := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       300,
		UserID:        "user123",
	}

	mockWarmPool.On("GetWarmSandbox", ctx, "python:3.10").Return("", errors.New("no warm pod available"))
	
	createdSandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-12345",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
		},
	}
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox"), mock.Anything).Return(createdSandbox, nil)
	mockDB.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(nil)
	mockMetrics.On("RecordSandboxCreation", "python:3.10", false, "user123").Return()

	// Step 2: Get sandbox
	mockSandbox.On("Get", "sb-12345", mock.Anything).Return(createdSandbox, nil)

	// Step 3: Terminate sandbox
	mockSandbox.On("Delete", "sb-12345", mock.Anything).Return(nil)
	mockMetrics.On("RecordSandboxTermination", "python:3.10", "user_requested").Return()

	// Execute lifecycle
	// 1. Create
	sandbox, err := service.CreateSandbox(ctx, createReq)
	assert.NoError(t, err)
	assert.NotNil(t, sandbox)
	assert.Equal(t, "sb-12345", sandbox.Name)

	// 2. Get
	retrieved, err := service.GetSandbox(ctx, "sb-12345")
	assert.NoError(t, err)
	assert.Equal(t, "sb-12345", retrieved.Name)

	// 3. Terminate
	err = service.TerminateSandbox(ctx, "sb-12345")
	assert.NoError(t, err)

	// Verify all expectations
	mockSandbox.AssertExpectations(t)
	mockDB.AssertExpectations(t)
	mockWarmPool.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}
