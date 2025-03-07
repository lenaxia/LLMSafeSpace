package file

import (
	"context"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8s "k8s.io/client-go/kubernetes"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Mock implementations
type MockK8sClient struct {
	mock.Mock
	*kubernetes.Client
}

func (m *MockK8sClient) Clientset() k8s.Interface {
	args := m.Called()
	return args.Get(0).(k8s.Interface)
}

func (m *MockK8sClient) RESTConfig() *rest.Config {
	args := m.Called()
	return args.Get(0).(*rest.Config)
}

func (m *MockK8sClient) ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) (*kubernetes.FileList, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*kubernetes.FileList), args.Error(1)
}

func (m *MockK8sClient) DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) ([]byte, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockK8sClient) UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) (*kubernetes.FileResult, error) {
	args := m.Called(ctx, namespace, name, fileReq)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*kubernetes.FileResult), args.Error(1)
}

func (m *MockK8sClient) DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) error {
	args := m.Called(ctx, namespace, name, fileReq)
	return args.Error(0)
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	
	// Create a mock K8s client
	mockK8sClient := new(MockK8sClient)
	mockK8sClient.On("Clientset").Return(fake.NewSimpleClientset())
	mockK8sClient.On("RESTConfig").Return(&rest.Config{})

	// Test successful creation
	k8sClient := &kubernetes.Client{}
	service, err := New(log, k8sClient)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, log, service.logger)
	assert.Equal(t, k8sClient, service.k8sClient)

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
	k8sClient := &kubernetes.Client{}
	service, _ := New(log, k8sClient)
	// Replace with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful list
	mockK8sClient.On("ListFilesInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.FileRequest) bool {
		return req.Path == "/workspace"
	})).Return(&kubernetes.FileList{
		Files: []kubernetes.FileInfo{
			{
				Path: "/workspace/file.txt",
				Size: 100,
				IsDir: false,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}, nil).Once()

	files, err := service.ListFiles(context.Background(), sandbox, "/workspace")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "/workspace/file.txt", files[0].Path)

	// Test case: Error
	mockK8sClient.On("ListFilesInSandbox", mock.Anything, "default", "test-sandbox", mock.Anything).Return(nil, assert.AnError).Once()

	_, err = service.ListFiles(context.Background(), sandbox, "/workspace")
	assert.Error(t, err)

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
	k8sClient := &kubernetes.Client{}
	service, _ := New(log, k8sClient)
	// Replace with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful download
	mockK8sClient.On("DownloadFileFromSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.FileRequest) bool {
		return req.Path == "/workspace/file.txt"
	})).Return([]byte("test content"), nil).Once()

	content, err := service.DownloadFile(context.Background(), sandbox, "/workspace/file.txt")
	assert.NoError(t, err)
	assert.Equal(t, []byte("test content"), content)

	// Test case: Error
	mockK8sClient.On("DownloadFileFromSandbox", mock.Anything, "default", "test-sandbox", mock.Anything).Return(nil, assert.AnError).Once()

	_, err = service.DownloadFile(context.Background(), sandbox, "/workspace/file.txt")
	assert.Error(t, err)

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
	k8sClient := &kubernetes.Client{}
	service, _ := New(log, k8sClient)
	// Replace with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful upload
	mockK8sClient.On("UploadFileToSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.FileRequest) bool {
		return req.Path == "/workspace/file.txt" && string(req.Content) == "test content"
	})).Return(&kubernetes.FileResult{
		Path: "/workspace/file.txt",
		Size: 12,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil).Once()

	fileInfo, err := service.UploadFile(context.Background(), sandbox, "/workspace/file.txt", []byte("test content"))
	assert.NoError(t, err)
	assert.Equal(t, "/workspace/file.txt", fileInfo.Path)
	assert.Equal(t, int64(12), fileInfo.Size)

	// Test case: Error
	mockK8sClient.On("UploadFileToSandbox", mock.Anything, "default", "test-sandbox", mock.Anything).Return(nil, assert.AnError).Once()

	_, err = service.UploadFile(context.Background(), sandbox, "/workspace/file.txt", []byte("test content"))
	assert.Error(t, err)

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
	k8sClient := &kubernetes.Client{}
	service, _ := New(log, k8sClient)
	// Replace with our mock
	service.k8sClient = mockK8sClient

	// Create a test sandbox
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful delete
	mockK8sClient.On("DeleteFileInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *kubernetes.FileRequest) bool {
		return req.Path == "/workspace/file.txt"
	})).Return(nil).Once()

	err := service.DeleteFile(context.Background(), sandbox, "/workspace/file.txt")
	assert.NoError(t, err)

	// Test case: Error
	mockK8sClient.On("DeleteFileInSandbox", mock.Anything, "default", "test-sandbox", mock.Anything).Return(assert.AnError).Once()

	err = service.DeleteFile(context.Background(), sandbox, "/workspace/file.txt")
	assert.Error(t, err)

	mockK8sClient.AssertExpectations(t)
}
