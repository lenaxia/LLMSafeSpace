package warmpool

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type WarmPoolTestSuite struct {
	suite.Suite
	ctx        context.Context
	k8sMock    *kmocks.MockKubernetesClient
	llmV1Mock  *kmocks.MockLLMSafespaceV1Interface
	wpMock     *kmocks.MockWarmPoolInterface
	cacheMock  *mocks.MockCacheService
	dbMock     *mocks.MockDatabaseService
	loggerMock *lmocks.MockLogger
	service    interfaces.WarmPoolService
}

func (s *WarmPoolTestSuite) SetupTest() {
	s.ctx = context.Background()
	s.k8sMock = kmocks.NewMockKubernetesClient()
	s.llmV1Mock = kmocks.NewMockLLMSafespaceV1Interface()
	s.wpMock = kmocks.NewMockWarmPoolInterface()
	s.cacheMock = &mocks.MockCacheService{}
	s.dbMock = &mocks.MockDatabaseService{}
	s.loggerMock = lmocks.NewMockLogger()

	// Setup logger mock to handle any log calls
	s.loggerMock.On("With", mock.Anything).Return(s.loggerMock)
	s.loggerMock.On("Info", mock.Anything, mock.Anything).Return()
	s.loggerMock.On("Error", mock.Anything, mock.Anything, mock.Anything).Return()
	s.loggerMock.On("Debug", mock.Anything, mock.Anything).Return()
	s.loggerMock.On("Warn", mock.Anything, mock.Anything).Return()

	// Setup Kubernetes client mock
	s.llmV1Mock.On("WarmPools", mock.Anything).Return(s.wpMock)
	s.k8sMock.On("LlmsafespaceV1").Return(s.llmV1Mock)

	s.service = NewService(
		s.loggerMock,
		s.k8sMock,
		s.cacheMock,
		s.dbMock,
	)
}

func TestWarmPoolSuite(t *testing.T) {
	suite.Run(t, new(WarmPoolTestSuite))
}

// CheckAvailability Tests

func (s *WarmPoolTestSuite) TestCheckAvailability_CacheHitTrue() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache hit with "true"
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("true", nil)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err)
	s.True(available)
	s.cacheMock.AssertExpectations(s.T())
	// Kubernetes client should not be called
	s.wpMock.AssertNotCalled(s.T(), "List", mock.Anything)
}

func (s *WarmPoolTestSuite) TestCheckAvailability_CacheHitFalse() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache hit with "false"
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("false", nil)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err)
	s.False(available)
	s.cacheMock.AssertExpectations(s.T())
	// Kubernetes client should not be called
	s.wpMock.AssertNotCalled(s.T(), "List", mock.Anything)
}

