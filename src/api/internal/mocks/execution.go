package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/stretchr/testify/mock"
)

// MockExecutionService implements the ExecutionService interface for testing
type MockExecutionService struct {
	mock.Mock
}

func (m *MockExecutionService) Execute(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int) (*types.ExecutionResult, error) {
	args := m.Called(ctx, sandbox, execType, content, timeout)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockExecutionService) ExecuteStream(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int, outputCallback func(stream, content string)) (*types.ExecutionResult, error) {
	args := m.Called(ctx, sandbox, execType, content, timeout, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockExecutionService) InstallPackages(ctx context.Context, sandbox *types.Sandbox, packages []string, manager string) (*types.ExecutionResult, error) {
	args := m.Called(ctx, sandbox, packages, manager)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockExecutionService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockExecutionService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
