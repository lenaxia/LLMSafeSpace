package file

import (
	"context"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")

	// Create mock service instance
	mockK8sClient := new(mocks.MockKubernetesClient)

	// Test successful creation
	service, err := New(log, mockK8sClient)
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
	mockK8sClient := new(mocks.MockKubernetesClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful list
	mockK8sClient.On("ListFilesInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.FileRequest) bool {
		return req.Path == "/workspace"
	})).Return(&types.FileList{
		Files: []types.FileInfo{
			{
				Path:      "/workspace/file.txt",
				Size:      100,
				IsDir:     false,
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
	mockK8sClient := new(mocks.MockKubernetesClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful download
	mockK8sClient.On("DownloadFileFromSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.FileRequest) bool {
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
	mockK8sClient := new(mocks.MockKubernetesClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful upload
	mockK8sClient.On("UploadFileToSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.FileRequest) bool {
		return req.Path == "/workspace/file.txt" && string(req.Content) == "test content"
	})).Return(&types.FileResult{
		Path:      "/workspace/file.txt",
		Size:      12,
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
	mockK8sClient := new(mocks.MockKubernetesClient)

	// Create the service
	service, _ := New(log, mockK8sClient)

	// Create a test sandbox
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}

	// Test case: Successful delete
	mockK8sClient.On("DeleteFileInSandbox", mock.Anything, "default", "test-sandbox", mock.MatchedBy(func(req *types.FileRequest) bool {
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
