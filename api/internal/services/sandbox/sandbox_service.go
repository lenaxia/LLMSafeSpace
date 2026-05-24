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
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
)

// Service implements apiinterfaces.SandboxService.
type Service struct {
	logger           pkginterfaces.LoggerInterface
	k8sClient        pkginterfaces.KubernetesClient
	dbService        apiinterfaces.DatabaseService
	cacheService     apiinterfaces.CacheService
	metricsService   apiinterfaces.MetricsService
	workspaceService apiinterfaces.WorkspaceService
	config           *Config
}

// Config holds sandbox service configuration.
type Config struct {
	Namespace      string
	DefaultTimeout int
	MaxSandboxes   int
}

var _ apiinterfaces.SandboxService = (*Service)(nil)

// New creates a validated sandbox service. config may be nil to use defaults.
func New(
	logger pkginterfaces.LoggerInterface,
	k8sClient pkginterfaces.KubernetesClient,
	dbService apiinterfaces.DatabaseService,
	cacheService apiinterfaces.CacheService,
	metricsService apiinterfaces.MetricsService,
	workspaceService apiinterfaces.WorkspaceService,
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
	if workspaceService == nil {
		return nil, fmt.Errorf("workspace service cannot be nil")
	}
	if config == nil {
		config = &Config{
			Namespace:      "default",
			DefaultTimeout: 300,
			MaxSandboxes:   100,
		}
	}
	return &Service{
		logger:           logger,
		k8sClient:        k8sClient,
		dbService:        dbService,
		cacheService:     cacheService,
		metricsService:   metricsService,
		workspaceService: workspaceService,
		config:           config,
	}, nil
}

// CreateSandbox validates the request, creates a Kubernetes Sandbox CRD, and
// persists metadata to the database. On database failure the CRD is deleted to
// keep state consistent.
func (s *Service) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.Sandbox, error) {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("CreateSandbox", "", 0, time.Since(start), 0)
	}()

	s.logger.Info("Creating sandbox", "runtime", req.Runtime, "userID", req.UserID)

	if err := validation.ValidateCreateSandboxRequest(req); err != nil {
		return nil, apierrors.NewValidationError(
			"Invalid sandbox creation request",
			map[string]interface{}{"details": err.Error()},
			err,
		)
	}

	user, err := s.dbService.GetUser(ctx, req.UserID)
	if err != nil {
		s.logger.Error("Error retrieving user", err, "userID", req.UserID)
		return nil, apierrors.NewInternalError("user_retrieval_failed", err)
	}
	if user == nil {
		return nil, apierrors.NewNotFoundError("user", req.UserID, fmt.Errorf("user not found"))
	}
	if !user.Active {
		return nil, apierrors.NewForbiddenError(
			"User account is inactive",
			fmt.Errorf("user %s is inactive", req.UserID),
		)
	}

	if req.Timeout <= 0 {
		req.Timeout = s.config.DefaultTimeout
	}

	// Authorization model:
	//   - Admin role bypasses ownership checks (cross-cutting authority).
	//   - Regular users may attach a sandbox only to a workspace they own.
	//   - With no WorkspaceRef the workspace is auto-created and owned by
	//     the requesting user, so no extra check is needed.
	workspaceID := req.WorkspaceRef
	if workspaceID == "" {
		ws, err := s.workspaceService.CreateWorkspace(ctx, req.UserID, types.CreateWorkspaceRequest{
			Name:        fmt.Sprintf("workspace-for-%s", req.UserID),
			Runtime:     req.Runtime,
			StorageSize: "10Gi",
		})
		if err != nil {
			return nil, fmt.Errorf("auto-creating workspace: %w", err)
		}
		workspaceID = ws.ID
	} else if user.Role != "admin" {
		if _, err := s.workspaceService.GetWorkspace(ctx, req.UserID, workspaceID); err != nil {
			// GetWorkspace returns NotFound for missing, Forbidden for foreign.
			// Both surface as the same error to the caller; do not leak existence.
			return nil, err
		}
	}

	crd := buildCRDFromRequest(req, workspaceID, s.config.Namespace)

	s.logger.Debug("Creating sandbox in Kubernetes", "namespace", crd.Namespace, "generateName", crd.GenerateName)

	created, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Create(crd)
	if err != nil {
		s.logger.Error("Failed to create sandbox in Kubernetes", err, "runtime", req.Runtime, "userID", req.UserID)
		return nil, apierrors.NewInternalError("sandbox_creation_failed", err)
	}

	s.logger.Info("Sandbox created", "sandboxID", created.Name, "runtime", req.Runtime)

	meta := &types.SandboxMetadata{
		ID:          created.Name,
		UserID:      req.UserID,
		Runtime:     req.Runtime,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Status:      string(created.Status.Phase),
		Labels:      created.Labels,
		WorkspaceID: workspaceID,
	}

	if err := s.dbService.CreateSandbox(ctx, meta); err != nil {
		s.logger.Error("Failed to store sandbox metadata", err, "sandboxID", created.Name)
		if delErr := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Delete(created.Name, metav1.DeleteOptions{}); delErr != nil {
			s.logger.Error("Failed to clean up sandbox after metadata error", delErr, "sandboxID", created.Name)
		}
		return nil, apierrors.NewInternalError("metadata_creation_failed", err)
	}

	s.metricsService.RecordSandboxCreation(req.Runtime, req.UserID)

	return convertCRDToAPI(created), nil
}

