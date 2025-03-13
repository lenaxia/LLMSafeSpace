package mocks

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockWarmPodInterface is a mock implementation of WarmPodInterface
type MockWarmPodInterface struct {
	mock.Mock
}

// Create mocks the Create method
func (m *MockWarmPodInterface) Create(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// Update mocks the Update method
func (m *MockWarmPodInterface) Update(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// UpdateStatus mocks the UpdateStatus method
func (m *MockWarmPodInterface) UpdateStatus(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// Delete mocks the Delete method
func (m *MockWarmPodInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// Get mocks the Get method
func (m *MockWarmPodInterface) Get(name string, options metav1.GetOptions) (*types.WarmPod, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// List mocks the List method
func (m *MockWarmPodInterface) List(opts metav1.ListOptions) (*types.WarmPodList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPodList), args.Error(1)
}

// Watch mocks the Watch method
func (m *MockWarmPodInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