func (s *WarmPoolTestSuite) TestCheckAvailability_CacheMissAvailable() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache miss
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("", nil)
	
	// Mock Kubernetes list with available pods
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: types.WarmPoolSpec{
					Runtime: runtime,
					MinSize: 5,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 3, // Available pods > 0
					AssignedPods: 2,
				},
			},
		},
	}
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector != ""
	})).Return(warmPoolList, nil)
	
	// Mock cache set
	s.cacheMock.On("Set", s.ctx, cacheKey, "true", defaultCacheTTL).Return(nil)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err)
	s.True(available)
	s.cacheMock.AssertExpectations(s.T())
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestCheckAvailability_CacheMissUnavailable() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache miss
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("", nil)
	
	// Mock Kubernetes list with no available pods
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: types.WarmPoolSpec{
					Runtime: runtime,
					MinSize: 5,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 0, // No available pods
					AssignedPods: 5,
				},
			},
		},
	}
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector != ""
	})).Return(warmPoolList, nil)
	
	// Mock cache set
	s.cacheMock.On("Set", s.ctx, cacheKey, "false", defaultCacheTTL).Return(nil)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err)
	s.False(available)
	s.cacheMock.AssertExpectations(s.T())
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestCheckAvailability_K8sListError() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache miss
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("", nil)
	
	// Mock Kubernetes list error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector != ""
	})).Return(nil, k8sErr)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.Error(err)
	s.False(available)
	s.Contains(err.Error(), "failed to list warm pools")
	s.cacheMock.AssertExpectations(s.T())
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestCheckAvailability_CacheSetError() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache miss
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("", nil)
	
	// Mock Kubernetes list with available pods
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: types.WarmPoolSpec{
					Runtime: runtime,
					MinSize: 5,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 3, // Available pods > 0
					AssignedPods: 2,
				},
			},
		},
	}
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector != ""
	})).Return(warmPoolList, nil)
	
	// Mock cache set error
	cacheErr := errors.New("cache error")
	s.cacheMock.On("Set", s.ctx, cacheKey, "true", defaultCacheTTL).Return(cacheErr)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err) // Should not return error even if cache set fails
	s.True(available)
	s.cacheMock.AssertExpectations(s.T())
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestCheckAvailability_PartialAvailability() {
	// Setup
	runtime := "python:3.10"
	securityLevel := "standard"
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	
	// Mock cache miss
	s.cacheMock.On("Get", s.ctx, cacheKey).Return("", nil)
	
	// Mock Kubernetes list with mixed availability
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool-1",
				},
				Spec: types.WarmPoolSpec{
					Runtime: runtime,
					MinSize: 5,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 0, // No available pods
					AssignedPods: 5,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool-2",
				},
				Spec: types.WarmPoolSpec{
					Runtime: runtime,
					MinSize: 3,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 2, // Available pods > 0
					AssignedPods: 1,
				},
			},
		},
	}
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector != ""
	})).Return(warmPoolList, nil)
	
	// Mock cache set
	s.cacheMock.On("Set", s.ctx, cacheKey, "true", defaultCacheTTL).Return(nil)
	
	// Execute
	available, err := s.service.CheckAvailability(s.ctx, runtime, securityLevel)
	
	// Assert
	s.NoError(err)
	s.True(available) // Should be true if any pool has available pods
	s.cacheMock.AssertExpectations(s.T())
	s.wpMock.AssertExpectations(s.T())
}

// CreateWarmPool Tests

func (s *WarmPoolTestSuite) TestCreateWarmPool_Success() {
	// Setup
	req := types.CreateWarmPoolRequest{
		Name:          "test-pool",
		Runtime:       "python:3.10",
		MinSize:       3,
		MaxSize:       10,
		SecurityLevel: "standard",
		Namespace:     "default",
		UserID:        "user-123",
	}
	
	// Mock Kubernetes create
	s.wpMock.On("Create", mock.MatchedBy(func(wp *types.WarmPool) bool {
		return wp.Name == req.Name && 
			   wp.Spec.Runtime == req.Runtime && 
			   wp.Spec.MinSize == req.MinSize
	})).Return(kmocks.NewMockWarmPool(req.Name, req.Namespace), nil)
	
	// Execute
	result, err := s.service.CreateWarmPool(s.ctx, req)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.Equal(req.Name, result.Name)
	s.Equal(req.Runtime, result.Spec.Runtime)
	s.Equal(req.MinSize, result.Spec.MinSize)
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestCreateWarmPool_ValidationError() {
	// Setup - missing required field (runtime)
	req := types.CreateWarmPoolRequest{
		Name:          "test-pool",
		Runtime:       "", // Missing required field
		MinSize:       3,
		MaxSize:       10,
		SecurityLevel: "standard",
		Namespace:     "default",
		UserID:        "user-123",
	}
	
	// Execute
	result, err := s.service.CreateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "invalid request")
	s.Contains(err.Error(), "runtime is required")
	s.wpMock.AssertNotCalled(s.T(), "Create", mock.Anything)
}

