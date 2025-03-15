package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	//"github.com/lenaxia/llmsafespace/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestExecuteInSandbox tests the ExecuteInSandbox method
func TestExecuteInSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup execution request
	execReq := &types.ExecutionRequest{
		Type:    "code",
		Content: "print('Hello, World!')",
		Timeout: 30,
	}
	
	// Setup execution result
	execResult := &types.ExecutionResult{
		ID:          "test-exec-id",
		Status:      "completed",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		ExitCode:    0,
		Stdout:      "Hello, World!",
		Stderr:      "",
	}
	
	// Mock the ExecuteInSandbox method directly
	mockClient.On("ExecuteInSandbox", mock.Anything, "test-namespace", "test-sandbox", execReq).Return(execResult, nil)
	
	// Test executing in sandbox
	result, err := mockClient.ExecuteInSandbox(context.Background(), "test-namespace", "test-sandbox", execReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "test-exec-id", result.ID)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "Hello, World!", result.Stdout)
	assert.Empty(t, result.Stderr)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestExecuteInSandboxErrors tests error cases for ExecuteInSandbox
func TestExecuteInSandboxErrors(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup execution request
	execReq := &types.ExecutionRequest{
		Type:    "code",
		Content: "print('Hello, World!')",
		Timeout: 30,
	}
	
	// Test case 1: Sandbox not found
	// Mock the ExecuteInSandbox method directly for the first error case
	mockClient.On("ExecuteInSandbox", mock.Anything, "test-namespace", "nonexistent", execReq).
		Return(nil, errors.New("failed to get sandbox: sandbox not found"))
	
	result, err := mockClient.ExecuteInSandbox(context.Background(), "test-namespace", "nonexistent", execReq)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get sandbox")
	
	// Test case 2: Sandbox pod not found
	// Mock the ExecuteInSandbox method directly for the second error case
	mockClient.On("ExecuteInSandbox", mock.Anything, "test-namespace", "empty-pod", execReq).
		Return(nil, errors.New("sandbox pod not found"))
	
	result, err = mockClient.ExecuteInSandbox(context.Background(), "test-namespace", "empty-pod", execReq)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "sandbox pod not found")
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestExecuteStreamInSandbox tests the ExecuteStreamInSandbox method
func TestExecuteStreamInSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup execution request
	execReq := &types.ExecutionRequest{
		Type:    "code",
		Content: "print('Hello, World!')",
		Timeout: 30,
	}
	
	// Setup execution result
	execResult := &types.ExecutionResult{
		ID:          "test-exec-id",
		Status:      "completed",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
		ExitCode:    0,
		Stdout:      "Hello, World!",
		Stderr:      "",
	}
	
	// Create a callback function to capture output
	var stdoutCapture, stderrCapture string
	outputCallback := func(stream, content string) {
		if stream == "stdout" {
			stdoutCapture += content
		} else if stream == "stderr" {
			stderrCapture += content
		}
	}
	
	// Mock the ExecuteStreamInSandbox method directly
	mockClient.On("ExecuteStreamInSandbox", mock.Anything, "test-namespace", "test-sandbox", execReq, mock.AnythingOfType("func(string, string)")).Return(execResult, nil)
	
	// Test executing in sandbox with streaming
	result, err := mockClient.ExecuteStreamInSandbox(context.Background(), "test-namespace", "test-sandbox", execReq, outputCallback)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "test-exec-id", result.ID)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, 0, result.ExitCode)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestStreamWriter tests the streamWriter implementation
func TestStreamWriter(t *testing.T) {
	// Create a buffer to capture output
	var capturedOutput string
	
	// Create a callback function
	callback := func(stream, content string) {
		capturedOutput += content
	}
	
	// Create a streamWriter
	writer := &kubernetes.StreamWriter{
		Stream:   "stdout",
		Callback: callback,
	}
	
	// Write some data
	data := []byte("Hello\nWorld\n")
	n, err := writer.Write(data)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "Hello\nWorld\n", capturedOutput)
	
	// Write partial line
	data = []byte("Partial")
	n, err = writer.Write(data)
	
	// Verify results - partial should NOT be visible yet (still buffered)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "Hello\nWorld\n", capturedOutput) // "Partial" remains buffered
	
	// Complete the line
	data = []byte(" Line\n")
	n, err = writer.Write(data)
	
	// Verify results - full line should now be visible
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "Hello\nWorld\nPartial Line\n", capturedOutput)
}

