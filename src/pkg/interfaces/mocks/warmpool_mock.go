package mocks

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockWarmPoolInterface is a mock implementation of WarmPoolInterface
type MockWarmPoolInterface struct {
	mock.Mock
}

// Create mocks the Create method
func (m *MockWarmPoolInterface) Create(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// Update mocks the Update method
func (m *MockWarmPoolInterface) Update(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// UpdateStatus mocks the UpdateStatus method
func (m *MockWarmPoolInterface) UpdateStatus(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// Delete mocks the Delete method
func (m *MockWarmPoolInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// Get mocks the Get method
func (m *MockWarmPoolInterface) Get(name string, options metav1.GetOptions) (*types.WarmPool, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// List mocks the List method
func (m *MockWarmPoolInterface) List(opts metav1.ListOptions) (*types.WarmPoolList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPoolList), args.Error(1)
}

// Watch mocks the Watch method
func (m *MockWarmPoolInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
