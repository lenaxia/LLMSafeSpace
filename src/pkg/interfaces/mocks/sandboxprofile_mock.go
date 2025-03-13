package mocks

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockSandboxProfileInterface is a mock implementation of SandboxProfileInterface
type MockSandboxProfileInterface struct {
	mock.Mock
}

// Create mocks the Create method
func (m *MockSandboxProfileInterface) Create(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// Update mocks the Update method
func (m *MockSandboxProfileInterface) Update(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// Delete mocks the Delete method
func (m *MockSandboxProfileInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// Get mocks the Get method
func (m *MockSandboxProfileInterface) Get(name string, options metav1.GetOptions) (*types.SandboxProfile, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// List mocks the List method
func (m *MockSandboxProfileInterface) List(opts metav1.ListOptions) (*types.SandboxProfileList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfileList), args.Error(1)
}

// Watch mocks the Watch method
func (m *MockSandboxProfileInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
