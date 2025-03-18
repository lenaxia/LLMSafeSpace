package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/informers"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/lenaxia/llmsafespace/mocks/kubernetes"
	"github.com/lenaxia/llmsafespace/mocks/logger"
)

// Test setup helper
func setupTestService() (*Service, *kubernetes.MockKubernetesClient, *kubernetes.MockLLMSafespaceV1Interface, *kubernetes.MockSandboxInterface, *interfaces.MockDatabaseService, *interfaces.MockWarmPoolService, *interfaces.MockMetricsService, *logger.MockLogger) {
	mockK8s := new(kubernetes.MockKubernetesClient)
	mockLLMSafespaceV1 := new(kubernetes.MockLLMSafespaceV1Interface)
	mockSandbox := new(kubernetes.MockSandboxInterface)
	mockDB := new(interfaces.MockDatabaseService)
	mockWarmPool := new(interfaces.MockWarmPoolService)
	mockMetrics := new(interfaces.MockMetricsService)
	mockLog := new(logger.MockLogger)

	// Setup default expectations
	mockK8s.On("LlmsafespaceV1").Return(mockLLMSafespaceV1)
	mockLLMSafespaceV1.On("Sandboxes", mock.Anything).Return(mockSandbox)
	mockLog.On("Info", mock.Anything, mock.Anything).Return()
	mockLog.On("Warn", mock.Anything, mock.Anything).Return()
	mockLog.On("Error", mock.Anything, mock.Anything, mock.Anything).Return()
	mockMetrics.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	
	// Add required method implementations for the interfaces
	mockK8s.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8s.On("DynamicClient").Return(nil)
	mockK8s.On("RESTConfig").Return(&rest.Config{})
	mockK8s.On("InformerFactory").Return(nil)
	mockK8s.On("Start").Return(nil)
	mockK8s.On("Stop").Return()
	
	mockLog.On("With", mock.Anything).Return(mockLog)
	mockLog.On("Sync").Return(nil)
	mockLog.On("Debug", mock.Anything, mock.Anything).Return()
	mockLog.On("Fatal", mock.Anything, mock.Anything, mock.Anything).Return()
	
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
	mockWarmPool.On("CreateWarmPool", mock.Anything, mock.Anything).Return(&types.WarmPool{}, nil)
	mockWarmPool.On("GetWarmPool", mock.Anything, mock.Anything, mock.Anything).Return(&types.WarmPool{}, nil)
	mockWarmPool.On("ListWarmPools", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]map[string]interface{}{}, nil)
	mockWarmPool.On("UpdateWarmPool", mock.Anything, mock.Anything).Return(&types.WarmPool{}, nil)
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
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox")).Return(createdSandbox, nil)
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
	
	mockWarmPodInterface := new(kubernetes.MockWarmPodInterface)
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
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox")).Return(createdSandbox, nil)
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
	assert.Contains(t, err.Error(), "validation_error")
	
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
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox")).Return(nil, errors.New("kubernetes error"))
	mockLog.On("Error", "Failed to create sandbox in Kubernetes", mock.Anything, "runtime", "python:3.10", "userID", "user123").Return()

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Failed to create sandbox in Kubernetes")
	
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
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox")).Return(createdSandbox, nil)
	mockDB.On("CreateSandboxMetadata", ctx, "sb-12345", "user123", "python:3.10").Return(errors.New("database error"))
	mockLog.On("Error", "Failed to store sandbox metadata", mock.Anything, "sandboxID", "sb-12345", "userID", "user123").Return()
	
	// Expect cleanup call
	mockSandbox.On("Delete", "sb-12345", mock.Anything).Return(nil)

	// Execute
	result, err := service.CreateSandbox(ctx, req)

	// Assert
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Failed to store sandbox metadata")
	
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
	assert.Contains(t, err.Error(), "not_found")
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
	
	mockSandbox.On("Create", mock.AnythingOfType("*types.Sandbox")).Return(createdSandbox, nil)
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
