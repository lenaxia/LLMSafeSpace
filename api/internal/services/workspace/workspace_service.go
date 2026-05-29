package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	apiinterfaces "github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/settings"
	"github.com/lenaxia/llmsafespace/pkg/types"

	"github.com/google/uuid"
)

func init() {
	opencode.Register()
}

// Service implements apiinterfaces.WorkspaceService.
type Service struct {
	logger           pkginterfaces.LoggerInterface
	k8sClient        pkginterfaces.KubernetesClient
	dbService        apiinterfaces.DatabaseService
	cacheService     apiinterfaces.CacheService
	metricsService   apiinterfaces.MetricsService
	sessionIndex     apiinterfaces.SessionIndexService
	secretInjector   SecretInjector
	instanceSettings *settings.InstanceService
	config           *Config
}

func (s *Service) syncPhase(workspaceID string, phase v1.WorkspacePhase) {
	if phase == "" || workspaceID == "" {
		return
	}
	pvcState := v1.PVCStateNone
	switch phase {
	case v1.WorkspacePhaseActive, v1.WorkspacePhaseCreating, v1.WorkspacePhaseResuming:
		pvcState = v1.PVCStateCluster
	case v1.WorkspacePhaseSuspended, v1.WorkspacePhaseSuspending:
		pvcState = v1.PVCStateCluster
	case v1.WorkspacePhaseTerminating, v1.WorkspacePhaseTerminated, v1.WorkspacePhaseFailed:
		pvcState = v1.PVCStateNone
	}
	db := s.dbService
	if db == nil {
		return
	}
	go func() {
		defer func() { recover() }()
		db.SyncWorkspacePhase(context.Background(), workspaceID, string(phase), string(pvcState))
	}()
}

func (s *Service) markDeleted(workspaceID string) {
	db := s.dbService
	if db == nil {
		return
	}
	go func() {
		defer func() { recover() }()
		db.MarkWorkspaceDeleted(context.Background(), workspaceID)
	}()
}

// Config holds workspace service configuration.
type Config struct {
	Namespace    string
	OpencodePort int // Port for opencode on sandbox pods. Default: 4096.
}

var _ apiinterfaces.WorkspaceService = (*Service)(nil)

// New creates a validated workspace service. config may be nil to use defaults.
func New(
	logger pkginterfaces.LoggerInterface,
	k8sClient pkginterfaces.KubernetesClient,
	dbService apiinterfaces.DatabaseService,
	cacheService apiinterfaces.CacheService,
	metricsService apiinterfaces.MetricsService,
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
		config = &Config{Namespace: "default"}
	}
	return &Service{
		logger:         logger,
		k8sClient:      k8sClient,
		dbService:      dbService,
		cacheService:   cacheService,
		metricsService: metricsService,
		config:         config,
	}, nil
}

// SetSessionIndex injects the session index service. Optional — nil disables session tracking.
func (s *Service) SetSessionIndex(si apiinterfaces.SessionIndexService) {
	s.sessionIndex = si
}

// SecretInjector prepares decrypted secrets for pod injection.
type SecretInjector interface {
	PrepareSecretsForInjection(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error)
}

// SetSecretInjector injects the secret service for pod secret materialization.
func (s *Service) SetSecretInjector(si SecretInjector) {
	s.secretInjector = si
}

func (s *Service) Start() error {
	s.logger.Info("Starting workspace service")
	return nil
}

func (s *Service) Stop() error {
	s.logger.Info("Stopping workspace service")
	return nil
}

