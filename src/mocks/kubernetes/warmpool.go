package kubernetes

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockWarmPoolInterface implements the WarmPoolInterface for testing
type MockWarmPoolInterface struct {
	mock.Mock
}

// NewMockWarmPoolInterface creates a new mock WarmPoolInterface
func NewMockWarmPoolInterface() *MockWarmPoolInterface {
	return &MockWarmPoolInterface{}
}

// Ensure MockWarmPoolInterface implements interfaces.WarmPoolInterface
var _ interfaces.WarmPoolInterface = (*MockWarmPoolInterface)(nil)

func (m *MockWarmPoolInterface) Create(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockWarmPoolInterface) SetupCreateMock() *mock.Call {
	warmPool := NewMockWarmPool("test-warmpool", "test-namespace")
	return m.On("Create", mock.Anything).Return(warmPool, nil)
}

func (m *MockWarmPoolInterface) Update(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// SetupUpdateMock sets up a default mock response for Update
func (m *MockWarmPoolInterface) SetupUpdateMock() *mock.Call {
	warmPool := NewMockWarmPool("test-warmpool", "test-namespace")
	return m.On("Update", mock.Anything).Return(warmPool, nil)
}

func (m *MockWarmPoolInterface) UpdateStatus(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// SetupUpdateStatusMock sets up a default mock response for UpdateStatus
func (m *MockWarmPoolInterface) SetupUpdateStatusMock() *mock.Call {
	warmPool := NewMockWarmPool("test-warmpool", "test-namespace")
	return m.On("UpdateStatus", mock.Anything).Return(warmPool, nil)
}

func (m *MockWarmPoolInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// SetupDeleteMock sets up a default mock response for Delete
func (m *MockWarmPoolInterface) SetupDeleteMock() *mock.Call {
	return m.On("Delete", mock.Anything, mock.Anything).Return(nil)
}

func (m *MockWarmPoolInterface) Get(name string, options metav1.GetOptions) (*types.WarmPool, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// SetupGetMock sets up a default mock response for Get
func (m *MockWarmPoolInterface) SetupGetMock(name string) *mock.Call {
	warmPool := NewMockWarmPool(name, "test-namespace")
	return m.On("Get", name, mock.Anything).Return(warmPool, nil)
}

func (m *MockWarmPoolInterface) List(opts metav1.ListOptions) (*types.WarmPoolList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPoolList), args.Error(1)
}

// SetupListMock sets up a default mock response for List
func (m *MockWarmPoolInterface) SetupListMock() *mock.Call {
	warmPoolList := &types.WarmPoolList{
		Items: []types.WarmPool{
			*NewMockWarmPool("test-warmpool-1", "test-namespace"),
			*NewMockWarmPool("test-warmpool-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(warmPoolList, nil)
}

func (m *MockWarmPoolInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return NewMockWatch(), nil
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}

// SetupWatchMock sets up a mock response for Watch with any ListOptions
func (m *MockWarmPoolInterface) SetupWatchMock() *mock.Call {
    mockWatch := NewMockWatch()
    return m.On("Watch", mock.Anything).Return(mockWatch, nil)
}

// NewMockWarmPool creates a mock WarmPool with the given name
func NewMockWarmPool(name, namespace string) *types.WarmPool {
	return &types.WarmPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPool",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.WarmPoolSpec{
			Runtime:       "python:3.10",
			MinSize:       3,
			MaxSize:       10,
			SecurityLevel: "standard",
		},
		Status: types.WarmPoolStatus{
			AvailablePods: 3,
			AssignedPods:  0,
			PendingPods:   0,
		},
	}
}
