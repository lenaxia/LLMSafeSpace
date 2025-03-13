package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockKubernetesClient is a mock implementation of KubernetesClient
type MockKubernetesClient struct {
	mock.Mock
}

// Start mocks the Start method
func (m *MockKubernetesClient) Start() error {
	args := m.Called()
	return args.Error(0)
}

// Stop mocks the Stop method
func (m *MockKubernetesClient) Stop() {
	m.Called()
}

// Clientset mocks the Clientset method
func (m *MockKubernetesClient) Clientset() kubernetes.Interface {
	args := m.Called()
	return args.Get(0).(kubernetes.Interface)
}

// DynamicClient mocks the DynamicClient method
func (m *MockKubernetesClient) DynamicClient() dynamic.Interface {
	args := m.Called()
	return args.Get(0).(dynamic.Interface)
}

// RESTConfig mocks the RESTConfig method
func (m *MockKubernetesClient) RESTConfig() *rest.Config {
	args := m.Called()
	return args.Get(0).(*rest.Config)
}

// InformerFactory mocks the InformerFactory method
func (m *MockKubernetesClient) InformerFactory() informers.SharedInformerFactory {
	args := m.Called()
	return args.Get(0).(informers.SharedInformerFactory)
}

// LlmsafespaceV1 mocks the LlmsafespaceV1 method
func (m *MockKubernetesClient) LlmsafespaceV1() interfaces.LLMSafespaceV1Interface {
	args := m.Called()
	return args.Get(0).(interfaces.LLMSafespaceV1Interface)
}

// ExecuteInSandbox mocks the ExecuteInSandbox method
func (m *MockKubernetesClient) ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

// ExecuteStreamInSandbox mocks the ExecuteStreamInSandbox method
func (m *MockKubernetesClient) ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

// ListFilesInSandbox mocks the ListFilesInSandbox method
func (m *MockKubernetesClient) ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileList), args.Error(1)
}

// DownloadFileFromSandbox mocks the DownloadFileFromSandbox method
func (m *MockKubernetesClient) DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

// UploadFileToSandbox mocks the UploadFileToSandbox method
func (m *MockKubernetesClient) UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileResult), args.Error(1)
}

// DeleteFileInSandbox mocks the DeleteFileInSandbox method
func (m *MockKubernetesClient) DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error {
	args := m.Called(ctx, namespace, name, fileReq)
	return args.Error(0)
}
