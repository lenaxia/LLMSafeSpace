package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockWarmPoolService implements the WarmPoolService interface for testing
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

func (m *MockWarmPoolService) GetWarmPoolStatus(ctx context.Context, name, namespace string) (map[string]interface{}, error) {
	args := m.Called(ctx, name, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockWarmPoolService) GetGlobalWarmPoolStatus(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockWarmPoolService) CheckAvailability(ctx context.Context, runtime, securityLevel string) (bool, error) {
	args := m.Called(ctx, runtime, securityLevel)
	return args.Bool(0), args.Error(1)
}

func (m *MockWarmPoolService) CreateWarmPool(ctx context.Context, req types.CreateWarmPoolRequest) (*types.WarmPool, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolService) GetWarmPool(ctx context.Context, name, namespace string) (*types.WarmPool, error) {
	args := m.Called(ctx, name, namespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolService) ListWarmPools(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockWarmPoolService) UpdateWarmPool(ctx context.Context, req types.UpdateWarmPoolRequest) (*types.WarmPool, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolService) DeleteWarmPool(ctx context.Context, name, namespace string) error {
	args := m.Called(ctx, name, namespace)
	return args.Error(0)
}

func (m *MockWarmPoolService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockWarmPoolService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
