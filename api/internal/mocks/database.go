package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockDatabaseService implements the DatabaseService interface for testing.
type MockDatabaseService struct {
	mock.Mock
}

var _ interface {
	GetUser(ctx context.Context, userID string) (*types.User, error)
	CreateUser(ctx context.Context, user *types.User) error
	UpdateUser(ctx context.Context, userID string, updates types.UserUpdates) error
	DeleteUser(ctx context.Context, userID string) error
	GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error)
	GetSandbox(ctx context.Context, sandboxID string) (*types.SandboxMetadata, error)
	CreateSandbox(ctx context.Context, sandbox *types.SandboxMetadata) error
	UpdateSandbox(ctx context.Context, sandboxID string, updates types.SandboxUpdates) error
	DeleteSandbox(ctx context.Context, sandboxID string) error
	ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]*types.SandboxMetadata, *types.PaginationMetadata, error)
	CheckPermission(userID, resourceType, resourceID, action string) (bool, error)
	CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error)
	Start() error
	Stop() error
} = (*MockDatabaseService)(nil)

func (m *MockDatabaseService) GetUser(ctx context.Context, userID string) (*types.User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

func (m *MockDatabaseService) CreateUser(ctx context.Context, user *types.User) error {
	return m.Called(ctx, user).Error(0)
}

func (m *MockDatabaseService) UpdateUser(ctx context.Context, userID string, updates types.UserUpdates) error {
	return m.Called(ctx, userID, updates).Error(0)
}

func (m *MockDatabaseService) DeleteUser(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}

func (m *MockDatabaseService) GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error) {
	args := m.Called(ctx, apiKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

func (m *MockDatabaseService) GetSandbox(ctx context.Context, sandboxID string) (*types.SandboxMetadata, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxMetadata), args.Error(1)
}

func (m *MockDatabaseService) CreateSandbox(ctx context.Context, sandbox *types.SandboxMetadata) error {
	return m.Called(ctx, sandbox).Error(0)
}

func (m *MockDatabaseService) UpdateSandbox(ctx context.Context, sandboxID string, updates types.SandboxUpdates) error {
	return m.Called(ctx, sandboxID, updates).Error(0)
}

func (m *MockDatabaseService) DeleteSandbox(ctx context.Context, sandboxID string) error {
	return m.Called(ctx, sandboxID).Error(0)
}

func (m *MockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]*types.SandboxMetadata, *types.PaginationMetadata, error) {
	args := m.Called(ctx, userID, limit, offset)
	var sandboxes []*types.SandboxMetadata
	if args.Get(0) != nil {
		sandboxes = args.Get(0).([]*types.SandboxMetadata)
	}
	var pagination *types.PaginationMetadata
	if args.Get(1) != nil {
		pagination = args.Get(1).(*types.PaginationMetadata)
	}
	return sandboxes, pagination, args.Error(2)
}

func (m *MockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) Start() error { return m.Called().Error(0) }
func (m *MockDatabaseService) Stop() error  { return m.Called().Error(0) }
