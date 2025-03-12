package mocks

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/stretchr/testify/mock"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/apimachinery/pkg/watch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MockKubernetesClient implements the KubernetesClient interface for testing
type MockKubernetesClient struct {
	mock.Mock
	clientset kubernetes.Interface
	restConfig *rest.Config
}

// Ensure MockKubernetesClient implements interfaces.KubernetesClient
var _ interfaces.KubernetesClient = (*MockKubernetesClient)(nil)

func (m *MockKubernetesClient) Clientset() kubernetes.Interface {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(kubernetes.Interface)
	}
	if m.clientset == nil {
		m.clientset = fake.NewSimpleClientset()
	}
	return m.clientset
}

// CoreV1 returns a mock implementation of the CoreV1Interface
func (m *MockKubernetesClient) CoreV1() interface{} {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0)
	}
	return m
}

// Pods returns a mock implementation of the PodInterface
func (m *MockKubernetesClient) Pods(namespace string) interface{} {
	args := m.Called(namespace)
	if args.Get(0) != nil {
		return args.Get(0)
	}
	return m
}

// Get returns a mock implementation of the Get method for pods
func (m *MockKubernetesClient) Get(ctx context.Context, name string, options interface{}) (interface{}, error) {
	args := m.Called(ctx, name, options)
	return args.Get(0), args.Error(1)
}

func (m *MockKubernetesClient) RESTConfig() *rest.Config {
	if m.restConfig == nil {
		m.restConfig = &rest.Config{}
	}
	return m.restConfig
}

func (m *MockKubernetesClient) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockKubernetesClient) Stop() {
	m.Called()
}

func (m *MockKubernetesClient) ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockKubernetesClient) ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockKubernetesClient) ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileList), args.Error(1)
}

func (m *MockKubernetesClient) DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockKubernetesClient) UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileResult), args.Error(1)
}

func (m *MockKubernetesClient) DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error {
	args := m.Called(ctx, namespace, name, fileReq)
	return args.Error(0)
}

// LlmsafespaceV1 returns a mock implementation of the LLMSafespaceV1Interface
func (m *MockKubernetesClient) LlmsafespaceV1() interfaces.LLMSafespaceV1Interface {
	args := m.Called()
	if args.Get(0) == nil {
		// Return self as a fallback if no expectation is set
		return &MockLLMSafespaceV1Interface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.LLMSafespaceV1Interface)
}

// MockLLMSafespaceV1Interface implements the LLMSafespaceV1Interface for testing
type MockLLMSafespaceV1Interface struct {
	mock mock.Mock
}

// Sandboxes returns a mock implementation of the SandboxInterface
func (m *MockLLMSafespaceV1Interface) Sandboxes(namespace string) interfaces.SandboxInterface {
	args := m.Mock.Called(namespace)
	if args.Get(0) == nil {
		return &MockSandboxInterface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.SandboxInterface)
}

// WarmPools returns a mock implementation of the WarmPoolInterface
func (m *MockLLMSafespaceV1Interface) WarmPools(namespace string) interfaces.WarmPoolInterface {
	args := m.mock.Called(namespace)
	if args.Get(0) == nil {
		return &MockWarmPoolInterface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.WarmPoolInterface)
}

// WarmPods returns a mock implementation of the WarmPodInterface
func (m *MockLLMSafespaceV1Interface) WarmPods(namespace string) interfaces.WarmPodInterface {
	args := m.mock.Called(namespace)
	if args.Get(0) == nil {
		return &MockWarmPodInterface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.WarmPodInterface)
}

// RuntimeEnvironments returns a mock implementation of the RuntimeEnvironmentInterface
func (m *MockLLMSafespaceV1Interface) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	args := m.mock.Called(namespace)
	if args.Get(0) == nil {
		return &MockRuntimeEnvironmentInterface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.RuntimeEnvironmentInterface)
}

// SandboxProfiles returns a mock implementation of the SandboxProfileInterface
func (m *MockLLMSafespaceV1Interface) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	args := m.mock.Called(namespace)
	if args.Get(0) == nil {
		return &MockSandboxProfileInterface{Mock: mock.Mock{}}
	}
	return args.Get(0).(interfaces.SandboxProfileInterface)
}

