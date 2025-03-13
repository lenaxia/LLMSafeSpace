package mocks

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/lenaxia/llmsafespace/pkg/types/mock"
	"github.com/stretchr/testify/mock"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/apimachinery/pkg/watch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MockKubernetesClient implements the KubernetesClient interface for testing
type MockKubernetesClient struct {
	mock.Mock
	clientset       kubernetes.Interface
	restConfig      *rest.Config
	informerFactory informers.SharedInformerFactory
}

// Ensure MockKubernetesClient implements interfaces.KubernetesClient
var _ interfaces.KubernetesClient = (*MockKubernetesClient)(nil)

// NewMockKubernetesClient creates a new mock Kubernetes client
func NewMockKubernetesClient() *MockKubernetesClient {
	return &MockKubernetesClient{
		clientset:  fake.NewSimpleClientset(),
		restConfig: &rest.Config{},
	}
}

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

// InformerFactory returns the informer factory
func (m *MockKubernetesClient) InformerFactory() informers.SharedInformerFactory {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(informers.SharedInformerFactory)
	}
	return m.informerFactory
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

// SetupExecuteInSandboxMock sets up a default mock response for ExecuteInSandbox
func (m *MockKubernetesClient) SetupExecuteInSandboxMock(exitCode int) *mock.Call {
	status := "failed"
	stderr := "Test stderr output"
	if exitCode == 0 {
		status = "completed"
		stderr = ""
	}
	
	result := &types.ExecutionResult{
		ID:          "test-exec-id",
		Status:      status,
		StartedAt:   time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
		ExitCode:    exitCode,
		Stdout:      "Test stdout output",
		Stderr:      stderr,
	}
	return m.On("ExecuteInSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(result, nil)
}

func (m *MockKubernetesClient) ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

// SetupExecuteStreamInSandboxMock sets up a default mock response for ExecuteStreamInSandbox
func (m *MockKubernetesClient) SetupExecuteStreamInSandboxMock(exitCode int) *mock.Call {
	status := "failed"
	stderr := "Test stderr output"
	if exitCode == 0 {
		status = "completed"
		stderr = ""
	}
	
	result := &types.ExecutionResult{
		ID:          "test-exec-id",
		Status:      status,
		StartedAt:   time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
		ExitCode:    exitCode,
		Stdout:      "Test stdout output",
		Stderr:      stderr,
	}
	return m.On("ExecuteStreamInSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(result, nil)
}

func (m *MockKubernetesClient) ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileList), args.Error(1)
}

// SetupListFilesInSandboxMock sets up a default mock response for ListFilesInSandbox
func (m *MockKubernetesClient) SetupListFilesInSandboxMock() *mock.Call {
	fileList := &types.FileList{
		Files: []types.FileInfo{
			{
				Path:      "/workspace/test.py",
				Name:      "test.py",
				Size:      1024,
				IsDir:     false,
				CreatedAt: time.Now().Add(-1 * time.Hour),
				UpdatedAt: time.Now().Add(-30 * time.Minute),
			},
			{
				Path:      "/workspace/data",
				Name:      "data",
				Size:      4096,
				IsDir:     true,
				CreatedAt: time.Now().Add(-2 * time.Hour),
				UpdatedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}
	return m.On("ListFilesInSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileList, nil)
}

func (m *MockKubernetesClient) DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

// SetupDownloadFileFromSandboxMock sets up a default mock response for DownloadFileFromSandbox
func (m *MockKubernetesClient) SetupDownloadFileFromSandboxMock() *mock.Call {
	content := []byte("Test file content")
	return m.On("DownloadFileFromSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(content, nil)
}

func (m *MockKubernetesClient) UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileResult), args.Error(1)
}

// SetupUploadFileToSandboxMock sets up a default mock response for UploadFileToSandbox
func (m *MockKubernetesClient) SetupUploadFileToSandboxMock(isDir bool) *mock.Call {
	path := "/workspace/test.py"
	size := int64(1024)
	if isDir {
		path = "/workspace/data"
		size = int64(4096)
	}
	
	result := &types.FileResult{
		Path:      path,
		Size:      size,
		IsDir:     isDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return m.On("UploadFileToSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(result, nil)
}

func (m *MockKubernetesClient) DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error {
	args := m.Called(ctx, namespace, name, fileReq)
	return args.Error(0)
}

// SetupDeleteFileInSandboxMock sets up a default mock response for DeleteFileInSandbox
func (m *MockKubernetesClient) SetupDeleteFileInSandboxMock() *mock.Call {
	return m.On("DeleteFileInSandbox", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

// LlmsafespaceV1 returns a mock implementation of the LLMSafespaceV1Interface
func (m *MockKubernetesClient) LlmsafespaceV1() interfaces.LLMSafespaceV1Interface {
	args := m.Called()
	if args.Get(0) == nil {
		// Return new mock instance
		return NewMockLLMSafespaceV1Interface()
	}
	return args.Get(0).(interfaces.LLMSafespaceV1Interface)
}

// SetupLlmsafespaceV1Mock sets up a default mock response for LlmsafespaceV1
func (m *MockKubernetesClient) SetupLlmsafespaceV1Mock() *mock.Call {
	return m.On("LlmsafespaceV1").Return(NewMockLLMSafespaceV1Interface())
}

// NewMockLLMSafespaceV1Interface creates a new mock LLMSafespaceV1Interface
func NewMockLLMSafespaceV1Interface() *MockLLMSafespaceV1Interface {
	return &MockLLMSafespaceV1Interface{}
}

// MockLLMSafespaceV1Interface implements the LLMSafespaceV1Interface for testing
type MockLLMSafespaceV1Interface struct {
	mock.Mock
}

// Sandboxes returns a mock implementation of the SandboxInterface
func (m *MockLLMSafespaceV1Interface) Sandboxes(namespace string) interfaces.SandboxInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockSandboxInterface()
	}
	return args.Get(0).(interfaces.SandboxInterface)
}

// SetupSandboxesMock sets up a default mock response for Sandboxes
func (m *MockLLMSafespaceV1Interface) SetupSandboxesMock(namespace string) *mock.Call {
	return m.On("Sandboxes", namespace).Return(NewMockSandboxInterface())
}

// WarmPools returns a mock implementation of the WarmPoolInterface
func (m *MockLLMSafespaceV1Interface) WarmPools(namespace string) interfaces.WarmPoolInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockWarmPoolInterface()
	}
	return args.Get(0).(interfaces.WarmPoolInterface)
}

// SetupWarmPoolsMock sets up a default mock response for WarmPools
func (m *MockLLMSafespaceV1Interface) SetupWarmPoolsMock(namespace string) *mock.Call {
	return m.On("WarmPools", namespace).Return(NewMockWarmPoolInterface())
}

// WarmPods returns a mock implementation of the WarmPodInterface
func (m *MockLLMSafespaceV1Interface) WarmPods(namespace string) interfaces.WarmPodInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockWarmPodInterface()
	}
	return args.Get(0).(interfaces.WarmPodInterface)
}

