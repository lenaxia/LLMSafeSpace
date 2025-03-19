package kubernetes

import (
	"github.com/stretchr/testify/mock"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// MockLLMSafespaceV1Interface implements interfaces.LLMSafespaceV1Interface for testing
type MockLLMSafespaceV1Interface struct {
	mock.Mock
}

// Ensure MockLLMSafespaceV1Interface implements the interface
var _ interfaces.LLMSafespaceV1Interface = (*MockLLMSafespaceV1Interface)(nil)

// NewMockLLMSafespaceV1Interface creates a new mock LLMSafespaceV1Interface
func NewMockLLMSafespaceV1Interface() *MockLLMSafespaceV1Interface {
	return &MockLLMSafespaceV1Interface{}
}

// Sandboxes returns a mock implementation of the SandboxInterface
func (m *MockLLMSafespaceV1Interface) Sandboxes(namespace string) interfaces.SandboxInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockSandboxInterface()
	}
	return args.Get(0).(interfaces.SandboxInterface)
}

// SetupSandboxesMock sets up a default mock response for Sandboxes
func (m *MockLLMSafespaceV1Interface) SetupSandboxesMock(namespace string) *MockSandboxInterface {
	mockInterface := NewMockSandboxInterface()
	m.On("Sandboxes", namespace).Return(mockInterface)
	return mockInterface
}

// WarmPools returns a mock implementation of the WarmPoolInterface
func (m *MockLLMSafespaceV1Interface) WarmPools(namespace string) interfaces.WarmPoolInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockWarmPoolInterface()
	}
	return args.Get(0).(interfaces.WarmPoolInterface)
}

// SetupWarmPoolsMock sets up a default mock response for WarmPools
func (m *MockLLMSafespaceV1Interface) SetupWarmPoolsMock(namespace string) *MockWarmPoolInterface {
	mockInterface := NewMockWarmPoolInterface()
	m.On("WarmPools", namespace).Return(mockInterface)
	return mockInterface
}

// WarmPods returns a mock implementation of the WarmPodInterface
func (m *MockLLMSafespaceV1Interface) WarmPods(namespace string) interfaces.WarmPodInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockWarmPodInterface()
	}
	return args.Get(0).(interfaces.WarmPodInterface)
}

// SetupWarmPodsMock sets up a default mock response for WarmPods
func (m *MockLLMSafespaceV1Interface) SetupWarmPodsMock(namespace string) *MockWarmPodInterface {
	mockInterface := NewMockWarmPodInterface()
	m.On("WarmPods", namespace).Return(mockInterface)
	return mockInterface
}

// RuntimeEnvironments returns a mock implementation of the RuntimeEnvironmentInterface
func (m *MockLLMSafespaceV1Interface) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockRuntimeEnvironmentInterface()
	}
	return args.Get(0).(interfaces.RuntimeEnvironmentInterface)
}

// SetupRuntimeEnvironmentsMock sets up a default mock response for RuntimeEnvironments
func (m *MockLLMSafespaceV1Interface) SetupRuntimeEnvironmentsMock(namespace string) *MockRuntimeEnvironmentInterface {
	mockInterface := NewMockRuntimeEnvironmentInterface()
	m.On("RuntimeEnvironments", namespace).Return(mockInterface)
	return mockInterface
}

// SandboxProfiles returns a mock implementation of the SandboxProfileInterface
func (m *MockLLMSafespaceV1Interface) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockSandboxProfileInterface()
	}
	return args.Get(0).(interfaces.SandboxProfileInterface)
}

// SetupSandboxProfilesMock sets up a default mock response for SandboxProfiles
func (m *MockLLMSafespaceV1Interface) SetupSandboxProfilesMock(namespace string) *MockSandboxProfileInterface {
	mockInterface := NewMockSandboxProfileInterface()
	m.On("SandboxProfiles", namespace).Return(mockInterface)
	return mockInterface
}
