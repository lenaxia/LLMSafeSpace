package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
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

	s.logger.Info("Creating sandbox", 
		"runtime", req.Runtime, 
		"securityLevel", req.SecurityLevel, 
		"userID", req.UserID,
		"useWarmPool", req.UseWarmPool)

	// Start required services
	if err := s.dbService.Start(); err != nil {
		s.logger.Error("Failed to start database service", err)
		return nil, apierrors.NewInternalError(
			"service_initialization_failed",
			err,
		)
	}
	if err := s.metricsService.Start(); err != nil {
		s.logger.Error("Failed to start metrics service", err)
		return nil, apierrors.NewInternalError(
			"service_initialization_failed",
			err,
		)
	}
	
	defer func() {
		if err := s.dbService.Stop(); err != nil {
			s.logger.Error("Failed to stop database service", err)
		}
		if err := s.metricsService.Stop(); err != nil {
			s.logger.Error("Failed to stop metrics service", err)
		}
	}()
	
	// Validate request
	if err := validation.ValidateCreateSandboxRequest(req); err != nil {
		s.logger.Warn("Invalid sandbox creation request", 
			"error", err.Error(), 
			"runtime", req.Runtime, 
			"userID", req.UserID)
		return nil, apierrors.NewValidationError(
			"Invalid sandbox creation request",
			map[string]interface{}{"details": err.Error()},
			err,
		)
	}

	// Verify user exists and has permissions
	user, err := s.dbService.GetUser(ctx, req.UserID)
	if err != nil {
		s.logger.Error("Error retrieving user", err, "userID", req.UserID)
		return nil, apierrors.NewInternalError(
			"user_retrieval_failed",
			err,
		)
	}
	
	if user == nil {
		s.logger.Error("User not found", nil, "userID", req.UserID)
		return nil, apierrors.NewNotFoundError(
			"user",
			req.UserID,
			fmt.Errorf("user not found"),
		)
	}
	
	s.logger.Debug("User found", "userID", req.UserID, "userName", user.Username)

	// Check if user has permission to create sandboxes
	hasPermission, err := s.dbService.CheckPermission(req.UserID, "sandbox", "", "create")
	if err != nil {
		s.logger.Error("Failed to check permissions", err, "userID", req.UserID)
		return nil, apierrors.NewInternalError(
			"permission_check_failed",
			err,
		)
	}
	if !hasPermission {
		s.logger.Warn("Permission denied", "userID", req.UserID, "action", "create", "resource", "sandbox")
		return nil, apierrors.NewForbiddenError(
			"User does not have permission to create sandboxes",
			fmt.Errorf("permission denied for user %s", req.UserID),
		)
	}

	// Set defaults if needed
	if req.Timeout <= 0 {
		s.logger.Debug("Using default timeout", "timeout", s.config.DefaultTimeout)
		req.Timeout = s.config.DefaultTimeout
	}

	// Check for warm pod availability
	var warmPod *types.WarmPod
	var warmPodUsed bool
	
	// Try to get a warm pod if UseWarmPool is true or not specified
	if req.UseWarmPool || req.UseWarmPool == false /* default behavior */ {
		s.logger.Debug("Attempting to use warm pod", "runtime", req.Runtime)
		warmPodID, err := s.warmPoolService.GetWarmSandbox(ctx, req.Runtime)
		if err != nil {
			s.logger.Debug("No warm pod available", "error", err.Error(), "runtime", req.Runtime)
		} else if warmPodID != "" {
			// Get the warm pod details
			warmPod, err = s.k8sClient.LlmsafespaceV1().WarmPods(s.config.Namespace).Get(warmPodID, metav1.GetOptions{})
			if err != nil {
				s.logger.Warn("Failed to get warm pod details", "error", err, "warmPodID", warmPodID)
				// Continue without warm pod
			} else {
				warmPodUsed = true
				s.logger.Info("Using warm pod", "warmPodID", warmPodID, "runtime", req.Runtime)
			}
		}
	}

	// Convert API request to Kubernetes CRD
	sandbox := convertToSandboxCRD(req, s.config.Namespace, warmPod)

	// Create sandbox in Kubernetes
	s.logger.Debug("Creating sandbox in Kubernetes", 
		"namespace", sandbox.Namespace, 
		"generateName", sandbox.GenerateName)
	
	createdSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Create(sandbox)
	if err != nil {
		s.logger.Error("Failed to create sandbox in Kubernetes", err, 
			"runtime", req.Runtime, 
			"userID", req.UserID,
			"namespace", s.config.Namespace)
		return nil, apierrors.NewInternalError(
			"sandbox_creation_failed",
			err,
		)
	}

	s.logger.Info("Sandbox created successfully", 
		"sandboxID", createdSandbox.Name, 
		"runtime", req.Runtime, 
		"userID", req.UserID)

	// Store metadata in database
	s.logger.Debug("Storing sandbox metadata", "sandboxID", createdSandbox.Name, "userID", req.UserID)
	
	// Create sandbox metadata
	sandboxMetadata := &types.SandboxMetadata{
		ID:        createdSandbox.Name,
		UserID:    req.UserID,
		Runtime:   req.Runtime,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Status:    string(createdSandbox.Status.Phase),
		Labels:    createdSandbox.Labels,
	}
	
	err = s.dbService.CreateSandbox(ctx, sandboxMetadata)
	if err != nil {
		s.logger.Error("Failed to store sandbox metadata", err, 
			"sandboxID", createdSandbox.Name, 
			"userID", req.UserID)
		
		// Attempt to clean up the Kubernetes resource
		s.logger.Debug("Cleaning up sandbox after metadata error", "sandboxID", createdSandbox.Name)
		deleteErr := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Delete(createdSandbox.Name, metav1.DeleteOptions{})
		if deleteErr != nil {
			s.logger.Error("Failed to clean up sandbox after metadata error", deleteErr, 
				"sandboxID", createdSandbox.Name)
		}
		
		return nil, apierrors.NewInternalError(
			"metadata_creation_failed",
			err,
		)
	}

	// Record metrics
	s.logger.Debug("Recording metrics", "runtime", req.Runtime, "warmPodUsed", warmPodUsed)
	s.metricsService.RecordSandboxCreation(req.Runtime, warmPodUsed, req.UserID)

	return createdSandbox, nil
}

