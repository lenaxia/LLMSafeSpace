package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockSandboxService implements interfaces.SandboxService for testing.
type MockSandboxService struct {
	mock.Mock
}

var _ interfaces.SandboxService = (*MockSandboxService)(nil)

func (m *MockSandboxService) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.Sandbox, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxService) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxService) ListSandboxes(ctx context.Context, userID string, limit, offset int) (*types.SandboxListResult, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxListResult), args.Error(1)
}

func (m *MockSandboxService) TerminateSandbox(ctx context.Context, sandboxID string) error {
	return m.Called(ctx, sandboxID).Error(0)
}

func (m *MockSandboxService) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxStatus), args.Error(1)
}

func (m *MockSandboxService) Start() error { return m.Called().Error(0) }
func (m *MockSandboxService) Stop() error  { return m.Called().Error(0) }