// SetupWarmPodsMock sets up a default mock response for WarmPods
func (m *MockLLMSafespaceV1Interface) SetupWarmPodsMock(namespace string) *mock.Call {
	return m.On("WarmPods", namespace).Return(NewMockWarmPodInterface())
}

// RuntimeEnvironments returns a mock implementation of the RuntimeEnvironmentInterface
func (m *MockLLMSafespaceV1Interface) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockRuntimeEnvironmentInterface()
	}
	return args.Get(0).(interfaces.RuntimeEnvironmentInterface)
}

// SetupRuntimeEnvironmentsMock sets up a default mock response for RuntimeEnvironments
func (m *MockLLMSafespaceV1Interface) SetupRuntimeEnvironmentsMock(namespace string) *mock.Call {
	return m.On("RuntimeEnvironments", namespace).Return(NewMockRuntimeEnvironmentInterface())
}

// SandboxProfiles returns a mock implementation of the SandboxProfileInterface
func (m *MockLLMSafespaceV1Interface) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	args := m.Called(namespace)
	if args.Get(0) == nil {
		return NewMockSandboxProfileInterface()
	}
	return args.Get(0).(interfaces.SandboxProfileInterface)
}

// SetupSandboxProfilesMock sets up a default mock response for SandboxProfiles
func (m *MockLLMSafespaceV1Interface) SetupSandboxProfilesMock(namespace string) *mock.Call {
	return m.On("SandboxProfiles", namespace).Return(NewMockSandboxProfileInterface())
}

// NewMockSandboxInterface creates a new mock SandboxInterface
func NewMockSandboxInterface() *MockSandboxInterface {
	return &MockSandboxInterface{}
}

// MockSandboxInterface implements the SandboxInterface for testing
type MockSandboxInterface struct {
	mock.Mock
}