// convertToSandboxCRD converts an API request to a Kubernetes CRD
func convertToSandboxCRD(req *types.CreateSandboxRequest, namespace string, warmPod *types.WarmPod) *types.Sandbox {
	sandbox := &types.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "llmsafespace.dev/v1",
			Kind:       "Sandbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Namespace:    namespace,
			Labels: map[string]string{
				"app":     "llmsafespace",
				"user-id": req.UserID,
				"runtime": req.Runtime,
			},
			Annotations: map[string]string{
				"llmsafespace.dev/created-by": req.UserID,
				"llmsafespace.dev/created-at": time.Now().Format(time.RFC3339),
			},
		},
		Spec: types.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     req.Resources,
			NetworkAccess: req.NetworkAccess,
			// Only include optional fields if they're defined in the request type
			// These fields may not be present in the CreateSandboxRequest type
		},
	}

	// Apply warm pod reference if available
	if warmPod != nil {
		sandbox.Status.WarmPodRef = &types.WarmPodReference{
			Name:      warmPod.Name,
			Namespace: warmPod.Namespace,
		}
	}

	return sandbox
}

// GetSandbox retrieves a sandbox by ID with namespace fallback
func (s *Service) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("GetSandbox", "", 0, time.Since(startTime), 0)
	}()

	s.logger.Debug("Getting sandbox", "sandboxID", sandboxID, "namespace", s.config.Namespace)

	// First try in the configured namespace
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err == nil {
		s.logger.Debug("Found sandbox in default namespace", "sandboxID", sandboxID, "namespace", s.config.Namespace)
		return convertFromSandboxCRD(sandbox), nil
	}

	s.logger.Debug("Sandbox not found in default namespace, searching all namespaces", "sandboxID", sandboxID)

	// If not found, try listing across all namespaces with a field selector for efficiency
	sandboxList, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", sandboxID),
	})
	if err != nil {
		s.logger.Error("Failed to list sandboxes", err, "sandboxID", sandboxID)
		return nil, apierrors.NewInternalError(
			"Failed to retrieve sandbox",
			err,
		)
	}

	if len(sandboxList.Items) == 0 {
		s.logger.Warn("Sandbox not found in any namespace", "sandboxID", sandboxID)
		return nil, &types.SandboxNotFoundError{ID: sandboxID}
	}

	s.logger.Debug("Found sandbox in alternate namespace", 
		"sandboxID", sandboxID, 
		"namespace", sandboxList.Items[0].Namespace)

	// Return the first matching sandbox
	return convertFromSandboxCRD(&sandboxList.Items[0]), nil
}

