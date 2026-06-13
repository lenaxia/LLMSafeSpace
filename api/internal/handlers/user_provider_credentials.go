// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// UserCredentialStore abstracts DB operations for user provider credentials.
type UserCredentialStore interface {
	CreateUserCredential(ctx context.Context, row *secrets.UserCredentialRow) error
	ListUserCredentials(ctx context.Context, userID string) ([]*secrets.UserCredentialRow, error)
	GetUserCredential(ctx context.Context, userID, id string) (*secrets.UserCredentialRow, error)
	DeleteUserCredential(ctx context.Context, userID, id string) error
	BindCredentialToWorkspace(ctx context.Context, credentialID, workspaceID string) error
	// UnbindCredentialFromWorkspace removes an EXPLICIT binding.
	// Returns secrets.ErrAutoBindingProtected for auto-managed bindings.
	UnbindCredentialFromWorkspace(ctx context.Context, credentialID, workspaceID string) error
	// GetCredentialBindingsWithSource returns bindings with source type (explicit vs auto).
	GetCredentialBindingsWithSource(ctx context.Context, credentialID, userID string) ([]secrets.CredentialBindingInfo, error)
	// GetCredentialBindings returns workspace IDs bound to the credential.
	GetCredentialBindings(ctx context.Context, credentialID, userID string) ([]string, error)
	// BindCredentialToAllUserWorkspaces binds a credential to every workspace owned by userID.
	BindCredentialToAllUserWorkspaces(ctx context.Context, credentialID, userID string) error
}

// WorkspaceOwnerChecker verifies workspace ownership for bind operations.
type WorkspaceOwnerChecker func(ctx context.Context, userID, workspaceID string) error

// UserProviderCredentialsHandler handles user-scoped provider credential CRUD.
type UserProviderCredentialsHandler struct {
	store           UserCredentialStore
	keys            *secrets.KeyService
	keyStore        secrets.KeyStore
	wsOwnerCheck    WorkspaceOwnerChecker
	credStateWriter CredentialStateWriter
}

// NewUserProviderCredentialsHandler creates a new handler.
func NewUserProviderCredentialsHandler(store UserCredentialStore, keys *secrets.KeyService, keyStore secrets.KeyStore) *UserProviderCredentialsHandler {
	return &UserProviderCredentialsHandler{store: store, keys: keys, keyStore: keyStore}
}

// SetWorkspaceOwnerChecker installs the ownership verification function.
func (h *UserProviderCredentialsHandler) SetWorkspaceOwnerChecker(fn WorkspaceOwnerChecker) {
	h.wsOwnerCheck = fn
}

// SetCredentialStateWriter installs the reload banner trigger.
func (h *UserProviderCredentialsHandler) SetCredentialStateWriter(w CredentialStateWriter) {
	h.credStateWriter = w
}

