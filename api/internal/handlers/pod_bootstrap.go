// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lenaxia/llmsafespaces/api/internal/services/prompt"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// bootstrapAudience is the TokenReview audience. Must match the projected
// ServiceAccountToken volume's audience in the init container (US-35.4) and
// the agentd bootstrap subcommand (US-35.2).
const bootstrapAudience = "llmsafespace-api"

// errTokenNotAuthenticated is returned by TokenReviewer.Review when the K8s
// API server successfully processed the TokenReview but reported
// Status.Authenticated == false (invalid token, wrong audience, expired).
// This is a CLIENT error (401), distinct from a transport-level failure (500).
var errTokenNotAuthenticated = errors.New("token not authenticated")

// TokenReviewer validates a projected ServiceAccount token via K8s TokenReview.
// Returns the authenticated username (e.g.
// "system:serviceaccount:<ns>:workspace-<id>") on success.
type TokenReviewer interface {
	Review(ctx context.Context, token string) (string, error)
}

// bootstrapInjector prepares decrypted secrets for pod injection.
type bootstrapInjector interface {
	PrepareSecretsForInjection(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error)
}

// bootstrapWorkspaceLookup resolves workspace metadata for bootstrap.
type bootstrapWorkspaceLookup interface {
	GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
}

// k8sTokenReviewer implements TokenReviewer via the K8s API server's
// TokenReview endpoint.
type k8sTokenReviewer struct {
	clientset kubernetes.Interface
}

func (r *k8sTokenReviewer) Review(ctx context.Context, token string) (string, error) {
	tr, err := r.clientset.AuthenticationV1().TokenReviews().Create(ctx, &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{bootstrapAudience},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("token review: %w", err)
	}
	if !tr.Status.Authenticated {
		return "", errTokenNotAuthenticated
	}
	return tr.Status.User.Username, nil
}

// bootstrapAPIResponse is the JSON envelope returned by POST /internal/v1/pod-bootstrap.
// Mirrors the bootstrapResponse in cmd/workspace-agentd/bootstrap.go.
type bootstrapAPIResponse struct {
	Secrets         json.RawMessage `json:"secrets"`
	WorkspaceConfig json.RawMessage `json:"workspaceConfig,omitempty"`
	AdminPrompt     string          `json:"adminPrompt,omitempty"`
}

// PodBootstrapHandler handles POST /internal/v1/pod-bootstrap — the
// secretless credential injection endpoint (Epic 35 US-35.3).
//
// Auth is via K8s TokenReview (projected SA token, audience "llmsafespace-api").
// No JWT middleware — the init container has no user identity. The handler
// verifies the SA name matches workspace-<workspaceID> AND the SA namespace
// matches the expected workspace namespace to enforce pod-to-workspace
// isolation: a compromised workspace pod can only retrieve its own credentials.
type PodBootstrapHandler struct {
	tokenReviewer     TokenReviewer
	injector          bootstrapInjector
	lookup            bootstrapWorkspaceLookup
	promptSvc         *prompt.Service
	expectedNamespace string
}

// NewPodBootstrapHandler constructs the handler. In production, pass a
// *k8sTokenReviewer wrapping the API's K8s clientset. expectedNamespace is the
// K8s namespace where workspace ServiceAccounts live — validated against the
// SA namespace in the TokenReview username (S1 defense-in-depth).
func NewPodBootstrapHandler(reviewer TokenReviewer, injector bootstrapInjector, lookup bootstrapWorkspaceLookup, promptSvc *prompt.Service, expectedNamespace string) *PodBootstrapHandler {
	return &PodBootstrapHandler{
		tokenReviewer:     reviewer,
		injector:          injector,
		lookup:            lookup,
		promptSvc:         promptSvc,
		expectedNamespace: expectedNamespace,
	}
}