// CreateWorkspace validates the request, creates a Workspace CRD, and persists
// metadata to the database. On database failure the CRD is deleted.
func (s *Service) CreateWorkspace(ctx context.Context, userID string, req types.CreateWorkspaceRequest) (*types.Workspace, error) {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("CreateWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	if req.Name == "" {
		return nil, apierrors.NewValidationError(
			"workspace name is required",
			map[string]interface{}{"field": "name"},
			fmt.Errorf("name is empty"),
		)
	}
	if req.StorageSize == "" && s.instanceSettings != nil {
		if size, err := s.instanceSettings.GetString(ctx, "workspace.defaultStorageSize"); err == nil && size != "" {
			req.StorageSize = size
		}
	}
	if req.StorageSize == "" {
		return nil, apierrors.NewValidationError(
			"storage size is required",
			map[string]interface{}{"field": "storageSize"},
			fmt.Errorf("storageSize is empty"),
		)
	}

	// Apply default runtime from settings if not specified
	if req.Runtime == "" && s.instanceSettings != nil {
		if img, err := s.instanceSettings.GetString(ctx, "workspace.defaultImage"); err == nil && img != "" {
			req.Runtime = img
		}
	}

	// Enforce max storage size from instance settings
	if err := s.enforceMaxStorageSize(ctx, req.StorageSize); err != nil {
		return nil, err
	}

	workspaceID := uuid.New().String()

	crd := buildWorkspaceCRD(workspaceID, userID, req, s.config.Namespace)

	// Apply defaults from instance settings to the CRD spec
	s.applyWorkspaceDefaults(ctx, crd)

	s.logger.Info("Creating workspace in Kubernetes", "userID", userID, "name", req.Name)

	created, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Create(crd)
	if err != nil {
		s.logger.Error("Failed to create workspace in Kubernetes", err, "userID", userID)
		return nil, apierrors.NewInternalError("workspace_creation_failed", err)
	}

	meta := &types.WorkspaceMetadata{
		ID:          created.Name,
		UserID:      userID,
		Name:        req.Name,
		Runtime:     req.Runtime,
		StorageSize: req.StorageSize,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.dbService.CreateWorkspace(ctx, meta); err != nil {
		s.logger.Error("Failed to store workspace metadata", err, "workspaceID", created.Name)
		if delErr := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Delete(created.Name, metav1.DeleteOptions{}); delErr != nil {
			s.logger.Error("Failed to clean up workspace after metadata error", delErr, "workspaceID", created.Name)
		}
		return nil, apierrors.NewInternalError("metadata_creation_failed", err)
	}

	s.logger.Info("Workspace created", "workspaceID", created.Name, "userID", userID)

	s.syncPhase(created.Name, created.Status.Phase)

	// Auto-provision default credentials if enabled

	ws := &types.Workspace{
		ID:          meta.ID,
		Name:        meta.Name,
		UserID:      meta.UserID,
		Runtime:     meta.Runtime,
		StorageSize: meta.StorageSize,
		Phase:       string(created.Status.Phase),
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
	}

	return ws, nil
}

// GetWorkspace retrieves a workspace by ID, verifying owner.
func (s *Service) GetWorkspace(ctx context.Context, userID, workspaceID string) (*types.Workspace, error) {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("GetWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	meta, err := s.dbService.GetWorkspace(ctx, workspaceID)
	if err != nil {
		s.logger.Error("Failed to retrieve workspace", err, "workspaceID", workspaceID)
		return nil, apierrors.NewInternalError("workspace_retrieval_failed", err)
	}
	if meta == nil {
		return nil, apierrors.NewNotFoundError("workspace", workspaceID, fmt.Errorf("workspace not found"))
	}
	if meta.UserID != userID {
		return nil, apierrors.NewForbiddenError(
			"user does not own this workspace",
			fmt.Errorf("user %s does not own workspace %s", userID, workspaceID),
		)
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			s.markDeleted(workspaceID)
			crd = nil
		} else {
			s.logger.Warn("Failed to get workspace CRD status", "error", err, "workspaceID", workspaceID)
			crd = nil
		}
	}

	ws := &types.Workspace{
		ID:          meta.ID,
		Name:        meta.Name,
		UserID:      meta.UserID,
		Runtime:     meta.Runtime,
		StorageSize: meta.StorageSize,
		Phase:       meta.Phase,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
	}
	if crd != nil {
		ws.Phase = string(crd.Status.Phase)
		ws.PVCName = crd.Status.PVCName
		s.syncPhase(workspaceID, crd.Status.Phase)
	}

	return ws, nil
}

// ListWorkspaces returns workspace metadata for a user with pagination.
func (s *Service) ListWorkspaces(ctx context.Context, userID string, opts types.ListOptions) (*types.WorkspaceListResult, error) {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("ListWorkspaces", "", 0, time.Since(start), 0)
		}
	}()

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	metas, pagination, err := s.dbService.ListWorkspaces(ctx, userID, limit, opts.Offset)
	if err != nil {
		s.logger.Error("Failed to list workspaces", err, "userID", userID)
		return nil, apierrors.NewInternalError("workspace_list_failed", err)
	}

	items := make([]types.WorkspaceListItem, 0, len(metas))
	for _, m := range metas {
		items = append(items, types.WorkspaceListItem{
			ID:           m.ID,
			Name:         m.Name,
			UserID:       m.UserID,
			Runtime:      m.Runtime,
			StorageSize:  m.StorageSize,
			Phase:        m.Phase,
			ImageTag:     m.ImageTag,
			AgentVersion: m.AgentVersion,
			CreatedAt:    m.CreatedAt,
			UpdatedAt:    m.UpdatedAt,
		})
	}

	return &types.WorkspaceListResult{Items: items, Pagination: pagination}, nil
}