func (s *WarmPoolTestSuite) TestCreateWarmPool_InvalidSizes() {
	// Setup - minSize > maxSize
	req := types.CreateWarmPoolRequest{
		Name:          "test-pool",
		Runtime:       "python:3.10",
		MinSize:       10,
		MaxSize:       5, // MinSize > MaxSize
		SecurityLevel: "standard",
		Namespace:     "default",
		UserID:        "user-123",
	}
	
	// Execute
	result, err := s.service.CreateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "invalid request")
	s.Contains(err.Error(), "minSize cannot be greater than maxSize")
	s.wpMock.AssertNotCalled(s.T(), "Create", mock.Anything)
}

func (s *WarmPoolTestSuite) TestCreateWarmPool_K8sCreateError() {
	// Setup
	req := types.CreateWarmPoolRequest{
		Name:          "test-pool",
		Runtime:       "python:3.10",
		MinSize:       3,
		MaxSize:       10,
		SecurityLevel: "standard",
		Namespace:     "default",
		UserID:        "user-123",
	}
	
	// Mock Kubernetes create error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("Create", mock.Anything).Return(nil, k8sErr)
	
	// Execute
	result, err := s.service.CreateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "failed to create warm pool")
	s.wpMock.AssertExpectations(s.T())
}

// GetWarmPoolStatus Tests