// Create handles POST /api/v1/provider-credentials.
//
// NOTE — DEK rotation (L-2 known limitation):
// User credentials are encrypted with the user's DEK at creation time.
// If the user later rotates their password (re-wrapping the DEK), existing
// provider credentials in provider_credentials are NOT re-encrypted, because
// the server cannot access the old DEK without an active session holding it.
// Credentials whose key_version is stale will fail to decrypt after a DEK rotation.
// A future improvement should re-encrypt provider_credentials as part of the
// password-rotation flow.
func (h *UserProviderCredentialsHandler) Create(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" || sessionID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req createAdminCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if strings.TrimSpace(req.Provider) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider must not be empty"})
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Name = strings.TrimSpace(req.Name)

	dek, err := h.keys.GetDEK(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "encryption unavailable"})
		return
	}

	plaintext, marshalErr := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // encrypting, not exposing
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
	if marshalErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}
	ciphertext, err := secrets.EncryptSecret(dek, plaintext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}

	record, err := h.keyStore.GetUserKey(c.Request.Context(), userID)
	if err != nil || record == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "user keys not available"})
		return
	}

	now := time.Now()
	row := &secrets.UserCredentialRow{
		ID:             uuid.New().String(),
		OwnerID:        userID,
		Name:           req.Name,
		Provider:       req.Provider,
		Ciphertext:     ciphertext,
		KeyVersion:     record.KeyVersion,
		ModelAllowlist: req.ModelAllowlist,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if row.ModelAllowlist == nil {
		row.ModelAllowlist = []string{}
	}

	if err := h.store.CreateUserCredential(c.Request.Context(), row); err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential) {
			c.JSON(http.StatusConflict, gin.H{"error": "credential for this provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store credential"})
		return
	}

	resp := AdminCredentialResponse{
		ID:             row.ID,
		Name:           row.Name,
		Provider:       row.Provider,
		ModelAllowlist: row.ModelAllowlist,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
	}

	// Bind to all existing workspaces (C-2 fix: surface failure via 207 not silent header).
	// Non-transactional with the credential insert by design: partial bind failures are
	// recoverable (SeedWorkspaceCredentials covers new workspaces; user can manually re-bind).
	if bindErr := h.store.BindCredentialToAllUserWorkspaces(c.Request.Context(), row.ID, userID); bindErr != nil {
		c.JSON(http.StatusMultiStatus, gin.H{
			"credential":  resp,
			"bindWarning": "credential created but failed to auto-bind to existing workspaces; please bind manually",
		})
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// List handles GET /api/v1/provider-credentials.
func (h *UserProviderCredentialsHandler) List(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	rows, err := h.store.ListUserCredentials(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list credentials"})
		return
	}
	resp := make([]AdminCredentialResponse, 0, len(rows))
	for _, row := range rows {
		r := AdminCredentialResponse{
			ID:             row.ID,
			Name:           row.Name,
			Provider:       row.Provider,
			ModelAllowlist: row.ModelAllowlist,
			CreatedAt:      row.CreatedAt.Format(time.RFC3339),
			UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
		}
		if r.ModelAllowlist == nil {
			r.ModelAllowlist = []string{}
		}
		resp = append(resp, r)
	}
	c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/provider-credentials/:id.
func (h *UserProviderCredentialsHandler) Get(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	row, err := h.store.GetUserCredential(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	c.JSON(http.StatusOK, AdminCredentialResponse{
		ID:             row.ID,
		Name:           row.Name,
		Provider:       row.Provider,
		ModelAllowlist: row.ModelAllowlist,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
	})
}

// Delete handles DELETE /api/v1/provider-credentials/:id.
// Notifies all workspaces that had this credential bound so running pods
// pick up the revocation on their next secret reload (C-3 fix).
func (h *UserProviderCredentialsHandler) Delete(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	credID := c.Param("id")

	// Snapshot bound workspaces BEFORE the FK cascade removes the bindings.
	boundWSIDs, listErr := h.store.GetCredentialBindings(c.Request.Context(), credID, userID)
	if listErr != nil {
		boundWSIDs = nil // non-fatal; worst case pods keep old key until next restart
	}

	if err := h.store.DeleteUserCredential(c.Request.Context(), userID, credID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete credential"})
		return
	}

	// Signal each previously-bound workspace so the reload banner appears and
	// the next pod restart writes a secrets manifest without this credential.
	if h.credStateWriter != nil {
		for _, wsID := range boundWSIDs {
			_ = h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID)
		}
	}

	c.Status(http.StatusNoContent)
}

// Bind handles POST /api/v1/provider-credentials/:id/bind/:workspaceId.
func (h *UserProviderCredentialsHandler) Bind(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	credID := c.Param("id")
	wsID := c.Param("workspaceId")

	cred, err := h.store.GetUserCredential(c.Request.Context(), userID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
		return
	}
	if cred == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	if h.wsOwnerCheck != nil {
		if err := h.wsOwnerCheck(c.Request.Context(), userID, wsID); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
	}

	if err := h.store.BindCredentialToWorkspace(c.Request.Context(), credID, wsID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to bind credential"})
		return
	}

	if h.credStateWriter != nil {
		_ = h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID)
	}

	c.JSON(http.StatusOK, gin.H{"bound": true})
}

// Unbind handles DELETE /api/v1/provider-credentials/:id/bind/:workspaceId.
// Returns 409 Conflict if the binding is auto-managed (H-1 fix: auto-bindings protected).
func (h *UserProviderCredentialsHandler) Unbind(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	credID := c.Param("id")
	wsID := c.Param("workspaceId")

	cred, err := h.store.GetUserCredential(c.Request.Context(), userID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
		return
	}
	if cred == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	if h.wsOwnerCheck != nil {
		if err := h.wsOwnerCheck(c.Request.Context(), userID, wsID); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
	}

	if err := h.store.UnbindCredentialFromWorkspace(c.Request.Context(), credID, wsID); err != nil {
		if errors.Is(err, secrets.ErrAutoBindingProtected) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unbind credential"})
		return
	}

	if h.credStateWriter != nil {
		_ = h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID)
	}

	c.Status(http.StatusNoContent)
}

// ListBindings handles GET /api/v1/provider-credentials/:id/bindings.
// Returns workspace IDs with their binding source type (explicit vs auto) so the
// UI can show which workspaces have user-initiated vs seeded bindings (M-1 fix).
func (h *UserProviderCredentialsHandler) ListBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	credID := c.Param("id")

	cred, err := h.store.GetUserCredential(c.Request.Context(), userID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
		return
	}
	if cred == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	bindings, err := h.store.GetCredentialBindingsWithSource(c.Request.Context(), credID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list bindings"})
		return
	}

	wsIDs := make([]string, len(bindings))
	for i, b := range bindings {
		wsIDs[i] = b.WorkspaceID
	}

	c.JSON(http.StatusOK, gin.H{
		"workspaceIds": wsIDs,
		"bindings":     bindings,
	})
}
