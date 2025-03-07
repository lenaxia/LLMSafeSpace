package file

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

func (m *MockK8sClient) Clientset() interface{} {
	args := m.Called()
	return args.Get(0)
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
	service, err := New(log, &kubernetes.Client{})
	// Replace the client with our mock
	service.k8sClient = mockK8sClient
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, mockK8sClient, service.k8sClient)

	mockK8sClient.AssertExpectations(t)
}

func TestListFiles(t *testing.T) {
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

	// Mock the exec command
	// Note: We can't fully test this without mocking the exec interface,
	// which is quite complex. We'll just test the error case.

	// Test case: Pod not found
	ctx := context.Background()
	_, err := service.ListFiles(ctx, sandbox, "/workspace")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get pod")

	mockK8sClient.AssertExpectations(t)
}

func TestDownloadFile(t *testing.T) {
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
	_, err := service.DownloadFile(ctx, sandbox, "/workspace/file.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get pod")

	mockK8sClient.AssertExpectations(t)
}

func TestUploadFile(t *testing.T) {
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
	content := []byte("test content")
	_, err := service.UploadFile(ctx, sandbox, "/workspace/file.txt", content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get pod")

	mockK8sClient.AssertExpectations(t)
}

func TestDeleteFile(t *testing.T) {
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
	err := service.DeleteFile(ctx, sandbox, "/workspace/file.txt")
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

	// Test case: Pod found
	ctx := context.Background()
	// Since getPod is not exported, we'll need to test the public methods that use it
	// This is a placeholder for the actual test
	_, err := service.ListFiles(ctx, sandbox, "/workspace")
	assert.NoError(t, err)
	assert.NotNil(t, pod)
	assert.Equal(t, "test-pod", pod.Name)

	// Test case: Pod not found
	sandbox.Status.PodName = "nonexistent"
	// Since getPod is not exported, we'll need to test the public methods that use it
	// This is a placeholder for the actual test
	_, err = service.ListFiles(ctx, sandbox, "/workspace")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test case: Missing pod name
	sandbox.Status.PodName = ""
	// This is a private method, we need to test through public methods
	_, err = service.ListFiles(ctx, sandbox, "/workspace")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pod name not set")

	mockK8sClient.AssertExpectations(t)
}

func TestValidatePath(t *testing.T) {
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

	// Test cases
	testCases := []struct {
		path      string
		expectErr bool
		errMsg    string
	}{
		{"/workspace/file.txt", false, ""},
		{"/workspace/dir/file.txt", false, ""},
		{"../etc/passwd", true, "path must be absolute"},
		{"", true, "path cannot be empty"},
		{"/etc/passwd", true, "path must be under /workspace"},
		{"/workspace/../etc/passwd", true, "path contains invalid components"},
	}

	for _, tc := range testCases {
		err := service.validatePath(tc.path)
		if tc.expectErr {
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestParseFileInfo(t *testing.T) {
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
		output    string
		expectErr bool
		numFiles  int
	}{
		{
			output: `-rw-r--r-- 1 user user 1024 Jan 1 12:00 file1.txt
-rw-r--r-- 1 user user 2048 Jan 2 13:00 file2.txt
drwxr-xr-x 2 user user 4096 Jan 3 14:00 dir1`,
			expectErr: false,
			numFiles:  3,
		},
		{
			output:    "invalid output",
			expectErr: true,
			numFiles:  0,
		},
		{
			output:    "",
			expectErr: false,
			numFiles:  0,
		},
	}

	for _, tc := range testCases {
		files, err := service.parseFileInfo(tc.output, "/workspace")
		if tc.expectErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
			assert.Len(t, files, tc.numFiles)
			
			if tc.numFiles > 0 {
				// Check first file
				assert.Equal(t, "/workspace/file1.txt", files[0].Path)
				assert.Equal(t, int64(1024), files[0].Size)
				assert.False(t, files[0].IsDirectory)
				
				// Check directory
				assert.Equal(t, "/workspace/dir1", files[2].Path)
				assert.True(t, files[2].IsDirectory)
			}
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
	_, err := service.execCommand(ctx, pod, []string{"ls", "-la"})
	assert.Error(t, err)

	mockK8sClient.AssertExpectations(t)
}