func (s *WarmPoolTestSuite) TestGetWarmPoolStatus_Success() {
	// Setup
	name := "test-pool"
	namespace := "default"
	
	// Create a mock warm pool with conditions
	now := metav1.Now()
	warmPool := kmocks.NewMockWarmPool(name, namespace)
	warmPool.Status.AvailablePods = 3
	warmPool.Status.AssignedPods = 2
	warmPool.Status.PendingPods = 1
	warmPool.Status.LastScaleTime = &now
	warmPool.Status.Conditions = []types.WarmPoolCondition{
		{
			Type:               "Ready",
			Status:             "True",
			Reason:             "PoolReady",
			Message:            "Warm pool is ready",
			LastTransitionTime: now,
		},
	}
	
	// Mock Kubernetes get
	s.wpMock.On("Get", name, mock.Anything).Return(warmPool, nil)
	
	// Execute
	status, err := s.service.GetWarmPoolStatus(s.ctx, name, namespace)
	
	// Assert
	s.NoError(err)
	s.NotNil(status)
	s.Equal(name, status["name"])
	s.Equal(namespace, status["namespace"])
	s.Equal(3, status["availablePods"])
	s.Equal(2, status["assignedPods"])
	s.Equal(1, status["pendingPods"])
	s.NotNil(status["lastScaleTime"])
	
	// Check conditions
	conditions, ok := status["conditions"].([]map[string]interface{})
	s.True(ok)
	s.Len(conditions, 1)
	s.Equal("Ready", conditions[0]["type"])
	s.Equal("True", conditions[0]["status"])
	s.Equal("PoolReady", conditions[0]["reason"])
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestGetWarmPoolStatus_NotFound() {
	// Setup
	name := "nonexistent-pool"
	namespace := "default"
	
	// Mock Kubernetes get not found error
	notFoundErr := errors.NewNotFound(schema.GroupResource{Group: "llmsafespace.dev", Resource: "warmpools"}, name)
	s.wpMock.On("Get", name, mock.Anything).Return(nil, notFoundErr)
	
	// Execute
	status, err := s.service.GetWarmPoolStatus(s.ctx, name, namespace)
	
	// Assert
	s.Error(err)
	s.Nil(status)
	s.Contains(err.Error(), "not found")
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestGetWarmPoolStatus_K8sGetError() {
	// Setup
	name := "test-pool"
	namespace := "default"
	
	// Mock Kubernetes get error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("Get", name, mock.Anything).Return(nil, k8sErr)
	
	// Execute
	status, err := s.service.GetWarmPoolStatus(s.ctx, name, namespace)
	
	// Assert
	s.Error(err)
	s.Nil(status)
	s.Contains(err.Error(), "failed to get warm pool")
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestGetWarmPoolStatus_NoConditions() {
	// Setup
	name := "test-pool"
	namespace := "default"
	
	// Create a mock warm pool without conditions
	warmPool := kmocks.NewMockWarmPool(name, namespace)
	warmPool.Status.AvailablePods = 3
	warmPool.Status.AssignedPods = 2
	warmPool.Status.PendingPods = 1
	warmPool.Status.Conditions = []types.WarmPoolCondition{} // Empty conditions
	
	// Mock Kubernetes get
	s.wpMock.On("Get", name, mock.Anything).Return(warmPool, nil)
	
	// Execute
	status, err := s.service.GetWarmPoolStatus(s.ctx, name, namespace)
	
	// Assert
	s.NoError(err)
	s.NotNil(status)
	
	// Check conditions
	conditions, ok := status["conditions"].([]map[string]interface{})
	s.True(ok)
	s.Len(conditions, 0) // Should be empty but not nil
	s.wpMock.AssertExpectations(s.T())
}

// GetGlobalWarmPoolStatus Tests

func (s *WarmPoolTestSuite) TestGetGlobalWarmPoolStatus_Success() {
	// Setup
	// Create mock warm pools with different runtimes
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "python-pool",
				},
				Spec: types.WarmPoolSpec{
					Runtime: "python:3.10",
					MinSize: 5,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 3,
					AssignedPods: 2,
					PendingPods: 0,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-pool",
				},
				Spec: types.WarmPoolSpec{
					Runtime: "node:16",
					MinSize: 3,
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 1,
					AssignedPods: 2,
					PendingPods: 1,
				},
			},
		},
	}
	
	// Mock Kubernetes list
	s.wpMock.On("List", mock.Anything).Return(warmPoolList, nil)
	
	// Execute
	status, err := s.service.GetGlobalWarmPoolStatus(s.ctx)
	
	// Assert
	s.NoError(err)
	s.NotNil(status)
	s.Equal(2, status["totalPools"])
	s.Equal(4, status["totalAvailable"]) // 3 + 1
	s.Equal(4, status["totalAssigned"]) // 2 + 2
	s.Equal(1, status["totalPending"]) // 0 + 1
	
	// Check runtime stats
	runtimeStats, ok := status["runtimeStats"].(map[string]map[string]int)
	s.True(ok)
	s.Len(runtimeStats, 2)
	
	// Check Python stats
	pythonStats, ok := runtimeStats["python:3.10"]
	s.True(ok)
	s.Equal(3, pythonStats["available"])
	s.Equal(2, pythonStats["assigned"])
	s.Equal(0, pythonStats["pending"])
	s.Equal(5, pythonStats["total"])
	
	// Check Node stats
	nodeStats, ok := runtimeStats["node:16"]
	s.True(ok)
	s.Equal(1, nodeStats["available"])
	s.Equal(2, nodeStats["assigned"])
	s.Equal(1, nodeStats["pending"])
	s.Equal(4, nodeStats["total"])
	
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestGetGlobalWarmPoolStatus_Empty() {
	// Setup
	// Create empty warm pool list
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{},
	}
	
	// Mock Kubernetes list
	s.wpMock.On("List", mock.Anything).Return(warmPoolList, nil)
	
	// Execute
	status, err := s.service.GetGlobalWarmPoolStatus(s.ctx)
	
	// Assert
	s.NoError(err)
	s.NotNil(status)
	s.Equal(0, status["totalPools"])
	s.Equal(0, status["totalAvailable"])
	s.Equal(0, status["totalAssigned"])
	s.Equal(0, status["totalPending"])
	
	// Check runtime stats
	runtimeStats, ok := status["runtimeStats"].(map[string]map[string]int)
	s.True(ok)
	s.Len(runtimeStats, 0) // Should be empty
	
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestGetGlobalWarmPoolStatus_K8sListError() {
	// Setup
	// Mock Kubernetes list error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("List", mock.Anything).Return(nil, k8sErr)
	
	// Execute
	status, err := s.service.GetGlobalWarmPoolStatus(s.ctx)
	
	// Assert
	s.Error(err)
	s.Nil(status)
	s.Contains(err.Error(), "failed to list warm pools")
	s.wpMock.AssertExpectations(s.T())
}