// convertFromSandboxCRD converts a Kubernetes CRD to an API type
// This function allows us to perform any necessary transformations between
// the Kubernetes CRD representation and our API representation
func convertFromSandboxCRD(sandbox *types.Sandbox) *types.Sandbox {
	// Create a deep copy to avoid modifying the original
	result := sandbox.DeepCopy()
	
	// Add any necessary transformations here
	// For example, we might want to:
	// - Set default values for missing fields
	// - Transform field formats
	// - Add computed fields
	
	// For now, we're just returning the copy as-is
	return result
}

// ListSandboxes lists sandboxes for a user with pagination
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	startTime := time.Now()
	defer func() {
		s.metricsService.RecordRequest("ListSandboxes", "", 0, time.Since(startTime), 0)
	}()

	// Query database for sandbox metadata
	sandboxes, pagination, err := s.dbService.ListSandboxes(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list sandboxes from database", err, "userID", userID)
		
		// Improved error handling with more specific error types
		if errors.Is(err, types.ErrNotFound) {
			return nil, apierrors.NewNotFoundError(
				"sandboxes",
				fmt.Sprintf("user %s", userID),
				err,
			)
		}
		if errors.Is(err, types.ErrPermissionDenied) {
			return nil, apierrors.NewForbiddenError(
				"User does not have permission to list sandboxes",
				err,
			)
		}
		
		return nil, apierrors.NewInternalError(
			"sandbox_list_failed",
			err,
		)
	}

	// Convert to map[string]interface{} for API response
	result := make([]map[string]interface{}, 0, len(sandboxes))
	
	// Enrich with Kubernetes status information
	for _, sandbox := range sandboxes {
		// Convert SandboxMetadata to map
		sandboxMap := map[string]interface{}{
			"id":        sandbox.ID,
			"userId":    sandbox.UserID,
			"runtime":   sandbox.Runtime,
			"createdAt": sandbox.CreatedAt,
			"updatedAt": sandbox.UpdatedAt,
			"status":    sandbox.Status,
		}
		
		if sandbox.Name != "" {
			sandboxMap["name"] = sandbox.Name
		}
		
		if sandbox.Labels != nil && len(sandbox.Labels) > 0 {
			sandboxMap["labels"] = sandbox.Labels
		}

		// Get sandbox from Kubernetes for additional status info
		k8sSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandbox.ID, metav1.GetOptions{})
		if err != nil {
			// Log but don't fail the entire request
			s.logger.Warn("Failed to get sandbox status", "error", err, "sandboxID", sandbox.ID)
		} else {
			// Add status information
			sandboxMap["phase"] = k8sSandbox.Status.Phase
			sandboxMap["startTime"] = k8sSandbox.Status.StartTime
			if k8sSandbox.Status.Resources != nil {
				sandboxMap["cpuUsage"] = k8sSandbox.Status.Resources.CPUUsage
				sandboxMap["memoryUsage"] = k8sSandbox.Status.Resources.MemoryUsage
			}
		}

		result = append(result, sandboxMap)
	}
	
	// Sort sandboxes by creation time (newest first)
	sort.Slice(result, func(i, j int) bool {
		createdAtI, okI := result[i]["createdAt"].(time.Time)
		createdAtJ, okJ := result[j]["createdAt"].(time.Time)
		
		// If either isn't a time.Time, fall back to comparing by ID
		if !okI || !okJ {
			idI, _ := result[i]["id"].(string)
			idJ, _ := result[j]["id"].(string)
			return idI > idJ
		}
		
		return createdAtI.After(createdAtJ)
	})

	// Add pagination metadata to the response
	if pagination != nil {
		for i := range result {
			result[i]["pagination"] = pagination
		}
	}

	return result, nil
}