// GetSandbox retrieves a sandbox by ID, first from the configured namespace,
// then falling back to a cluster-wide search.
func (s *Service) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("GetSandbox", "", 0, time.Since(start), 0)
	}()

	s.logger.Debug("Getting sandbox", "sandboxID", sandboxID, "namespace", s.config.Namespace)

	crd, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err == nil {
		return convertCRDToAPI(crd), nil
	}

	s.logger.Debug("Sandbox not found in default namespace, searching all namespaces", "sandboxID", sandboxID)

	list, err := s.k8sClient.LlmsafespaceV1().Sandboxes("").List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", sandboxID),
	})
	if err != nil {
		s.logger.Error("Failed to list sandboxes", err, "sandboxID", sandboxID)
		return nil, apierrors.NewInternalError("Failed to retrieve sandbox", err)
	}

	if len(list.Items) == 0 {
		return nil, &types.SandboxNotFoundError{ID: sandboxID}
	}

	return convertCRDToAPI(&list.Items[0]), nil
}

// ListSandboxes returns sandbox metadata for a user, enriched with live
// Kubernetes status where available. Results are sorted newest-first.
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) (*types.SandboxListResult, error) {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("ListSandboxes", "", 0, time.Since(start), 0)
	}()

	sandboxes, pagination, err := s.dbService.ListSandboxes(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list sandboxes", err, "userID", userID)
		if errors.Is(err, types.ErrNotFound) {
			return nil, apierrors.NewNotFoundError("sandboxes", fmt.Sprintf("user %s", userID), err)
		}
		if errors.Is(err, types.ErrPermissionDenied) {
			return nil, apierrors.NewForbiddenError("User does not have permission to list sandboxes", err)
		}
		return nil, apierrors.NewInternalError("sandbox_list_failed", err)
	}

	items := make([]types.SandboxListItem, 0, len(sandboxes))
	for _, sb := range sandboxes {
		item := types.SandboxListItem{
			ID:        sb.ID,
			UserID:    sb.UserID,
			Runtime:   sb.Runtime,
			CreatedAt: sb.CreatedAt,
			UpdatedAt: sb.UpdatedAt,
			Status:    sb.Status,
			Name:      sb.Name,
			Labels:    sb.Labels,
		}

		crd, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sb.ID, metav1.GetOptions{})
		if err != nil {
			s.logger.Warn("Failed to get live sandbox status", "error", err, "sandboxID", sb.ID)
		} else {
			item.Phase = string(crd.Status.Phase)
			item.StartTime = metav1TimeToStdLib(crd.Status.StartTime)
			if crd.Status.Resources != nil {
				item.CPUUsage = crd.Status.Resources.CPUUsage
				item.MemoryUsage = crd.Status.Resources.MemoryUsage
			}
		}

		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return &types.SandboxListResult{Items: items, Pagination: pagination}, nil
}

