package tests

import (
	"context"
	"testing"

	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	"github.com/lenaxia/llmsafespace/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// TestMockKubernetesClient tests the MockKubernetesClient implementation
func TestMockKubernetesClient(t *testing.T) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
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

// TestMockSandboxOperations tests the mock sandbox operations
func TestMockSandboxOperations(t *testing.T) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
	// Setup LlmsafespaceV1 mock
	v1Client := kmocks.NewMockLLMSafespaceV1Interface()
	client.On("LlmsafespaceV1").Return(v1Client)
	
	// Setup Sandboxes mock with List method
	sandboxClient := kmocks.NewMockSandboxInterface()
	sandboxClient.SetupListMock()
	v1Client.On("Sandboxes", "test-namespace").Return(sandboxClient)
	
	// Setup Get mock
	factory := mocks.NewMockFactory()
	sandbox := factory.NewSandbox("test-sandbox", "test-namespace", "python:3.10")
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

// TestMockExecuteInSandbox tests the mock ExecuteInSandbox method
func TestMockExecuteInSandbox(t *testing.T) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
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

// TestMockFileOperations tests the mock file operations
func TestMockFileOperations(t *testing.T) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
	// Setup ListFilesInSandbox mock
	fileReq := &types.FileRequest{
		Path: "/workspace",
	}
	
	fileList := &types.FileList{
		Files: []types.FileInfo{
			mocks.MockFileInfo("/workspace/test.py", 1024, false),
			mocks.MockFileInfo("/workspace/data", 4096, true),
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

// TestMockWatch tests the mock watch implementation
func TestMockWatch(t *testing.T) {
	// Create a mock watch
	mockWatch := kmocks.NewMockWatch()
	mockWatch.On("ResultChan").Return(mockWatch.ResultChan())
	mockWatch.On("Stop").Return()
	
	// Setup sandbox client
	sandboxClient := kmocks.NewMockSandboxInterface()
	sandboxClient.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil)
	
	// Start watching
	watcher, err := sandboxClient.Watch(metav1.ListOptions{})
	assert.NoError(t, err)
	
	// Send an event
	factory := mocks.NewMockFactory()
	sandbox := factory.NewSandbox("test-sandbox", "test-namespace", "python:3.10")
	go func() {
		mockWatch.SendEvent(watch.EventType("ADDED"), sandbox)
	}()
	
	// Receive the event
	event := <-watcher.ResultChan()
	assert.Equal(t, watch.Added, event.Type)
	assert.Equal(t, "test-sandbox", event.Object.(*types.Sandbox).Name)
	
	// Stop watching
	watcher.Stop()
	
	// Verify expectations
	mockWatch.AssertExpectations(t)
	sandboxClient.AssertExpectations(t)
}

// TestSetupHelperMethods tests the setup helper methods
func TestSetupHelperMethods(t *testing.T) {
	// Create a mock client
	client := kmocks.NewMockKubernetesClient()
	
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

// TestMockInterfaces tests the mock interfaces
func TestMockInterfaces(t *testing.T) {
	// Create mock interfaces
	v1Client := kmocks.NewMockLLMSafespaceV1Interface()
	sandboxClient := kmocks.NewMockSandboxInterface()
	warmPoolClient := kmocks.NewMockWarmPoolInterface()
	warmPodClient := kmocks.NewMockWarmPodInterface()
	runtimeEnvClient := kmocks.NewMockRuntimeEnvironmentInterface()
	profileClient := kmocks.NewMockSandboxProfileInterface()
	
	// Setup mock methods with proper Watch expectations
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Set up Sandboxes mock
	sandboxMock := v1Client.SetupSandboxesMock("test-namespace")
	sandboxMock.SetupListMock()
	sandboxMock.SetupWatchMock()
	
	// Set up WarmPools mock
	warmPoolMock := v1Client.SetupWarmPoolsMock("test-namespace")
	warmPoolMock.SetupListMock()
	warmPoolMock.SetupWatchMock()
	
	// Set up WarmPods mock
	warmPodMock := v1Client.SetupWarmPodsMock("test-namespace")
	warmPodMock.SetupListMock()
	warmPodMock.SetupWatchMock()
	
	// Set up RuntimeEnvironments mock
	runtimeEnvMock := v1Client.SetupRuntimeEnvironmentsMock("test-namespace")
	runtimeEnvMock.SetupListMock()
	runtimeEnvMock.SetupWatchMock()
	
	// Set up SandboxProfiles mock
	profileMock := v1Client.SetupSandboxProfilesMock("test-namespace")
	profileMock.SetupListMock()
	profileMock.SetupWatchMock()
	
	sandboxClient.SetupCreateMock()
	sandboxClient.SetupUpdateMock()
	sandboxClient.SetupUpdateStatusMock()
	sandboxClient.SetupDeleteMock()
	sandboxClient.SetupGetMock("test-sandbox")
	sandboxClient.SetupListMock()
	
	warmPoolClient.SetupCreateMock()
	warmPoolClient.SetupUpdateMock()
	warmPoolClient.SetupUpdateStatusMock()
	warmPoolClient.SetupDeleteMock()
	warmPoolClient.SetupGetMock("test-warmpool")
	warmPoolClient.SetupListMock()
	
	warmPodClient.SetupCreateMock()
	warmPodClient.SetupUpdateMock()
	warmPodClient.SetupUpdateStatusMock()
	warmPodClient.SetupDeleteMock()
	warmPodClient.SetupGetMock("test-warmpod")
	warmPodClient.SetupListMock()
	
	runtimeEnvClient.SetupCreateMock()
	runtimeEnvClient.SetupUpdateMock()
	runtimeEnvClient.SetupUpdateStatusMock()
	runtimeEnvClient.SetupDeleteMock()
	runtimeEnvClient.SetupGetMock("test-runtime")
	runtimeEnvClient.SetupListMock()
	
	profileClient.SetupCreateMock()
	profileClient.SetupUpdateMock()
	profileClient.SetupDeleteMock()
	profileClient.SetupGetMock("test-profile")
	profileClient.SetupListMock()
	
	// Test interface methods
	assert.NotNil(t, v1Client.Sandboxes("test-namespace"))
	assert.NotNil(t, v1Client.WarmPools("test-namespace"))
	assert.NotNil(t, v1Client.WarmPods("test-namespace"))
	assert.NotNil(t, v1Client.RuntimeEnvironments("test-namespace"))
	assert.NotNil(t, v1Client.SandboxProfiles("test-namespace"))
	
	// Test sandbox methods
	factory := mocks.NewMockFactory()
	sandbox := factory.NewSandbox("test-sandbox", "test-namespace", "python:3.10")
	result, err := sandboxClient.Create(sandbox)
	assert.NoError(t, err)
	assert.Equal(t, "test-sandbox", result.Name)
	
	// Verify expectations
	v1Client.AssertExpectations(t)
	sandboxClient.AssertExpectations(t)
	warmPoolClient.AssertExpectations(t)
	warmPodClient.AssertExpectations(t)
	runtimeEnvClient.AssertExpectations(t)
	profileClient.AssertExpectations(t)
}
