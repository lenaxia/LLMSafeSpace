// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"k8s.io/client-go/util/retry"

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

// WorkspaceConfig is non-sensitive workspace metadata persisted for pod boot.
// Re-exported from pkg/types to avoid requiring callers to import both.
type WorkspaceConfig = types.WorkspaceConfig

// Service implements apiinterfaces.WorkspaceService.
type Service struct {
	logger           pkginterfaces.LoggerInterface
	k8sClient        pkginterfaces.KubernetesClient
	dbService        apiinterfaces.DatabaseService
	cacheService     apiinterfaces.CacheService
	metricsService   apiinterfaces.MetricsService
	sessionIndex     apiinterfaces.SessionIndexService
	secretInjector   SecretInjector
	credProvisioner  CredentialProvisioner
	instanceSettings *settings.InstanceService
	orgStore         OrgMembershipChecker
	policyChecker    PolicyChecker
	config           *Config
}

type OrgMembershipChecker interface {
	IsOrgMember(ctx context.Context, orgID, userID string) (bool, error)
	IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error)
}

// PolicyChecker reads the effective org policy for enforcement. The policy
// service implements this; nil means no policy enforcement (dev/test).
type PolicyChecker interface {
	GetEffectivePolicy(ctx context.Context, orgID string) (*types.OrgPolicyValues, error)
}

func (s *Service) SetOrgStore(store OrgMembershipChecker) {
	s.orgStore = store
}

// SetPolicyChecker installs the org policy checker for workspace quota enforcement.
func (s *Service) SetPolicyChecker(checker PolicyChecker) {
	s.policyChecker = checker
}

// CredentialProvisioner seeds workspace_credential_bindings from credential_auto_apply.
type CredentialProvisioner interface {
	SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string, orgID *string) error
}

// SetCredentialProvisioner installs the credential auto-apply seeder.
func (s *Service) SetCredentialProvisioner(cp CredentialProvisioner) {
	s.credProvisioner = cp
}

func (s *Service) workspaceCRDClient() (pkginterfaces.WorkspaceInterface, error) {
	v1Client, err := s.k8sClient.LlmsafespaceV1()
	if err != nil {
		return nil, fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
	}
	return v1Client.Workspaces(s.config.Namespace), nil
}