// TerminateSandbox deletes a sandbox and its metadata. The caller must own the
// sandbox or have the admin role. The userID is read from context.
func (s *Service) TerminateSandbox(ctx context.Context, sandboxID string) error {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("TerminateSandbox", "", 0, time.Since(start), 0)
	}()

	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			return apierrors.NewNotFoundError("sandbox", sandboxID, err)
		}
		return apierrors.NewInternalError("sandbox_retrieval_failed", err)
	}

	userID := userIDFromContext(ctx)
	if userID == "" {
		return apierrors.NewForbiddenError("User authentication required", fmt.Errorf("no user ID in context"))
	}

	owner, err := s.dbService.CheckResourceOwnership(userID, "sandbox", sandboxID)
	if err != nil {
		s.logger.Error("Failed to check resource ownership", err, "userID", userID, "sandboxID", sandboxID)
		return apierrors.NewInternalError("ownership_check_failed", err)
	}

	if !owner {
		// Only admins can terminate sandboxes they don't own.
		user, err := s.dbService.GetUser(ctx, userID)
		if err != nil {
			s.logger.Error("Failed to retrieve user for role check", err, "userID", userID)
			return apierrors.NewInternalError("user_retrieval_failed", err)
		}
		if user == nil || user.Role != "admin" {
			return apierrors.NewForbiddenError(
				"User does not have permission to terminate this sandbox",
				fmt.Errorf("permission denied for user %s", userID),
			)
		}
	}

	if err := s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Delete(sandboxID, metav1.DeleteOptions{}); err != nil {
		s.logger.Error("Failed to delete sandbox", err, "sandboxID", sandboxID)
		return apierrors.NewInternalError("sandbox_termination_failed", err)
	}

	if err := s.dbService.DeleteSandbox(ctx, sandboxID); err != nil {
		s.logger.Error("Failed to delete sandbox metadata", err, "sandboxID", sandboxID)
		return apierrors.NewInternalError(
			"metadata_deletion_failed",
			fmt.Errorf("sandbox terminated but metadata deletion failed: %w", err),
		)
	}

	s.metricsService.RecordSandboxTermination(sandbox.Spec.Runtime, "user_requested")
	s.logger.Info("Sandbox terminated", "sandboxID", sandboxID, "userID", userID)

	return nil
}

// GetSandboxStatus returns the status portion of a sandbox.
func (s *Service) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("GetSandboxStatus", "", 0, time.Since(start), 0)
	}()

	sandbox, err := s.GetSandbox(ctx, sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			return nil, apierrors.NewNotFoundError("sandbox", sandboxID, err)
		}
		return nil, apierrors.NewInternalError("Failed to retrieve sandbox status", err)
	}

	return &sandbox.Status, nil
}

func (s *Service) Start() error {
	s.logger.Info("Starting sandbox service")
	return nil
}

func (s *Service) Stop() error {
	s.logger.Info("Stopping sandbox service")
	return nil
}

// userIDFromContext extracts the authenticated user ID from the request context.
// The auth middleware stores it using types.ContextKeyUserID.
func userIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(types.ContextKeyUserID).(string)
	return v
}

// buildCRDFromRequest constructs a v1.Sandbox CRD from an API request.
func buildCRDFromRequest(req *types.CreateSandboxRequest, workspaceID, namespace string) *v1.Sandbox {
	return &v1.Sandbox{
		TypeMeta: metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Namespace:    namespace,
			// Runtime values are image-style identifiers like "python:3.11";
			// the ':' is rejected by K8s label validation, so sanitize. The
			// canonical (unsanitized) value still lives in spec.runtime; this
			// label is only for grouping/filtering. See pkg/utilities/labels.go
			// for the sanitization rules.
			Labels: map[string]string{
				"app":                        "llmsafespace",
				"user-id":                    req.UserID,
				"runtime":                    utilities.SanitizeLabelValue(req.Runtime),
				"llmsafespace.dev/workspace": workspaceID,
			},
			Annotations: map[string]string{
				"llmsafespace.dev/created-by": req.UserID,
				"llmsafespace.dev/created-at": time.Now().UTC().Format(time.RFC3339),
			},
		},
		Spec: v1.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     apiResourcesToCRD(req.Resources),
			NetworkAccess: apiNetworkToCRD(req.NetworkAccess),
			WorkspaceRef:  workspaceID,
		},
	}
}

