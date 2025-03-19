package execution

import (
	"context"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
	interfaces.KubernetesClient
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

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create mock service instances
	mockK8sClient := new(MockK8sClient)
	mockMetrics := new(mocks.MockMetricsService)
	
	// Test successful creation
	service, err := New(log, mockK8sClient, mockMetrics)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, mockK8sClient, service.k8sClient)
	assert.Equal(t, mockMetrics, service.metrics)

	mockK8sClient.AssertExpectations(t)
}

func TestExecute(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create mock service instances
	mockK8sClient := new(MockK8sClient)
	mockMetrics := new(mocks.MockMetricsService)

	// Create the service
	service, _ := New(log, mockK8sClient, mockMetrics)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
			Labels: map[string]string{
				"user": "test-user",
			},
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
	mockK8sClient.On("ExecuteInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.ExecutionRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.Timeout == 30
	})).Return(&types.ExecutionResult{
		ID:          "test-exec-1",
		Status:      "completed",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		ExitCode:    0,
		Stdout:      "Hello, World!\n",
		Stderr:      "",
	}, nil).Once()

	// Set up mock expectation for RecordExecution
	mockMetrics.On("RecordExecution", "code", "python:3.10", "completed", "test-user", mock.AnythingOfType("time.Duration")).Return()

	// Test case: Successful execution
	ctx := context.Background()
	result, err := service.Execute(ctx, sandbox, "code", "print('Hello, World!')", 30)
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "Hello, World!\n", result.Stdout)

	mockK8sClient.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

func TestExecuteStream(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create mock service instances
	mockK8sClient := new(MockK8sClient)
	mockMetrics := new(mocks.MockMetricsService)

	// Create the service
	service, _ := New(log, mockK8sClient, mockMetrics)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
			Labels: map[string]string{
				"user": "test-user",
			},
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: types.SandboxStatus{
			PodName:      "test-pod",
			PodNamespace: "default",
		},
	}

	// Set up mock expectation for ExecuteStreamInSandbox
	mockK8sClient.On("ExecuteStreamInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.ExecutionRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.Timeout == 30 && req.Stream == true
	}), mock.AnythingOfType("func(string, string)")).Return(&types.ExecutionResult{
		ID:          "test-exec-2",
		Status:      "completed",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		ExitCode:    0,
		Stdout:      "Hello, World!\n",
		Stderr:      "",
	}, nil).Once()

	// Set up mock expectation for RecordExecution
	mockMetrics.On("RecordExecution", "code", "python:3.10", "completed", "test-user", mock.AnythingOfType("time.Duration")).Return()

	// Test case: Successful stream execution
	ctx := context.Background()
	outputCallback := func(stream, content string) {}
	result, err := service.ExecuteStream(ctx, sandbox, "code", "print('Hello, World!')", 30, outputCallback)
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "Hello, World!\n", result.Stdout)

	mockK8sClient.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

func TestInstallPackages(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create mock service instances
	mockK8sClient := new(MockK8sClient)
	mockMetrics := new(mocks.MockMetricsService)

	// Create the service
	service, _ := New(log, mockK8sClient, mockMetrics)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
			Labels: map[string]string{
				"user": "test-user",
			},
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
	mockK8sClient.On("ExecuteInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.ExecutionRequest) bool {
		return req.Type == "command" && req.Content == "pip install numpy pandas" && req.Timeout == 300
	})).Return(&types.ExecutionResult{
		ID:          "test-exec-3",
		Status:      "completed",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		ExitCode:    0,
		Stdout:      "Successfully installed numpy pandas\n",
		Stderr:      "",
	}, nil).Once()

	// Set up mock expectations for metrics
	mockMetrics.On("RecordExecution", "command", "python:3.10", "completed", "test-user", mock.AnythingOfType("time.Duration")).Return()
	mockMetrics.On("RecordPackageInstallation", "python:3.10", "pip", "completed").Return()

	// Test case: Successful package installation
	ctx := context.Background()
	packages := []string{"numpy", "pandas"}
	result, err := service.InstallPackages(ctx, sandbox, packages, "")
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "Successfully installed")

	mockK8sClient.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}
