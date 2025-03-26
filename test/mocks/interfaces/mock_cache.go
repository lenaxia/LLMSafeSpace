package interfaces

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockCacheService implements CacheService for testing
type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	args := m.Called(ctx, key, value, ttl)
	return args.Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheService) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockCacheService) CreateSession(ctx context.Context, userID string, data map[string]string, ttl time.Duration) (string, error) {
	args := m.Called(ctx, userID, data, ttl)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (map[string]string, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).(map[string]string), args.Error(1)
}

func (m *MockCacheService) RefreshSession(ctx context.Context, sessionID string, ttl time.Duration) error {
	args := m.Called(ctx, sessionID, ttl)
	return args.Error(0)
}

func (m *MockCacheService) InvalidateSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockCacheService) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockCacheService) Close() error {
	args := m.Called()
	return args.Error(0)
}