// convertCRDToAPI converts a v1.Sandbox CRD to the API pkg/types.Sandbox DTO.
func convertCRDToAPI(crd *v1.Sandbox) *types.Sandbox {
	if crd == nil {
		return nil
	}
	return &types.Sandbox{
		ID:                crd.Name,
		Namespace:         crd.Namespace,
		Labels:            crd.Labels,
		Annotations:       crd.Annotations,
		CreationTimestamp: crd.CreationTimestamp.Time,
		Spec: types.SandboxSpec{
			Runtime:         crd.Spec.Runtime,
			SecurityLevel:   crd.Spec.SecurityLevel,
			Timeout:         crd.Spec.Timeout,
			Resources:       crdResourcesToAPI(crd.Spec.Resources),
			NetworkAccess:   crdNetworkToAPI(crd.Spec.NetworkAccess),
			Filesystem:      crdFilesystemToAPI(crd.Spec.Filesystem),
			Storage:         crdStorageToAPI(crd.Spec.Storage),
			SecurityContext: crdSecurityCtxToAPI(crd.Spec.SecurityContext),
			ProfileRef:      crdProfileRefToAPI(crd.Spec.ProfileRef),
		},
		Status: types.SandboxStatus{
			Phase:      crd.Status.Phase,
			Conditions: crdConditionsToAPI(crd.Status.Conditions),
			PodName:    crd.Status.PodName,
			StartTime:  metav1TimeToStdLib(crd.Status.StartTime),
			Resources:  crdResourceStatusToAPI(crd.Status.Resources),
			PodIP:      crd.Status.PodIP,
		},
	}
}

// metav1TimeToStdLib converts a *metav1.Time (CRD-side) to a *time.Time
// (DTO-side). Returns nil for nil input. The .Time field of metav1.Time is
// the underlying time.Time value.
func metav1TimeToStdLib(t *metav1.Time) *time.Time {
	if t == nil {
		return nil
	}
	out := t.Time
	return &out
}

func apiResourcesToCRD(r *types.ResourceRequirements) *v1.ResourceRequirements {
	if r == nil {
		return nil
	}
	return &v1.ResourceRequirements{CPU: r.CPU, Memory: r.Memory, EphemeralStorage: r.EphemeralStorage}
}

func apiNetworkToCRD(n *types.NetworkAccess) *v1.NetworkAccess {
	if n == nil {
		return nil
	}
	egress := make([]v1.EgressRule, 0, len(n.Egress))
	for _, r := range n.Egress {
		ports := make([]v1.PortRule, 0, len(r.Ports))
		for _, p := range r.Ports {
			ports = append(ports, v1.PortRule{Port: p.Port, Protocol: p.Protocol})
		}
		egress = append(egress, v1.EgressRule{Domain: r.Domain, Ports: ports})
	}
	return &v1.NetworkAccess{Egress: egress, Ingress: n.Ingress}
}

func crdResourcesToAPI(r *v1.ResourceRequirements) *types.ResourceRequirements {
	if r == nil {
		return nil
	}
	return &types.ResourceRequirements{CPU: r.CPU, Memory: r.Memory, EphemeralStorage: r.EphemeralStorage}
}

func crdNetworkToAPI(n *v1.NetworkAccess) *types.NetworkAccess {
	if n == nil {
		return nil
	}
	egress := make([]types.EgressRule, 0, len(n.Egress))
	for _, r := range n.Egress {
		ports := make([]types.PortRule, 0, len(r.Ports))
		for _, p := range r.Ports {
			ports = append(ports, types.PortRule{Port: p.Port, Protocol: p.Protocol})
		}
		egress = append(egress, types.EgressRule{Domain: r.Domain, Ports: ports})
	}
	return &types.NetworkAccess{Egress: egress, Ingress: n.Ingress}
}

func crdFilesystemToAPI(f *v1.FilesystemConfig) *types.FilesystemConfig {
	if f == nil {
		return nil
	}
	return &types.FilesystemConfig{ReadOnlyRoot: f.ReadOnlyRoot, WritablePaths: f.WritablePaths}
}

func crdStorageToAPI(s *v1.StorageConfig) *types.StorageConfig {
	if s == nil {
		return nil
	}
	return &types.StorageConfig{Persistent: s.Persistent, VolumeSize: s.VolumeSize}
}

func crdSecurityCtxToAPI(s *v1.SecurityContext) *types.SecurityContext {
	if s == nil {
		return nil
	}
	return &types.SecurityContext{RunAsUser: s.RunAsUser, RunAsGroup: s.RunAsGroup}
}

func crdProfileRefToAPI(p *v1.ProfileReference) *types.ProfileReference {
	if p == nil {
		return nil
	}
	return &types.ProfileReference{Name: p.Name, Namespace: p.Namespace}
}

func crdConditionsToAPI(conditions []v1.SandboxCondition) []types.SandboxCondition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]types.SandboxCondition, 0, len(conditions))
	for _, c := range conditions {
		t := c.LastTransitionTime.Time
		out = append(out, types.SandboxCondition{
			Type:               c.Type,
			Status:             c.Status,
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: &t,
		})
	}
	return out
}