// DeleteWorkspace marks a workspace as terminating and deletes the CRD.
func (s *Service) DeleteWorkspace(ctx context.Context, userID, workspaceID string) error {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("DeleteWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}

	if err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Delete(workspaceID, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		s.logger.Error("Failed to delete workspace CRD", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_deletion_failed", err)
	}

	s.markDeleted(workspaceID)

	s.logger.Info("Workspace deleted", "workspaceID", workspaceID, "userID", userID)
	return nil
}

// SuspendWorkspace transitions a workspace to Suspending phase.
func (s *Service) SuspendWorkspace(ctx context.Context, userID, workspaceID string) error {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("SuspendWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		return apierrors.NewInternalError("workspace_get_failed", err)
	}

	if crd.Status.Phase == v1.WorkspacePhaseSuspended || crd.Status.Phase == v1.WorkspacePhaseSuspending {
		s.syncPhase(workspaceID, crd.Status.Phase)
		return nil
	}

	if crd.Status.Phase != v1.WorkspacePhaseActive {
		return apierrors.NewConflictError(
			"workspace",
			workspaceID,
			fmt.Errorf("cannot suspend workspace in phase %q", crd.Status.Phase),
		)
	}

	crd.Status.Phase = v1.WorkspacePhaseSuspending
	if _, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).UpdateStatus(crd); err != nil {
		s.logger.Error("Failed to update workspace status to Suspending", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_suspend_failed", err)
	}

	s.syncPhase(workspaceID, v1.WorkspacePhaseSuspending)

	s.logger.Info("Workspace suspend initiated", "workspaceID", workspaceID, "userID", userID)
	return nil
}

// ResumeWorkspace transitions a workspace to Resuming phase.
func (s *Service) ResumeWorkspace(ctx context.Context, userID, workspaceID string) error {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("ResumeWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		return apierrors.NewInternalError("workspace_get_failed", err)
	}

	if isActivePhase(crd.Status.Phase) {
		s.syncPhase(workspaceID, crd.Status.Phase)
		return nil
	}

	if crd.Status.Phase != v1.WorkspacePhaseSuspended {
		return apierrors.NewConflictError(
			"workspace",
			workspaceID,
			fmt.Errorf("cannot resume workspace in phase %q (must be Suspended)", crd.Status.Phase),
		)
	}

	crd.Status.Phase = v1.WorkspacePhaseResuming
	if _, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).UpdateStatus(crd); err != nil {
		s.logger.Error("Failed to update workspace status to Resuming", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_resume_failed", err)
	}

	s.syncPhase(workspaceID, v1.WorkspacePhaseResuming)

	s.logger.Info("Workspace resume initiated", "workspaceID", workspaceID, "userID", userID)
	return nil
}

// GetWorkspaceStatus returns infrastructure state from the Workspace CRD.
func (s *Service) GetWorkspaceStatus(ctx context.Context, userID, workspaceID string) (*types.WorkspaceStatusResult, error) {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("GetWorkspaceStatus", "", 0, time.Since(start), 0)
		}
	}()

	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			s.markDeleted(workspaceID)
			return nil, apierrors.NewNotFoundError("workspace", workspaceID, err)
		}
		return nil, apierrors.NewInternalError("workspace_get_failed", err)
	}

	result := &types.WorkspaceStatusResult{
		Phase:          string(crd.Status.Phase),
		PVCName:        crd.Status.PVCName,
		ActiveSessions: int(crd.Status.ActiveSessions),
		Message:        crd.Status.Message,
		ImageTag:       crd.Status.ImageTag,
	}

	// Fallback: if controller hasn't set ImageTag yet (pre-upgrade pods), read from pod spec
	if result.ImageTag == "" && crd.Status.PodName != "" {
		ns := crd.Status.PodNamespace
		if ns == "" {
			ns = s.config.Namespace
		}
		if pod, podErr := s.k8sClient.Clientset().CoreV1().Pods(ns).Get(ctx, crd.Status.PodName, metav1.GetOptions{}); podErr == nil {
			if len(pod.Spec.Containers) > 0 {
				image := pod.Spec.Containers[0].Image
				if i := strings.LastIndex(image, ":"); i >= 0 {
					result.ImageTag = image[i+1:]
				} else {
					result.ImageTag = image
				}
			}
		}
	}
	if crd.Status.LastActivityAt != nil {
		t := crd.Status.LastActivityAt.Time
		result.LastActivityAt = &t
	}
	for _, c := range crd.Status.Conditions {
		result.Conditions = append(result.Conditions, types.WorkspaceConditionResult{
			Type:    string(c.Type),
			Status:  c.Status,
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	result.CredentialState = credStateFromConditions(crd.Status.Conditions)
	result.AgentHealth = agentHealthFromConditions(crd.Status.Conditions, crd.Status.LastHealthCheckAt)

	for _, s := range crd.Status.Sessions {
		result.Sessions = append(result.Sessions, types.SessionStatusItem{
			ID: s.ID, Title: s.Title, Status: s.Status,
		})
	}
	result.DiskUsedBytes = crd.Status.DiskUsedBytes
	result.DiskTotalBytes = crd.Status.DiskTotalBytes

	s.syncPhase(workspaceID, crd.Status.Phase)

	// Persist version info to DB so it's available in workspace list without extra K8s calls
	if result.ImageTag != "" || result.AgentHealth.AgentVersion != "" {
		s.dbService.SyncWorkspaceVersionInfo(ctx, workspaceID, result.ImageTag, result.AgentHealth.AgentVersion)
	}

	return result, nil
}

// verifyOwner returns a forbidden or not-found error if the user does not own
// the workspace. Returns nil when the user is the owner.
func (s *Service) verifyOwner(ctx context.Context, userID, workspaceID string) error {
	meta, err := s.dbService.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return apierrors.NewInternalError("workspace_retrieval_failed", err)
	}
	if meta == nil {
		return apierrors.NewNotFoundError("workspace", workspaceID, fmt.Errorf("workspace not found"))
	}
	if meta.UserID != userID {
		return apierrors.NewForbiddenError(
			"user does not own this workspace",
			fmt.Errorf("user %s does not own workspace %s", userID, workspaceID),
		)
	}
	return nil
}

// buildWorkspaceCRD constructs a v1.Workspace CRD from an API request.
func buildWorkspaceCRD(workspaceID, userID string, req types.CreateWorkspaceRequest, namespace string) *v1.Workspace {
	labels := map[string]string{
		"app":     "llmsafespace",
		"user-id": userID,
	}
	for k, v := range req.Labels {
		labels[k] = v
	}

	spec := v1.WorkspaceSpec{
		Owner: v1.WorkspaceOwner{UserID: userID},
		Storage: v1.WorkspaceStorageConfig{
			Size:             req.StorageSize,
			StorageClassName: req.StorageClass,
		},
		Runtime: req.Runtime,
	}

	return &v1.Workspace{
		TypeMeta: metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Workspace"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      workspaceID,
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"llmsafespace.dev/created-by": userID,
				"llmsafespace.dev/name":       req.Name,
			},
		},
		Spec: spec,
	}
}

