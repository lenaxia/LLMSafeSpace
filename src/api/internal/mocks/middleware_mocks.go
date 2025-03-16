package mocks

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
)

// MockMetricsService is a mock implementation of the MetricsService interface
type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	m.Called(method, path, status, duration, size)
}

func (m *MockMetricsService) RecordSandboxCreation(runtime string, warmPodUsed bool, userID string) {
	m.Called(runtime, warmPodUsed, userID)
}

func (m *MockMetricsService) RecordSandboxTermination(runtime, reason string) {
	m.Called(runtime, reason)
}

func (m *MockMetricsService) RecordExecution(execType, runtime, status, userID string, duration time.Duration) {
	m.Called(execType, runtime, status, userID, duration)
}

func (m *MockMetricsService) RecordError(errorType, endpoint, code string) {
	m.Called(errorType, endpoint, code)
}

func (m *MockMetricsService) RecordPackageInstallation(runtime, manager, status string) {
	m.Called(runtime, manager, status)
}

func (m *MockMetricsService) RecordFileOperation(operation, status string) {
	m.Called(operation, status)
}

func (m *MockMetricsService) RecordResourceUsage(sandboxID string, cpu float64, memoryBytes int64) {
	m.Called(sandboxID, cpu, memoryBytes)
}

func (m *MockMetricsService) RecordWarmPoolMetrics(runtime, poolName string, utilization float64) {
	m.Called(runtime, poolName, utilization)
}

func (m *MockMetricsService) RecordWarmPoolScaling(runtime, operation, reason string) {
	m.Called(runtime, operation, reason)
}

func (m *MockMetricsService) IncrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *MockMetricsService) DecrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *MockMetricsService) UpdateWarmPoolHitRatio(runtime string, ratio float64) {
	m.Called(runtime, ratio)
}

func (m *MockMetricsService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

// MockRateLimiterService is a mock implementation of the RateLimiterService interface
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
