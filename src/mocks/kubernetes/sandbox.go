package kubernetes

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockSandboxInterface implements the SandboxInterface for testing
type MockSandboxInterface struct {
	mock.Mock
}

// NewMockSandboxInterface creates a new mock SandboxInterface
func NewMockSandboxInterface() *MockSandboxInterface {
	return &MockSandboxInterface{}
}

// Ensure MockSandboxInterface implements interfaces.SandboxInterface
var _ interfaces.SandboxInterface = (*MockSandboxInterface)(nil)

func (m *MockSandboxInterface) Create(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockSandboxInterface) SetupCreateMock() *mock.Call {
	sandbox := NewMockSandbox("test-sandbox", "test-namespace")
	return m.On("Create", mock.Anything).Return(sandbox, nil)
}

func (m *MockSandboxInterface) Update(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// SetupUpdateMock sets up a default mock response for Update
func (m *MockSandboxInterface) SetupUpdateMock() *mock.Call {
	sandbox := NewMockSandbox("test-sandbox", "test-namespace")
	return m.On("Update", mock.Anything).Return(sandbox, nil)
}

func (m *MockSandboxInterface) UpdateStatus(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// SetupUpdateStatusMock sets up a default mock response for UpdateStatus
func (m *MockSandboxInterface) SetupUpdateStatusMock() *mock.Call {
	sandbox := NewMockSandbox("test-sandbox", "test-namespace")
	return m.On("UpdateStatus", mock.Anything).Return(sandbox, nil)
}

func (m *MockSandboxInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// SetupDeleteMock sets up a default mock response for Delete
func (m *MockSandboxInterface) SetupDeleteMock() *mock.Call {
	return m.On("Delete", mock.Anything, mock.Anything).Return(nil)
}

func (m *MockSandboxInterface) Get(name string, options metav1.GetOptions) (*types.Sandbox, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// SetupGetMock sets up a default mock response for Get
func (m *MockSandboxInterface) SetupGetMock(name string) *mock.Call {
	sandbox := NewMockSandbox(name, "test-namespace")
	return m.On("Get", name, mock.Anything).Return(sandbox, nil)
}

func (m *MockSandboxInterface) List(opts metav1.ListOptions) (*types.SandboxList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxList), args.Error(1)
}

// SetupListMock sets up a default mock response for List
func (m *MockSandboxInterface) SetupListMock() *mock.Call {
	sandboxList := &types.SandboxList{
		Items: []types.Sandbox{
			*NewMockSandbox("test-sandbox-1", "test-namespace"),
			*NewMockSandbox("test-sandbox-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(sandboxList, nil)
}

func (m *MockSandboxInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return NewMockWatch(), nil
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}

// SetupWatchMock sets up a default mock response for Watch
func (m *MockSandboxInterface) SetupWatchMock() *mock.Call {
    mockWatch := NewMockWatch()
    return m.On("Watch", mock.AnythingOfType("metav1.ListOptions")).Return(mockWatch, nil)
}

// NewMockSandbox creates a mock Sandbox with the given name
func NewMockSandbox(name, namespace string) *types.Sandbox {
	return &types.Sandbox{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Sandbox",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: types.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
			Resources: &types.ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
		},
		Status: types.SandboxStatus{
			Phase:    "Running",
			PodName:  name + "-pod",
			Endpoint: "http://localhost:8080",
			StartTime: &metav1.Time{
				Time: metav1.Now().Time,
			},
		},
	}
}
