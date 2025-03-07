package file

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Service handles file operations
type Service struct {
	logger    *logger.Logger
	k8sClient *kubernetes.Client
}

// FileInfo represents information about a file
type FileInfo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	IsDir     bool      `json:"isDir"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// New creates a new file service
func New(logger *logger.Logger, k8sClient *kubernetes.Client) (*Service, error) {
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
	}, nil
}

// ListFiles lists files in a sandbox
func (s *Service) ListFiles(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]FileInfo, error) {
	// Create file request
	fileReq := &kubernetes.FileRequest{
		Path: path,
	}

	// List files via Kubernetes API
	fileList, err := s.k8sClient.ListFilesInSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to list files in sandbox: %w", err)
	}

	// Convert to FileInfo
	files := make([]FileInfo, len(fileList.Files))
	for i, file := range fileList.Files {
		files[i] = FileInfo{
			Path:      file.Path,
			Name:      filepath.Base(file.Path),
			Size:      file.Size,
			IsDir:     file.IsDir,
			CreatedAt: file.CreatedAt,
			UpdatedAt: file.UpdatedAt,
		}
	}

	return files, nil
}

// DownloadFile downloads a file from a sandbox
func (s *Service) DownloadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]byte, error) {
	// Create file request
	fileReq := &kubernetes.FileRequest{
		Path: path,
	}

	// Download file via Kubernetes API
	fileContent, err := s.k8sClient.DownloadFileFromSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from sandbox: %w", err)
	}

	return fileContent, nil
}

// UploadFile uploads a file to a sandbox
func (s *Service) UploadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string, content []byte) (*FileInfo, error) {
	// Create file request
	fileReq := &kubernetes.FileRequest{
		Path:    path,
		Content: content,
	}

	// Upload file via Kubernetes API
	fileResult, err := s.k8sClient.UploadFileToSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file to sandbox: %w", err)
	}

	// Return file info
	return &FileInfo{
		Path:      fileResult.Path,
		Name:      filepath.Base(fileResult.Path),
		Size:      fileResult.Size,
		IsDir:     false,
		CreatedAt: fileResult.CreatedAt,
		UpdatedAt: fileResult.UpdatedAt,
	}, nil
}

// DeleteFile deletes a file from a sandbox
func (s *Service) DeleteFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) error {
	// Create file request
	fileReq := &kubernetes.FileRequest{
		Path: path,
	}

	// Delete file via Kubernetes API
	err := s.k8sClient.DeleteFileInSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return fmt.Errorf("failed to delete file in sandbox: %w", err)
	}

	return nil
}
