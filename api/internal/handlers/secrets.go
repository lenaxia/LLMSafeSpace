// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// SecretsHandler handles HTTP requests for the secrets API.
type SecretsHandler struct {
	svc              *secrets.SecretService
	podIPResolver    PodIPResolver
	manifestWriter   SecretsManifestWriter
	logger           pkginterfaces.LoggerInterface
	passwordVerifier PasswordVerifier
	wsUpdater        ModelStore
	credStateWriter  CredentialStateWriter
	passwordGetter   func(ctx context.Context, workspaceID string) (string, error)
	// relayActive is true when INFERENCE_RELAY_BASEURL is set in the pod env,
	// meaning free-tier opencode models should route through the CF Worker relay.
	// When true, ListModels remaps free opencode models to providerID=opencode-relay.
	relayActive bool
	// metricsRecorder records billing/metering events (optional).
	metricsRecorder ModelSelectionRecorder
}

// ModelSelectionRecorder records model selection events for billing/metering.
type ModelSelectionRecorder interface {
	RecordModelSelection(modelID, providerID string)
}

// CredentialStateWriter records that workspace credentials have changed.
// Satisfied by *database.Service.
type CredentialStateWriter interface {
	MarkCredentialChanged(ctx context.Context, workspaceID string) error
}

// SetCredentialStateWriter installs the writer. If nil, MarkCredentialChanged
// is silently skipped (banner won't appear but no crash).
func (h *SecretsHandler) SetCredentialStateWriter(w CredentialStateWriter) {
	h.credStateWriter = w
}

// PodIPResolver looks up the pod IP for a workspace.
type PodIPResolver interface {
	GetWorkspacePodIP(ctx context.Context, userID, workspaceID string) (string, error)
}

// SecretsManifestWriter persists the per-workspace ephemeral K8s Secret
// (`workspace-secrets-<id>`) used by the in-pod init container to seed
// `/sandbox-cfg/secrets.json` on every pod start. Without this, secrets
// pushed via the live HTTP reload path vanish on pod recycle (Bug 3 in
// worklog 0085). The writer is responsible for create-or-update semantics
// and for choosing the appropriate K8s namespace.
type SecretsManifestWriter interface {
	EnsureSecretsManifest(ctx context.Context, workspaceID string, secretsJSON []byte) error
	EnsureWorkspaceConfig(ctx context.Context, workspaceID string, config types.WorkspaceConfig) error
}

// PasswordVerifier confirms a user's password against the stored bcrypt
// hash. Used by RevealSecret to enforce a re-authentication gate before
// returning plaintext: a stolen JWT alone must not be sufficient to
// extract every secret. Implementations MUST run constant-time
// comparison (bcrypt.CompareHashAndPassword satisfies this) and MUST
// return a sentinel-typed error rather than the raw bcrypt error so
// the handler can map it to a uniform 403 without leaking timing or
// state information.
type PasswordVerifier interface {
	VerifyPassword(ctx context.Context, userID string, password []byte) error
}

// NewSecretsHandler creates a new SecretsHandler.
func NewSecretsHandler(svc *secrets.SecretService) *SecretsHandler {
	return &SecretsHandler{svc: svc}
}

// SetMetricsRecorder installs the recorder for billing/metering events.
func (h *SecretsHandler) SetMetricsRecorder(r ModelSelectionRecorder) {
	h.metricsRecorder = r
}

// SetRelayActive configures whether the inference relay is active.
// When true, ListModels remaps free-tier opencode models to providerID=opencode-relay.
func (h *SecretsHandler) SetRelayActive(active bool) {
	h.relayActive = active
}

// SetPasswordVerifier installs the verifier used to confirm the
// caller's password on RevealSecret. If left nil the reveal handler
// rejects every request with 503; this is intentional because shipping
// without password verification is exactly the security theater we
// fixed (validator finding on RevealSecret in worklog 0094 audit).
func (h *SecretsHandler) SetPasswordVerifier(v PasswordVerifier) {
	h.passwordVerifier = v
}

// SetPodIPResolver sets the resolver for looking up pod IPs.
func (h *SecretsHandler) SetPodIPResolver(r PodIPResolver) {
	h.podIPResolver = r
}

