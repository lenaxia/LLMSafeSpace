package kubernetes

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockRuntimeEnvironmentInterface implements the RuntimeEnvironmentInterface for testing
type MockRuntimeEnvironmentInterface struct {
	mock.Mock
}

// NewMockRuntimeEnvironmentInterface creates a new mock RuntimeEnvironmentInterface
func NewMockRuntimeEnvironmentInterface() *MockRuntimeEnvironmentInterface {
	return &MockRuntimeEnvironmentInterface{}
}

// Ensure MockRuntimeEnvironmentInterface implements interfaces.RuntimeEnvironmentInterface
var _ interfaces.RuntimeEnvironmentInterface = (*MockRuntimeEnvironmentInterface)(nil)

func (m *MockRuntimeEnvironmentInterface) Create(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockRuntimeEnvironmentInterface) SetupCreateMock() *mock.Call {
	runtimeEnv := NewMockRuntimeEnvironment("test-runtime", "test-namespace")
	return m.On("Create", mock.Anything).Return(runtimeEnv, nil)
}

func (m *MockRuntimeEnvironmentInterface) Update(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// SetupUpdateMock sets up a default mock response for Update
func (m *MockRuntimeEnvironmentInterface) SetupUpdateMock() *mock.Call {
	runtimeEnv := NewMockRuntimeEnvironment("test-runtime", "test-namespace")
	return m.On("Update", mock.Anything).Return(runtimeEnv, nil)
}

func (m *MockRuntimeEnvironmentInterface) UpdateStatus(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// SetupUpdateStatusMock sets up a default mock response for UpdateStatus
func (m *MockRuntimeEnvironmentInterface) SetupUpdateStatusMock() *mock.Call {
	runtimeEnv := NewMockRuntimeEnvironment("test-runtime", "test-namespace")
	return m.On("UpdateStatus", mock.Anything).Return(runtimeEnv, nil)
}

func (m *MockRuntimeEnvironmentInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// SetupDeleteMock sets up a default mock response for Delete
func (m *MockRuntimeEnvironmentInterface) SetupDeleteMock() *mock.Call {
	return m.On("Delete", mock.Anything, mock.Anything).Return(nil)
}

func (m *MockRuntimeEnvironmentInterface) Get(name string, options metav1.GetOptions) (*types.RuntimeEnvironment, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// SetupGetMock sets up a default mock response for Get
func (m *MockRuntimeEnvironmentInterface) SetupGetMock(name string) *mock.Call {
	runtimeEnv := NewMockRuntimeEnvironment(name, "test-namespace")
	return m.On("Get", name, mock.Anything).Return(runtimeEnv, nil)
}

func (m *MockRuntimeEnvironmentInterface) List(opts metav1.ListOptions) (*types.RuntimeEnvironmentList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironmentList), args.Error(1)
}

// SetupListMock sets up a default mock response for List
func (m *MockRuntimeEnvironmentInterface) SetupListMock() *mock.Call {
	runtimeEnvList := &types.RuntimeEnvironmentList{
		Items: []types.RuntimeEnvironment{
			*NewMockRuntimeEnvironment("test-runtime-1", "test-namespace"),
			*NewMockRuntimeEnvironment("test-runtime-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(runtimeEnvList, nil)
}

func (m *MockRuntimeEnvironmentInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// SetupWatchMock sets up a default mock response for Watch
func (m *MockRuntimeEnvironmentInterface) SetupWatchMock() *mock.Call {
	mockWatch := NewMockWatch()
	return m.On("Watch", mock.Anything).Return(mockWatch, nil)
}

// NewMockRuntimeEnvironment creates a mock RuntimeEnvironment with the given name
func NewMockRuntimeEnvironment(name, namespace string) *types.RuntimeEnvironment {
	return &types.RuntimeEnvironment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeEnvironment",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "test-uid",
		},
		Spec: types.RuntimeEnvironmentSpec{
			Image:    "llmsafespace/python:3.10",
			Language: "python",
			Version:  "3.10",
		},
		Status: types.RuntimeEnvironmentStatus{
			Available: true,
			LastValidated: &metav1.Time{
				Time: metav1.Now().Time,
			},
		},
	}
}
