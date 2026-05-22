package mocks

import (
	"context"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockCacheService implements the CacheService interface for testing.
type MockCacheService struct {
	mock.Mock
}

func (m *MockCacheService) Start() error { return m.Called().Error(0) }
func (m *MockCacheService) Stop() error  { return m.Called().Error(0) }

func (m *MockCacheService) Get(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockCacheService) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return m.Called(ctx, key, value, expiration).Error(0)
}

func (m *MockCacheService) Delete(ctx context.Context, key string) error {
	return m.Called(ctx, key).Error(0)
}

func (m *MockCacheService) GetObject(ctx context.Context, key string, value interface{}) error {
	return m.Called(ctx, key, value).Error(0)
}

func (m *MockCacheService) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return m.Called(ctx, key, value, expiration).Error(0)
}

func (m *MockCacheService) GetSession(ctx context.Context, sessionID string) (*types.CachedSession, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.CachedSession), args.Error(1)
}

func (m *MockCacheService) SetSession(ctx context.Context, sessionID string, session types.CachedSession, expiration time.Duration) error {
	return m.Called(ctx, sessionID, session, expiration).Error(0)
}

func (m *MockCacheService) DeleteSession(ctx context.Context, sessionID string) error {
	return m.Called(ctx, sessionID).Error(0)
}
