package mocks

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockCacheService implements the CacheService interface for testing
type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheService) GetObject(ctx context.Context, key string, value interface{}) error {
	args := m.Called(ctx, key, value)
	return args.Error(0)
}

func (m *MockCacheService) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := m.Called(ctx, key, value, expiration)
	return args.Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	args := m.Called(ctx, sessionID, session, expiration)
	return args.Error(0)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}