// applyWorkspaceDefaults reads instance settings and applies defaults to the
// CRD spec for fields not already set by the request. Gracefully degrades if
// settings are unavailable.
func (s *Service) applyWorkspaceDefaults(ctx context.Context, crd *v1.Workspace) {
	if s.instanceSettings == nil {
		return
	}

	// Security level
	if crd.Spec.SecurityLevel == "" {
		if level, err := s.instanceSettings.GetString(ctx, "workspace.defaultSecurityLevel"); err == nil && level != "" {
			crd.Spec.SecurityLevel = level
		}
	}

	// Storage class
	if crd.Spec.Storage.StorageClassName == "" {
		if sc, err := s.instanceSettings.GetString(ctx, "workspace.defaultStorageClass"); err == nil && sc != "" {
			crd.Spec.Storage.StorageClassName = sc
		}
	}

	// Resources
	if crd.Spec.Resources == nil {
		cpu, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.cpu")
		mem, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.memory")
		eph, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.ephemeralStorage")
		if cpu != "" || mem != "" || eph != "" {
			crd.Spec.Resources = &v1.ResourceRequirements{
				CPU: cpu, Memory: mem, EphemeralStorage: eph,
			}
		}
	}

	// Auto-suspend (only if not already set by request/CRD)
	if crd.Spec.AutoSuspend == nil {
		autoSuspendEnabled := true
		idleTimeout := int64(86400)
		if v, err := s.instanceSettings.GetBool(ctx, "workspace.autoSuspend.enabled"); err == nil {
			autoSuspendEnabled = v
		}
		if v, err := s.instanceSettings.GetInt(ctx, "workspace.autoSuspend.idleTimeoutMinutes"); err == nil && v > 0 {
			idleTimeout = int64(v) * 60
		}
		crd.Spec.AutoSuspend = &v1.WorkspaceAutoSuspend{
			Enabled:            autoSuspendEnabled,
			IdleTimeoutSeconds: idleTimeout,
		}
	}

	// TTL after suspended
	if days, err := s.instanceSettings.GetInt(ctx, "workspace.ttlDaysAfterSuspended"); err == nil && days > 0 {
		crd.Spec.TTLSecondsAfterSuspended = int64(days) * 86400
	}

	// Network access
	if crd.Spec.NetworkAccess == nil {
		ingress, _ := s.instanceSettings.GetBool(ctx, "workspace.defaultNetworkAccess.ingress")
		domains, _ := s.instanceSettings.GetStrings(ctx, "workspace.defaultNetworkAccess.egressDomains")
		if ingress || len(domains) > 0 {
			egress := make([]v1.WorkspaceEgressRule, len(domains))
			for i, d := range domains {
				egress[i] = v1.WorkspaceEgressRule{Domain: d}
			}
			crd.Spec.NetworkAccess = &v1.WorkspaceNetworkAccess{
				Ingress: ingress, Egress: egress,
			}
		}
	}
}

