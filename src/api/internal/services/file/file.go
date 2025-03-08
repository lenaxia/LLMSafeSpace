package file

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Service handles file operations
type Service struct {
	logger    *logger.Logger
	k8sClient interfaces.KubernetesClient
}

// Ensure Service implements the FileService interface
var _ interfaces.FileService = (*Service)(nil)

// New creates a new file service
func New(logger *logger.Logger, k8sClient interfaces.KubernetesClient) (*Service, error) {
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
	}, nil
}

// Start initializes the file service
func (s *Service) Start() error {
	return nil
}

// Stop cleans up the file service
func (s *Service) Stop() error {
	return nil
}

// ListFiles lists files in a sandbox
func (s *Service) ListFiles(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]interfaces.FileInfo, error) {
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
	files := make([]interfaces.FileInfo, len(fileList.Files))
	for i, file := range fileList.Files {
		files[i] = interfaces.FileInfo{
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
func (s *Service) UploadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string, content []byte) (*interfaces.FileInfo, error) {
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
	return &interfaces.FileInfo{
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
