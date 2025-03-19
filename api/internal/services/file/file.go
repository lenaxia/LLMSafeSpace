package file

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Service handles file operations
type Service struct {
	logger    *logger.Logger
	k8sClient interfaces.KubernetesClient
}

// Ensure Service implements interfaces.FileService
var _ interfaces.FileService = &Service{}

// New creates a new file service
func New(logger *logger.Logger, k8sClient interfaces.KubernetesClient) (*Service, error) {
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
	}, nil
}

// Start initializes the file service
func (s *Service) Start() error {
	s.logger.Info("Starting file service")
	return nil
}

// Stop cleans up the file service
func (s *Service) Stop() error {
	s.logger.Info("Stopping file service")
	return nil
}

func (s *Service) ListFiles(ctx context.Context, sandbox *types.Sandbox, path string) ([]types.FileInfo, error) {
	startTime := time.Now() 
	sb := sandbox

	s.logger.Debug("Listing files in sandbox",
		"namespace", sb.Namespace,
		"name", sb.Name,
		"path", path)
	
	// Normalize path
	if path == "" {
		path = "/workspace"
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}

	// List files via Kubernetes API
	fileList, err := s.k8sClient.ListFilesInSandbox(ctx, sb.Namespace, sb.Name, fileReq)
	if err != nil {
		s.logger.Error("Failed to list files in sandbox", err, 
			"namespace", sb.Namespace, 
			"name", sb.Name, 
			"path", path)
		return nil, fmt.Errorf("failed to list files in sandbox: %w", err)
	}

	// Convert to FileInfo
	files := make([]types.FileInfo, len(fileList.Files))
	for i, file := range fileList.Files {
		files[i] = types.FileInfo{
			Path:      file.Path,
			Name:      filepath.Base(file.Path),
			Size:      file.Size,
			IsDir:     file.IsDir,
			CreatedAt: file.CreatedAt,
			UpdatedAt: file.UpdatedAt,
		}
	}

	duration := time.Since(startTime)
	s.logger.Debug("Listed files in sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path, 
		"file_count", len(files), 
		"duration_ms", duration.Milliseconds())

	return files, nil
}

// DownloadFile downloads a file from a sandbox
func (s *Service) DownloadFile(ctx context.Context, sandbox *types.Sandbox, path string) ([]byte, error) {
	startTime := time.Now()
	sb := sandbox
	
	s.logger.Debug("Downloading file from sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path)
	
	// Validate path
	if path == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}

	// Download file via Kubernetes API
	fileContent, err := s.k8sClient.DownloadFileFromSandbox(ctx, sb.Namespace, sb.Name, fileReq)
	if err != nil {
		s.logger.Error("Failed to download file from sandbox", err, 
			"namespace", sb.Namespace, 
			"name", sb.Name, 
			"path", path)
		return nil, fmt.Errorf("failed to download file from sandbox: %w", err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Downloaded file from sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path, 
		"size", len(fileContent), 
		"duration_ms", duration.Milliseconds())

	return fileContent, nil
}

// UploadFile uploads a file to a sandbox
func (s *Service) UploadFile(ctx context.Context, sandbox *types.Sandbox, path string, content []byte) (*types.FileInfo, error) {
	startTime := time.Now()
	sb := sandbox
	
	s.logger.Debug("Uploading file to sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path, 
		"size", len(content))
	
	// Validate path
	if path == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}
	
	// Ensure path is within workspace
	if !strings.HasPrefix(path, "/workspace/") && path != "/workspace" {
		path = filepath.Join("/workspace", path)
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path:    path,
		Content: content,
	}

	// Upload file via Kubernetes API
	fileResult, err := s.k8sClient.UploadFileToSandbox(ctx, sb.Namespace, sb.Name, fileReq)
	if err != nil {
		s.logger.Error("Failed to upload file to sandbox", err, 
			"namespace", sb.Namespace, 
			"name", sb.Name, 
			"path", path)
		return nil, fmt.Errorf("failed to upload file to sandbox: %w", err)
	}

	// Return file info
	fileInfo := &types.FileInfo{
		Path:      fileResult.Path,
		Name:      filepath.Base(fileResult.Path),
		Size:      fileResult.Size,
		IsDir:     false,
		CreatedAt: fileResult.CreatedAt,
		UpdatedAt: fileResult.UpdatedAt,
	}

	duration := time.Since(startTime)
	s.logger.Debug("Uploaded file to sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path, 
		"size", fileInfo.Size, 
		"duration_ms", duration.Milliseconds())

	return fileInfo, nil
}

// DeleteFile deletes a file from a sandbox
func (s *Service) DeleteFile(ctx context.Context, sandbox *types.Sandbox, path string) error {
	startTime := time.Now()
	
	s.logger.Debug("Deleting file from sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"path", path)
	
	// Validate path
	if path == "" {
		return fmt.Errorf("file path cannot be empty")
	}
	
	// Prevent deletion of workspace root
	if path == "/workspace" {
		return fmt.Errorf("cannot delete workspace root directory")
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}

	// Delete file via Kubernetes API
	err := s.k8sClient.DeleteFileInSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		s.logger.Error("Failed to delete file in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name, 
			"path", path)
		return fmt.Errorf("failed to delete file in sandbox: %w", err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Deleted file from sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"path", path, 
		"duration_ms", duration.Milliseconds())

	return nil
}

// CreateDirectory creates a directory in a sandbox
func (s *Service) CreateDirectory(ctx context.Context, sandbox *types.Sandbox, path string) (*types.FileInfo, error) {
	startTime := time.Now()
	sb := sandbox
	
	s.logger.Debug("Creating directory in sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path)
	
	// Validate path
	if path == "" {
		return nil, fmt.Errorf("directory path cannot be empty")
	}
	
	// Ensure path is within workspace
	if !strings.HasPrefix(path, "/workspace/") && path != "/workspace" {
		path = filepath.Join("/workspace", path)
	}
	
	// Create file request with directory flag
	fileReq := &types.FileRequest{
		Path:    path,
		IsDir:   true,
	}

	// Create directory via Kubernetes API
	fileResult, err := s.k8sClient.UploadFileToSandbox(ctx, sb.Namespace, sb.Name, fileReq)
	if err != nil {
		s.logger.Error("Failed to create directory in sandbox", err, 
			"namespace", sb.Namespace, 
			"name", sb.Name, 
			"path", path)
		return nil, fmt.Errorf("failed to create directory in sandbox: %w", err)
	}

	// Return file info
	fileInfo := &types.FileInfo{
		Path:      fileResult.Path,
		Name:      filepath.Base(fileResult.Path),
		Size:      fileResult.Size,
		IsDir:     true,
		CreatedAt: fileResult.CreatedAt,
		UpdatedAt: fileResult.UpdatedAt,
	}

	duration := time.Since(startTime)
	s.logger.Debug("Created directory in sandbox", 
		"namespace", sb.Namespace, 
		"name", sb.Name, 
		"path", path, 
		"duration_ms", duration.Milliseconds())

	return fileInfo, nil
}
