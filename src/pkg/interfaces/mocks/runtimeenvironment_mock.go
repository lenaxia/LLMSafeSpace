package mocks

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockRuntimeEnvironmentInterface is a mock implementation of RuntimeEnvironmentInterface
type MockRuntimeEnvironmentInterface struct {
	mock.Mock
}

// Create mocks the Create method
func (m *MockRuntimeEnvironmentInterface) Create(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// Update mocks the Update method
func (m *MockRuntimeEnvironmentInterface) Update(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// UpdateStatus mocks the UpdateStatus method
func (m *MockRuntimeEnvironmentInterface) UpdateStatus(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// Delete mocks the Delete method
func (m *MockRuntimeEnvironmentInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// Get mocks the Get method
func (m *MockRuntimeEnvironmentInterface) Get(name string, options metav1.GetOptions) (*types.RuntimeEnvironment, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// List mocks the List method
func (m *MockRuntimeEnvironmentInterface) List(opts metav1.ListOptions) (*types.RuntimeEnvironmentList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironmentList), args.Error(1)
}

// Watch mocks the Watch method
func (m *MockRuntimeEnvironmentInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
