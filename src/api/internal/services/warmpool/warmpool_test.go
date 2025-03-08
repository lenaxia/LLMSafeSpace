package warmpool

import (
	"context"
	"errors"
	"testing"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"

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

type MockWarmPoolInterface struct {
	mock.Mock
	kubernetes.WarmPoolInterface
}

func (m *MockK8sClient) LlmsafespaceV1() kubernetes.LLMSafespaceV1Interface {
	args := m.Called()
	return args.Get(0).(kubernetes.LLMSafespaceV1Interface)
}

func (m *MockLLMSafespaceV1Client) WarmPools(namespace string) kubernetes.WarmPoolInterface {
	args := m.Called(namespace)
	return args.Get(0).(kubernetes.WarmPoolInterface)
}


func (m *MockWarmPoolInterface) Create(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) Update(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) UpdateStatus(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

func (m *MockWarmPoolInterface) Get(name string, options metav1.GetOptions) (*llmsafespacev1.WarmPool, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) List(opts metav1.ListOptions) (*llmsafespacev1.WarmPoolList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.WarmPoolList), args.Error(1)
}

func (m *MockWarmPoolInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

type MockDatabaseService struct {
	mock.Mock
	database.Service
}

type MockMetricsService struct {
	mock.Mock
	metrics.Service
}

func setupWarmPoolService(t *testing.T) (*Service, *MockK8sClient) {
	log, _ := logger.New(true, "debug", "console")
	mockK8sClient := new(MockK8sClient)
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPoolInterface := new(MockWarmPoolInterface)
	mockDbService := new(MockDatabaseService)
	mockMetricsService := new(MockMetricsService)

	mockK8sClient.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPoolInterface)

	service := &Service{
		logger:     log,
		k8sClient:  mockK8sClient,
		dbService:  mockDbService,
		metricsSvc: mockMetricsService,
	}

	return service, mockK8s
}

func TestCheckAvailability(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)

	ctx := context.Background()
	runtime := "python:3.10"
	securityLevel := "standard"

	// Test case: Available warm pods
	mockLLMClient.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		selector, err := labels.Parse("runtime=python-3.10,security-level=standard")
		if err != nil {
			return false
		}
		return opts.LabelSelector == selector.String()
	})).Return(&llmsafespacev1.WarmPoolList{
		Items: []llmsafespacev1.WarmPool{
			{
				Status: llmsafespacev1.WarmPoolStatus{
					AvailablePods: 5,
				},
			},
		},
	}, nil).Once()

	available, err := service.CheckAvailability(ctx, runtime, securityLevel)
	assert.NoError(t, err)
	assert.True(t, available)

	// Test case: No available warm pods
	mockLLMClient.On("List", mock.Anything).Return(&llmsafespacev1.WarmPoolList{
		Items: []llmsafespacev1.WarmPool{
			{
				Status: llmsafespacev1.WarmPoolStatus{
					AvailablePods: 0,
				},
			},
		},
	}, nil).Once()

	available, err = service.CheckAvailability(ctx, runtime, securityLevel)
	assert.NoError(t, err)
	assert.False(t, available)

	// Test case: No matching warm pools
	mockLLMClient.On("List", mock.Anything).Return(&llmsafespacev1.WarmPoolList{
		Items: []llmsafespacev1.WarmPool{},
	}, nil).Once()

	available, err = service.CheckAvailability(ctx, runtime, securityLevel)
	assert.NoError(t, err)
	assert.False(t, available)

	// Test case: Error listing warm pools
	mockLLMClient.On("List", mock.Anything).Return(nil, errors.New("kubernetes error")).Once()

	available, err = service.CheckAvailability(ctx, runtime, securityLevel)
	assert.Error(t, err)
	assert.False(t, available)
	assert.Contains(t, err.Error(), "failed to list warm pools")

	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}

func TestCreateWarmPool(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)
	
	// Setup mocks
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPool := new(MockWarmPoolInterface)
	mockK8sClient.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPoolInterface)

	ctx := context.Background()
	req := CreateWarmPoolRequest{
		Name:          "test-pool",
		Runtime:       "python:3.10",
		MinSize:       5,
		MaxSize:       10,
		SecurityLevel: "standard",
		UserID:        "user123",
		Namespace:     "default",
	}

	// Test case: Successful creation
	mockWarmPool.On("Create", mock.MatchedBy(func(warmPool *llmsafespacev1.WarmPool) bool {
		return warmPool.Name == "test-pool" && 
		       warmPool.Spec.Runtime == "python:3.10" && 
		       warmPool.Spec.MinSize == 5
	})).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
	}, nil).Once()

	// Mock the database service method
	service.storeWarmPoolMetadata = func(ctx context.Context, name, namespace, userID, runtime string) error {
		return nil
	}

	result, err := service.CreateWarmPool(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, "test-pool", result.Name)

	// Test case: Creation error
	mockWarmPool.On("Create", mock.Anything).Return(nil, errors.New("kubernetes error")).Once()

	_, err = service.CreateWarmPool(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create warm pool")

	mockWarmPool.AssertExpectations(t)
	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}

func TestGetWarmPool(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)
	
	// Setup mocks
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPool := new(MockWarmPoolInterface)
	mockK8s.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPool)

	ctx := context.Background()
	name := "test-pool"
	namespace := "default"

	// Test case: Successful get
	mockWarmPool.On("Get", name, mock.Anything).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: llmsafespacev1.WarmPoolSpec{
			Runtime: "python:3.10",
			MinSize: 5,
		},
	}, nil).Once()

	result, err := service.GetWarmPool(ctx, name, namespace)
	assert.NoError(t, err)
	assert.Equal(t, name, result.Name)
	assert.Equal(t, "python:3.10", result.Spec.Runtime)
	assert.Equal(t, 5, result.Spec.MinSize)

	// Test case: Get error
	mockWarmPool.On("Get", "nonexistent", mock.Anything).Return(nil, errors.New("not found")).Once()

	_, err = service.GetWarmPool(ctx, "nonexistent", namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get warm pool")

	mockWarmPool.AssertExpectations(t)
	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}