// --- Frontend methods (Phase A) ---

// EnsureSession guarantees the workspace has a Running sandbox and creates a
// new session on it. If the workspace is suspended it resumes it; if no sandbox
// exists it creates one. Blocks until the sandbox reaches Running, then creates
// the session via opencode's POST /session endpoint.
func (s *Service) EnsureSession(ctx context.Context, userID, workspaceID string) (*types.EnsureSessionResponse, error) {
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		return nil, apierrors.NewInternalError("workspace_get_failed", err)
	}

	resumed := false
	switch crd.Status.Phase {
	case v1.WorkspacePhaseSuspended:
		if err := s.ResumeWorkspace(ctx, userID, workspaceID); err != nil {
			return nil, err
		}
		resumed = true
	case v1.WorkspacePhaseTerminating, v1.WorkspacePhaseTerminated, v1.WorkspacePhaseFailed:
		return nil, apierrors.NewValidationError(
			"workspace is not usable",
			map[string]interface{}{"phase": string(crd.Status.Phase)},
			fmt.Errorf("workspace %s is in %s phase", workspaceID, crd.Status.Phase),
		)
	case v1.WorkspacePhasePending, v1.WorkspacePhaseCreating, v1.WorkspacePhaseActive, v1.WorkspacePhaseResuming:
		// Will wait for Active below.
	}

	// Wait for workspace to reach Active with PodIP.
	podIP, err := s.waitForWorkspaceActive(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	// Create session directly on workspace pod.
	sessionID, err := s.createSessionOnWorkspace(ctx, workspaceID, podIP)
	if err != nil {
		return nil, err
	}

	return &types.EnsureSessionResponse{
		WorkspaceID:    workspaceID,
		WorkspacePhase: "Active",
		SessionID:      sessionID,
		Resumed:        resumed,
	}, nil
}

