package sandbox

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox/client"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox/validation"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox/metrics"
)

type service struct {
	logger        *logger.Logger
	k8sClient     interfaces.KubernetesClient
	warmPoolSvc   interfaces.WarmPoolService
	metrics       metrics.MetricsRecorder
	sessionMgr    interfaces.SessionManager
}

func NewService(
	logger *logger.Logger, 
	k8sClient interfaces.KubernetesClient,
	warmPoolSvc interfaces.WarmPoolService,
	metrics metrics.MetricsRecorder,
	sessionMgr interfaces.SessionManager,
) interfaces.SandboxService {
	return &service{
		logger:        logger.With("component", "sandbox-service"),
		k8sClient:     k8sClient,
		warmPoolSvc:   warmPoolSvc,
		metrics:       metrics,
		sessionMgr:    sessionMgr,
	}
}

func (s *service) CreateSandbox(ctx context.Context, req types.CreateSandboxRequest) (*types.Sandbox, error) {
	startTime := time.Now()
	
	// Validate request
	if err := validation.ValidateCreateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Check warm pool availability first
	useWarmPod := false
	if req.UseWarmPool {
		available, err := s.warmPoolSvc.CheckAvailability(ctx, req.Runtime, req.SecurityLevel)
		if err != nil {
			s.logger.Error("Failed to check warm pool availability", err, 
				"runtime", req.Runtime,
				"securityLevel", req.SecurityLevel)
		} else if available {
			useWarmPod = true
		}
	}

	// Convert to Kubernetes CRD
	sandboxCRD := client.ConvertToCRD(req, useWarmPod)

	// Create the Sandbox resource
	created, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Create(ctx, sandboxCRD, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}

	// Convert back to API type
	result := client.ConvertFromCRD(created)
	
	s.metrics.RecordSandboxCreation(req.Runtime, useWarmPod)
	s.metrics.RecordOperationDuration("create", time.Since(startTime))
	
	return result, nil
}

func (s *service) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").Get(ctx, sandboxID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &types.SandboxNotFoundError{ID: sandboxID}
		}
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}
	return client.ConvertFromCRD(sandbox), nil
}

func (s *service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// Implementation would use k8sClient to list sandboxes with appropriate filters
	// and convert the results to the expected format
	return nil, fmt.Errorf("not implemented")
}

func (s *service) TerminateSandbox(ctx context.Context, sandboxID string) error {
	startTime := time.Now()
	
	err := s.k8sClient.LlmsafespaceV1().Sandboxes("").Delete(ctx, sandboxID, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &types.SandboxNotFoundError{ID: sandboxID}
		}
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}

	s.metrics.RecordSandboxTermination(sandboxID)
	s.metrics.RecordOperationDuration("delete", time.Since(startTime))
	
	return nil
}

func (s *service) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").Get(ctx, sandboxID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &types.SandboxNotFoundError{ID: sandboxID}
		}
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}
	return &sandbox.Status, nil
}

func (s *service) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecutionResult, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, req.SandboxID)
	if err != nil {
		return nil, err
	}
	
	// Create execution request
	execReq := &types.ExecutionRequest{
		Type:    req.Type,
		Content: req.Content,
		Timeout: req.Timeout,
	}
	
	// Execute in sandbox
	result, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}
	
	return result, nil
}

func (s *service) ListFiles(ctx context.Context, sandboxID, path string) ([]types.FileInfo, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}
	
	// List files in sandbox
	result, err := s.k8sClient.ListFilesInSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	
	return result.Files, nil
}

func (s *service) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}
	
	// Download file from sandbox
	content, err := s.k8sClient.DownloadFileFromSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	
	return content, nil
}

func (s *service) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*types.FileInfo, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path:    path,
		Content: content,
	}
	
	// Upload file to sandbox
	result, err := s.k8sClient.UploadFileToSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	
	return &types.FileInfo{
		Path:      result.Path,
		Size:      result.Size,
		CreatedAt: result.CreatedAt,
		UpdatedAt: result.UpdatedAt,
		IsDir:     result.IsDir,
	}, nil
}

func (s *service) DeleteFile(ctx context.Context, sandboxID, path string) error {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	
	// Create file request
	fileReq := &types.FileRequest{
		Path: path,
	}
	
	// Delete file in sandbox
	err = s.k8sClient.DeleteFileInSandbox(ctx, sandbox.Namespace, sandbox.Name, fileReq)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	
	return nil
}

func (s *service) InstallPackages(ctx context.Context, req types.InstallPackagesRequest) (*types.ExecutionResult, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, req.SandboxID)
	if err != nil {
		return nil, err
	}
	
	// Determine package manager if not specified
	manager := req.Manager
	if manager == "" {
		// Auto-detect based on runtime
		if sandbox.Runtime == "python:3.10" || sandbox.Runtime == "python:3.9" {
			manager = "pip"
		} else if sandbox.Runtime == "nodejs:16" || sandbox.Runtime == "nodejs:18" {
			manager = "npm"
		} else {
			return nil, fmt.Errorf("unable to determine package manager for runtime %s", sandbox.Runtime)
		}
	}
	
	// Build installation command
	var command string
	switch manager {
	case "pip":
		command = "pip install " + strings.Join(req.Packages, " ")
	case "npm":
		command = "npm install " + strings.Join(req.Packages, " ")
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
	}
	
	// Execute installation command
	execReq := &types.ExecutionRequest{
		Type:    "command",
		Content: command,
		Timeout: 300, // Default timeout for package installation
	}
	
	result, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		return nil, fmt.Errorf("package installation failed: %w", err)
	}
	
	return result, nil
}

func (s *service) CreateSession(userID, sandboxID string, conn interfaces.WSConnection) (*types.Session, error) {
	return s.sessionMgr.CreateSession(userID, sandboxID, conn)
}

func (s *service) CloseSession(sessionID string) {
	s.sessionMgr.CloseSession(sessionID)
}

func (s *service) HandleSession(session *types.Session) {
	// Implementation would handle WebSocket session messages
	// This would typically be a long-running function that processes
	// incoming messages and routes them to the appropriate handlers
}

func (s *service) Start() error {
	return s.sessionMgr.Start()
}

func (s *service) Stop() error {
	return s.sessionMgr.Stop()
}