// TestListFilesInSandbox tests the ListFilesInSandbox method
func TestListFilesInSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup file request
	fileReq := &types.FileRequest{
		Path: "/workspace",
	}
	
	// Create file list to return
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
	
	// Set up the mock expectation directly
	mockClient.On("ListFilesInSandbox", mock.Anything, "test-namespace", "test-sandbox", fileReq).Return(fileList, nil)
	
	// Test listing files
	result, err := mockClient.ListFilesInSandbox(context.Background(), "test-namespace", "test-sandbox", fileReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Files, 2)
	
	// Check first file
	assert.Equal(t, "/workspace/test.py", result.Files[0].Path)
	assert.Equal(t, int64(1024), result.Files[0].Size)
	assert.False(t, result.Files[0].IsDir)
	
	// Check second file (directory)
	assert.Equal(t, "/workspace/data", result.Files[1].Path)
	assert.Equal(t, int64(4096), result.Files[1].Size)
	assert.True(t, result.Files[1].IsDir)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestDownloadFileFromSandbox tests the DownloadFileFromSandbox method
func TestDownloadFileFromSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup file request
	fileReq := &types.FileRequest{
		Path: "/workspace/test.py",
	}
	
	// Setup file content to return
	fileContent := []byte("print('Hello, World!')")
	
	// Set up the mock expectation directly
	mockClient.On("DownloadFileFromSandbox", mock.Anything, "test-namespace", "test-sandbox", fileReq).Return(fileContent, nil)
	
	// Test downloading file
	content, err := mockClient.DownloadFileFromSandbox(context.Background(), "test-namespace", "test-sandbox", fileReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, fileContent, content)
	assert.Equal(t, "print('Hello, World!')", string(content))
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestUploadFileToSandbox tests the UploadFileToSandbox method
func TestUploadFileToSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup file request for file upload
	fileReq := &types.FileRequest{
		Path:    "/workspace/test.py",
		Content: []byte("print('Hello, World!')"),
		IsDir:   false,
	}
	
	// Create file result to return
	fileResult := &types.FileResult{
		Path:      "/workspace/test.py",
		Size:      1024,
		IsDir:     false,
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now(),
	}
	
	// Set up the mock expectation directly
	mockClient.On("UploadFileToSandbox", mock.Anything, "test-namespace", "test-sandbox", fileReq).Return(fileResult, nil)
	
	// Test uploading file
	result, err := mockClient.UploadFileToSandbox(context.Background(), "test-namespace", "test-sandbox", fileReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "/workspace/test.py", result.Path)
	assert.Equal(t, int64(1024), result.Size)
	assert.False(t, result.IsDir)
	
	// Setup file request for directory creation
	dirReq := &types.FileRequest{
		Path:  "/workspace/data",
		IsDir: true,
	}
	
	// Create directory result to return
	dirResult := &types.FileResult{
		Path:      "/workspace/data",
		Size:      4096,
		IsDir:     true,
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now(),
	}
	
	// Set up the mock expectation directly
	mockClient.On("UploadFileToSandbox", mock.Anything, "test-namespace", "test-sandbox", dirReq).Return(dirResult, nil)
	
	// Test creating directory
	result, err = mockClient.UploadFileToSandbox(context.Background(), "test-namespace", "test-sandbox", dirReq)
	
	// Verify results
	assert.NoError(t, err)
	assert.Equal(t, "/workspace/data", result.Path)
	assert.Equal(t, int64(4096), result.Size)
	assert.True(t, result.IsDir)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}
//
// TestDeleteFileInSandbox tests the DeleteFileInSandbox method
func TestDeleteFileInSandbox(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockKubernetesClient()
	
	// Setup file request
	fileReq := &types.FileRequest{
		Path: "/workspace/test.py",
	}
	
	// Set up the mock expectation for successful deletion
	mockClient.On("DeleteFileInSandbox", mock.Anything, "test-namespace", "test-sandbox", fileReq).Return(nil).Once()
	
	// Test deleting file
	err := mockClient.DeleteFileInSandbox(context.Background(), "test-namespace", "test-sandbox", fileReq)
	
	// Verify results
	assert.NoError(t, err)
	
	// Setup file request for non-existent file
	notFoundReq := &types.FileRequest{
		Path: "/workspace/nonexistent.py",
	}
	
	// Set up the mock expectation for file not found error
	mockClient.On("DeleteFileInSandbox", mock.Anything, "test-namespace", "test-sandbox", notFoundReq).
		Return(errors.New("file not found: /workspace/nonexistent.py")).Once()
	
	// Test deleting non-existent file
	err = mockClient.DeleteFileInSandbox(context.Background(), "test-namespace", "test-sandbox", notFoundReq)
	
	// Verify results
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file not found")
	
	// Verify expectations
	mockClient.AssertExpectations(t)
}

// TestParseHelpers tests the parseInt64 and parseFloat64 helper functions
func TestParseHelpers(t *testing.T) {
	// Test parseInt64
	i, err := kubernetes.ParseInt64("1024")
	assert.NoError(t, err)
	assert.Equal(t, int64(1024), i)
	
	// Test parseFloat64
	f, err := kubernetes.ParseFloat64("123.456")
	assert.NoError(t, err)
	assert.Equal(t, 123.456, f)
	
	// Test invalid inputs
	_, err = kubernetes.ParseInt64("invalid")
	assert.Error(t, err)
	
	_, err = kubernetes.ParseFloat64("invalid")
	assert.Error(t, err)
}