func TestUpdateWarmPool(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)
	
	// Setup mocks
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPool := new(MockWarmPoolInterface)
	mockK8s.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPool)

	ctx := context.Background()
	req := UpdateWarmPoolRequest{
		Name:      "test-pool",
		MinSize:   10,
		MaxSize:   20,
		Namespace: "default",
	}

	// Test case: Successful update
	mockWarmPool.On("Get", "test-pool", mock.Anything).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
		Spec: llmsafespacev1.WarmPoolSpec{
			Runtime: "python:3.10",
			MinSize: 5,
			MaxSize: 10,
		},
	}, nil).Once()

	mockWarmPool.On("Update", mock.MatchedBy(func(warmPool *llmsafespacev1.WarmPool) bool {
		return warmPool.Name == "test-pool" && 
		       warmPool.Spec.MinSize == 10 && 
		       warmPool.Spec.MaxSize == 20
	})).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pool",
		},
		Spec: llmsafespacev1.WarmPoolSpec{
			Runtime: "python:3.10",
			MinSize: 10,
			MaxSize: 20,
		},
	}, nil).Once()

	result, err := service.UpdateWarmPool(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, "test-pool", result.Name)
	assert.Equal(t, 10, result.Spec.MinSize)
	assert.Equal(t, 20, result.Spec.MaxSize)

	// Test case: Get error
	mockWarmPool.On("Get", "nonexistent", mock.Anything).Return(nil, errors.New("not found")).Once()

	req.Name = "nonexistent"
	_, err = service.UpdateWarmPool(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get warm pool")

	// Test case: Update error
	mockWarmPool.On("Get", "update-error", mock.Anything).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "update-error",
		},
	}, nil).Once()

	mockWarmPool.On("Update", mock.Anything).Return(nil, errors.New("update error")).Once()

	req.Name = "update-error"
	_, err = service.UpdateWarmPool(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update warm pool")

	mockWarmPool.AssertExpectations(t)
	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}

func TestDeleteWarmPool(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)
	
	// Setup mocks
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPool := new(MockWarmPoolInterface)
	mockK8s.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPool)

	ctx := context.Background()
	name := "test-pool"
	namespace := "default"

	// Override the getWarmPoolMetadata method for testing
	service.getWarmPoolMetadata = func(ctx context.Context, name string) (map[string]interface{}, error) {
		if name == "test-pool" {
			return map[string]interface{}{
				"name":      name,
				"user_id":   "user123",
				"runtime":   "python:3.10",
				"namespace": namespace,
			}, nil
		} else if name == "nonexistent" {
			return nil, nil
		} else if name == "metadata-error" {
			return nil, errors.New("database error")
		} else if name == "delete-error" {
			return map[string]interface{}{
				"name":      "delete-error",
				"user_id":   "user123",
				"runtime":   "python:3.10",
				"namespace": namespace,
			}, nil
		}
		return nil, errors.New("unexpected name")
	}

	// Override the deleteWarmPoolMetadata method for testing
	service.deleteWarmPoolMetadata = func(ctx context.Context, name string) error {
		return nil
	}

	// Test case: Successful delete
	mockWarmPool.On("Delete", name, mock.Anything).Return(nil).Once()

	err := service.DeleteWarmPool(ctx, name, namespace)
	assert.NoError(t, err)

	// Test case: Metadata not found
	err = service.DeleteWarmPool(ctx, "nonexistent", namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "warm pool not found")

	// Test case: Metadata error
	err = service.DeleteWarmPool(ctx, "metadata-error", namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get warm pool metadata")

	// Test case: Delete error
	mockWarmPool.On("Delete", "delete-error", mock.Anything).Return(errors.New("kubernetes error")).Once()

	err = service.DeleteWarmPool(ctx, "delete-error", namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete warm pool")

	mockWarmPool.AssertExpectations(t)
	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}

func TestGetWarmPoolStatus(t *testing.T) {
	service, mockK8s := setupWarmPoolService(t)
	
	// Setup mocks
	mockLLMClient := new(MockLLMSafespaceV1Client)
	mockWarmPool := new(MockWarmPoolInterface)
	mockK8s.On("LlmsafespaceV1").Return(mockLLMClient)
	mockLLMClient.On("WarmPools", "default").Return(mockWarmPool)

	ctx := context.Background()
	name := "test-pool"
	namespace := "default"

	// Test case: Successful get status
	mockWarmPool.On("Get", name, mock.Anything).Return(&llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: llmsafespacev1.WarmPoolStatus{
			AvailablePods: 5,
			AssignedPods:  2,
			PendingPods:   1,
		},
	}, nil).Once()

	status, err := service.GetWarmPoolStatus(ctx, name, namespace)
	assert.NoError(t, err)
	assert.Equal(t, 5, status.AvailablePods)
	assert.Equal(t, 2, status.AssignedPods)
	assert.Equal(t, 1, status.PendingPods)

	// Test case: Get error
	mockWarmPool.On("Get", "nonexistent", mock.Anything).Return(nil, errors.New("not found")).Once()

	_, err = service.GetWarmPoolStatus(ctx, "nonexistent", namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get warm pool")

	mockWarmPool.AssertExpectations(t)
	mockLLMClient.AssertExpectations(t)
	mockK8s.AssertExpectations(t)
}
