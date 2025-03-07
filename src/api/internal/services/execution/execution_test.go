package execution

import (
	"context"
	"testing"
	
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
	kubernetes.Client
}

func (m *MockK8sClient) Clientset() clientset.Interface {
	args := m.Called()
	return args.Get(0).(clientset.Interface)
}

func (m *MockK8sClient) RESTConfig() *rest.Config {
	args := m.Called()
	return args.Get(0).(*rest.Config)
}

func (m *MockK8sClient) ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *kubernetes.ExecutionRequest) (*kubernetes.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq)
	return args.Get(0).(*kubernetes.ExecutionResult), args.Error(1)
}

func (m *MockK8sClient) ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *kubernetes.ExecutionRequest, outputCallback func(stream, content string)) (*kubernetes.ExecutionResult, error) {
	args := m.Called(ctx, namespace, name, execReq, outputCallback)
	return args.Get(0).(*kubernetes.ExecutionResult), args.Error(1)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Test successful creation
	service, err := New(log, &kubernetes.Client{})
	// Replace the client with our mock
	service.k8sClient = mockK8sClient
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
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Create the service
	service, _ := New(log, &kubernetes.Client{})
	// Replace the client with our mock
	var k8sClientInterface kubernetes.Client = mockK8sClient
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
		},
		Status: llmsafespacev1.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

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
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Create the service
	service, _ := New(log, &kubernetes.Client{})
	// Replace the client with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
		},
		Status: llmsafespacev1.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

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
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Create the service
	service, _ := New(log, &kubernetes.Client{})
	// Replace the client with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: llmsafespacev1.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

	// Test case: Pod not found
	ctx := context.Background()
	packages := []string{"numpy", "pandas"}
	_, err := service.InstallPackages(ctx, sandbox, packages, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute in sandbox")

	mockK8sClient.AssertExpectations(t)
}
