package mocks

import (
	"time"

	"github.com/stretchr/testify/mock"
)

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

func (m *MockMetricsService) RecordWorkspaceCreation(runtime, userID string) {
	m.Called(runtime, userID)
}

func (m *MockMetricsService) RecordWorkspaceTermination(runtime, reason string) {
	m.Called(runtime, reason)
}

func (m *MockMetricsService) RecordError(errorType, endpoint, code string) {
	m.Called(errorType, endpoint, code)
}

func (m *MockMetricsService) RecordResourceUsage(workspaceID string, cpu float64, memoryBytes int64) {
	m.Called(workspaceID, cpu, memoryBytes)
}

func (m *MockMetricsService) IncrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}

func (m *MockMetricsService) DecrementActiveConnections(connType, userID string) {
	m.Called(connType, userID)
}