// markDeleted soft-deletes a workspace metadata row in the background.
// It accepts a context for symmetry with the caller and to silence
// contextcheck, but deliberately does NOT propagate it: the caller is
// typically a request goroutine whose context gets canceled as soon
// as the HTTP response is flushed, which would race with the marker
// write. context.Background inside the goroutine is correct and
// intentional. The ctx parameter is currently unused; it exists so
// future tracing/observability can be plumbed without changing every
// call site.
func (s *Service) markDeleted(_ context.Context, workspaceID string) {
	db := s.dbService
	if db == nil {
		return
	}
	//nolint:gosec,contextcheck // G118: intentional fresh context for fire-and-forget cleanup; see godoc above
	go func() {
		defer func() { _ = recover() }()
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

	if req.OrgID != nil && *req.OrgID != "" {
		if s.orgStore == nil {
			return nil, apierrors.NewValidationError(
				"org support not configured",
				map[string]interface{}{"field": "orgId"},
				fmt.Errorf("org store not available"),
			)
		}
		isMember, err := s.orgStore.IsOrgMember(ctx, *req.OrgID, userID)
		if err != nil {
			return nil, apierrors.NewInternalError("org_membership_check_failed", err)
		}
		if !isMember {
			return nil, apierrors.NewForbiddenError("user is not a member of the specified org", fmt.Errorf("user %s is not a member of org %s", userID, *req.OrgID))
		}

		// US-43.8: Enforce org workspace quotas.
		if s.policyChecker != nil {
			pol, err := s.policyChecker.GetEffectivePolicy(ctx, *req.OrgID)
			if err != nil {
				return nil, apierrors.NewInternalError("policy_check_failed", err)
			}
			if pol != nil {
				if maxTotal := pol.MaxWorkspaces(); maxTotal >= 0 {
					count, err := s.dbService.CountWorkspacesByUserAndOrg(ctx, userID, *req.OrgID)
					if err != nil {
						return nil, apierrors.NewInternalError("workspace_count_failed", err)
					}
					if count >= maxTotal {
						return nil, apierrors.NewValidationError(
							fmt.Sprintf("workspace quota exceeded: you have %d of %d allowed workspaces in this org", count, maxTotal),
							map[string]interface{}{"policy": "max_workspaces_per_member"},
							fmt.Errorf("quota exceeded: %d >= %d", count, maxTotal),
						)
					}
				}
				if maxActive := pol.MaxActive(); maxActive >= 0 {
					active, err := s.dbService.CountActiveWorkspacesByUserAndOrg(ctx, userID, *req.OrgID)
					if err != nil {
						return nil, apierrors.NewInternalError("active_workspace_count_failed", err)
					}
					if active >= maxActive {
						return nil, apierrors.NewValidationError(
							fmt.Sprintf("active workspace quota exceeded: you have %d of %d concurrent active workspaces", active, maxActive),
							map[string]interface{}{"policy": "max_active_workspaces_per_member"},
							fmt.Errorf("active quota exceeded: %d >= %d", active, maxActive),
						)
					}
				}
			}
		}
	}

	// Apply default runtime from settings if not specified
	if req.Runtime == "" && s.instanceSettings != nil {
		if img, err := s.instanceSettings.GetString(ctx, "workspace.defaultImage"); err == nil && img != "" {
			req.Runtime = img
		}
	}

	workspaceID := uuid.New().String()

	crd := buildWorkspaceCRD(workspaceID, userID, req, s.config.Namespace)

	// Apply defaults from instance settings to the CRD spec
	s.applyWorkspaceDefaults(ctx, crd)

	s.logger.Info("Creating workspace in Kubernetes", "userID", userID, "name", req.Name)

	created, err := func() (*v1.Workspace, error) {
		wsClient, err := s.workspaceCRDClient()
		if err != nil {
			return nil, err
		}
		return wsClient.Create(ctx, crd)
	}()
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
		OrgID:       req.OrgID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.dbService.CreateWorkspace(ctx, meta); err != nil {
		s.logger.Error("Failed to store workspace metadata", err, "workspaceID", created.Name)
		if delErr := func() error {
			wsClient, wErr := s.workspaceCRDClient()
			if wErr != nil {
				return wErr
			}
			return wsClient.Delete(ctx, created.Name, metav1.DeleteOptions{})
		}(); delErr != nil {
			s.logger.Error("Failed to clean up workspace after metadata error", delErr, "workspaceID", created.Name)
		}
		return nil, apierrors.NewInternalError("metadata_creation_failed", err)
	}

	s.logger.Info("Workspace created", "workspaceID", created.Name, "userID", userID)

	// Auto-provision default credentials if enabled (Epic 30).
	// Seeding is best-effort: a failure does NOT roll back workspace creation,
	// but is logged at Error (not Warn) so it surfaces in alerting dashboards.
	if s.credProvisioner != nil {
		if err := s.credProvisioner.SeedWorkspaceCredentials(ctx, meta.ID, userID, meta.OrgID); err != nil {
			s.logger.Error("credential seeding failed for new workspace; it will have no LLM credentials",
				err, "workspaceID", meta.ID, "userID", userID)
		}
	}

	// Write the workspace-secrets K8s Secret so the pod's init container
	// finds credentials on first boot. The user's DEK is always available
	// at workspace creation time — the user must be authenticated (JWT in
	// context) to reach this code path, and the DEK is unlocked at login
	// and at registration. Using refreshEphemeralSecrets (not seed) injects
	// the full user credential set (thekao API keys etc.) rather than
	// admin-only platform credentials.
	s.refreshEphemeralSecrets(ctx, userID, meta.ID)

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
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		if k8serrors.IsNotFound(err) {
			s.markDeleted(ctx, workspaceID)
			crd = nil
		} else {
			s.logger.Warn("Failed to get workspace CRD status", "error", err, "workspaceID", workspaceID)
			crd = nil
		}
	}

	ws := &types.Workspace{
		ID:                      meta.ID,
		Name:                    meta.Name,
		UserID:                  meta.UserID,
		Runtime:                 meta.Runtime,
		StorageSize:             meta.StorageSize,
		CreatedAt:               meta.CreatedAt,
		UpdatedAt:               meta.UpdatedAt,
		DefaultModel:            meta.DefaultModel,
		AgentNeedsRefresh:       meta.AgentNeedsRefresh,
		CredentialsPendingSince: meta.CredentialsPendingSince,
	}
	if crd != nil {
		ws.Phase = string(crd.Status.Phase)
		ws.PVCName = crd.Status.PVCName
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

	// Phase is owned by the Workspace CRD; the DB only stores immutable
	// metadata. We enrich the list with phase via a single label-scoped LIST.
	// On k8s error the items are returned with empty phase; the platform is
	// already unusable in that scenario (every other operation hits the
	// kube-apiserver too) so there's nothing meaningful to fall back to.

	items := make([]types.WorkspaceListItem, 0, len(metas))
	phases := s.fetchUserWorkspacePhases(ctx, userID)
	for _, m := range metas {
		items = append(items, types.WorkspaceListItem{
			ID:                      m.ID,
			Name:                    m.Name,
			UserID:                  m.UserID,
			Runtime:                 m.Runtime,
			StorageSize:             m.StorageSize,
			Phase:                   phases[m.ID],
			ImageTag:                m.ImageTag,
			AgentVersion:            m.AgentVersion,
			CreatedAt:               m.CreatedAt,
			UpdatedAt:               m.UpdatedAt,
			DefaultModel:            m.DefaultModel,
			AgentNeedsRefresh:       m.AgentNeedsRefresh,
			CredentialsPendingSince: m.CredentialsPendingSince,
		})
	}

	return &types.WorkspaceListResult{Items: items, Pagination: pagination}, nil
}

// fetchUserWorkspacePhases returns id -> phase for the user's workspaces by
// listing CRDs filtered with the user-id label. Returns nil on k8s error so
// callers degrade gracefully (empty phase is propagated to the API response).
// A nil map is safe to read from in Go.
func (s *Service) fetchUserWorkspacePhases(ctx context.Context, userID string) map[string]string {
	if s.k8sClient == nil || userID == "" {
		return nil
	}
	list, err := func() (*v1.WorkspaceList, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.List(ctx, metav1.ListOptions{
			LabelSelector: "user-id=" + userID,
		})
	}()
	if err != nil {
		s.logger.Warn("Failed to list workspaces from CRDs for phase enrichment",
			"userID", userID, "error", err.Error())
		return nil
	}
	out := make(map[string]string, len(list.Items))
	for i := range list.Items {
		w := &list.Items[i]
		out[w.Name] = string(w.Status.Phase)
	}
	return out
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

	if err := func() error {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return wErr
		}
		return wsClient.Delete(ctx, workspaceID, metav1.DeleteOptions{})
	}(); err != nil && !k8serrors.IsNotFound(err) {
		s.logger.Error("Failed to delete workspace CRD", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_deletion_failed", err)
	}

	s.markDeleted(ctx, workspaceID)

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

	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		return apierrors.NewInternalError("workspace_get_failed", err)
	}

	if crd.Status.Phase == v1.WorkspacePhaseSuspended || crd.Status.Phase == v1.WorkspacePhaseSuspending {
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
	if _, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.UpdateStatus(ctx, crd)
	}(); err != nil {
		s.logger.Error("Failed to update workspace status to Suspending", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_suspend_failed", err)
	}

	s.logger.Info("Workspace suspend initiated", "workspaceID", workspaceID, "userID", userID)
	return nil
}

// RestartWorkspace bumps spec.restartGeneration so the controller's
// handleFailed (Epic 21 Change A) or handleActive recovery paths walk
// the workspace back through Pending and rebuild the pod from scratch.
//
// Use cases:
//   - Recover a Failed workspace (the original motivation; previously
//     required `kubectl patch --subresource=status`).
//   - Force-restart a stuck Active workspace whose agent is hung but
//     the controller hasn't yet exhausted its transient-failure budget.
//
// Restart is REJECTED for Terminating/Terminated phases — those are
// genuinely terminal and would race with finalizer cleanup. For all
// other phases the call is idempotent at the spec layer (each call
// bumps the field by 1; the controller responds to each bump exactly
// once via the strict-greater-than check on observedRestartGeneration).
//
// Before the spec bump, refreshEphemeralSecrets is invoked so the
// freshly-built pod will mount up-to-date user-provided secrets. This
// fixes the bug where restarting an Active workspace would lose its
// `workspace-secrets-<id>` K8s Secret (e.g. SSH keys), because the
// controller-side pod rebuild path does not call back into the API
// service to re-emit it. See worklog 0120.
func (s *Service) RestartWorkspace(ctx context.Context, userID, workspaceID string) error {
	start := time.Now()
	defer func() {
		if s.metricsService != nil {
			s.metricsService.RecordRequest("RestartWorkspace", "", 0, time.Since(start), 0)
		}
	}()

	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}

	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		return apierrors.NewInternalError("workspace_get_failed", err)
	}

	if crd.Status.Phase == v1.WorkspacePhaseTerminating || crd.Status.Phase == v1.WorkspacePhaseTerminated {
		return apierrors.NewConflictError(
			"workspace",
			workspaceID,
			fmt.Errorf("cannot restart workspace in phase %q", crd.Status.Phase),
		)
	}

	s.refreshEphemeralSecrets(ctx, userID, workspaceID)

	crd.Spec.RestartGeneration++
	if _, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Update(ctx, crd)
	}(); err != nil {
		s.logger.Error("Failed to bump RestartGeneration", err, "workspaceID", workspaceID)
		return apierrors.NewInternalError("workspace_restart_failed", err)
	}

	s.logger.Info("Workspace restart initiated",
		"workspaceID", workspaceID, "userID", userID,
		"restartGeneration", crd.Spec.RestartGeneration, "fromPhase", string(crd.Status.Phase))
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

	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		if k8serrors.IsNotFound(err) {
			s.markDeleted(ctx, workspaceID)
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
			ID: s.ID, Title: s.Title, Status: s.Status, ContextUsed: s.ContextUsed,
		})
	}
	result.DiskUsedBytes = crd.Status.DiskUsedBytes
	result.DiskTotalBytes = crd.Status.DiskTotalBytes
	result.MemoryUsedBytes = crd.Status.MemoryUsedBytes
	result.MemoryTotalBytes = crd.Status.MemoryTotalBytes
	result.ContextUsed = crd.Status.ContextUsed
	result.ContextTotal = crd.Status.ContextTotal

	// Persist version info to DB so it's available in workspace list without extra K8s calls
	if result.ImageTag != "" || result.AgentHealth.AgentVersion != "" {
		s.dbService.SyncWorkspaceVersionInfo(ctx, workspaceID, result.ImageTag, result.AgentHealth.AgentVersion)
	}

	return result, nil
}