// UpdateWarmPool Tests

func (s *WarmPoolTestSuite) TestUpdateWarmPool_Success() {
	// Setup
	req := types.UpdateWarmPoolRequest{
		Name:      "test-pool",
		Namespace: "default",
		MinSize:   5,
		MaxSize:   10,
		UserID:    "user-123",
	}
	
	// Create a mock warm pool
	warmPool := kmocks.NewMockWarmPool(req.Name, req.Namespace)
	warmPool.Spec.MinSize = 3 // Original value
	warmPool.Spec.MaxSize = 8 // Original value
	
	// Mock Kubernetes get
	s.wpMock.On("Get", req.Name, mock.Anything).Return(warmPool, nil)
	
	// Mock ownership check
	s.dbMock.On("CheckResourceOwnership", req.UserID, "warmpool", req.Name).Return(true, nil)
	
	// Mock Kubernetes update
	s.wpMock.On("Update", mock.MatchedBy(func(wp *types.WarmPool) bool {
		return wp.Name == req.Name && 
			   wp.Spec.MinSize == req.MinSize && // Updated value
			   wp.Spec.MaxSize == req.MaxSize    // Updated value
	})).Return(warmPool, nil)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_ValidationError() {
	// Setup - minSize > maxSize
	req := types.UpdateWarmPoolRequest{
		Name:      "test-pool",
		Namespace: "default",
		MinSize:   10,
		MaxSize:   5, // MinSize > MaxSize
		UserID:    "user-123",
	}
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "invalid request")
	s.Contains(err.Error(), "minSize cannot be greater than maxSize")
	s.wpMock.AssertNotCalled(s.T(), "Get", mock.Anything, mock.Anything)
	s.dbMock.AssertNotCalled(s.T(), "CheckResourceOwnership", mock.Anything, mock.Anything, mock.Anything)
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_NotFound() {
	// Setup
	req := types.UpdateWarmPoolRequest{
		Name:      "nonexistent-pool",
		Namespace: "default",
		MinSize:   5,
		MaxSize:   10,
		UserID:    "user-123",
	}
	
	// Mock Kubernetes get not found error
	notFoundErr := errors.NewNotFound(schema.GroupResource{Group: "llmsafespace.dev", Resource: "warmpools"}, req.Name)
	s.wpMock.On("Get", req.Name, mock.Anything).Return(nil, notFoundErr)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "not found")
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertNotCalled(s.T(), "CheckResourceOwnership", mock.Anything, mock.Anything, mock.Anything)
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_Unauthorized() {
	// Setup
	req := types.UpdateWarmPoolRequest{
		Name:      "test-pool",
		Namespace: "default",
		MinSize:   5,
		MaxSize:   10,
		UserID:    "user-123",
	}
	
	// Create a mock warm pool
	warmPool := kmocks.NewMockWarmPool(req.Name, req.Namespace)
	
	// Mock Kubernetes get
	s.wpMock.On("Get", req.Name, mock.Anything).Return(warmPool, nil)
	
	// Mock ownership check - unauthorized
	s.dbMock.On("CheckResourceOwnership", req.UserID, "warmpool", req.Name).Return(false, nil)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "does not own warm pool")
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertExpectations(s.T())
	s.wpMock.AssertNotCalled(s.T(), "Update", mock.Anything)
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_OwnershipCheckError() {
	// Setup
	req := types.UpdateWarmPoolRequest{
		Name:      "test-pool",
		Namespace: "default",
		MinSize:   5,
		MaxSize:   10,
		UserID:    "user-123",
	}
	
	// Create a mock warm pool
	warmPool := kmocks.NewMockWarmPool(req.Name, req.Namespace)
	
	// Mock Kubernetes get
	s.wpMock.On("Get", req.Name, mock.Anything).Return(warmPool, nil)
	
	// Mock ownership check error
	dbErr := errors.New("database error")
	s.dbMock.On("CheckResourceOwnership", req.UserID, "warmpool", req.Name).Return(false, dbErr)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "failed to check resource ownership")
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertExpectations(s.T())
	s.wpMock.AssertNotCalled(s.T(), "Update", mock.Anything)
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_K8sUpdateError() {
	// Setup
	req := types.UpdateWarmPoolRequest{
		Name:      "test-pool",
		Namespace: "default",
		MinSize:   5,
		MaxSize:   10,
		UserID:    "user-123",
	}
	
	// Create a mock warm pool
	warmPool := kmocks.NewMockWarmPool(req.Name, req.Namespace)
	
	// Mock Kubernetes get
	s.wpMock.On("Get", req.Name, mock.Anything).Return(warmPool, nil)
	
	// Mock ownership check
	s.dbMock.On("CheckResourceOwnership", req.UserID, "warmpool", req.Name).Return(true, nil)
	
	// Mock Kubernetes update error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("Update", mock.Anything).Return(nil, k8sErr)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "failed to update warm pool")
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestUpdateWarmPool_AutoScalingUpdate() {
	// Setup
	autoScaling := &types.AutoScalingConfig{
		Enabled:           true,
		TargetUtilization: 75,
		ScaleDownDelay:    300,
	}
	req := types.UpdateWarmPoolRequest{
		Name:        "test-pool",
		Namespace:   "default",
		AutoScaling: autoScaling,
		UserID:      "user-123",
	}
	
	// Create a mock warm pool
	warmPool := kmocks.NewMockWarmPool(req.Name, req.Namespace)
	warmPool.Spec.AutoScaling = nil // Original value
	
	// Mock Kubernetes get
	s.wpMock.On("Get", req.Name, mock.Anything).Return(warmPool, nil)
	
	// Mock ownership check
	s.dbMock.On("CheckResourceOwnership", req.UserID, "warmpool", req.Name).Return(true, nil)
	
	// Mock Kubernetes update
	s.wpMock.On("Update", mock.MatchedBy(func(wp *types.WarmPool) bool {
		return wp.Name == req.Name && 
			   wp.Spec.AutoScaling != nil &&
			   wp.Spec.AutoScaling.Enabled == autoScaling.Enabled &&
			   wp.Spec.AutoScaling.TargetUtilization == autoScaling.TargetUtilization
	})).Return(warmPool, nil)
	
	// Execute
	result, err := s.service.UpdateWarmPool(s.ctx, req)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.wpMock.AssertExpectations(s.T())
	s.dbMock.AssertExpectations(s.T())
}

// DeleteWarmPool Tests

func (s *WarmPoolTestSuite) TestDeleteWarmPool_Success() {
	// Setup
	name := "test-pool"
	namespace := "default"
	
	// Mock Kubernetes delete
	s.wpMock.On("Delete", name, mock.Anything).Return(nil)
	
	// Execute
	err := s.service.DeleteWarmPool(s.ctx, name, namespace)
	
	// Assert
	s.NoError(err)
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestDeleteWarmPool_K8sDeleteError() {
	// Setup
	name := "test-pool"
	namespace := "default"
	
	// Mock Kubernetes delete error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("Delete", name, mock.Anything).Return(k8sErr)
	
	// Execute
	err := s.service.DeleteWarmPool(s.ctx, name, namespace)
	
	// Assert
	s.Error(err)
	s.Equal(k8sErr, err) // Should return the original error
	s.wpMock.AssertExpectations(s.T())
}

// ListWarmPools Tests

func (s *WarmPoolTestSuite) TestListWarmPools_Success() {
	// Setup
	userID := "user-123"
	limit := 10
	offset := 0
	
	// Create mock warm pools
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool-1",
					Namespace: "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
				},
				Spec: types.WarmPoolSpec{
					Runtime:       "python:3.10",
					MinSize:       3,
					MaxSize:       10,
					SecurityLevel: "standard",
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 2,
					AssignedPods:  1,
					PendingPods:   0,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool-2",
					Namespace: "default",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
				},
				Spec: types.WarmPoolSpec{
					Runtime:       "node:16",
					MinSize:       2,
					MaxSize:       5,
					SecurityLevel: "high",
				},
				Status: types.WarmPoolStatus{
					AvailablePods: 1,
					AssignedPods:  1,
					PendingPods:   0,
				},
			},
		},
	}
	
	// Mock Kubernetes list
	s.wpMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector == "user-id=user-123"
	})).Return(warmPoolList, nil)
	
	// Execute
	result, err := s.service.ListWarmPools(s.ctx, userID, limit, offset)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.Len(result, 2)
	
	// Check first pool
	s.Equal("test-pool-1", result[0]["name"])
	s.Equal("default", result[0]["namespace"])
	s.Equal("python:3.10", result[0]["runtime"])
	s.Equal(3, result[0]["minSize"])
	s.Equal(10, result[0]["maxSize"])
	s.Equal("standard", result[0]["securityLevel"])
	s.Equal(2, result[0]["availablePods"])
	s.Equal(1, result[0]["assignedPods"])
	s.Equal(0, result[0]["pendingPods"])
	
	// Check second pool
	s.Equal("test-pool-2", result[1]["name"])
	s.Equal("node:16", result[1]["runtime"])
	s.Equal("high", result[1]["securityLevel"])
	
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestListWarmPools_Empty() {
	// Setup
	userID := "user-123"
	limit := 10
	offset := 0
	
	// Create empty warm pool list
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{},
	}
	
	// Mock Kubernetes list
	s.wpMock.On("List", mock.Anything).Return(warmPoolList, nil)
	
	// Execute
	result, err := s.service.ListWarmPools(s.ctx, userID, limit, offset)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.Len(result, 0) // Should be empty
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestListWarmPools_K8sListError() {
	// Setup
	userID := "user-123"
	limit := 10
	offset := 0
	
	// Mock Kubernetes list error
	k8sErr := errors.New("kubernetes error")
	s.wpMock.On("List", mock.Anything).Return(nil, k8sErr)
	
	// Execute
	result, err := s.service.ListWarmPools(s.ctx, userID, limit, offset)
	
	// Assert
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "failed to list warm pools")
	s.wpMock.AssertExpectations(s.T())
}

