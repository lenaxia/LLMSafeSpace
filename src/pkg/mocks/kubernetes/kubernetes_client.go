package kubernetes

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
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

// DynamicClient returns the dynamic client
func (m *MockKubernetesClient) DynamicClient() dynamic.Interface {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(dynamic.Interface)
	}
	return nil
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
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(*rest.Config)
	}
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
