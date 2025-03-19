package mocks

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockRateLimiterService implements the RateLimiterService interface for testing
type MockRateLimiterService struct {
	mock.Mock
}

func (m *MockRateLimiterService) Increment(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error) {
	args := m.Called(ctx, key, value, expiration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRateLimiterService) AddToWindow(ctx context.Context, key string, timestamp int64, member string, expiration time.Duration) error {
	args := m.Called(ctx, key, timestamp, member, expiration)
	return args.Error(0)
}

func (m *MockRateLimiterService) RemoveFromWindow(ctx context.Context, key string, cutoff int64) error {
	args := m.Called(ctx, key, cutoff)
	return args.Error(0)
}

func (m *MockRateLimiterService) CountInWindow(ctx context.Context, key string, min, max int64) (int, error) {
	args := m.Called(ctx, key, min, max)
	return args.Int(0), args.Error(1)
}

func (m *MockRateLimiterService) GetWindowEntries(ctx context.Context, key string, start, stop int) ([]string, error) {
	args := m.Called(ctx, key, start, stop)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockRateLimiterService) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockRateLimiterService) Allow(key string, rate float64, burst int) bool {
	args := m.Called(key, rate, burst)
	return args.Bool(0)
}

func (m *MockRateLimiterService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRateLimiterService) Stop() error {
	args := m.Called()
	return args.Error(0)
}