func (s *WarmPoolTestSuite) TestListWarmPools_LimitOffset() {
	// Setup
	userID := "user-123"
	limit := 1
	offset := 1
	
	// Create mock warm pools
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool-1",
					Namespace: "default",
				},
				Spec: types.WarmPoolSpec{
					Runtime: "python:3.10",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool-2",
					Namespace: "default",
				},
				Spec: types.WarmPoolSpec{
					Runtime: "node:16",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pool-3",
					Namespace: "default",
				},
				Spec: types.WarmPoolSpec{
					Runtime: "go:1.18",
				},
			},
		},
	}
	
	// Mock Kubernetes list
	s.wpMock.On("List", mock.Anything).Return(warmPoolList, nil)
	
	// Execute
	result, err := s.service.ListWarmPools(s.ctx, userID, limit, offset)
	
	// Assert
	s.NoError(err)
	s.NotNil(result)
	s.Len(result, 1) // Should only return one item
	s.Equal("test-pool-2", result[0]["name"]) // Should be the second item
	s.Equal("node:16", result[0]["runtime"])
	s.wpMock.AssertExpectations(s.T())
}

// Service Lifecycle Tests

func (s *WarmPoolTestSuite) TestStart() {
	// Execute
	err := s.service.Start()
	
	// Assert
	s.NoError(err)
}

func (s *WarmPoolTestSuite) TestStop() {
	// Execute
	err := s.service.Stop()
	
	// Assert
	s.NoError(err)
}