// HasPodIPResolver reports whether a PodIPResolver has been configured.
// Used by wiring tests to verify the handler is fully constructed; without
// a resolver the reload-secrets endpoint and the SetBindings auto-push
// silently no-op (Bug 1 + Bug 2 in worklog 0085).
func (h *SecretsHandler) HasPodIPResolver() bool {
	return h.podIPResolver != nil
}

// SetSecretsManifestWriter installs the writer used to persist the
// per-workspace K8s Secret on bind. Optional; if nil, only the live HTTP
// push runs and bound secrets do not survive pod restarts.
func (h *SecretsHandler) SetSecretsManifestWriter(w SecretsManifestWriter) {
	h.manifestWriter = w
}

// SetLogger installs the logger used to surface non-fatal failures from
// the bind-time auto-push. Optional; if nil, failures are silent (which
// is exactly Bug 2 in worklog 0085 — do not leave nil in production).
func (h *SecretsHandler) SetLogger(l pkginterfaces.LoggerInterface) {
	h.logger = l
}

// SetPasswordGetter injects the workspace password getter for authenticated
// opencode calls (ListModels, SetModel). Without this, model operations
// that require direct opencode communication will fail with 503.
func (h *SecretsHandler) SetPasswordGetter(getter func(ctx context.Context, workspaceID string) (string, error)) {
	h.passwordGetter = getter
}

// CreateSecret handles POST /api/v1/secrets
func (h *SecretsHandler) CreateSecret(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req secrets.CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := h.svc.CreateSecret(c.Request.Context(), userID, sessionID, req)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// ListSecrets handles GET /api/v1/secrets
func (h *SecretsHandler) ListSecrets(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	list, err := h.svc.ListSecrets(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list secrets"})
		return
	}
	if list == nil {
		list = []*secrets.SecretResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"secrets": list})
}

// GetSecret handles GET /api/v1/secrets/:id
func (h *SecretsHandler) GetSecret(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	resp, err := h.svc.GetSecret(c.Request.Context(), userID, secretID)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UpdateSecret handles PUT /api/v1/secrets/:id
func (h *SecretsHandler) UpdateSecret(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	var req secrets.UpdateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.svc.UpdateSecret(c.Request.Context(), userID, sessionID, secretID, req); err != nil {
		handleSecretError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// DeleteSecret handles DELETE /api/v1/secrets/:id
func (h *SecretsHandler) DeleteSecret(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	if err := h.svc.DeleteSecret(c.Request.Context(), userID, secretID); err != nil {
		handleSecretError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// RevealSecret handles POST /api/v1/secrets/:id/reveal
// Requires password reconfirmation: a stolen JWT alone must not be
// sufficient to extract every secret. Without a configured
// PasswordVerifier the handler returns 503 — shipping without
// verification is exactly the security theater the validator audit
// flagged. The bcrypt.CompareHashAndPassword call inside the verifier
// is constant-time, so failed-password timing does not differentiate
// from missing-DEK timing in practice.
func (h *SecretsHandler) RevealSecret(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required to reveal secret"})
		return
	}

	if h.passwordVerifier == nil {
		// Fail closed: refusing to serve reveals without verification
		// is safer than serving them without verification.
		h.warn("RevealSecret blocked: no password verifier configured",
			"userID", userID, "secretID", secretID)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "password verification not configured"})
		return
	}
	if err := h.passwordVerifier.VerifyPassword(c.Request.Context(), userID, []byte(req.Password)); err != nil {
		// Uniform 403 regardless of why verification failed (wrong
		// password, user not found, bcrypt error). Do not log the
		// raw error at the request level since it could include
		// bcrypt diagnostic detail; warn-level only.
		h.warn("RevealSecret password verification failed",
			"userID", userID, "secretID", secretID)
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid password"})
		return
	}

	plaintext, err := h.svc.DecryptSecretValue(c.Request.Context(), userID, sessionID, secretID)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"value": string(plaintext)})
}

// GetSecretBindings handles GET /api/v1/secrets/:id/bindings
func (h *SecretsHandler) GetSecretBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	secretID := c.Param("id")
	workspaces, err := h.svc.GetBindingsForSecret(c.Request.Context(), userID, secretID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get bindings"})
		return
	}
	if workspaces == nil {
		workspaces = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}