// verifyOwner returns a forbidden or not-found error if the user does not own
// the workspace. Returns nil when the user is the owner.
//
// Per Epic 43 decision D6, access to an org workspace requires either being the
// creator (meta.UserID) or an org admin (IsOrgAdmin). Plain org members can only
// access workspaces they themselves created — they can no longer reach other
// members' org workspaces. This makes verifyOwner equivalent to the former
// verifyOrgAdmin; that method has been consolidated into this one.
func (s *Service) verifyOwner(ctx context.Context, userID, workspaceID string) error {
	meta, err := s.dbService.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return apierrors.NewInternalError("workspace_retrieval_failed", err)
	}
	if meta == nil {
		return apierrors.NewNotFoundError("workspace", workspaceID, fmt.Errorf("workspace not found"))
	}
	if meta.UserID == userID {
		return nil
	}
	if meta.OrgID != nil && *meta.OrgID != "" && s.orgStore != nil {
		isAdmin, err := s.orgStore.IsOrgAdmin(ctx, *meta.OrgID, userID)
		if err != nil {
			return fmt.Errorf("check org admin: %w", err)
		}
		if isAdmin {
			return nil
		}
	}
	return apierrors.NewForbiddenError(
		"workspace access denied",
		fmt.Errorf("user %s does not have access to workspace %s", userID, workspaceID),
	)
}

