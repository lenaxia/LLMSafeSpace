package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/stretchr/testify/mock"
)

// MockFileService implements the FileService interface for testing
type MockFileService struct {
	mock.Mock
}

func (m *MockFileService) ListFiles(ctx context.Context, sandbox *types.Sandbox, path string) ([]types.FileInfo, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]types.FileInfo), args.Error(1)
}

func (m *MockFileService) DownloadFile(ctx context.Context, sandbox *types.Sandbox, path string) ([]byte, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockFileService) UploadFile(ctx context.Context, sandbox *types.Sandbox, path string, content []byte) (*types.FileInfo, error) {
	args := m.Called(ctx, sandbox, path, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileInfo), args.Error(1)
}

func (m *MockFileService) DeleteFile(ctx context.Context, sandbox *types.Sandbox, path string) error {
	args := m.Called(ctx, sandbox, path)
	return args.Error(0)
}

func (m *MockFileService) CreateDirectory(ctx context.Context, sandbox *types.Sandbox, path string) (*types.FileInfo, error) {
	args := m.Called(ctx, sandbox, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileInfo), args.Error(1)
}

func (m *MockFileService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockFileService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