// MockSandboxInterface implements the SandboxInterface for testing
type MockSandboxInterface struct {
	Mock mock.Mock
}

func (m *MockSandboxInterface) Create(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Mock.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) Update(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.mock.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) UpdateStatus(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.mock.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.mock.Called(name, options)
	return args.Error(0)
}

func (m *MockSandboxInterface) Get(name string, options metav1.GetOptions) (*types.Sandbox, error) {
	args := m.mock.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxInterface) List(opts metav1.ListOptions) (*types.SandboxList, error) {
	args := m.mock.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxList), args.Error(1)
}

func (m *MockSandboxInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.mock.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockWarmPoolInterface implements the WarmPoolInterface for testing
type MockWarmPoolInterface struct {
	Mock mock.Mock
}

func (m *MockWarmPoolInterface) Create(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Mock.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) Update(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Mock.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) UpdateStatus(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Mock.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Mock.Called(name, options)
	return args.Error(0)
}

func (m *MockWarmPoolInterface) Get(name string, options metav1.GetOptions) (*types.WarmPool, error) {
	args := m.Mock.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

func (m *MockWarmPoolInterface) List(opts metav1.ListOptions) (*types.WarmPoolList, error) {
	args := m.Mock.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPoolList), args.Error(1)
}

func (m *MockWarmPoolInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Mock.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockWarmPodInterface implements the WarmPodInterface for testing
type MockWarmPodInterface struct {
	Mock mock.Mock
}

func (m *MockWarmPodInterface) Create(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Mock.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

func (m *MockWarmPodInterface) Update(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Mock.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

func (m *MockWarmPodInterface) UpdateStatus(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Mock.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

func (m *MockWarmPodInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Mock.Called(name, options)
	return args.Error(0)
}

func (m *MockWarmPodInterface) Get(name string, options metav1.GetOptions) (*types.WarmPod, error) {
	args := m.Mock.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

func (m *MockWarmPodInterface) List(opts metav1.ListOptions) (*types.WarmPodList, error) {
	args := m.Mock.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPodList), args.Error(1)
}

func (m *MockWarmPodInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Mock.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockRuntimeEnvironmentInterface implements the RuntimeEnvironmentInterface for testing
type MockRuntimeEnvironmentInterface struct {
	Mock mock.Mock
}

func (m *MockRuntimeEnvironmentInterface) Create(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Mock.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

func (m *MockRuntimeEnvironmentInterface) Update(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Mock.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

func (m *MockRuntimeEnvironmentInterface) UpdateStatus(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Mock.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

func (m *MockRuntimeEnvironmentInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Mock.Called(name, options)
	return args.Error(0)
}

func (m *MockRuntimeEnvironmentInterface) Get(name string, options metav1.GetOptions) (*types.RuntimeEnvironment, error) {
	args := m.Mock.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

func (m *MockRuntimeEnvironmentInterface) List(opts metav1.ListOptions) (*types.RuntimeEnvironmentList, error) {
	args := m.Mock.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironmentList), args.Error(1)
}

func (m *MockRuntimeEnvironmentInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Mock.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockSandboxProfileInterface implements the SandboxProfileInterface for testing
type MockSandboxProfileInterface struct {
	Mock mock.Mock
}

func (m *MockSandboxProfileInterface) Create(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Mock.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

func (m *MockSandboxProfileInterface) Update(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Mock.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

func (m *MockSandboxProfileInterface) Delete(name string, options metav1.DeleteOptions) error {
	args := m.Mock.Called(name, options)
	return args.Error(0)
}

func (m *MockSandboxProfileInterface) Get(name string, options metav1.GetOptions) (*types.SandboxProfile, error) {
	args := m.Mock.Called(name, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

func (m *MockSandboxProfileInterface) List(opts metav1.ListOptions) (*types.SandboxProfileList, error) {
	args := m.Mock.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfileList), args.Error(1)
}

func (m *MockSandboxProfileInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Mock.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockFileInfo creates a mock FileInfo for testing
func MockFileInfo(path string, size int64, isDir bool) types.FileInfo {
	return types.FileInfo{
		Path:      path,
		Name:      filepath.Base(path),
		Size:      size,
		IsDir:     isDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}
