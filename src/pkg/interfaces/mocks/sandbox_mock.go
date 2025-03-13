package mocks

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockSandboxInterface is a mock implementation of SandboxInterface
type MockSandboxInterface struct {
	mock.Mock
}

// Create mocks the Create method
func (m *MockSandboxInterface) Create(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// Update mocks the Update method
func (m *MockSandboxInterface) Update(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// UpdateStatus mocks the UpdateStatus method
func (m *MockSandboxInterface) UpdateStatus(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// Delete mocks the Delete method
func (m *MockSandboxInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// Get mocks the Get method
func (m *MockSandboxInterface) Get(name string, options metav1.GetOptions) (*types.Sandbox, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// List mocks the List method
func (m *MockSandboxInterface) List(opts metav1.ListOptions) (*types.SandboxList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxList), args.Error(1)
}

// Watch mocks the Watch method
func (m *MockSandboxInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
