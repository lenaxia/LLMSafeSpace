package kubernetes

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockWarmPodInterface implements the WarmPodInterface for testing
type MockWarmPodInterface struct {
	mock.Mock
}

// NewMockWarmPodInterface creates a new mock WarmPodInterface
func NewMockWarmPodInterface() *MockWarmPodInterface {
	return &MockWarmPodInterface{}
}

// Ensure MockWarmPodInterface implements interfaces.WarmPodInterface
var _ interfaces.WarmPodInterface = (*MockWarmPodInterface)(nil)

func (m *MockWarmPodInterface) Create(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockWarmPodInterface) SetupCreateMock() *mock.Call {
	warmPod := NewMockWarmPod("test-warmpod", "test-namespace")
	return m.On("Create", mock.Anything).Return(warmPod, nil)
}

func (m *MockWarmPodInterface) Update(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// SetupUpdateMock sets up a default mock response for Update
func (m *MockWarmPodInterface) SetupUpdateMock() *mock.Call {
	warmPod := NewMockWarmPod("test-warmpod", "test-namespace")
	return m.On("Update", mock.Anything).Return(warmPod, nil)
}

func (m *MockWarmPodInterface) UpdateStatus(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// SetupUpdateStatusMock sets up a default mock response for UpdateStatus
func (m *MockWarmPodInterface) SetupUpdateStatusMock() *mock.Call {
	warmPod := NewMockWarmPod("test-warmpod", "test-namespace")
	return m.On("UpdateStatus", mock.Anything).Return(warmPod, nil)
}

func (m *MockWarmPodInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// SetupDeleteMock sets up a default mock response for Delete
func (m *MockWarmPodInterface) SetupDeleteMock() *mock.Call {
	return m.On("Delete", mock.Anything, mock.Anything).Return(nil)
}

func (m *MockWarmPodInterface) Get(name string, options metav1.GetOptions) (*types.WarmPod, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// SetupGetMock sets up a default mock response for Get
func (m *MockWarmPodInterface) SetupGetMock(name string) *mock.Call {
	warmPod := NewMockWarmPod(name, "test-namespace")
	return m.On("Get", name, mock.Anything).Return(warmPod, nil)
}

func (m *MockWarmPodInterface) List(opts metav1.ListOptions) (*types.WarmPodList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPodList), args.Error(1)
}

// SetupListMock sets up a default mock response for List
func (m *MockWarmPodInterface) SetupListMock() *mock.Call {
	warmPodList := &types.WarmPodList{
		Items: []types.WarmPod{
			*NewMockWarmPod("test-warmpod-1", "test-namespace"),
			*NewMockWarmPod("test-warmpod-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(warmPodList, nil)
}

func (m *MockWarmPodInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// SetupWatchMock sets up a mock response for Watch with Watch=true
func (m *MockWarmPodInterface) SetupWatchMock() *mock.Call {
    mockWatch := NewMockWatch()
    return m.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
        return true // Accept any ListOptions to avoid strict matching issues
    })).Return(mockWatch, nil)
}

// NewMockWarmPod creates a mock WarmPod with the given name
func NewMockWarmPod(name, namespace string) *types.WarmPod {
	return &types.WarmPod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WarmPod",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.WarmPodSpec{
			PoolRef: types.PoolReference{
				Name:      "test-pool",
				Namespace: namespace,
			},
			CreationTimestamp: &metav1.Time{
				Time: metav1.Now().Time,
			},
		},
		Status: types.WarmPodStatus{
			Phase:        "Ready",
			PodName:      name + "-pod",
			PodNamespace: namespace,
		},
	}
}
