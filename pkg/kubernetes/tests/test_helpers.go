package tests

import (
	"context"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/logger"
	"github.com/lenaxia/llmsafespace/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// SetupTestClient creates a mock client with common mocks set up
func SetupTestClient(t *testing.T) (*kmocks.MockKubernetesClient, *kmocks.MockLLMSafespaceV1Interface, *kmocks.MockSandboxInterface) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
	// Setup LlmsafespaceV1 mock
	v1Client := kmocks.NewMockLLMSafespaceV1Interface()
	client.On("LlmsafespaceV1").Return(v1Client)
	
	// Setup Sandboxes mock
	sandboxClient := kmocks.NewMockSandboxInterface()
	v1Client.On("Sandboxes", "test-namespace").Return(sandboxClient)
	
	return client, v1Client, sandboxClient
}

// SetupTestSandbox creates a mock sandbox with a pod name
func SetupTestSandbox(name, namespace, podName string) *types.Sandbox {
	factory := mocks.NewMockFactory()
	sandbox := factory.NewSandbox(name, namespace, "python:3.10")
	sandbox.Status.PodName = podName
	return sandbox
}

// SetupTestExecutionRequest creates a test execution request
func SetupTestExecutionRequest(requestType, content string, timeout int) *types.ExecutionRequest {
	return &types.ExecutionRequest{
		Type:    requestType,
		Content: content,
		Timeout: timeout,
	}
}

// SetupTestFileRequest creates a test file request
func SetupTestFileRequest(path string, isDir bool, content []byte) *types.FileRequest {
	return &types.FileRequest{
		Path:    path,
		IsDir:   isDir,
		Content: content,
	}
}

// SetupTestLogger creates a test logger
func SetupTestLogger() *logger.Logger {
	log, _ := logger.New(true, "debug", "console")
	return log
}

// SetupTestClient creates a real client with fake components for testing
func SetupRealTestClient(t *testing.T) *kubernetes.Client {
	// Create fake clientset
	clientset := fake.NewSimpleClientset()
	
	// Create REST config
	restConfig := &rest.Config{
		Host: "https://localhost:8443",
	}
	
	// Create logger
	log := SetupTestLogger()
	
	// Create client
	client := kubernetes.NewForTesting(clientset, nil, restConfig, nil, log)
	
	return client
}

// AssertExecutionResult verifies an execution result
func AssertExecutionResult(t *testing.T, result *types.ExecutionResult, expectedStatus string, expectedExitCode int, expectedStdout, expectedStderr string) {
	assert.NotNil(t, result)
	assert.Equal(t, expectedStatus, result.Status)
	assert.Equal(t, expectedExitCode, result.ExitCode)
	assert.Equal(t, expectedStdout, result.Stdout)
	assert.Equal(t, expectedStderr, result.Stderr)
	assert.False(t, result.StartedAt.IsZero())
	assert.False(t, result.CompletedAt.IsZero())
}

// AssertFileResult verifies a file result
func AssertFileResult(t *testing.T, result *types.FileResult, expectedPath string, expectedIsDir bool) {
	assert.NotNil(t, result)
	assert.Equal(t, expectedPath, result.Path)
	assert.Equal(t, expectedIsDir, result.IsDir)
	assert.False(t, result.CreatedAt.IsZero())
	assert.False(t, result.UpdatedAt.IsZero())
}

// AssertFileList verifies a file list
func AssertFileList(t *testing.T, result *types.FileList, expectedCount int) {
	assert.NotNil(t, result)
	assert.Len(t, result.Files, expectedCount)
}

// MockCommandExecution mocks the ExecuteCommand method
func MockCommandExecution(client *kmocks.MockKubernetesClient, namespace, podName string, stdout, stderr string, exitCode int, err error) *mock.Call {
	return client.On("ExecuteCommand", mock.Anything, namespace, podName, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		options := args.Get(4).(*kubernetes.ExecOptions)
		if options.Stdout != nil && stdout != "" {
			options.Stdout.Write([]byte(stdout))
		}
		if options.Stderr != nil && stderr != "" {
			options.Stderr.Write([]byte(stderr))
		}
	}).Return(exitCode, err)
}

// RunTestWithTimeout runs a test function with a timeout
func RunTestWithTimeout(t *testing.T, testFunc func(ctx context.Context), timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	done := make(chan struct{})
	go func() {
		testFunc(ctx)
		close(done)
	}()
	
	select {
	case <-done:
		// Test completed successfully
	case <-ctx.Done():
		t.Fatalf("Test timed out after %v", timeout)
	}
}
