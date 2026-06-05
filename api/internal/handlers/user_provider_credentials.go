// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
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
	UpdateUserCredential(ctx context.Context, row *secrets.UserCredentialRow) error
	DeleteUserCredential(ctx context.Context, userID, id string) error
	BindCredentialToWorkspace(ctx context.Context, credentialID, workspaceID string) error
	UnbindCredentialFromWorkspace(ctx context.Context, credentialID, workspaceID string) error
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
func (h *UserProviderCredentialsHandler) Create(c *gin.Context) {
	userID := c.GetString("userID")
	sessionID := c.GetString("sessionID")
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

	plaintext, _ := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // encrypting, not exposing
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
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
		if isDuplicateErr(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "credential for this provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store credential"})
		return
	}

	c.JSON(http.StatusCreated, AdminCredentialResponse{
		ID:             row.ID,
		Name:           row.Name,
		Provider:       row.Provider,
		ModelAllowlist: row.ModelAllowlist,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
	})
}

// List handles GET /api/v1/provider-credentials.
func (h *UserProviderCredentialsHandler) List(c *gin.Context) {
	userID := c.GetString("userID")
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
	userID := c.GetString("userID")
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
func (h *UserProviderCredentialsHandler) Delete(c *gin.Context) {
	userID := c.GetString("userID")
	if err := h.store.DeleteUserCredential(c.Request.Context(), userID, c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete credential"})
		return
	}
	c.Status(http.StatusNoContent)
}

// Bind handles POST /api/v1/provider-credentials/:id/bind/:workspaceId.
func (h *UserProviderCredentialsHandler) Bind(c *gin.Context) {
	userID := c.GetString("userID")
	credID := c.Param("id")
	wsID := c.Param("workspaceId")

	// Verify the user owns the credential.
	cred, err := h.store.GetUserCredential(c.Request.Context(), userID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
		return
	}
	if cred == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	// Verify the user owns the workspace (via the workspace owner verifier on the store).
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

	// Trigger reload banner (Epic 27a).
	if h.credStateWriter != nil {
		_ = h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID)
	}

	c.JSON(http.StatusOK, gin.H{"bound": true})
}

// Unbind handles DELETE /api/v1/provider-credentials/:id/bind/:workspaceId.
func (h *UserProviderCredentialsHandler) Unbind(c *gin.Context) {
	userID := c.GetString("userID")
	credID := c.Param("id")
	wsID := c.Param("workspaceId")

	// Verify the user owns the credential.
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unbind credential"})
		return
	}

	if h.credStateWriter != nil {
		_ = h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID)
	}

	c.Status(http.StatusNoContent)
}