// verifyOwner (above) is the single access check post-D6. It grants access to
// the workspace creator or, for org workspaces, an org admin. The former
// verifyOrgAdmin was identical in behavior and has been removed; its call sites
// now use verifyOwner directly.

// buildWorkspaceCRD constructs a v1.Workspace CRD from an API request.
func buildWorkspaceCRD(workspaceID, userID string, req types.CreateWorkspaceRequest, namespace string) *v1.Workspace {
	labels := map[string]string{
		"app":     "llmsafespace",
		"user-id": userID,
	}
	if req.OrgID != nil && *req.OrgID != "" {
		labels["org-id"] = *req.OrgID
	}
	for k, v := range req.Labels {
		labels[k] = v
	}

	owner := v1.WorkspaceOwner{UserID: userID}
	if req.OrgID != nil {
		owner.OrgID = *req.OrgID
	}

	spec := v1.WorkspaceSpec{
		Owner: owner,
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
				// AnnotationRequestedAt records the exact moment the API
				// received the create request. The controller reads this to
				// anchor WorkspaceCreateDurationSeconds from the user's
				// perspective rather than the controller's first reconcile.
				v1.AnnotationRequestedAt: time.Now().UTC().Format(time.RFC3339Nano),
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
		if cpu != "" || mem != "" {
			crd.Spec.Resources = &v1.ResourceRequirements{
				CPU: cpu, Memory: mem,
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

	// Max active sessions — enforced by the proxy on every request.
	// Without this, the proxy falls back to a hardcoded constant of 5
	// regardless of what the admin configured. (Epic 13 US-13.3)
	if crd.Spec.MaxActiveSessions == 0 {
		if v, err := s.instanceSettings.GetInt(ctx, "workspace.defaultMaxActiveSessions"); err == nil && v > 0 {
			crd.Spec.MaxActiveSessions = int32(v) //nolint:gosec // v is bounded by settings schema (1-100); overflow impossible
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

	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		return nil, apierrors.NewInternalError("workspace_get_failed", err)
	}

	resumed := false
	switch crd.Status.Phase {
	case v1.WorkspacePhaseSuspended:
		// ActivateWorkspace injects credentials then transitions to Resuming.
		if _, err := s.ActivateWorkspace(ctx, userID, workspaceID); err != nil {
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
// a PodIP, or the context is canceled. Returns the pod IP.
func (s *Service) waitForWorkspaceActive(ctx context.Context, workspaceID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		crd, err := func() (*v1.Workspace, error) {
			wsClient, wErr := s.workspaceCRDClient()
			if wErr != nil {
				return nil, wErr
			}
			return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
		}()
		if err != nil {
			return "", apierrors.NewInternalError("workspace_get_failed", err)
		}
		if crd.Status.Phase == v1.WorkspacePhaseActive && crd.Status.PodIP != "" {
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
	defer func() { _ = resp.Body.Close() }()

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

	// Inject credentials into ephemeral K8s Secret before transitioning to
	// Resuming so the pod's credential-setup init container finds secrets.json
	// on boot. This is the critical step missing from the removed resume path.
	s.refreshEphemeralSecrets(ctx, userID, workspaceID)

	// Enforce max active workspaces — may suspend the stalest workspace
	suspended, err := s.enforceMaxActiveWorkspaces(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	// Fetch current CRD state and transition to Resuming.
	crd, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil {
		return nil, apierrors.NewInternalError("workspace_get_failed", err)
	}

	if isActivePhase(crd.Status.Phase) {
		// Already active — nothing to do (idempotent).
		return &types.ActivateWorkspaceResponse{
			Resumed:   workspaceID,
			Suspended: suspended,
		}, nil
	}

	if crd.Status.Phase != v1.WorkspacePhaseSuspended {
		return nil, apierrors.NewConflictError(
			"workspace",
			workspaceID,
			fmt.Errorf("cannot activate workspace in phase %q (must be Suspended or Active)", crd.Status.Phase),
		)
	}

	crd.Status.Phase = v1.WorkspacePhaseResuming
	now := metav1.Now()
	crd.Status.LastActivityAt = &now
	if _, err := func() (*v1.Workspace, error) {
		wsClient, wErr := s.workspaceCRDClient()
		if wErr != nil {
			return nil, wErr
		}
		return wsClient.UpdateStatus(ctx, crd)
	}(); err != nil {
		s.logger.Error("Failed to update workspace status to Resuming", err, "workspaceID", workspaceID)
		return nil, apierrors.NewInternalError("workspace_resume_failed", err)
	}

	s.logger.Info("Workspace activated", "workspaceID", workspaceID, "userID", userID)
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

func (s *Service) MarkSessionSeen(ctx context.Context, userID, workspaceID, sessionID string) error {
	if err := s.verifyOwner(ctx, userID, workspaceID); err != nil {
		return err
	}
	if s.sessionIndex == nil {
		return nil
	}
	return s.sessionIndex.UpdateLastSeen(ctx, workspaceID, sessionID)
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
			switch c.Status {
			case "True":
				status = "Healthy"
			case "False":
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
				_, _ = fmt.Sscanf(m[1], "%d", &result.ProvidersConfigured)
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
	if err := s.EnsureSecretsManifest(ctx, workspaceID, secretsJSON); err != nil {
		s.logger.Error("Failed to write ephemeral secrets secret", err, "workspaceID", workspaceID)
	}
}

// refreshEphemeralSecrets re-materializes the workspace-secrets-<id>
// K8s Secret from the user's currently-bound secrets in PostgreSQL.
//
// This is the single source of truth for the lifecycle paths that
// build (or rebuild) a workspace pod and need its user-provided
// secrets to be present at mount time. Two callers today:
//
//   - ActivateWorkspace: resume from Suspended; pod is created fresh.
//   - RestartWorkspace:  bump restartGeneration; controller deletes
//     the existing pod and recreates it.
//
// Both paths previously had their own (or missing) secret-refresh
// logic. RestartWorkspace had none, which meant SSH keys and other
// bound secrets were dropped on restart (worklog 0120, the bug that
// motivated this helper). Folding the logic into one function
// guarantees the lifecycle invariant "if a pod is about to be
// (re)built, its secrets are refreshed first" is enforced uniformly.
//
// SESSION HANDLING
//
//   - When sessionID is present in ctx (set by ContextWithSessionID),
//     both user credentials (per-session DEK) and admin platform
//     credentials (server-side KEK) are injected.
//   - When sessionID is absent (API-key auth, background reconcile,
//     controller-initiated restart), the function falls back to
//     seedEphemeralSecrets, which injects only admin platform
//     credentials (server-side KEK, no session required). User
//     credentials are not available without a session and are skipped.
//     The resulting K8s Secret will contain only admin credentials;
//     if the same workspace is later activated with a full JWT session,
//     refreshEphemeralSecrets will overwrite it with the full set.
//
// # FAILURE MODES
//
// Every failure path logs Warn and returns. The caller's lifecycle
// action proceeds regardless. Rationale: losing one secret refresh
// is recoverable (user can re-bind or re-activate); failing the
// lifecycle action would leave the workspace stuck. A pre-existing
// K8s Secret from the previous successful refresh is preserved
// untouched on failure, so the pod can still come up with whatever
// was last seen.
//
// NOTE: This is the API-side guarantee. The controller-side path
// that rebuilds pods (Active phase + restartGeneration bump) does
// NOT call back into the API service — a kubectl-driven or
// operator-driven `restartGeneration` bump bypasses this helper
// entirely. That's the follow-up work tracked in worklog 0120: the
// controller should refuse to start a pod when the bindings table
// says secrets should be present but the K8s Secret is empty/absent.
func (s *Service) refreshEphemeralSecrets(ctx context.Context, userID, workspaceID string) {
	if s.secretInjector == nil {
		return
	}
	sessionID, _ := ctx.Value(sessionIDContextKey).(string)
	if sessionID == "" {
		// No user session (e.g. API-key auth, background reconcile). We cannot
		// decrypt user-owned credentials but we CAN inject admin platform
		// credentials (server-side KEK, no session required). Fall through to
		// seedEphemeralSecrets which uses sessionID="" — correct for admin creds.
		s.logger.Warn("refreshEphemeralSecrets: no sessionID — falling back to admin-only credential injection",
			"workspaceID", workspaceID)
		s.seedEphemeralSecrets(ctx, userID, workspaceID)
		return
	}
	secretsJSON, err := s.secretInjector.PrepareSecretsForInjection(ctx, userID, sessionID, workspaceID)
	if err != nil {
		s.logger.Warn("Failed to prepare secrets for injection",
			"workspaceID", workspaceID, "error", err.Error())
		return
	}
	// PrepareSecretsForInjection returns "[]" (2 bytes) when the user
	// has no bindings on this workspace. Skip the manifest write in
	// that case — calling EnsureSecretsManifest with "[]" would
	// clobber any existing workspace-secrets-<id> Secret with an
	// empty payload. On a restart path that's specifically the wrong
	// default: we want to *refresh* secrets, not *remove* them. The
	// user's explicit "unbind" path (SetBindings with []) goes
	// through pushSecretsToAgent which is the correct place to clear
	// the manifest.
	if len(secretsJSON) <= 2 {
		return
	}
	s.createEphemeralSecretsSecret(ctx, workspaceID, secretsJSON)
}

// seedEphemeralSecrets injects admin platform credentials (server-side KEK,
// no user session required) into the workspace-secrets-<id> K8s Secret.
//
// Called from one path:
//   - refreshEphemeralSecrets fallback: when no sessionID is in context
//     (API-key auth, controller reconcile, expired DEK), this is called instead
//     of skipping entirely, ensuring platform credentials are always current.
//
// Uses MergeSecretsManifest rather than a full overwrite so that user-owned
// credentials (which require the DEK and could not be decrypted here) are
// preserved from the prior full refresh. Without this, every DEK-absent activate
// silently drops user credentials and the pod boots with only admin providers.
//
// User credentials are injected by refreshEphemeralSecrets when the user opens
// the workspace with a live session DEK.
func (s *Service) seedEphemeralSecrets(ctx context.Context, userID, workspaceID string) {
	if s.secretInjector == nil {
		return
	}
	secretsJSON, err := s.secretInjector.PrepareSecretsForInjection(ctx, userID, "", workspaceID)
	if err != nil {
		s.logger.Warn("seedEphemeralSecrets: failed to prepare admin credentials for new workspace",
			"workspaceID", workspaceID, "error", err.Error())
		return
	}
	// Merge rather than overwrite. An empty result (no admin bindings, or all
	// decrypts failed) still runs the merge path — which is a no-op against
	// existing credentials, preserving them intact.
	if err := s.MergeSecretsManifest(ctx, workspaceID, secretsJSON); err != nil {
		s.logger.Error("seedEphemeralSecrets: failed to merge secrets manifest", err,
			"workspaceID", workspaceID, "userID", userID)
	}
}

// EnsureSecretsManifest writes (create-or-update) the K8s Secret named
// `workspace-secrets-<id>` consumed by the in-pod init container. This is
// the durable channel for binding delivery: a pod restart re-reads the
// Secret on its next start. The HTTP `/v1/reload-secrets` push to agentd
// is the live channel; the two together guarantee bound secrets reach
// the pod regardless of its lifecycle state.
//
// The Get-then-Update path uses retry.RetryOnConflict so two concurrent
// SetBindings calls (e.g. from two API replicas) cannot lose updates:
// the second writer's Update will receive a 409 Conflict from the
// apiserver and the helper re-Gets and retries with the latest
// resourceVersion. Without this, the later writer would silently
// overwrite the earlier writer's payload.
//
// Returns nil on apiserver success regardless of create-vs-update.
// Errors are returned to the caller (rather than only logged) so the
// bind handler can surface them at Warn (Bug 2).
func (s *Service) EnsureSecretsManifest(ctx context.Context, workspaceID string, secretsJSON []byte) error {
	if workspaceID == "" {
		return fmt.Errorf("workspaceID is required")
	}
	secretName := fmt.Sprintf("workspace-secrets-%s", workspaceID)
	clientset := s.k8sClient.Clientset()
	secretClient := clientset.CoreV1().Secrets(s.config.Namespace)

	labels := map[string]string{
		"app":                        "llmsafespace",
		"llmsafespace.dev/workspace": workspaceID,
		"llmsafespace.dev/ephemeral": "true",
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
		if err == nil {
			// Merge secrets.json into existing data. Replace the key only, so
			// workspace-config.json (written by EnsureWorkspaceConfig) and any
			// other keys added in the future are preserved unmodified.
			if existing.Data == nil {
				existing.Data = map[string][]byte{}
			}
			existing.Data["secrets.json"] = secretsJSON
			// Merge labels: preserve any additions made by an
			// operator while ensuring our markers are present.
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range labels {
				existing.Labels[k] = v
			}
			if _, err := secretClient.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
				return err // retry.RetryOnConflict examines the error
			}
			return nil
		}
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get secrets manifest: %w", err)
		}
		desired := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: s.config.Namespace,
				Labels:    labels,
			},
			Data: map[string][]byte{"secrets.json": secretsJSON},
		}
		if _, err := secretClient.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			// AlreadyExists means another writer created it between
			// our Get and Create — surface as conflict so retry
			// re-runs the Get path.
			if k8serrors.IsAlreadyExists(err) {
				return k8serrors.NewConflict(corev1.Resource("secrets"), secretName, err)
			}
			return fmt.Errorf("create secrets manifest: %w", err)
		}
		return nil
	})
}

// MergeSecretsManifest writes the incoming secrets.json payload into the
// workspace-secrets-<id> K8s Secret using a merge strategy: incoming entries
// win for names they contain, and existing entries fill in names the incoming
// payload lacks.
//
// This is the correct write path for seedEphemeralSecrets (DEK-absent activate).
// When only admin credentials can be decrypted, user-owned credentials that were
// written by a prior full-DEK refresh must not be overwritten. Without this,
// every workspace activate with an expired or absent DEK silently drops user
// credentials (e.g. thekao cloud) and the pod boots with only the relay provider.
//
// Merge semantics:
//   - incoming entry (by name) always wins — ensures rotated admin keys propagate
//   - existing entry whose name is absent from incoming is preserved — ensures
//     user credentials survive a DEK-absent refresh
//   - if no existing secret: behaves identically to EnsureSecretsManifest
//   - empty incoming ([]): preserves all existing entries unchanged
func (s *Service) MergeSecretsManifest(ctx context.Context, workspaceID string, incomingJSON []byte) error {
	if workspaceID == "" {
		return fmt.Errorf("workspaceID is required")
	}

	secretName := fmt.Sprintf("workspace-secrets-%s", workspaceID)
	clientset := s.k8sClient.Clientset()
	secretClient := clientset.CoreV1().Secrets(s.config.Namespace)

	labels := map[string]string{
		"app":                        "llmsafespace",
		"llmsafespace.dev/workspace": workspaceID,
		"llmsafespace.dev/ephemeral": "true",
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return fmt.Errorf("get secrets manifest for merge: %w", err)
			}
			// No existing secret. Only create if incoming is non-empty: writing
			// an empty secrets.json to a brand-new Secret would mount as `[]`
			// in the init container, which is indistinguishable from "no
			// credentials" and silently suppresses the first real bind. The
			// correct behavior for empty+no-secret is to skip — the next
			// successful credential bind will create the Secret with real data.
			if len(incomingJSON) <= 2 {
				return nil
			}
			desired := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: s.config.Namespace,
					Labels:    labels,
				},
				Data: map[string][]byte{"secrets.json": incomingJSON},
			}
			if _, createErr := secretClient.Create(ctx, desired, metav1.CreateOptions{}); createErr != nil {
				if k8serrors.IsAlreadyExists(createErr) {
					return k8serrors.NewConflict(corev1.Resource("secrets"), secretName, createErr)
				}
				return fmt.Errorf("create secrets manifest (merge): %w", createErr)
			}
			return nil
		}

		// Secret exists — merge incoming over existing, keyed by name.
		merged, mergeErr := mergeSecretsByName(existing.Data["secrets.json"], incomingJSON)
		if mergeErr != nil {
			// Merge failed (malformed JSON in stored secret). Fall back to
			// writing incoming as-is rather than leaving a corrupt state.
			s.logger.Warn("MergeSecretsManifest: failed to merge existing secrets; overwriting with incoming",
				"workspaceID", workspaceID, "error", mergeErr.Error())
			merged = incomingJSON
		}

		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data["secrets.json"] = merged
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for k, v := range labels {
			existing.Labels[k] = v
		}
		if _, updateErr := secretClient.Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
			return updateErr
		}
		return nil
	})
}