func (m *MockSandboxInterface) Create(sandbox *types.Sandbox) (*types.Sandbox, error) {
	args := m.Called(sandbox)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockSandboxInterface) SetupCreateMock() *mock.Call {
	sandbox := mock.NewMockSandbox("test-sandbox", "test-namespace")
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
	sandbox := mock.NewMockSandbox("test-sandbox", "test-namespace")
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
	sandbox := mock.NewMockSandbox("test-sandbox", "test-namespace")
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
	sandbox := mock.NewMockSandbox(name, "test-namespace")
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
			*mock.NewMockSandbox("test-sandbox-1", "test-namespace"),
			*mock.NewMockSandbox("test-sandbox-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(sandboxList, nil)
}

func (m *MockSandboxInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// NewMockWarmPoolInterface creates a new mock WarmPoolInterface
func NewMockWarmPoolInterface() *MockWarmPoolInterface {
	return &MockWarmPoolInterface{}
}

// MockWarmPoolInterface implements the WarmPoolInterface for testing
type MockWarmPoolInterface struct {
	mock.Mock
}

func (m *MockWarmPoolInterface) Create(warmPool *types.WarmPool) (*types.WarmPool, error) {
	args := m.Called(warmPool)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPool), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockWarmPoolInterface) SetupCreateMock() *mock.Call {
	warmPool := mock.NewMockWarmPool("test-warmpool", "test-namespace")
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
	warmPool := mock.NewMockWarmPool("test-warmpool", "test-namespace")
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
	warmPool := mock.NewMockWarmPool("test-warmpool", "test-namespace")
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
	warmPool := mock.NewMockWarmPool(name, "test-namespace")
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
			*mock.NewMockWarmPool("test-warmpool-1", "test-namespace"),
			*mock.NewMockWarmPool("test-warmpool-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(warmPoolList, nil)
}

func (m *MockWarmPoolInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// NewMockWarmPodInterface creates a new mock WarmPodInterface
func NewMockWarmPodInterface() *MockWarmPodInterface {
	return &MockWarmPodInterface{}
}

// MockWarmPodInterface implements the WarmPodInterface for testing
type MockWarmPodInterface struct {
	mock.Mock
}

func (m *MockWarmPodInterface) Create(warmPod *types.WarmPod) (*types.WarmPod, error) {
	args := m.Called(warmPod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WarmPod), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockWarmPodInterface) SetupCreateMock() *mock.Call {
	warmPod := mock.NewMockWarmPod("test-warmpod", "test-namespace")
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
	warmPod := mock.NewMockWarmPod("test-warmpod", "test-namespace")
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
	warmPod := mock.NewMockWarmPod("test-warmpod", "test-namespace")
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
	warmPod := mock.NewMockWarmPod(name, "test-namespace")
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
			*mock.NewMockWarmPod("test-warmpod-1", "test-namespace"),
			*mock.NewMockWarmPod("test-warmpod-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(warmPodList, nil)
}

func (m *MockWarmPodInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// NewMockRuntimeEnvironmentInterface creates a new mock RuntimeEnvironmentInterface
func NewMockRuntimeEnvironmentInterface() *MockRuntimeEnvironmentInterface {
	return &MockRuntimeEnvironmentInterface{}
}

// MockRuntimeEnvironmentInterface implements the RuntimeEnvironmentInterface for testing
type MockRuntimeEnvironmentInterface struct {
	mock.Mock
}

func (m *MockRuntimeEnvironmentInterface) Create(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	args := m.Called(runtimeEnv)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.RuntimeEnvironment), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockRuntimeEnvironmentInterface) SetupCreateMock() *mock.Call {
	runtimeEnv := mock.NewMockRuntimeEnvironment("test-runtime", "test-namespace")
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
	runtimeEnv := mock.NewMockRuntimeEnvironment("test-runtime", "test-namespace")
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
	runtimeEnv := mock.NewMockRuntimeEnvironment("test-runtime", "test-namespace")
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
	runtimeEnv := mock.NewMockRuntimeEnvironment(name, "test-namespace")
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
			*mock.NewMockRuntimeEnvironment("test-runtime-1", "test-namespace"),
			*mock.NewMockRuntimeEnvironment("test-runtime-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(runtimeEnvList, nil)
}

func (m *MockRuntimeEnvironmentInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
	return args.Get(0).(watch.Interface), args.Error(1)
}

// NewMockSandboxProfileInterface creates a new mock SandboxProfileInterface
func NewMockSandboxProfileInterface() *MockSandboxProfileInterface {
	return &MockSandboxProfileInterface{}
}

// MockSandboxProfileInterface implements the SandboxProfileInterface for testing
type MockSandboxProfileInterface struct {
	mock.Mock
}

func (m *MockSandboxProfileInterface) Create(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	args := m.Called(profile)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxProfile), args.Error(1)
}

// SetupCreateMock sets up a default mock response for Create
func (m *MockSandboxProfileInterface) SetupCreateMock() *mock.Call {
	profile := mock.NewMockSandboxProfile("test-profile", "test-namespace")
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
	profile := mock.NewMockSandboxProfile("test-profile", "test-namespace")
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
	profile := mock.NewMockSandboxProfile(name, "test-namespace")
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
			*mock.NewMockSandboxProfile("test-profile-1", "test-namespace"),
			*mock.NewMockSandboxProfile("test-profile-2", "test-namespace"),
		},
	}
	return m.On("List", mock.Anything).Return(profileList, nil)
}

func (m *MockSandboxProfileInterface) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(opts)
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

// MockWatch implements watch.Interface for testing
type MockWatch struct {
	mock.Mock
	resultChan chan watch.Event
}

// NewMockWatch creates a new mock watch
func NewMockWatch() *MockWatch {
	return &MockWatch{
		resultChan: make(chan watch.Event, 10),
	}
}

// Stop implements watch.Interface
func (m *MockWatch) Stop() {
	m.Called()
	close(m.resultChan)
}

// ResultChan implements watch.Interface
func (m *MockWatch) ResultChan() <-chan watch.Event {
	m.Called()
	return m.resultChan
}

// SendEvent sends an event to the result channel
func (m *MockWatch) SendEvent(eventType watch.EventType, object interface{}) {
	m.resultChan <- watch.Event{
		Type:   eventType,
		Object: object,
	}
}