func crdResourceStatusToAPI(r *v1.ResourceStatus) *types.ResourceStatus {
	if r == nil {
		return nil
	}
	return &types.ResourceStatus{CPUUsage: r.CPUUsage, MemoryUsage: r.MemoryUsage}
}

// RestartSandbox triggers a graceful pod restart by incrementing the CRD's
// Spec.RestartGeneration. The controller observes the change and recycles
// the pod. Only works when the sandbox is in Running phase.
func (s *Service) RestartSandbox(ctx context.Context, sandboxID string) error {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("RestartSandbox", "", 0, time.Since(start), 0)
	}()

	userID := userIDFromContext(ctx)
	if userID == "" {
		return apierrors.NewForbiddenError("User authentication required", fmt.Errorf("no user ID in context"))
	}

	// Get the CRD directly (not the DB metadata) since we need to patch spec.
	crd, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return apierrors.NewNotFoundError("sandbox", sandboxID, err)
	}

	// Ownership check: sandbox must belong to the requesting user.
	if owner := crd.Labels["user-id"]; owner != "" && owner != userID {
		return apierrors.NewNotFoundError("sandbox", sandboxID, fmt.Errorf("not found"))
	}

	// Only Running sandboxes can be restarted.
	if crd.Status.Phase != "Running" {
		return &apierrors.APIError{
			Type:    apierrors.ErrorTypeConflict,
			Code:    "invalid_phase",
			Message: fmt.Sprintf("sandbox is in phase %q; restart requires Running", crd.Status.Phase),
			Err:     fmt.Errorf("invalid phase for restart: %s", crd.Status.Phase),
		}
	}

	// Increment RestartGeneration. The controller will observe the change.
	crd.Spec.RestartGeneration = time.Now().UnixNano()

	if _, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Update(crd); err != nil {
		s.logger.Error("Failed to update sandbox for restart", err, "sandboxID", sandboxID)
		return apierrors.NewInternalError("restart_update_failed", err)
	}

	s.logger.Info("Sandbox restart requested", "sandboxID", sandboxID, "userID", userID,
		"restartGeneration", crd.Spec.RestartGeneration)
	return nil
}

// RetrySandbox resets a Failed sandbox back to Pending so the controller
// recreates the pod. Bounded by Spec.MaxRetries (default 3).
func (s *Service) RetrySandbox(ctx context.Context, sandboxID string) error {
	start := time.Now()
	defer func() {
		s.metricsService.RecordRequest("RetrySandbox", "", 0, time.Since(start), 0)
	}()

	userID := userIDFromContext(ctx)
	if userID == "" {
		return apierrors.NewForbiddenError("User authentication required", fmt.Errorf("no user ID in context"))
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return apierrors.NewNotFoundError("sandbox", sandboxID, err)
	}

	if owner := crd.Labels["user-id"]; owner != "" && owner != userID {
		return apierrors.NewNotFoundError("sandbox", sandboxID, fmt.Errorf("not found"))
	}

	if crd.Status.Phase != "Failed" {
		return &apierrors.APIError{
			Type:    apierrors.ErrorTypeConflict,
			Code:    "invalid_phase",
			Message: fmt.Sprintf("sandbox is in phase %q; retry requires Failed", crd.Status.Phase),
			Err:     fmt.Errorf("invalid phase for retry: %s", crd.Status.Phase),
		}
	}

	maxRetries := crd.Spec.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3 // default
	}
	if crd.Status.RestartCount >= maxRetries {
		return &apierrors.APIError{
			Type:    apierrors.ErrorTypeConflict,
			Code:    "max_retries_exceeded",
			Message: fmt.Sprintf("sandbox has reached maximum retries (%d/%d)", crd.Status.RestartCount, maxRetries),
			Err:     fmt.Errorf("max retries exceeded"),
		}
	}

	// Reset to Pending. The controller will create a fresh pod.
	crd.Status.Phase = "Pending"
	crd.Status.TransientFailureCount = 0
	crd.Status.PodName = ""
	crd.Status.PodNamespace = ""
	crd.Status.PodIP = ""

	if _, err := s.k8sClient.LlmsafespaceV1().Sandboxes(s.config.Namespace).UpdateStatus(crd); err != nil {
		s.logger.Error("Failed to update sandbox status for retry", err, "sandboxID", sandboxID)
		return apierrors.NewInternalError("retry_update_failed", err)
	}

	s.logger.Info("Sandbox retry requested", "sandboxID", sandboxID, "userID", userID)
	return nil
}
