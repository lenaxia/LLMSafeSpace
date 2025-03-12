package sandbox

import (
	"context"
	"fmt"
	"strings"
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
	dbService     interfaces.DatabaseService
	warmPoolSvc   interfaces.WarmPoolService
	executionSvc  interfaces.ExecutionService
	fileSvc       interfaces.FileService
	metrics       metrics.MetricsRecorder
	sessionMgr    interfaces.SessionManager
	reconciler    *ReconciliationHelper
}

func NewService(
	logger *logger.Logger, 
	k8sClient interfaces.KubernetesClient,
	dbService interfaces.DatabaseService,
	warmPoolSvc interfaces.WarmPoolService,
	executionSvc interfaces.ExecutionService,
	fileSvc interfaces.FileService,
	metrics metrics.MetricsRecorder,
	sessionMgr interfaces.SessionManager,
) interfaces.SandboxService {
	svc := &service{
		logger:        logger.With("component", "sandbox-service"),
		k8sClient:     k8sClient,
		dbService:     dbService,
		warmPoolSvc:   warmPoolSvc,
		executionSvc:  executionSvc,
		fileSvc:       fileSvc,
		metrics:       metrics,
		sessionMgr:    sessionMgr,
	}
	
	// Create reconciliation helper
	svc.reconciler = NewReconciliationHelper(k8sClient, logger)
	
	return svc
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
			s.logger.Info("Warm pod available for sandbox creation", 
				"runtime", req.Runtime,
				"securityLevel", req.SecurityLevel)
		}
	}

	// Convert to Kubernetes CRD
	sandboxCRD := client.ConvertToCRD(req, useWarmPod)

	// Create the Sandbox resource
	created, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Create(sandboxCRD)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}

	// Store sandbox metadata in database
	err = s.dbService.CreateSandboxMetadata(ctx, created.Name, req.UserID, req.Runtime)
	if err != nil {
		// Attempt to clean up the Kubernetes resource on database error
		s.logger.Error("Failed to create sandbox metadata, cleaning up sandbox resource", err,
			"sandbox", created.Name,
			"namespace", created.Namespace)
		
		deleteErr := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Delete(created.Name, metav1.DeleteOptions{})
		if deleteErr != nil {
			s.logger.Error("Failed to clean up sandbox resource after metadata creation failure", deleteErr,
				"sandbox", created.Name,
				"namespace", created.Namespace)
		}
		
		return nil, fmt.Errorf("failed to create sandbox metadata: %w", err)
	}

	// Convert back to API type
	result := client.ConvertFromCRD(created)
	
	s.metrics.RecordSandboxCreation(req.Runtime, useWarmPod)
	s.metrics.RecordOperationDuration("create", time.Since(startTime))
	
	s.logger.Info("Sandbox created successfully", 
		"sandbox", created.Name,
		"namespace", created.Namespace,
		"runtime", req.Runtime,
		"useWarmPod", useWarmPod,
		"duration_ms", time.Since(startTime).Milliseconds())
	
	return result, nil
}

func (s *service) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	// Try to find the sandbox in any namespace
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &types.SandboxNotFoundError{ID: sandboxID}
		}
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}
	return client.ConvertFromCRD(sandbox), nil
}

func (s *service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// Get sandboxes from database
	sandboxes, err := s.dbService.ListSandboxes(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}
	
	// Enrich with Kubernetes data if available
	for i, sandbox := range sandboxes {
		sandboxID, ok := sandbox["id"].(string)
		if !ok || sandboxID == "" {
			continue
		}
		
		// Try to get sandbox from Kubernetes
		k8sSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").Get(sandboxID, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				s.logger.Error("Failed to get sandbox from Kubernetes", err, "sandbox_id", sandboxID)
			}
			continue
		}
		
		// Add Kubernetes data
		sandboxes[i]["status"] = k8sSandbox.Status.Phase
		sandboxes[i]["endpoint"] = k8sSandbox.Status.Endpoint
		
		if k8sSandbox.Status.StartTime != nil {
			sandboxes[i]["startTime"] = k8sSandbox.Status.StartTime.Time
		}
		
		if k8sSandbox.Status.Resources != nil {
			sandboxes[i]["cpuUsage"] = k8sSandbox.Status.Resources.CPUUsage
			sandboxes[i]["memoryUsage"] = k8sSandbox.Status.Resources.MemoryUsage
		}
	}
	
	return sandboxes, nil
}