// mergeSecretsByName merges two secrets.json payloads. Both are JSON arrays of
// objects with a "name" field. The incoming payload wins for any name it contains;
// existing entries whose names are absent from incoming are preserved.
// An empty or nil incoming payload leaves existing unchanged.
func mergeSecretsByName(existingJSON, incomingJSON []byte) ([]byte, error) {
	// Parse incoming.
	var incoming []json.RawMessage
	if len(incomingJSON) > 2 {
		if err := json.Unmarshal(incomingJSON, &incoming); err != nil {
			return nil, fmt.Errorf("unmarshal incoming secrets: %w", err)
		}
	}

	// Build name→entry map for incoming.
	incomingByName := make(map[string]json.RawMessage, len(incoming))
	for _, raw := range incoming {
		var hdr struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &hdr); err != nil || hdr.Name == "" {
			continue
		}
		incomingByName[hdr.Name] = raw
	}

	// Parse existing.
	var existing []json.RawMessage
	if len(existingJSON) > 2 {
		if err := json.Unmarshal(existingJSON, &existing); err != nil {
			return nil, fmt.Errorf("unmarshal existing secrets: %w", err)
		}
	}

	// Start with incoming entries (preserve their order).
	result := make([]json.RawMessage, 0, len(incoming)+len(existing))
	result = append(result, incoming...)

	// Append existing entries not covered by incoming.
	for _, raw := range existing {
		var hdr struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &hdr); err != nil || hdr.Name == "" {
			continue
		}
		if _, covered := incomingByName[hdr.Name]; !covered {
			result = append(result, raw)
		}
	}

	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal merged secrets: %w", err)
	}
	return out, nil
}

