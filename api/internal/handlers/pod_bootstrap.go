// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// bootstrapAudience is the TokenReview audience. Must match the projected
// ServiceAccountToken volume's audience in the init container (US-35.4) and
// the agentd bootstrap subcommand (US-35.2).
const bootstrapAudience = "llmsafespace-api"

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
		return "", fmt.Errorf("token not authenticated")
	}
	return tr.Status.User.Username, nil
}

// bootstrapAPIResponse is the JSON envelope returned by POST /internal/v1/pod-bootstrap.
// Mirrors the bootstrapResponse in cmd/workspace-agentd/bootstrap.go.
type bootstrapAPIResponse struct {
	Secrets         json.RawMessage `json:"secrets"`
	WorkspaceConfig json.RawMessage `json:"workspaceConfig,omitempty"`
}

// PodBootstrapHandler handles POST /internal/v1/pod-bootstrap — the
// secretless credential injection endpoint (Epic 35 US-35.3).
//
// Auth is via K8s TokenReview (projected SA token, audience "llmsafespace-api").
// No JWT middleware — the init container has no user identity. The handler
// verifies the SA name matches workspace-<workspaceID> to enforce pod-to-
// workspace isolation: a compromised workspace pod can only retrieve its own
// credentials.
type PodBootstrapHandler struct {
	tokenReviewer TokenReviewer
	injector      bootstrapInjector
	lookup        bootstrapWorkspaceLookup
}

// NewPodBootstrapHandler constructs the handler. In production, pass a
// *k8sTokenReviewer wrapping the API's K8s clientset.
func NewPodBootstrapHandler(reviewer TokenReviewer, injector bootstrapInjector, lookup bootstrapWorkspaceLookup) *PodBootstrapHandler {
	return &PodBootstrapHandler{
		tokenReviewer: reviewer,
		injector:      injector,
		lookup:        lookup,
	}
}

// NewPodBootstrapHandlerFromClientset is the production constructor that wraps
// a kubernetes.Interface into a k8sTokenReviewer.
func NewPodBootstrapHandlerFromClientset(clientset kubernetes.Interface, injector bootstrapInjector, lookup bootstrapWorkspaceLookup) *PodBootstrapHandler {
	return NewPodBootstrapHandler(&k8sTokenReviewer{clientset: clientset}, injector, lookup)
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

	saWorkspaceID, ok := parseWorkspaceIDFromSAName(username)
	if !ok || saWorkspaceID != req.WorkspaceID {
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
		cfgJSON, _ := json.Marshal(types.WorkspaceConfig{DefaultModel: ws.DefaultModel})
		resp.WorkspaceConfig = cfgJSON
	}

	c.JSON(http.StatusOK, resp)
}

func extractBearerToken(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

// parseWorkspaceIDFromSAName extracts the workspace ID from a K8s
// TokenReview username of the form
// "system:serviceaccount:<namespace>:workspace-<workspaceID>".
//
// The SA name uses "workspace-" as a prefix (not a delimiter) so UUID hyphens
// in the workspaceID are preserved. Returns ok=false if the username does not
// match the workspace SA pattern.
func parseWorkspaceIDFromSAName(username string) (string, bool) {
	const saPrefix = "system:serviceaccount:"
	if !strings.HasPrefix(username, saPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(username, saPrefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	saName := parts[1]
	const wsPrefix = "workspace-"
	if !strings.HasPrefix(saName, wsPrefix) {
		return "", false
	}
	return strings.TrimPrefix(saName, wsPrefix), true
}
