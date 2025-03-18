package sandbox

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/errors"
	apiinterfaces "github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox/validation"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Service implements the SandboxService interface
type Service struct {
	logger        pkginterfaces.LoggerInterface
	k8sClient     pkginterfaces.KubernetesClient
	dbService     apiinterfaces.DatabaseService
	cacheService  apiinterfaces.CacheService
	metricsService apiinterfaces.MetricsService
	warmPoolService apiinterfaces.WarmPoolService
	fileService   apiinterfaces.FileService
	execService   apiinterfaces.ExecutionService
	config        *Config
}

// Config holds service configuration
type Config struct {
	Namespace      string
	DefaultTimeout int
	MaxSandboxes   int
}

// New creates a new sandbox service
func New(
	logger pkginterfaces.LoggerInterface,
	k8sClient pkginterfaces.KubernetesClient,
	dbService apiinterfaces.DatabaseService,
	cacheService apiinterfaces.CacheService,
	metricsService apiinterfaces.MetricsService,
	warmPoolService apiinterfaces.WarmPoolService,
	fileService apiinterfaces.FileService,
	execService apiinterfaces.ExecutionService,
	config *Config,
) (*Service, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client cannot be nil")
	}
	if dbService == nil {
		return nil, fmt.Errorf("database service cannot be nil")
	}
	if config == nil {
		config = &Config{
			Namespace:      "default",
			DefaultTimeout: 300,
			MaxSandboxes:   100,
		}
	}

	return &Service{
		logger:         logger,
		k8sClient:      k8sClient,
		dbService:      dbService,
		cacheService:   cacheService,
		metricsService: metricsService,
		warmPoolService: warmPoolService,
		fileService:    fileService,
		execService:    execService,
		config:         config,
	}, nil
}

// CreateSandbox creates a new sandbox environment
func (s *Service) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.Sandbox, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("CreateSandbox", "", 0, time.Since(startTime), 0)
	}()

	// Validate request
	if err := validation.ValidateCreateSandboxRequest(req); err != nil {
		return nil, errors.NewValidationError(
			"Invalid sandbox creation request",
			map[string]interface{}{"details": err.Error()},
			err,
		)
	}

	// Set defaults if needed
	if req.Timeout <= 0 {
		req.Timeout = s.config.DefaultTimeout
	}

	// Check for warm pod availability if requested
	var warmPod *types.WarmPod
	var warmPodUsed bool
	if req.UseWarmPool {
		var err error
		warmPodID, err := s.warmPoolService.GetWarmSandbox(ctx, req.Runtime)
		if err == nil && warmPodID != "" {
			// Get the warm pod details
			warmPod, err = s.k8sClient.LlmsafespaceV1().WarmPods(s.config.Namespace).Get(warmPodID, metav1.GetOptions{})
			if err != nil {
				s.logger.Warn("Failed to get warm pod details", "error", err, "warmPodID", warmPodID)
				// Continue without warm pod
			} else {
				warmPodUsed = true
			}
		}
	}

	// Create sandbox resource
	sandbox := &types.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "llmsafespace.dev/v1",
			Kind:       "Sandbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Namespace:    s.config.Namespace,
			Labels: map[string]string{
				"app":     "llmsafespace",
				"user-id": req.UserID,
			},
		},
		Spec: types.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     req.Resources,
			NetworkAccess: req.NetworkAccess,
		},
	}

	// Apply warm pod reference if available
	if warmPod != nil {
		sandbox.Status.WarmPodRef = &types.WarmPodReference{
			Name:      warmPod.Name,
			Namespace: warmPod.Namespace,
		}
	}

	// Create sandbox in Kubernetes
	createdSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Create(sandbox)
	if err != nil {
		s.logger.Error("Failed to create sandbox in Kubernetes", err, 
			"runtime", req.Runtime, 
			"userID", req.UserID)
		return nil, errors.NewInternalError(
			"sandbox_creation_failed",
			err,
		)
	}

	// Store metadata in database
	err = s.dbService.CreateSandboxMetadata(ctx, createdSandbox.Name, req.UserID, req.Runtime)
	if err != nil {
		s.logger.Error("Failed to store sandbox metadata", err, 
			"sandboxID", createdSandbox.Name, 
			"userID", req.UserID)
		
		// Attempt to clean up the Kubernetes resource
		deleteErr := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Delete(createdSandbox.Name, metav1.DeleteOptions{})
		if deleteErr != nil {
			s.logger.Error("Failed to clean up sandbox after metadata error", deleteErr, 
				"sandboxID", createdSandbox.Name)
		}
		
		return nil, errors.NewInternalError(
			"metadata_creation_failed: Failed to store sandbox metadata",
			err,
		)
	}

	// Record metrics
	s.metricsService.RecordSandboxCreation(req.Runtime, warmPodUsed, req.UserID)

	return createdSandbox, nil
}

