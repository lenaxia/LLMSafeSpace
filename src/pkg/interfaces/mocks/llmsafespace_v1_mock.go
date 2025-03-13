package mocks

import (
	"github.com/stretchr/testify/mock"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// MockLLMSafespaceV1 is a mock implementation of LLMSafespaceV1Interface
type MockLLMSafespaceV1 struct {
	mock.Mock
}

// Sandboxes mocks the Sandboxes method
func (m *MockLLMSafespaceV1) Sandboxes(namespace string) interfaces.SandboxInterface {
	args := m.Called(namespace)
	return args.Get(0).(interfaces.SandboxInterface)
}

// WarmPools mocks the WarmPools method
func (m *MockLLMSafespaceV1) WarmPools(namespace string) interfaces.WarmPoolInterface {
	args := m.Called(namespace)
	return args.Get(0).(interfaces.WarmPoolInterface)
}

// WarmPods mocks the WarmPods method
func (m *MockLLMSafespaceV1) WarmPods(namespace string) interfaces.WarmPodInterface {
	args := m.Called(namespace)
	return args.Get(0).(interfaces.WarmPodInterface)
}

// RuntimeEnvironments mocks the RuntimeEnvironments method
func (m *MockLLMSafespaceV1) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	args := m.Called(namespace)
	return args.Get(0).(interfaces.RuntimeEnvironmentInterface)
}

// SandboxProfiles mocks the SandboxProfiles method
func (m *MockLLMSafespaceV1) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	args := m.Called(namespace)
	return args.Get(0).(interfaces.SandboxProfileInterface)
}
