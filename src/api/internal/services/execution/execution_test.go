package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
}

func (m *MockK8sClient) Clientset() kubernetes.Interface {
	args := m.Called()
	return args.Get(0).(kubernetes.Interface)
}

func (m *MockK8sClient) RESTConfig() *rest.Config {
	args := m.Called()
	return args.Get(0).(*rest.Config)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Test successful creation
	service, err := New(log, mockK8sClient)
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
	service, _ := New(log, mockK8sClient)

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
	assert.Contains(t, err.Error(), "failed to get pod")

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
	service, _ := New(log, mockK8sClient)

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
	assert.Contains(t, err.Error(), "failed to get pod")

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
	service, _ := New(log, mockK8sClient)

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
	assert.Contains(t, err.Error(), "failed to get pod")

	mockK8sClient.AssertExpectations(t)
}

func TestGetPod(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a fake clientset with a pod
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	})
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(clientset)

	// Create the service
	service := &Service{
		logger:    log,
		k8sClient: mockK8sClient,
	}

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

	// Test case: Pod found
	ctx := context.Background()
	pod, err := service.getPod(ctx, sandbox)
	assert.NoError(t, err)
	assert.NotNil(t, pod)
	assert.Equal(t, "test-pod", pod.Name)

	// Test case: Pod not found
	sandbox.Status.PodName = "nonexistent"
	_, err = service.getPod(ctx, sandbox)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test case: Missing pod name
	sandbox.Status.PodName = ""
	_, err = service.getPod(ctx, sandbox)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pod name not set")

	mockK8sClient.AssertExpectations(t)
}

func TestGetPackageManager(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Test cases
	testCases := []struct {
		runtime       string
		manager       string
		expectedCmd   string
		expectedArgs  []string
		expectDefault bool
	}{
		{"python:3.10", "", "pip", []string{"install", "-U"}, true},
		{"python:3.10", "pip", "pip", []string{"install", "-U"}, false},
		{"python:3.10", "conda", "conda", []string{"install", "-y"}, false},
		{"nodejs:16", "", "npm", []string{"install", "-g"}, true},
		{"nodejs:16", "npm", "npm", []string{"install", "-g"}, false},
		{"nodejs:16", "yarn", "yarn", []string{"global", "add"}, false},
		{"ruby:3.0", "", "gem", []string{"install"}, true},
		{"go:1.18", "", "go", []string{"install"}, true},
		{"unknown:1.0", "", "", nil, true},
	}

	for _, tc := range testCases {
		cmd, args, err := service.getPackageManager(tc.runtime, tc.manager)
		if tc.expectedCmd == "" {
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported runtime")
		} else {
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedCmd, cmd)
			assert.Equal(t, tc.expectedArgs, args)
		}
	}
}

func TestExecCommand(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{
		Host: "https://localhost:8443",  // Invalid host to force error
	})

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Test case: Exec error
	ctx := context.Background()
	_, _, err := service.execCommand(ctx, pod, []string{"ls", "-la"})
	assert.Error(t, err)

	mockK8sClient.AssertExpectations(t)
}

func TestExecCodeFile(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{
		Host: "https://localhost:8443",  // Invalid host to force error
	})

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Test case: Python code
	ctx := context.Background()
	_, _, err := service.execCodeFile(ctx, pod, "python", "print('Hello, World!')")
	assert.Error(t, err)  // Will fail due to invalid REST config

	// Test case: Unsupported language
	_, _, err = service.execCodeFile(ctx, pod, "unsupported", "code")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported language")

	mockK8sClient.AssertExpectations(t)
}

func TestGetInterpreter(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Test cases
	testCases := []struct {
		language      string
		expectedCmd   string
		expectedExt   string
		expectDefault bool
	}{
		{"python", "python", ".py", false},
		{"javascript", "node", ".js", false},
		{"nodejs", "node", ".js", false},
		{"ruby", "ruby", ".rb", false},
		{"go", "go run", ".go", false},
		{"bash", "bash", ".sh", false},
		{"sh", "sh", ".sh", false},
		{"unsupported", "", "", true},
	}

	for _, tc := range testCases {
		cmd, ext, err := service.getInterpreter(tc.language)
		if tc.expectDefault {
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported language")
		} else {
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedCmd, cmd)
			assert.Equal(t, tc.expectedExt, ext)
		}
	}
}