// NewPodBootstrapHandlerFromClientset is the production constructor that wraps
// a kubernetes.Interface into a k8sTokenReviewer.
func NewPodBootstrapHandlerFromClientset(clientset kubernetes.Interface, injector bootstrapInjector, lookup bootstrapWorkspaceLookup, promptSvc *prompt.Service, expectedNamespace string) *PodBootstrapHandler {
	return NewPodBootstrapHandler(&k8sTokenReviewer{clientset: clientset}, injector, lookup, promptSvc, expectedNamespace)
}

// SetPromptService wires the prompt resolution service after construction.
// Used when the prompt service is built later in the startup sequence.
func (h *PodBootstrapHandler) SetPromptService(svc *prompt.Service) {
	h.promptSvc = svc
}

// Bootstrap handles POST /internal/v1/pod-bootstrap.
func (h *PodBootstrapHandler) Bootstrap(c *gin.Context) {
	token := extractBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization"})
		return
	}

	username, err := h.tokenReviewer.Review(c.Request.Context(), token)
	if err != nil {
		// C1: distinguish "token rejected by apiserver" (401, client error)
		// from "TokenReview API call failed" (500, server fault).
		if errors.Is(err, errTokenNotAuthenticated) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token not authenticated"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "token review failed"})
		return
	}

	var req struct {
		WorkspaceID string `json:"workspaceID"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.WorkspaceID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "workspaceID required"})
		return
	}

	// S1: validate both the workspaceID (from SA name) AND the namespace.
	// A token from a different namespace's workspace-<id> SA must be rejected.
	saNamespace, saWorkspaceID, ok := parseSAPrincipal(username)
	if !ok || saWorkspaceID != req.WorkspaceID || saNamespace != h.expectedNamespace {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "workspace identity mismatch"})
		return
	}

	ws, err := h.lookup.GetWorkspace(c.Request.Context(), req.WorkspaceID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "workspace lookup failed"})
		return
	}
	if ws == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	secretsJSON, err := h.injector.PrepareSecretsForInjection(c.Request.Context(), ws.UserID, "", req.WorkspaceID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "secret preparation failed"})
		return
	}
	if len(secretsJSON) == 0 {
		secretsJSON = []byte("[]")
	}

	resp := bootstrapAPIResponse{Secrets: secretsJSON}
	if ws.DefaultModel != "" {
		cfgJSON, err := json.Marshal(types.WorkspaceConfig{DefaultModel: ws.DefaultModel})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "workspace config marshal failed"})
			return
		}
		resp.WorkspaceConfig = cfgJSON
	}

	// Resolve effective admin prompt (platform → org → user). Failures are
	// non-fatal — the pod boots without admin instructions. agentd writes
	// an empty file and opencode treats it as "no prompt configured".
	if h.promptSvc != nil {
		effective, err := h.promptSvc.ResolveEffective(c.Request.Context(), req.WorkspaceID)
		if err == nil && effective != nil && effective.Resolved != "" {
			resp.AdminPrompt = effective.Resolved
		}
	}

	c.JSON(http.StatusOK, resp)
}

func extractBearerToken(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

// parseSAPrincipal extracts the namespace and workspace ID from a K8s
// TokenReview username of the form
// "system:serviceaccount:<namespace>:workspace-<workspaceID>".
//
// Returns (namespace, workspaceID, ok). The SA name uses "workspace-" as a
// prefix (not a delimiter) so UUID hyphens in the workspaceID are preserved.
// Returns ok=false if the username does not match the workspace SA pattern.
func parseSAPrincipal(username string) (namespace, workspaceID string, ok bool) {
	const saPrefix = "system:serviceaccount:"
	if !strings.HasPrefix(username, saPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(username, saPrefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	namespace = parts[0]
	saName := parts[1]
	const wsPrefix = "workspace-"
	if !strings.HasPrefix(saName, wsPrefix) {
		return "", "", false
	}
	return namespace, strings.TrimPrefix(saName, wsPrefix), true
}
