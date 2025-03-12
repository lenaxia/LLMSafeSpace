package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockDatabaseService implements the DatabaseService interface for testing
type MockDatabaseService struct {
	mock.Mock
}

func (m *MockDatabaseService) GetUserByID(ctx context.Context, userID string) (map[string]interface{}, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) GetSandboxByID(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0), args.Error(1)
}

func (m *MockDatabaseService) GetUserIDByAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockDatabaseService) CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error {
	args := m.Called(ctx, sandboxID, userID, runtime)
	return args.Error(0)
}

func (m *MockDatabaseService) GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockDatabaseService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDatabaseService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