// GetSandbox retrieves a sandbox by ID with namespace fallback
func (s *Service) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("GetSandbox", "", 0, time.Since(startTime), 0)
	}()

	// First try in the configured namespace
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err == nil {
		return sandbox, nil
	}

	// If not found and it's a "not found" error, try listing across all namespaces
	sandboxList, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", sandboxID),
	})
	if err != nil {
		s.logger.Error("Failed to list sandboxes", err, "sandboxID", sandboxID)
		return nil, errors.NewInternalError(
			"Failed to retrieve sandbox",
			err,
		)
	}

	if len(sandboxList.Items) == 0 {
		return nil, &types.SandboxNotFoundError{ID: sandboxID}
	}

	// Return the first matching sandbox
	return &sandboxList.Items[0], nil
}

// ListSandboxes lists sandboxes for a user with pagination
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("ListSandboxes", "", 0, time.Since(startTime), 0)
	}()

	// Query database for sandbox metadata
	sandboxes, err := s.dbService.ListSandboxes(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list sandboxes from database", err, "userID", userID)
		return nil, errors.NewInternalError(
			"sandbox_list_failed",
			err,
		)
	}

	// Enrich with Kubernetes status information
	for i, sandbox := range sandboxes {
		sandboxID, ok := sandbox["id"].(string)
		if !ok {
			continue
		}

		// Get sandbox from Kubernetes
		k8sSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
		if err != nil {
			// Log but don't fail the entire request
			s.logger.Warn("Failed to get sandbox status", "error", err, "sandboxID", sandboxID)
			continue
		}

		// Add status information
		sandbox["status"] = k8sSandbox.Status.Phase
		sandbox["startTime"] = k8sSandbox.Status.StartTime
		if k8sSandbox.Status.Resources != nil {
			sandbox["cpuUsage"] = k8sSandbox.Status.Resources.CPUUsage
			sandbox["memoryUsage"] = k8sSandbox.Status.Resources.MemoryUsage
		}

		sandboxes[i] = sandbox
	}

	return sandboxes, nil
}

// TerminateSandbox terminates a sandbox
func (s *Service) TerminateSandbox(ctx context.Context, sandboxID string) error {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("TerminateSandbox", "", 0, time.Since(startTime), 0)
	}()

	// Get sandbox to verify it exists and get runtime info for metrics
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			return errors.NewNotFoundError(
				"sandbox",
				sandboxID,
				err,
			)
		}
		return errors.NewInternalError(
			"sandbox_retrieval_failed",
			err,
		)
	}

	// Delete the sandbox
	err = s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Delete(sandboxID, metav1.DeleteOptions{})
	if err != nil {
		s.logger.Error("Failed to delete sandbox", err, "sandboxID", sandboxID)
		return errors.NewInternalError(
			"sandbox_termination_failed",
			err,
		)
	}

	// Record metrics
	s.metricsService.RecordSandboxTermination(sandbox.Spec.Runtime, "user_requested")

	return nil
}

// GetSandboxStatus gets detailed status of a sandbox
func (s *Service) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("GetSandboxStatus", "", 0, time.Since(startTime), 0)
	}()

	// Get sandbox
	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			return nil, errors.NewNotFoundError(
				"sandbox",
				sandboxID,
				err,
			)
		}
		return nil, errors.NewInternalError(
			"Failed to retrieve sandbox status",
			err,
		)
	}

	return &sandbox.Status, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecutionResult, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"Execute method not yet implemented",
		nil,
	)
}

// ListFiles lists files in a sandbox
func (s *Service) ListFiles(ctx context.Context, sandboxID, path string) ([]types.FileInfo, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"ListFiles method not yet implemented",
		nil,
	)
}

// DownloadFile downloads a file from a sandbox
func (s *Service) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"DownloadFile method not yet implemented",
		nil,
	)
}

// UploadFile uploads a file to a sandbox
func (s *Service) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*types.FileInfo, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"UploadFile method not yet implemented",
		nil,
	)
}

// DeleteFile deletes a file in a sandbox
func (s *Service) DeleteFile(ctx context.Context, sandboxID, path string) error {
	// This will be implemented in a future phase
	return errors.NewNotImplementedError(
		"not_implemented",
		"DeleteFile method not yet implemented",
		nil,
	)
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, req types.InstallPackagesRequest) (*types.ExecutionResult, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"InstallPackages method not yet implemented",
		nil,
	)
}

// CreateSession creates a WebSocket session for a sandbox
func (s *Service) CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error) {
	// This will be implemented in a future phase
	return nil, errors.NewNotImplementedError(
		"not_implemented",
		"CreateSession method not yet implemented",
		nil,
	)
}

// CloseSession closes a WebSocket session
func (s *Service) CloseSession(sessionID string) {
	// This will be implemented in a future phase
}

// HandleSession handles a WebSocket session
func (s *Service) HandleSession(session *types.Session) {
	// This will be implemented in a future phase
}

// Start initializes the service
func (s *Service) Start() error {
	s.logger.Info("Starting sandbox service")
	return nil
}

// Stop cleans up resources
func (s *Service) Stop() error {
	s.logger.Info("Stopping sandbox service")
	return nil
}
