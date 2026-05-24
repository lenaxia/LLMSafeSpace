package mocks

import (
	"context"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockDatabaseService implements the DatabaseService interface for testing.
type MockDatabaseService struct {
	mock.Mock
}

// Compile-time check against the real interface — not an anonymous copy.
var _ interfaces.DatabaseService = (*MockDatabaseService)(nil)

func (m *MockDatabaseService) GetUser(ctx context.Context, userID string) (*types.User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

func (m *MockDatabaseService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.User), args.Error(1)
}

func (m *MockDatabaseService) CreateUser(ctx context.Context, user *types.User) error {
	return m.Called(ctx, user).Error(0)
}

func (m *MockDatabaseService) CountUsers(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
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

func (m *MockDatabaseService) CreateAPIKey(ctx context.Context, apiKey *types.APIKey) error {
	return m.Called(ctx, apiKey).Error(0)
}

func (m *MockDatabaseService) ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.APIKey), args.Error(1)
}

func (m *MockDatabaseService) GetAPIKey(ctx context.Context, userID, keyID string) (*types.APIKey, error) {
	args := m.Called(ctx, userID, keyID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.APIKey), args.Error(1)
}

func (m *MockDatabaseService) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	return m.Called(ctx, userID, keyID).Error(0)
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

func (m *MockDatabaseService) GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	args := m.Called(ctx, workspaceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WorkspaceMetadata), args.Error(1)
}

func (m *MockDatabaseService) CreateWorkspace(ctx context.Context, workspace *types.WorkspaceMetadata) error {
	return m.Called(ctx, workspace).Error(0)
}

func (m *MockDatabaseService) UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error {
	return m.Called(ctx, workspaceID, updates).Error(0)
}

func (m *MockDatabaseService) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	return m.Called(ctx, workspaceID).Error(0)
}

func (m *MockDatabaseService) ListWorkspaces(ctx context.Context, userID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	args := m.Called(ctx, userID, limit, offset)
	var workspaces []*types.WorkspaceMetadata
	if args.Get(0) != nil {
		workspaces = args.Get(0).([]*types.WorkspaceMetadata)
	}
	var pagination *types.PaginationMetadata
	if args.Get(1) != nil {
		pagination = args.Get(1).(*types.PaginationMetadata)
	}
	return workspaces, pagination, args.Error(2)
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

func (m *MockDatabaseService) Ping(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDatabaseService) ListSessionIndex(ctx context.Context, workspaceID string) ([]types.SessionListItem, error) {
	args := m.Called(ctx, workspaceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]types.SessionListItem), args.Error(1)
}

func (m *MockDatabaseService) DeleteSessionIndex(ctx context.Context, workspaceID string) error {
	return m.Called(ctx, workspaceID).Error(0)
}

func (m *MockDatabaseService) UpsertSessionMessage(ctx context.Context, workspaceID, sessionID string, at time.Time) error {
	return m.Called(ctx, workspaceID, sessionID, at).Error(0)
}

func (m *MockDatabaseService) UpsertSessionTitle(ctx context.Context, workspaceID, sessionID, title string) error {
	return m.Called(ctx, workspaceID, sessionID, title).Error(0)
}
