package execution

import (
	"context"
	"fmt"
	"testing"
	
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	k8s "k8s.io/client-go/kubernetes"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
}

func (m *MockK8sClient) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockK8sClient) Stop() {
	m.Called()
}

func (m *MockK8sClient) Clientset() k8s.Interface {
	args := m.Called()
	return args.Get(0).(k8s.Interface)
}

func (m *MockK8sClient) RESTConfig() *rest.Config {
	args := m.Called()
	return args.Get(0).(*rest.Config)
}

func (m *MockK8sClient) LlmsafespaceV1() k8sinterfaces.LLMSafespaceV1Interface {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(k8sinterfaces.LLMSafespaceV1Interface)
}

func (m *MockK8sClient) ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockK8sClient) ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq, outputCallback)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockK8sClient) ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileList), args.Error(1)
}

func (m *MockK8sClient) DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockK8sClient) UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.FileResult), args.Error(1)
}

func (m *MockK8sClient) DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error {
	args := m.Called(ctx, namespace, name, fileReq)
	return args.Error(0)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create mock service instance
	mockK8sClient := new(MockK8sClient)
	var k8sClient k8sinterfaces.KubernetesClient = mockK8sClient

	// Test successful creation
	service, err := New(log, k8sClient)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, mockK8sClient, service.k8sClient)

	mockK8sClient.AssertExpectations(t)
}

func TestExecute(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
		Status: types.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

	// Set up mock expectations - we don't need to expect these calls
	// since they're not actually used in the Execute method
	
	// Set up mock expectation for ExecuteInSandbox
	mockK8sClient.On("ExecuteInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.ExecutionRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.Timeout == 30
	})).Return(nil, fmt.Errorf("pod not found")).Once()

	// Test case: Pod not found
	ctx := context.Background()
	_, err := service.Execute(ctx, sandbox, "code", "print('Hello, World!')", 30)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute in sandbox")

	mockK8sClient.AssertExpectations(t)
}

func TestExecuteStream(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
		Status: types.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

	// Set up mock expectation for ExecuteStreamInSandbox
	mockK8sClient.On("ExecuteStreamInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.ExecutionRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.Timeout == 30 && req.Stream == true
	}), mock.AnythingOfType("func(string, string)")).Return(nil, fmt.Errorf("pod not found")).Once()

	// Test case: Pod not found
	ctx := context.Background()
	outputCallback := func(stream, content string) {}
	_, err := service.ExecuteStream(ctx, sandbox, "code", "print('Hello, World!')", 30, outputCallback)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute stream in sandbox")

	mockK8sClient.AssertExpectations(t)
}

func TestInstallPackages(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: types.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

	// Set up mock expectation for ExecuteInSandbox
	mockK8sClient.On("ExecuteInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.ExecutionRequest) bool {
		return req.Type == "command" && req.Content == "pip install numpy pandas" && req.Timeout == 300
	})).Return(nil, fmt.Errorf("pod not found")).Once()

	// Test case: Pod not found
	ctx := context.Background()
	packages := []string{"numpy", "pandas"}
	_, err := service.InstallPackages(ctx, sandbox, packages, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute in sandbox")

	mockK8sClient.AssertExpectations(t)
}