// Helper function to get userID from context
func getUserIDFromContext(ctx context.Context) string {
	if userID, ok := ctx.Value("userID").(string); ok {
		return userID
	}
	return ""
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
			return apierrors.NewNotFoundError(
				"sandbox",
				sandboxID,
				err,
			)
		}
		return apierrors.NewInternalError(
			"sandbox_retrieval_failed",
			err,
		)
	}

	// Verify user has permission to terminate the sandbox
	userID := getUserIDFromContext(ctx)
	if userID == "" {
		s.logger.Warn("No user ID found in context for sandbox termination", "sandboxID", sandboxID)
		return apierrors.NewForbiddenError(
			"User authentication required",
			fmt.Errorf("no user ID in context"),
		)
	}

	// First check if user owns the sandbox
	isOwner, err := s.dbService.CheckResourceOwnership(userID, "sandbox", sandboxID)
	if err != nil {
		s.logger.Error("Failed to check resource ownership", err, 
			"userID", userID, 
			"sandboxID", sandboxID)
		return apierrors.NewInternalError(
			"ownership_check_failed",
			err,
		)
	}

	// If not owner, check for delete permission
	if !isOwner {
		hasPermission, err := s.dbService.CheckPermission(userID, "sandbox", sandboxID, "delete")
		if err != nil {
			s.logger.Error("Failed to check permissions", err, 
				"userID", userID, 
				"sandboxID", sandboxID)
			return apierrors.NewInternalError(
				"permission_check_failed",
				err,
			)
		}
		if !hasPermission {
			s.logger.Warn("Permission denied", 
				"userID", userID, 
				"action", "delete", 
				"resource", sandboxID)
			return apierrors.NewForbiddenError(
				"User does not have permission to terminate this sandbox",
				fmt.Errorf("permission denied for user %s", userID),
			)
		}
	}

	// Delete the sandbox
	err = s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Delete(sandboxID, metav1.DeleteOptions{})
	if err != nil {
		s.logger.Error("Failed to delete sandbox", err, "sandboxID", sandboxID)
		return apierrors.NewInternalError(
			"sandbox_termination_failed",
			err,
		)
	}

	// Delete sandbox metadata from database
	err = s.dbService.DeleteSandbox(ctx, sandboxID)
	if err != nil {
		s.logger.Error("Failed to delete sandbox metadata", err, "sandboxID", sandboxID)
		// Continue even if metadata deletion fails, but return an error
		return apierrors.NewInternalError(
			"metadata_deletion_failed",
			fmt.Errorf("sandbox terminated but metadata deletion failed: %w", err),
		)
	}

	// Record metrics
	s.metricsService.RecordSandboxTermination(sandbox.Spec.Runtime, "user_requested")

	s.logger.Info("Sandbox terminated successfully", 
		"sandboxID", sandboxID, 
		"userID", userID,
		"runtime", sandbox.Spec.Runtime)

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
			return nil, apierrors.NewNotFoundError(
				"sandbox",
				sandboxID,
				err,
			)
		}
		return nil, apierrors.NewInternalError(
			"Failed to retrieve sandbox status",
			err,
		)
	}

	return &sandbox.Status, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecutionResult, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
		"not_implemented",
		"Execute method not yet implemented",
		nil,
	)
}

// ListFiles lists files in a sandbox
func (s *Service) ListFiles(ctx context.Context, sandboxID, path string) ([]types.FileInfo, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
		"not_implemented",
		"ListFiles method not yet implemented",
		nil,
	)
}

// DownloadFile downloads a file from a sandbox
func (s *Service) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
		"not_implemented",
		"DownloadFile method not yet implemented",
		nil,
	)
}

// UploadFile uploads a file to a sandbox
func (s *Service) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*types.FileInfo, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
		"not_implemented",
		"UploadFile method not yet implemented",
		nil,
	)
}

// DeleteFile deletes a file in a sandbox
func (s *Service) DeleteFile(ctx context.Context, sandboxID, path string) error {
	// This will be implemented in a future phase
	return apierrors.NewNotImplementedError(
		"not_implemented",
		"DeleteFile method not yet implemented",
		nil,
	)
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, req types.InstallPackagesRequest) (*types.ExecutionResult, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
		"not_implemented",
		"InstallPackages method not yet implemented",
		nil,
	)
}

// CreateSession creates a WebSocket session for a sandbox
func (s *Service) CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error) {
	// This will be implemented in a future phase
	return nil, apierrors.NewNotImplementedError(
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
