package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockDatabaseService implements the DatabaseService interface for testing
type MockDatabaseService struct {
	mock.Mock
}

// User operations
func (m *MockDatabaseService) GetUser(ctx context.Context, userID string) (*types.User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

func (m *MockDatabaseService) CreateUser(ctx context.Context, user *types.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockDatabaseService) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) error {
	args := m.Called(ctx, userID, updates)
	return args.Error(0)
}

func (m *MockDatabaseService) DeleteUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockDatabaseService) GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error) {
	args := m.Called(ctx, apiKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

// Sandbox operations
func (m *MockDatabaseService) GetSandbox(ctx context.Context, sandboxID string) (*types.SandboxMetadata, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxMetadata), args.Error(1)
}

func (m *MockDatabaseService) CreateSandbox(ctx context.Context, sandbox *types.SandboxMetadata) error {
	args := m.Called(ctx, sandbox)
	return args.Error(0)
}

func (m *MockDatabaseService) UpdateSandbox(ctx context.Context, sandboxID string, updates map[string]interface{}) error {
	args := m.Called(ctx, sandboxID, updates)
	return args.Error(0)
}

func (m *MockDatabaseService) DeleteSandbox(ctx context.Context, sandboxID string) error {
	args := m.Called(ctx, sandboxID)
	return args.Error(0)
}

func (m *MockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]*types.SandboxMetadata, *types.PaginationMetadata, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, nil, args.Error(2)
	}
	return args.Get(0).([]*types.SandboxMetadata), args.Get(1).(*types.PaginationMetadata), args.Error(2)
}

// Permission operations
func (m *MockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

// Service lifecycle
func (m *MockDatabaseService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
