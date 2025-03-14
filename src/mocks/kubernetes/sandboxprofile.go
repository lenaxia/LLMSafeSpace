package kubernetes

import (
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockSandboxProfileInterface implements the SandboxProfileInterface for testing
type MockSandboxProfileInterface struct {
	mock.Mock
}

// NewMockSandboxProfileInterface creates a new mock SandboxProfileInterface
func NewMockSandboxProfileInterface() *MockSandboxProfileInterface {
	return &MockSandboxProfileInterface{}
}

// Ensure MockSandboxProfileInterface implements interfaces.SandboxProfileInterface
var _ interfaces.SandboxProfileInterface = (*MockSandboxProfileInterface)(nil)

func (m *MockSandboxProfileInterface) Create(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockSandboxProfileInterface) SetupCreateMock() *mock.Call {
	profile := NewMockSandboxProfile("test-profile", "test-namespace")
	return m.On("Create", mock.Anything).Return(profile, nil)
}

func (m *MockSandboxProfileInterface) Update(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// SetupUpdateMock sets up a default mock response for Update
func (m *MockSandboxProfileInterface) SetupUpdateMock() *mock.Call {
	profile := NewMockSandboxProfile("test-profile", "test-namespace")
	return m.On("Update", mock.Anything).Return(profile, nil)
}

func (m *MockSandboxProfileInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Called(name, options)
	return args.Error(0)
}

// SetupDeleteMock sets up a default mock response for Delete
func (m *MockSandboxProfileInterface) SetupDeleteMock() *mock.Call {
	return m.On("Delete", mock.Anything, mock.Anything).Return(nil)
}

func (m *MockSandboxProfileInterface) Get(name string, options metav1.GetOptions) (*types.SandboxProfile, error) {
	args := m.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// SetupGetMock sets up a default mock response for Get
func (m *MockSandboxProfileInterface) SetupGetMock(name string) *mock.Call {
	profile := NewMockSandboxProfile(name, "test-namespace")
	return m.On("Get", name, mock.Anything).Return(profile, nil)
}

func (m *MockSandboxProfileInterface) List(opts metav1.ListOptions) (*types.SandboxProfileList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfileList), args.Error(1)
}

// SetupListMock sets up a default mock response for List
func (m *MockSandboxProfileInterface) SetupListMock() *mock.Call {
	profileList := &types.SandboxProfileList{
		Items: []types.SandboxProfile{
			*NewMockSandboxProfile("test-profile-1", "test-namespace"),
			*NewMockSandboxProfile("test-profile-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(profileList, nil)
}

func (m *MockSandboxProfileInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return NewMockWatch(), nil
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}

// SetupWatchMock sets up a mock response for Watch with any ListOptions
func (m *MockSandboxProfileInterface) SetupWatchMock() *mock.Call {
    mockWatch := NewMockWatch()
    return m.On("Watch", mock.Anything).Return(mockWatch, nil)
}

// NewMockSandboxProfile creates a mock SandboxProfile with the given name
func NewMockSandboxProfile(name, namespace string) *types.SandboxProfile {
	return &types.SandboxProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SandboxProfile",
			APIVersion: "llmsafespace.dev/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "test-uid",
		},
		Spec: types.SandboxProfileSpec{
			Language:      "python",
			SecurityLevel: "standard",
		},
	}
}