// EnsureWorkspaceConfig writes workspace-level configuration (non-sensitive
// metadata like default model) into the workspace-secrets-<id> K8s Secret.
// This is read by the agentd init container at pod boot to configure the agent.
//
// The Secret is created if it does not exist. This is necessary because users
// with zero LLM credentials never have the secret created by seedEphemeralSecrets
// (which guards on an empty secrets payload and skips writing). workspace-config.json
// is independent of secrets.json — it must be writable regardless of whether the
// workspace has any credentials.
//
// The create path sets the same labels as EnsureSecretsManifest so the secret
// is discoverable by operators and the controller lifecycle. secrets.json is not
// written on the create path — the init container handles a missing secrets.json
// gracefully (optional mount). On the update path, secrets.json is preserved
// unmodified; only workspace-config.json is touched.
func (s *Service) EnsureWorkspaceConfig(ctx context.Context, workspaceID string, config WorkspaceConfig) error {
	if workspaceID == "" {
		return fmt.Errorf("workspaceID is required")
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal workspace config: %w", err)
	}

	secretName := fmt.Sprintf("workspace-secrets-%s", workspaceID)
	clientset := s.k8sClient.Clientset()
	secretClient := clientset.CoreV1().Secrets(s.config.Namespace)

	labels := map[string]string{
		"app":                        "llmsafespace",
		"llmsafespace.dev/workspace": workspaceID,
		"llmsafespace.dev/ephemeral": "true",
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
		if err == nil {
			// Secret exists — merge workspace-config.json, preserve all other keys.
			if existing.Data == nil {
				existing.Data = map[string][]byte{}
			}
			existing.Data["workspace-config.json"] = configJSON
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range labels {
				existing.Labels[k] = v
			}
			_, updateErr := secretClient.Update(ctx, existing, metav1.UpdateOptions{})
			return updateErr
		}
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get workspace config secret: %w", err)
		}
		// Secret does not exist — create it with workspace-config.json only.
		// secrets.json is intentionally absent: the init container mounts the
		// secret as optional:true and guards with `if [ -f ... ]`, so a missing
		// secrets.json is safe. It will be written by the next credential bind.
		desired := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: s.config.Namespace,
				Labels:    labels,
			},
			Data: map[string][]byte{"workspace-config.json": configJSON},
		}
		if _, createErr := secretClient.Create(ctx, desired, metav1.CreateOptions{}); createErr != nil {
			if k8serrors.IsAlreadyExists(createErr) {
				return k8serrors.NewConflict(corev1.Resource("secrets"), secretName, createErr)
			}
			return fmt.Errorf("create workspace config secret: %w", createErr)
		}
		return nil
	})
}
