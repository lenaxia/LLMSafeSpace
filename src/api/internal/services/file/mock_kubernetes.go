package file

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
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
	if m.clientset == nil {
		m.clientset = fake.NewSimpleClientset()
	}
	return m.clientset
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
	return args.Get(0).(interfaces.LLMSafespaceV1Interface)
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