// waitForWorkspaceActive polls the workspace CRD until it reaches Active with
// a PodIP, or the context is cancelled. Returns the pod IP.
func (s *Service) waitForWorkspaceActive(ctx context.Context, workspaceID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
		if err != nil {
			return "", apierrors.NewInternalError("workspace_get_failed", err)
		}
		if crd.Status.Phase == v1.WorkspacePhaseActive && crd.Status.PodIP != "" {
			s.syncPhase(workspaceID, crd.Status.Phase)
			return crd.Status.PodIP, nil
		}
		if crd.Status.Phase == v1.WorkspacePhaseFailed || crd.Status.Phase == v1.WorkspacePhaseTerminated {
			return "", apierrors.NewInternalError("workspace_failed", fmt.Errorf("workspace %s entered %s phase", workspaceID, crd.Status.Phase))
		}
		select {
		case <-ctx.Done():
			return "", apierrors.NewInternalError("workspace_timeout", fmt.Errorf("timed out waiting for workspace %s to reach Active", workspaceID))
		case <-ticker.C:
		}
	}
}

// createSessionOnWorkspace calls opencode's POST /session on the workspace pod.
func (s *Service) createSessionOnWorkspace(ctx context.Context, workspaceID, podIP string) (string, error) {
	secretName := fmt.Sprintf("workspace-pw-%s", workspaceID)
	secret, err := s.k8sClient.Clientset().CoreV1().Secrets(s.config.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", apierrors.NewInternalError("workspace_password_failed", err)
	}
	password := string(secret.Data["password"])

	port := s.config.OpencodePort
	if port == 0 {
		port = 4096
	}
	url := fmt.Sprintf("http://%s:%d/session", podIP, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", apierrors.NewInternalError("session_request_failed", err)
	}
	req.SetBasicAuth("opencode", password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", apierrors.NewInternalError("session_create_failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apierrors.NewInternalError("session_create_failed", fmt.Errorf("opencode returned %d", resp.StatusCode))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", apierrors.NewInternalError("session_decode_failed", err)
	}
	return result.ID, nil
}

// ActivateWorkspace resumes a workspace, suspending the stalest active one if at cap.
func (s *Service) ActivateWorkspace(ctx context.Context, userID, workspaceID string) (*types.ActivateWorkspaceResponse, error) {
	// Verify ownership
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	// Inject secrets into ephemeral K8s Secret (Epic 10).
	if s.secretInjector != nil {
		sessionID, _ := ctx.Value(sessionIDContextKey).(string)
		if sessionID != "" {
			secretsJSON, err := s.secretInjector.PrepareSecretsForInjection(ctx, userID, sessionID, workspaceID)
			if err != nil {
				s.logger.Warn("Failed to prepare secrets for injection", "workspaceID", workspaceID, "error", err.Error())
			} else if len(secretsJSON) > 2 {
				s.createEphemeralSecretsSecret(ctx, workspaceID, secretsJSON)
			}
		}
	}

	// Enforce max active workspaces — may suspend the stalest workspace
	suspended, err := s.enforceMaxActiveWorkspaces(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	// Resume the target workspace
	if err := s.ResumeWorkspace(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	return &types.ActivateWorkspaceResponse{
		Resumed:   workspaceID,
		Suspended: suspended,
	}, nil
}

// ListWorkspaceSessions returns session index entries for a workspace.
func (s *Service) ListWorkspaceSessions(ctx context.Context, userID, workspaceID string) ([]types.SessionListItem, error) {
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	if s.sessionIndex == nil {
		return []types.SessionListItem{}, nil
	}
	return s.sessionIndex.ListByWorkspace(ctx, workspaceID)
}

// RenameWorkspace updates the name of a workspace.
func (s *Service) RenameWorkspace(ctx context.Context, userID, workspaceID, name string) error {
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}
	return s.dbService.UpdateWorkspace(ctx, workspaceID, types.WorkspaceUpdates{Name: &name})
}

// RenameSession updates the title of a session in the session index.
func (s *Service) RenameSession(ctx context.Context, userID, workspaceID, sessionID, title string) error {
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}
	if s.sessionIndex == nil {
		return nil
	}
	return s.sessionIndex.UpsertTitle(ctx, workspaceID, sessionID, title)
}

