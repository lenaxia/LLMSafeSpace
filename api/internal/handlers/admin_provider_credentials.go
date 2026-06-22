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
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
)

// CredentialStore is the unified DB interface for provider credential CRUD,
// scoped by (ownerType, ownerID) for multi-tenant isolation. All three
// credential handlers (admin/user/org) depend on it; their specialized stores
// (bindings, auto-apply) are wired separately.
type CredentialStore interface {
	CreateCredential(ctx context.Context, ownerType, ownerID string, row *secrets.CredentialRow) error
	ListCredentials(ctx context.Context, ownerType, ownerID string) ([]*secrets.CredentialRow, error)
	GetCredential(ctx context.Context, ownerType, ownerID, credID string) (*secrets.CredentialRow, error)
	UpdateCredential(ctx context.Context, ownerType, ownerID, credID string, row *secrets.CredentialRow) error
	DeleteCredential(ctx context.Context, ownerType, ownerID, credID string) error
}

// CredentialResponse is the API response for any provider credential.
// Never exposes apiKey. BaseURL is extracted from the encrypted ciphertext.
// OrgID is populated only for org-scoped credentials (omitted otherwise).
// BindWarning is set only by org Create when auto-bind fails (non-fatal).
type CredentialResponse struct {
	ID                 string         `json:"id"`
	OrgID              string         `json:"orgId,omitempty"`
	Name               string         `json:"name"`
	Provider           string         `json:"provider"`
	BaseURL            string         `json:"baseURL,omitempty"`
	ModelAllowlist     []string       `json:"modelAllowlist"`
	ModelContextLimits map[string]int `json:"modelContextLimits"`
	CreatedAt          string         `json:"createdAt"`
	UpdatedAt          string         `json:"updatedAt"`
	BindWarning        string         `json:"bindWarning,omitempty"`
}

type createAdminCredentialRequest struct {
	Name               string         `json:"name" binding:"required"`
	Provider           string         `json:"provider" binding:"required"`
	APIKey             string         `json:"apiKey" binding:"required"`
	BaseURL            string         `json:"baseURL"`
	ModelAllowlist     []string       `json:"modelAllowlist"`
	ModelContextLimits map[string]int `json:"modelContextLimits"`
}

// updateAdminCredentialRequest is used for PUT — all fields are optional so
// callers can rotate just the API key without resending name/provider.
type updateAdminCredentialRequest struct {
	Name               *string        `json:"name"`
	Provider           *string        `json:"provider"`
	APIKey             *string        `json:"apiKey"`
	BaseURL            *string        `json:"baseURL"`
	ModelAllowlist     []string       `json:"modelAllowlist"`
	ModelContextLimits map[string]int `json:"modelContextLimits"`
}

// buildCredentialResponse converts a stored credential row into the API
// response DTO, decrypting the ciphertext to extract baseURL. A decryption
// failure is non-fatal: baseURL is omitted and the remaining metadata is
// returned (the credential remains usable — only the display field is lost).
// The decrypted plaintext (which contains the API key) is zeroed before return.
func buildCredentialResponse(ctx context.Context, row *secrets.CredentialRow, provider secrets.RootKeyProvider) CredentialResponse {
	resp := CredentialResponse{
		ID:                 row.ID,
		Name:               row.Name,
		Provider:           row.Provider,
		ModelAllowlist:     row.ModelAllowlist,
		ModelContextLimits: row.ModelContextLimits,
		CreatedAt:          row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          row.UpdatedAt.Format(time.RFC3339),
	}
	// OrgID is populated only for org-scoped credentials; admin/user credentials
	// omit it (OwnerID is "_platform" or a user id — not meaningful to clients).
	if row.OwnerType == "org" {
		resp.OrgID = row.OwnerID
	}
	if resp.ModelAllowlist == nil {
		resp.ModelAllowlist = []string{}
	}
	if resp.ModelContextLimits == nil {
		resp.ModelContextLimits = map[string]int{}
	}
	if provider != nil {
		if plain, err := provider.Decrypt(ctx, row.Ciphertext); err == nil {
			var pd secrets.LLMProviderData
			if json.Unmarshal(plain, &pd) == nil {
				resp.BaseURL = pd.BaseURL
			}
			zeroBytes(plain)
		}
	}
	return resp
}

// AdminProviderCredentialsHandler handles CRUD for admin provider credentials.
type AdminProviderCredentialsHandler struct {
	store          CredentialStore
	autoApplyStore AutoApplyStore
	provider       secrets.RootKeyProvider
}

