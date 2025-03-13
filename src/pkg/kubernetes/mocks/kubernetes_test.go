package mocks

import (
	"context"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/logger/mock"
	typesmock "github.com/lenaxia/llmsafespace/pkg/types/mock"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

func TestMockKubernetesClient(t *testing.T) {
	// Create a mock client
	client := NewMockKubernetesClient()
	
	// Test basic methods
	client.On("Start").Return(nil)
	client.On("Stop").Return()
	
	// Start and stop the client
	err := client.Start()
	assert.NoError(t, err)
	client.Stop()
	
	// Verify expectations
	client.AssertExpectations(t)
}

func TestMockSandboxOperations(t *testing.T) {
	// Create a mock client
	client := NewMockKubernetesClient()
	
	// Setup LlmsafespaceV1 mock
	v1Client := NewMockLLMSafespaceV1Interface()
	client.On("LlmsafespaceV1").Return(v1Client)
	
	// Setup Sandboxes mock
	sandboxClient := NewMockSandboxInterface()
	v1Client.On("Sandboxes", "test-namespace").Return(sandboxClient)
	
	// Setup Get mock
	sandbox := typesmock.NewMockSandbox("test-sandbox", "test-namespace")
	sandboxClient.On("Get", "test-sandbox", metav1.GetOptions{}).Return(sandbox, nil)
	
	// Test getting a sandbox
	result, err := client.LlmsafespaceV1().Sandboxes("test-namespace").Get("test-sandbox", metav1.GetOptions{})
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "test-sandbox", result.Name)
	assert.Equal(t, "test-namespace", result.Namespace)
	
	// Verify expectations
	client.AssertExpectations(t)
	v1Client.AssertExpectations(t)
	sandboxClient.AssertExpectations(t)
}

func TestMockExecuteInSandbox(t *testing.T) {
	// Create a mock client
	client := NewMockKubernetesClient()
	
	// Setup ExecuteInSandbox mock
	execReq := &types.ExecutionRequest{
		Type:    "code",
		Content: "print('Hello, World!')",
		Timeout: 30,
	}
	
	execResult := &types.ExecutionResult{
		ID:       "test-exec-id",
		Status:   "completed",
		ExitCode: 0,
		Stdout:   "Hello, World!",
		Stderr:   "",
	}
	
	client.On("ExecuteInSandbox", context.Background(), "test-namespace", "test-sandbox", execReq).Return(execResult, nil)
	
	// Test executing in sandbox
	result, err := client.ExecuteInSandbox(context.Background(), "test-namespace", "test-sandbox", execReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "test-exec-id", result.ID)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "Hello, World!", result.Stdout)
	assert.Empty(t, result.Stderr)
	
	// Verify expectations
	client.AssertExpectations(t)
}

func TestMockFileOperations(t *testing.T) {
	// Create a mock client
	client := NewMockKubernetesClient()
	
	// Setup ListFilesInSandbox mock
	fileReq := &types.FileRequest{
		Path: "/workspace",
	}
	
	fileList := &types.FileList{
		Files: []types.FileInfo{
			MockFileInfo("/workspace/test.py", 1024, false),
			MockFileInfo("/workspace/data", 4096, true),
		},
	}
	
	client.On("ListFilesInSandbox", context.Background(), "test-namespace", "test-sandbox", fileReq).Return(fileList, nil)
	
	// Test listing files
	result, err := client.ListFilesInSandbox(context.Background(), "test-namespace", "test-sandbox", fileReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Len(t, result.Files, 2)
	assert.Equal(t, "/workspace/test.py", result.Files[0].Path)
	assert.Equal(t, "/workspace/data", result.Files[1].Path)
	assert.False(t, result.Files[0].IsDir)
	assert.True(t, result.Files[1].IsDir)
	
	// Verify expectations
	client.AssertExpectations(t)
}

func TestMockWatch(t *testing.T) {
	// Create a mock watch
	mockWatch := NewMockWatch()
	mockWatch.On("ResultChan").Return(mockWatch.resultChan)
	mockWatch.On("Stop").Return()
	
	// Setup sandbox client
	sandboxClient := NewMockSandboxInterface()
	sandboxClient.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil)
	
	// Start watching
	watch, err := sandboxClient.Watch(metav1.ListOptions{})
	assert.NoError(t, err)
	
	// Send an event
	sandbox := typesmock.NewMockSandbox("test-sandbox", "test-namespace")
	go func() {
		mockWatch.SendEvent(watch.Added, sandbox)
	}()
	
	// Receive the event
	event := <-watch.ResultChan()
	assert.Equal(t, watch.Added, event.Type)
	assert.Equal(t, "test-sandbox", event.Object.(*types.Sandbox).Name)
	
	// Stop watching
	watch.Stop()
	
	// Verify expectations
	mockWatch.AssertExpectations(t)
	sandboxClient.AssertExpectations(t)
}

func TestSetupHelperMethods(t *testing.T) {
	// Create a mock client
	client := NewMockKubernetesClient()
	
	// Test setup helper methods
	client.SetupExecuteInSandboxMock(0)
	client.SetupListFilesInSandboxMock()
	client.SetupDownloadFileFromSandboxMock()
	client.SetupUploadFileToSandboxMock(false)
	client.SetupDeleteFileInSandboxMock()
	client.SetupLlmsafespaceV1Mock()
	
	// Use the client
	v1Client := client.LlmsafespaceV1()
	assert.NotNil(t, v1Client)
	
	// Execute in sandbox
	result, err := client.ExecuteInSandbox(context.Background(), "test-namespace", "test-sandbox", &types.ExecutionRequest{})
	assert.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, 0, result.ExitCode)
	
	// List files
	files, err := client.ListFilesInSandbox(context.Background(), "test-namespace", "test-sandbox", &types.FileRequest{})
	assert.NoError(t, err)
	assert.Len(t, files.Files, 2)
	
	// Download file
	content, err := client.DownloadFileFromSandbox(context.Background(), "test-namespace", "test-sandbox", &types.FileRequest{})
	assert.NoError(t, err)
	assert.Equal(t, "Test file content", string(content))
	
	// Upload file
	fileResult, err := client.UploadFileToSandbox(context.Background(), "test-namespace", "test-sandbox", &types.FileRequest{})
	assert.NoError(t, err)
	assert.Equal(t, "/workspace/test.py", fileResult.Path)
	
	// Delete file
	err = client.DeleteFileInSandbox(context.Background(), "test-namespace", "test-sandbox", &types.FileRequest{})
	assert.NoError(t, err)
	
	// Verify expectations
	client.AssertExpectations(t)
}

func TestMockLogger(t *testing.T) {
	// Create a mock logger
	logger := mock.NewTestLogger()
	
	// Setup expectations
	logger.On("Info", "Test message", []interface{}{"key", "value"}).Return()
	logger.On("Error", "Error message", assert.AnError, []interface{}{"key", "value"}).Return()
	
	// Use the logger
	logger.Info("Test message", "key", "value")
	logger.Error("Error message", assert.AnError, "key", "value")
	
	// Verify expectations
	logger.AssertExpectations(t)
}