func (s *service) TerminateSandbox(ctx context.Context, sandboxID string) error {
	startTime := time.Now()
	
	// Get sandbox first to check if it exists and get its runtime
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	
	// Delete the sandbox
	err = s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Delete(sandboxID, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &types.SandboxNotFoundError{ID: sandboxID}
		}
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}

	s.metrics.RecordSandboxTermination(sandbox.Spec.Runtime)
	s.metrics.RecordOperationDuration("delete", time.Since(startTime))
	
	s.logger.Info("Sandbox terminated successfully", 
		"sandbox", sandboxID,
		"namespace", sandbox.Namespace,
		"runtime", sandbox.Spec.Runtime,
		"duration_ms", time.Since(startTime).Milliseconds())
	
	return nil
}

func (s *service) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Get sandbox status from Kubernetes
	k8sSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, &types.SandboxNotFoundError{ID: sandboxID}
		}
		return nil, fmt.Errorf("failed to get sandbox status: %w", err)
	}
	
	return &k8sSandbox.Status, nil
}

func (s *service) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecutionResult, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, req.SandboxID)
	if err != nil {
		return nil, err
	}
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Execute in sandbox
	result, err := s.executionSvc.Execute(ctx, sandbox, req.Type, req.Content, req.Timeout)
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
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// List files
	files, err := s.fileSvc.ListFiles(ctx, sandbox, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	
	return files, nil
}

func (s *service) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Download file
	content, err := s.fileSvc.DownloadFile(ctx, sandbox, path)
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
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Upload file
	fileInfo, err := s.fileSvc.UploadFile(ctx, sandbox, path, content)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	
	return fileInfo, nil
}

func (s *service) DeleteFile(ctx context.Context, sandboxID, path string) error {
	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Delete file
	err = s.fileSvc.DeleteFile(ctx, sandbox, path)
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
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Determine package manager if not specified
	manager := req.Manager
	if manager == "" {
		// Auto-detect based on runtime
		if strings.HasPrefix(sandbox.Spec.Runtime, "python") {
			manager = "pip"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "nodejs") {
			manager = "npm"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "ruby") {
			manager = "gem"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "go") {
			manager = "go"
		} else {
			return nil, fmt.Errorf("unable to determine package manager for runtime %s", sandbox.Spec.Runtime)
		}
	}
	
	// Install packages
	result, err := s.executionSvc.InstallPackages(ctx, sandbox, req.Packages, manager)
	if err != nil {
		return nil, fmt.Errorf("package installation failed: %w", err)
	}
	
	return result, nil
}

func (s *service) CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error) {
	// Check if sandbox exists
	sandbox, err := s.GetSandbox(context.Background(), sandboxID)
	if err != nil {
		return nil, err
	}
	
	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("cannot connect to sandbox: sandbox is not running (current status: %s)", sandbox.Status.Phase)
	}
	
	// Create session
	return s.sessionMgr.CreateSession(userID, sandboxID, conn.(types.WSConnection))
}

func (s *service) CloseSession(sessionID string) {
	s.sessionMgr.CloseSession(sessionID)
}

func (s *service) HandleSession(session *types.Session) {
	// This is handled by the session manager's readPump
}

func (s *service) Start() error {
	s.logger.Info("Starting sandbox service")
	
	// Start reconciliation loop
	go s.reconciler.StartReconciliationLoop(context.Background())
	
	// Start session manager
	return s.sessionMgr.Start()
}

func (s *service) Stop() error {
	s.logger.Info("Stopping sandbox service")
	
	// Stop session manager
	return s.sessionMgr.Stop()
}