// NewAdminProviderCredentialsHandler creates a new handler.
func NewAdminProviderCredentialsHandler(store CredentialStore, provider secrets.RootKeyProvider) *AdminProviderCredentialsHandler {
	return &AdminProviderCredentialsHandler{store: store, provider: provider}
}

// Create handles POST /api/v1/admin/provider-credentials.
func (h *AdminProviderCredentialsHandler) Create(c *gin.Context) {
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

	if h.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "master secret not configured"})
		return
	}

	ciphertext, err := encryptCredentialData(c.Request.Context(), h.provider.Encrypt, req.Provider, req.APIKey, req.BaseURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}

	now := time.Now()
	row := &secrets.CredentialRow{
		ID:                 uuid.New().String(),
		Name:               req.Name,
		Provider:           req.Provider,
		Ciphertext:         ciphertext,
		KeyVersion:         1,
		ModelAllowlist:     req.ModelAllowlist,
		ModelContextLimits: req.ModelContextLimits,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if row.ModelAllowlist == nil {
		row.ModelAllowlist = []string{}
	}
	if row.ModelContextLimits == nil {
		row.ModelContextLimits = map[string]int{}
	}

	if err := h.store.CreateCredential(c.Request.Context(), "admin", "_platform", row); err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential) {
			c.JSON(http.StatusConflict, gin.H{"error": "credential for this provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store credential"})
		return
	}

	c.JSON(http.StatusCreated, CredentialResponse{
		ID:                 row.ID,
		Name:               row.Name,
		Provider:           row.Provider,
		BaseURL:            req.BaseURL,
		ModelAllowlist:     row.ModelAllowlist,
		ModelContextLimits: row.ModelContextLimits,
		CreatedAt:          row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          row.UpdatedAt.Format(time.RFC3339),
	})
}

// List handles GET /api/v1/admin/provider-credentials.
func (h *AdminProviderCredentialsHandler) List(c *gin.Context) {
	rows, err := h.store.ListCredentials(c.Request.Context(), "admin", "_platform")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list credentials"})
		return
	}

	resp := make([]CredentialResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, buildCredentialResponse(c.Request.Context(), row, h.provider))
	}
	c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Get(c *gin.Context) {
	id := c.Param("id")
	row, err := h.store.GetCredential(c.Request.Context(), "admin", "_platform", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	c.JSON(http.StatusOK, buildCredentialResponse(c.Request.Context(), row, h.provider))
}

// Update handles PUT /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Update(c *gin.Context) {
	id := c.Param("id")
	existing, err := h.store.GetCredential(c.Request.Context(), "admin", "_platform", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	var req updateAdminCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Apply partial updates — only fields present in the request are changed.
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Provider != nil {
		existing.Provider = *req.Provider
	}
	if req.ModelAllowlist != nil {
		existing.ModelAllowlist = req.ModelAllowlist
	}
	if req.ModelContextLimits != nil {
		existing.ModelContextLimits = req.ModelContextLimits
	}

	// Re-encrypt only when the caller is changing an encrypted field (apiKey or baseURL).
	if req.APIKey != nil || req.BaseURL != nil {
		if h.provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "master secret not configured"})
			return
		}
		// Decrypt the existing ciphertext to get current values (C-4 fix).
		// If decryption fails, return 500 — do NOT proceed with a zeroed struct,
		// which would silently corrupt the stored credential.
		existingPlain, decErr := h.provider.Decrypt(c.Request.Context(), existing.Ciphertext)
		if decErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "existing credential data is unreadable; manual remediation required before key rotation",
			})
			return
		}
		defer zeroBytes(existingPlain) // zero on all exit paths (success and failure)
		var existingData secrets.LLMProviderData
		if err := json.Unmarshal(existingPlain, &existingData); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "existing credential data is corrupt; cannot apply partial update",
			})
			return
		}
		// Apply only the fields being changed.
		if req.Provider != nil {
			existingData.Provider = *req.Provider
		}
		if req.APIKey != nil {
			existingData.APIKey = *req.APIKey
		}
		if req.BaseURL != nil {
			existingData.BaseURL = *req.BaseURL
		}
		plaintext, marshalErr := json.Marshal(existingData) //nolint:gosec // marshaling for encryption
		if marshalErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
			return
		}
		ciphertext, encErr := h.provider.Encrypt(c.Request.Context(), plaintext)
		zeroBytes(plaintext)
		if encErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
			return
		}
		existing.Ciphertext = ciphertext
		existing.KeyVersion++ // increment on every ciphertext write (M-2 fix)
	}

	if err := h.store.UpdateCredential(c.Request.Context(), "admin", "_platform", existing.ID, existing); err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential) {
			c.JSON(http.StatusConflict, gin.H{"error": "a credential for that provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update credential"})
		return
	}

	// existing.UpdatedAt is now populated from RETURNING updated_at (M-8 fix).
	// buildCredentialResponse decrypts to include baseURL (consistent with GET/List).
	c.JSON(http.StatusOK, buildCredentialResponse(c.Request.Context(), existing, h.provider))
}