func credStateFromConditions(conditions []v1.WorkspaceCondition) types.CredentialStateResult {
	for _, c := range conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			return types.CredentialStateResult{
				Available: c.Status == "True",
				Reason:    c.Reason,
				Message:   c.Message,
			}
		}
	}
	return types.CredentialStateResult{Available: false, Reason: "NotChecked"}
}

var connectedRe = regexp.MustCompile(`connected=\[([^\]]*)\]`)
var versionRe = regexp.MustCompile(`version=(\S+)`)
var configuredRe = regexp.MustCompile(`configured=(\d+)`)

func agentHealthFromConditions(conditions []v1.WorkspaceCondition, lastCheckAt *metav1.Time) types.AgentHealthResult {
	for _, c := range conditions {
		if c.Type == v1.WorkspaceConditionAgentHealthy {
			status := "Unknown"
			if c.Status == "True" {
				status = "Healthy"
			} else if c.Status == "False" {
				switch c.Reason {
				case v1.ReasonAgentUnhealthy, v1.ReasonHealthCheckFailed:
					status = "Unhealthy"
				default:
					status = "Degraded"
				}
			}
			result := types.AgentHealthResult{
				Status:  status,
				Message: c.Message,
			}
			if m := connectedRe.FindStringSubmatch(c.Message); len(m) > 1 && m[1] != "" {
				parts := strings.Split(m[1], " ")
				result.Connected = make([]string, 0, len(parts))
				for _, p := range parts {
					if p != "" {
						result.Connected = append(result.Connected, p)
					}
				}
			}
			if m := versionRe.FindStringSubmatch(c.Message); len(m) > 1 {
				result.AgentVersion = m[1]
			}
			if m := configuredRe.FindStringSubmatch(c.Message); len(m) > 1 {
				fmt.Sscanf(m[1], "%d", &result.ProvidersConfigured)
			}
			if lastCheckAt != nil {
				result.LastCheckedAt = lastCheckAt.Format(time.RFC3339)
			}
			return result
		}
	}
	return types.AgentHealthResult{Status: "Unknown"}
}

// --- Epic 10: Secret injection helpers ---

type sessionIDCtxKey struct{}

var sessionIDContextKey = sessionIDCtxKey{}

// ContextWithSessionID adds the session ID to context for secret injection during activation.
func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey, sessionID)
}

func (s *Service) createEphemeralSecretsSecret(ctx context.Context, workspaceID string, secretsJSON []byte) {
	secretName := fmt.Sprintf("workspace-secrets-%s", workspaceID)
	clientset := s.k8sClient.Clientset()
	secretClient := clientset.CoreV1().Secrets(s.config.Namespace)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: s.config.Namespace,
			Labels: map[string]string{
				"app":                        "llmsafespace",
				"llmsafespace.dev/workspace": workspaceID,
				"llmsafespace.dev/ephemeral": "true",
			},
		},
		Data: map[string][]byte{
			"secrets.json": secretsJSON,
		},
	}

	existing, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		existing.Data = secret.Data
		if _, err := secretClient.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			s.logger.Error("Failed to update ephemeral secrets secret", err, "workspaceID", workspaceID)
		}
	} else {
		if _, err := secretClient.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			s.logger.Error("Failed to create ephemeral secrets secret", err, "workspaceID", workspaceID)
		}
	}
}
