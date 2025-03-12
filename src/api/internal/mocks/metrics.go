package mocks

import (
	"time"

	"github.com/stretchr/testify/mock"
)

// MockMetricsRecorder implements the MetricsRecorder interface for testing
type MockMetricsRecorder struct {
	mock.Mock
}

func (m *MockMetricsRecorder) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	m.Called(runtime, warmPodUsed)
}

func (m *MockMetricsRecorder) RecordSandboxTermination(runtime string) {
	m.Called(runtime)
}

func (m *MockMetricsRecorder) RecordOperationDuration(operation string, duration time.Duration) {
	m.Called(operation, duration)
}