// Delete handles DELETE /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteCredential(c.Request.Context(), "admin", "_platform", id); err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrCredentialNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete credential"})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Auto-apply endpoints ---

type createAutoApplyRequest struct {
	TargetType string `json:"targetType" binding:"required"`
	TargetID   string `json:"targetId"`
	Priority   int    `json:"withinPriority"`
}

type autoApplyResponse struct {
	CredentialID string `json:"credentialId"`
	TargetType   string `json:"targetType"`
	TargetID     string `json:"targetId,omitempty"`
	Priority     int    `json:"withinPriority"`
}

// AutoApplyStore abstracts auto-apply DB operations.
type AutoApplyStore interface {
	CreateAutoApply(ctx context.Context, credentialID, targetType string, targetID *string, priority int) error
	DeleteAutoApply(ctx context.Context, credentialID, targetType string, targetID *string) error
	ListAutoApply(ctx context.Context, credentialID string) ([]secrets.AutoApplyRule, error)
}

// SetAutoApplyStore sets the auto-apply store (called after construction).
func (h *AdminProviderCredentialsHandler) SetAutoApplyStore(s AutoApplyStore) {
	h.autoApplyStore = s
}

// CreateAutoApply handles POST /api/v1/admin/provider-credentials/:id/auto-apply.
func (h *AdminProviderCredentialsHandler) CreateAutoApply(c *gin.Context) {
	credID := c.Param("id")
	var req createAutoApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.TargetType != "all" && req.TargetType != "user" && req.TargetType != "org" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "targetType must be 'all', 'user', or 'org'"})
		return
	}
	if h.autoApplyStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auto-apply not configured"})
		return
	}

	var targetID *string
	if req.TargetType != "all" {
		if req.TargetID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "targetId required when targetType is not 'all'"})
			return
		}
		targetID = &req.TargetID
	}

	if err := h.autoApplyStore.CreateAutoApply(c.Request.Context(), credID, req.TargetType, targetID, req.Priority); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create auto-apply rule"})
		return
	}
	c.JSON(http.StatusCreated, autoApplyResponse{
		CredentialID: credID,
		TargetType:   req.TargetType,
		TargetID:     req.TargetID,
		Priority:     req.Priority,
	})
}

// ListAutoApply handles GET /api/v1/admin/provider-credentials/:id/auto-apply.
func (h *AdminProviderCredentialsHandler) ListAutoApply(c *gin.Context) {
	credID := c.Param("id")
	if h.autoApplyStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auto-apply not configured"})
		return
	}
	rules, err := h.autoApplyStore.ListAutoApply(c.Request.Context(), credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list auto-apply rules"})
		return
	}
	resp := make([]autoApplyResponse, 0, len(rules))
	for _, r := range rules {
		ar := autoApplyResponse{CredentialID: r.CredentialID, TargetType: r.TargetType, Priority: r.Priority}
		if r.TargetID != nil {
			ar.TargetID = *r.TargetID
		}
		resp = append(resp, ar)
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteAutoApply handles DELETE /api/v1/admin/provider-credentials/:id/auto-apply/:targetType/:targetId.
func (h *AdminProviderCredentialsHandler) DeleteAutoApply(c *gin.Context) {
	credID := c.Param("id")
	targetType := c.Param("targetType")
	targetIDParam := c.Param("targetId")
	if h.autoApplyStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auto-apply not configured"})
		return
	}
	var targetID *string
	if targetType != "all" && targetIDParam != "" {
		targetID = &targetIDParam
	}
	if err := h.autoApplyStore.DeleteAutoApply(c.Request.Context(), credID, targetType, targetID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete auto-apply rule"})
		return
	}
	c.Status(http.StatusNoContent)
}