func (h *SecretsHandler) SetBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	var req secrets.SetBindingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	result, err := h.svc.SetBindings(c.Request.Context(), userID, workspaceID, req.SecretIDs)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	if result.LLMProviderAffected && h.credStateWriter != nil {
		if err := h.credStateWriter.MarkCredentialChanged(c.Request.Context(), workspaceID); err != nil {
			if h.logger != nil {
				h.logger.Warn("mark credential changed failed; banner may not appear",
					"workspaceID", workspaceID, "error", err.Error())
			}
		}
	}

	h.pushSecretsToAgent(c, userID, workspaceID)

	c.Status(http.StatusNoContent)
}

// GetBindings handles GET /api/v1/workspaces/:id/bindings
func (h *SecretsHandler) GetBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	resp, err := h.svc.GetBindings(c.Request.Context(), userID, workspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get bindings"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ReloadSecrets handles POST /api/v1/workspaces/:id/reload-secrets
// Decrypts bound secrets and pushes them to the running pod's agentd.
func (h *SecretsHandler) ReloadSecrets(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	secretsJSON, err := h.svc.PrepareSecretsForInjection(c.Request.Context(), userID, sessionID, workspaceID)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	result, err := h.doReload(c.Request.Context(), userID, workspaceID, secretsJSON)
	if err != nil {
		switch err {
		case errPodIPResolverNotConfigured:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "secret reload not configured"})
		case errNoRunningPod:
			c.JSON(http.StatusConflict, gin.H{"error": "workspace has no running pod"})
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}

var (
	errPodIPResolverNotConfigured = fmt.Errorf("secret reload not configured")
	errNoRunningPod               = fmt.Errorf("workspace has no running pod")
)

type reloadResult struct {
	Reloaded  int  `json:"reloaded"`
	Restarted bool `json:"restarted"`
}

// pushSecretsToAgent runs the bind-time delivery of the latest secret
// snapshot for a workspace. Two side-effects are intentional and ordered:
//
//  1. EnsureSecretsManifest writes/updates the per-workspace K8s Secret
//     `workspace-secrets-<id>` that the pod's init container reads at
//     start time. This MUST run unconditionally, including when there
//     is no live pod yet, because the pod has not yet been created
//     when a freshly-spawned workspace receives its first bind. Without
//     this write the next pod-create would mount nothing.
//  2. doReload pushes the same payload to the live agentd over HTTP so
//     pods that are already running see the change without a restart.
//     This step is skipped when there are no bindings to push (the
//     empty-array short-circuit) or when the resolver reports no
//     running pod.
//
// Both steps are best-effort: the bind itself has already committed to
// Postgres before this function is called, the user gets 204, and any
// transient failure here is recoverable on the next bind or pod start.
// Failures are surfaced to operators via Warn-level logs (Bug 2 in
// worklog 0085) rather than silently swallowed.
func (h *SecretsHandler) pushSecretsToAgent(c *gin.Context, userID, workspaceID string) {
	_, sessionID := extractAuth(c)
	ctx := c.Request.Context()

	secretsJSON, err := h.svc.PrepareSecretsForInjection(ctx, userID, sessionID, workspaceID)
	if err != nil {
		h.warn("PrepareSecretsForInjection failed",
			"workspaceID", workspaceID, "error", err.Error())
		return
	}

	// Step 1: durable manifest. secretsJSON is `[]` when no bindings
	// exist; the writer accepts that and effectively clears the
	// workspace's secret state on the next pod start. Run regardless
	// of whether a pod is currently running — this is the only path
	// that seeds the K8s Secret a future pod will mount.
	if h.manifestWriter != nil {
		if werr := h.manifestWriter.EnsureSecretsManifest(ctx, workspaceID, secretsJSON); werr != nil {
			h.warn("EnsureSecretsManifest failed",
				"workspaceID", workspaceID, "error", werr.Error())
		}
	}

	// Step 2: live HTTP push. We send the payload even when it is
	// the empty array '[]' — the agent uses this to CLEAR its
	// in-memory secret materialisations. Without this an unbind
	// leaves the live pod with stale plaintext until restart, even
	// though the manifest is already empty and the next pod start
	// would seed nothing (validator finding N8 in worklog 0094
	// pass-2 audit).
	if _, derr := h.doReload(ctx, userID, workspaceID, secretsJSON); derr != nil {
		// errNoRunningPod is the expected case when the workspace has
		// not been activated yet; downgrade to Info so it does not
		// pollute Warn-level dashboards. Everything else is a real
		// failure operators need to see.
		if derr == errNoRunningPod {
			h.info("reload-secrets skipped: no running pod",
				"workspaceID", workspaceID)
			return
		}
		h.warn("reload-secrets push to agent failed",
			"workspaceID", workspaceID, "error", derr.Error())
	}
}

func (h *SecretsHandler) warn(msg string, fields ...interface{}) {
	if h.logger != nil {
		h.logger.Warn(msg, fields...)
	}
}

func (h *SecretsHandler) info(msg string, fields ...interface{}) {
	if h.logger != nil {
		h.logger.Info(msg, fields...)
	}
}

// reloadHTTPClient is the bounded-timeout client used for the live
// HTTP push to in-pod agentd. Without an explicit timeout, http.Post
// inherits no deadline and a slow or hung agent could block the bind
// handler indefinitely; the handler itself is invoked from a Gin
// request goroutine that holds an open client connection. 5s covers a
// healthy agent (sub-100ms in practice) with margin for transient
// network jitter.
var reloadHTTPClient = &http.Client{Timeout: 5 * time.Second}

func (h *SecretsHandler) doReload(ctx context.Context, userID, workspaceID string, secretsJSON []byte) (*reloadResult, error) {
	if h.podIPResolver == nil {
		return nil, errPodIPResolverNotConfigured
	}
	podIP, err := h.podIPResolver.GetWorkspacePodIP(ctx, userID, workspaceID)
	if err != nil || podIP == "" {
		return nil, errNoRunningPod
	}

	agentdURL := fmt.Sprintf("http://%s:4097/v1/reload-secrets", podIP)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentdURL, bytes.NewReader(secretsJSON))
	if err != nil {
		return nil, fmt.Errorf("build reload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := reloadHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach workspace agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var agentErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&agentErr)
		msg := "agent reload failed"
		if agentErr.Error != "" {
			msg = agentErr.Error
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var result reloadResult
	_ = json.NewDecoder(resp.Body).Decode(&result)

	// Evict this workspace's model cache so the next ListModels call reflects
	// any new or removed providers that the credential bind just activated.
	// Without this, the 5s TTL means users see a stale model list for up to
	// 5 seconds after a successful bind (Gap6 — worklog 0186).
	defaultModelCache.Evict(workspaceID)

	return &result, nil
}

// SetWorkspaceEnv handles PUT /api/v1/workspaces/:id/env
// Creates or updates env-secret type secrets bound to this workspace.
//
// Concurrency: SetWorkspaceEnv only ADDs bindings (it never removes
// — that's what DeleteWorkspaceEnv is for), so we can use the
// store's AddBindings primitive which holds a workspace-scoped
// advisory lock for the duration of the binding write. Two
// concurrent SetWorkspaceEnv calls on the same workspace serialize
// at the AddBindings step and neither's secrets are lost.
//
// Error handling: every UpdateSecret/CreateSecret/AddBindings
// failure surfaces as 500 with the offending var name. Pre-fix the
// handler returned 204 even when the writes silently failed.
func (h *SecretsHandler) SetWorkspaceEnv(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	var req struct {
		Vars map[string]string `json:"vars" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vars map required"})
		return
	}

	ctx := c.Request.Context()

	newBindings := make([]string, 0, len(req.Vars))
	for varName, value := range req.Vars {
		secretName := fmt.Sprintf("%s-env-%s", workspaceID, strings.ToLower(varName))
		metadata, _ := json.Marshal(map[string]string{"var_name": varName})

		existing, err := h.svc.GetSecretByName(ctx, userID, secretName)
		if err != nil {
			h.warn("SetWorkspaceEnv: GetSecretByName failed",
				"varName", varName, "workspaceID", workspaceID, "error", err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up env var: " + varName})
			return
		}
		if existing != nil {
			if err := h.svc.UpdateSecret(ctx, userID, sessionID, existing.ID,
				secrets.UpdateSecretRequest{Value: value}); err != nil {
				h.warn("SetWorkspaceEnv: UpdateSecret failed",
					"varName", varName, "secretID", existing.ID, "error", err.Error())
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update env var: " + varName})
				return
			}
			newBindings = append(newBindings, existing.ID)
			continue
		}
		created, err := h.svc.CreateSecret(ctx, userID, sessionID, secrets.CreateSecretRequest{
			Name: secretName, Type: secrets.SecretTypeEnvSecret, Value: value,
			Metadata: metadata,
		})
		if err != nil {
			h.warn("SetWorkspaceEnv: CreateSecret failed",
				"varName", varName, "workspaceID", workspaceID, "error", err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set env var: " + varName})
			return
		}
		newBindings = append(newBindings, created.ID)
	}

	// AddBindings is atomic and idempotent: it adds these secret IDs
	// to the workspace's binding set under a workspace-scoped
	// advisory lock without touching any existing bindings. Two
	// concurrent SetWorkspaceEnv calls on the same workspace
	// serialize at this step rather than racing on a Get-then-Set
	// snapshot (worklog 0094 pass-2 finding O1).
	if _, err := h.svc.AddBindings(ctx, userID, workspaceID, newBindings); err != nil {
		h.warn("SetWorkspaceEnv: AddBindings failed",
			"workspaceID", workspaceID, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit workspace bindings"})
		return
	}

	c.Status(http.StatusNoContent)
}

// GetWorkspaceEnv handles GET /api/v1/workspaces/:id/env
// Returns env var names (never values) bound to this workspace.
func (h *SecretsHandler) GetWorkspaceEnv(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	resp, err := h.svc.GetBindings(c.Request.Context(), userID, workspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get env"})
		return
	}

	vars := []string{}
	for _, b := range resp.Bindings {
		if b.Type == secrets.SecretTypeEnvSecret {
			vars = append(vars, b.Name)
		}
	}
	c.JSON(http.StatusOK, gin.H{"vars": vars})
}

// DeleteWorkspaceEnv handles DELETE /api/v1/workspaces/:id/env/:name
func (h *SecretsHandler) DeleteWorkspaceEnv(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	varName := c.Param("name")
	secretName := fmt.Sprintf("%s-env-%s", workspaceID, strings.ToLower(varName))

	existing, err := h.svc.GetSecretByName(c.Request.Context(), userID, secretName)
	if err != nil {
		h.warn("DeleteWorkspaceEnv: GetSecretByName failed",
			"varName", varName, "workspaceID", workspaceID, "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up env var"})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "env var not found"})
		return
	}

	if err := h.svc.DeleteSecret(c.Request.Context(), userID, existing.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete env var"})
		return
	}

	c.Status(http.StatusNoContent)
}

// GetAuditLog handles GET /api/v1/secrets/audit
func (h *SecretsHandler) GetAuditLog(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	query := secrets.AuditQuery{
		Action:      c.Query("action"),
		SecretID:    c.Query("secretId"),
		WorkspaceID: c.Query("workspaceId"),
		Limit:       100,
	}

	entries, err := h.svc.QueryAudit(c.Request.Context(), userID, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit log"})
		return
	}
	if entries == nil {
		entries = []*secrets.AuditEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// KeyRotator is the interface needed by the rotation handler.
type KeyRotator interface {
	RotateKeyWithPassword(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) (secrets.RotationResult, error)
	ChangePassword(ctx context.Context, userID, sessionID string, oldPassword, newPassword []byte) error
	ResetWithRecoveryKey(ctx context.Context, userID string, recoveryKeyHex string, newPassword []byte) (string, error)
}

// PasswordHashUpdater updates the user's bcrypt hash in the database.
type PasswordHashUpdater interface {
	UpdatePasswordHash(ctx context.Context, userID string, newPassword []byte) error
}

// RotateKeyHandler handles account key management endpoints.
type RotateKeyHandler struct {
	keySvc    KeyRotator
	pwUpdater PasswordHashUpdater
	auditFunc func(userID, action string) // optional audit callback
}

// NewRotateKeyHandler creates a new RotateKeyHandler.
func NewRotateKeyHandler(keySvc KeyRotator) *RotateKeyHandler {
	return &RotateKeyHandler{keySvc: keySvc}
}

// SetPasswordUpdater sets the optional password hash updater.
func (h *RotateKeyHandler) SetPasswordUpdater(u PasswordHashUpdater) {
	h.pwUpdater = u
}

// SetAuditFunc sets an optional audit callback for key operations.
func (h *RotateKeyHandler) SetAuditFunc(f func(userID, action string)) {
	h.auditFunc = f
}

// RotateKey handles POST /api/v1/account/rotate-key.
// On success the response includes the new keyVersion AND a freshly-
// issued recoveryKey: the old recovery key wraps the now-discarded
// old DEK, so the user must save the new one. This is a one-time
// display — the API does not store it anywhere recoverable.
func (h *RotateKeyHandler) RotateKey(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required for key rotation"})
		return
	}

	result, err := h.keySvc.RotateKeyWithPassword(c.Request.Context(), userID, []byte(req.Password), sessionID, 24*time.Hour)
	if err != nil {
		if errors.Is(err, secrets.ErrInvalidPassword) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key rotation failed"})
		return
	}

	if h.auditFunc != nil {
		h.auditFunc(userID, "rotate")
	}

	c.JSON(http.StatusOK, gin.H{
		"keyVersion":  result.NewKeyVersion,
		"recoveryKey": result.NewRecoveryKeyHex,
	})
}

// ChangePassword handles POST /api/v1/account/change-password
func (h *RotateKeyHandler) ChangePassword(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req struct {
		OldPassword string `json:"oldPassword" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oldPassword and newPassword (min 8 chars) required"})
		return
	}

	if err := h.keySvc.ChangePassword(c.Request.Context(), userID, sessionID, []byte(req.OldPassword), []byte(req.NewPassword)); err != nil {
		if errors.Is(err, secrets.ErrInvalidPassword) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid current password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password change failed"})
		return
	}

	// Also update the bcrypt hash in the user database
	if h.pwUpdater != nil {
		if err := h.pwUpdater.UpdatePasswordHash(c.Request.Context(), userID, []byte(req.NewPassword)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "password change failed"})
			return
		}
	}

	c.Status(http.StatusNoContent)
}

// RecoverAccount handles POST /api/v1/account/recover
func (h *RotateKeyHandler) RecoverAccount(c *gin.Context) {
	// This is a public-ish endpoint (user forgot password) but still needs some identity.
	// In practice, this would be called after email verification. For now, require userID in body.
	var req struct {
		UserID      string `json:"userId" binding:"required"`
		RecoveryKey string `json:"recoveryKey" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId, recoveryKey, and newPassword required"})
		return
	}

	newRecoveryKey, err := h.keySvc.ResetWithRecoveryKey(c.Request.Context(), req.UserID, req.RecoveryKey, []byte(req.NewPassword))
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid recovery key"})
		return
	}

	// Also update the bcrypt hash so the user can login with the new password
	if h.pwUpdater != nil {
		if err := h.pwUpdater.UpdatePasswordHash(c.Request.Context(), req.UserID, []byte(req.NewPassword)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "recovery failed"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"recoveryKey": newRecoveryKey})
}

// extractAuth gets userID and sessionID (jti) from the Gin context.
// Both values are type-asserted with the comma-ok form so a malformed
// context (e.g. middleware put a non-string under the key) produces an
// empty result rather than a goroutine panic that takes down the
// request. Empty userID is treated as unauthenticated by every caller.
func extractAuth(c *gin.Context) (userID, sessionID string) {
	if uid, exists := c.Get("userID"); exists {
		if s, ok := uid.(string); ok {
			userID = s
		}
	}
	if sid, exists := c.Get("sessionID"); exists {
		if s, ok := sid.(string); ok {
			sessionID = s
		}
	}
	return userID, sessionID
}

// handleSecretError maps domain errors to HTTP responses using
// errors.Is rather than substring matching. The string-matching
// predecessor was fragile to upstream wrap text and silently
// mis-routed any future error containing the words "requires" /
// "not found" / "duplicate". Sentinels live in pkg/secrets/errors.go.
func handleSecretError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, secrets.ErrSecretNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "secret not found"})
	case errors.Is(err, secrets.ErrWorkspaceNotOwned):
		// Same status as secret-not-found so the response shape does
		// not differentiate between "the workspace doesn't exist"
		// and "you don't own it" — leaking that distinction would
		// re-enable cross-user workspace existence enumeration via
		// the bindings API (validator pass-3 finding SO-1).
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
	case errors.Is(err, secrets.ErrDuplicateSecret):
		c.JSON(http.StatusConflict, gin.H{"error": "secret with this name already exists"})
	case errors.Is(err, secrets.ErrDEKUnavailable):
		c.JSON(http.StatusForbidden, gin.H{"error": "encryption key not available; re-authenticate"})
	case errors.Is(err, secrets.ErrUserKeysMissing):
		c.JSON(http.StatusPreconditionFailed, gin.H{"error": "user key material not initialized; please re-login"})
	case errors.Is(err, secrets.ErrInvalidSecretType), errors.Is(err, secrets.ErrInvalidMetadata):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
