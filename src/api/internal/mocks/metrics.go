package mocks

import (
	"time"

	"github.com/stretchr/testify/mock"
)

// MockMetricsService implements the MetricsService interface for testing
type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMetricsService) Stop() error {
	args := m.Called()
	return args.Error(0)
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

func (m *MockMetricsService) RecordWarmPoolHit() {
	m.Called()
}
