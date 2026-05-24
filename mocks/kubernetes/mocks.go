package kubernetes

import (
	"sync"

	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// MockKubernetesClient mocks interfaces.KubernetesClient.
type MockKubernetesClient struct{ mock.Mock }

var _ interfaces.KubernetesClient = (*MockKubernetesClient)(nil)

func NewMockKubernetesClient() *MockKubernetesClient { return &MockKubernetesClient{} }

func (m *MockKubernetesClient) Start() error { return m.Called().Error(0) }
func (m *MockKubernetesClient) Stop()        { m.Called() }
func (m *MockKubernetesClient) Clientset() k8s.Interface {
	return m.Called().Get(0).(k8s.Interface)
}
func (m *MockKubernetesClient) DynamicClient() dynamic.Interface {
	return m.Called().Get(0).(dynamic.Interface)
}
func (m *MockKubernetesClient) RESTConfig() *rest.Config {
	return m.Called().Get(0).(*rest.Config)
}
func (m *MockKubernetesClient) InformerFactory() informers.SharedInformerFactory {
	v := m.Called().Get(0)
	if v == nil {
		return nil
	}
	return v.(informers.SharedInformerFactory)
}
func (m *MockKubernetesClient) LlmsafespaceV1() interfaces.LLMSafespaceV1Interface {
	return m.Called().Get(0).(interfaces.LLMSafespaceV1Interface)
}

// MockLLMSafespaceV1Interface mocks interfaces.LLMSafespaceV1Interface.
type MockLLMSafespaceV1Interface struct{ mock.Mock }

var _ interfaces.LLMSafespaceV1Interface = (*MockLLMSafespaceV1Interface)(nil)

func NewMockLLMSafespaceV1Interface() *MockLLMSafespaceV1Interface {
	return &MockLLMSafespaceV1Interface{}
}

func (m *MockLLMSafespaceV1Interface) RuntimeEnvironments(ns string) interfaces.RuntimeEnvironmentInterface {
	return m.Called(ns).Get(0).(interfaces.RuntimeEnvironmentInterface)
}
func (m *MockLLMSafespaceV1Interface) Workspaces(ns string) interfaces.WorkspaceInterface {
	return m.Called(ns).Get(0).(interfaces.WorkspaceInterface)
}


// MockRuntimeEnvironmentInterface mocks interfaces.RuntimeEnvironmentInterface.
type MockRuntimeEnvironmentInterface struct{ mock.Mock }

var _ interfaces.RuntimeEnvironmentInterface = (*MockRuntimeEnvironmentInterface)(nil)

func NewMockRuntimeEnvironmentInterface() *MockRuntimeEnvironmentInterface {
	return &MockRuntimeEnvironmentInterface{}
}

func (m *MockRuntimeEnvironmentInterface) Create(r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Update(r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) UpdateStatus(r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Delete(name string, opts metav1.DeleteOptions) error {
	return m.Called(name, opts).Error(0)
}
func (m *MockRuntimeEnvironmentInterface) Get(name string, opts metav1.GetOptions) (*v1.RuntimeEnvironment, error) {
	args := m.Called(name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) List(opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironmentList), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}


// MockWorkspaceInterface mocks interfaces.WorkspaceInterface.
type MockWorkspaceInterface struct{ mock.Mock }

var _ interfaces.WorkspaceInterface = (*MockWorkspaceInterface)(nil)

func NewMockWorkspaceInterface() *MockWorkspaceInterface { return &MockWorkspaceInterface{} }

func (m *MockWorkspaceInterface) Create(w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) Update(w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) UpdateStatus(w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) Delete(name string, opts metav1.DeleteOptions) error {
	return m.Called(name, opts).Error(0)
}
func (m *MockWorkspaceInterface) Get(name string, opts metav1.GetOptions) (*v1.Workspace, error) {
	args := m.Called(name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) List(opts metav1.ListOptions) (*v1.WorkspaceList, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.WorkspaceList), args.Error(1)
}
func (m *MockWorkspaceInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockWatch mocks watch.Interface.
type MockWatch struct {
	mock.Mock
	ch   chan watch.Event
	once sync.Once
}

var _ watch.Interface = (*MockWatch)(nil)

func NewMockWatch() *MockWatch {
	return &MockWatch{ch: make(chan watch.Event, 10)}
}

// Stop closes the event channel exactly once, satisfying the watch.Interface contract.
func (m *MockWatch) Stop() {
	m.Called()
	m.once.Do(func() { close(m.ch) })
}

func (m *MockWatch) ResultChan() <-chan watch.Event {
	return m.ch
}

func (m *MockWatch) SendEvent(t watch.EventType, obj runtime.Object) {
	m.ch <- watch.Event{Type: t, Object: obj}
}
