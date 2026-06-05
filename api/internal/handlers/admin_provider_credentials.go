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

// isDuplicateErr checks for PostgreSQL unique constraint violation (23505).
func isDuplicateErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// AdminCredentialStore abstracts DB operations for admin provider credentials.
type AdminCredentialStore interface {
	CreateAdminCredential(ctx context.Context, row *secrets.AdminCredentialRow) error
	ListAdminCredentials(ctx context.Context) ([]*secrets.AdminCredentialRow, error)
	GetAdminCredential(ctx context.Context, id string) (*secrets.AdminCredentialRow, error)
	UpdateAdminCredential(ctx context.Context, row *secrets.AdminCredentialRow) error
	DeleteAdminCredential(ctx context.Context, id string) error
}

// AdminCredentialResponse is the API response for an admin credential (never exposes apiKey).
type AdminCredentialResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	BaseURL        string   `json:"baseURL,omitempty"`
	ModelAllowlist []string `json:"modelAllowlist"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
}

type createAdminCredentialRequest struct {
	Name           string   `json:"name" binding:"required"`
	Provider       string   `json:"provider" binding:"required"`
	APIKey         string   `json:"apiKey" binding:"required"`
	BaseURL        string   `json:"baseURL"`
	ModelAllowlist []string `json:"modelAllowlist"`
}

// AdminProviderCredentialsHandler handles CRUD for admin provider credentials.
type AdminProviderCredentialsHandler struct {
	store          AdminCredentialStore
	autoApplyStore AutoApplyStore
	deriveKey      secrets.AdminKeyDeriver
}

// NewAdminProviderCredentialsHandler creates a new handler.
func NewAdminProviderCredentialsHandler(store AdminCredentialStore, deriveKey secrets.AdminKeyDeriver) *AdminProviderCredentialsHandler {
	return &AdminProviderCredentialsHandler{store: store, deriveKey: deriveKey}
}

func (h *AdminProviderCredentialsHandler) kek() []byte {
	if h.deriveKey == nil {
		return nil
	}
	return h.deriveKey("provider-credentials")
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

	kek := h.kek()
	if kek == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "master secret not configured"})
		return
	}

	plaintext, marshalErr := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // marshaling for encryption, not API response
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
	if marshalErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}
	ciphertext, err := secrets.EncryptSecret(kek, plaintext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}

	now := time.Now()
	row := &secrets.AdminCredentialRow{
		ID:             uuid.New().String(),
		Name:           req.Name,
		Provider:       req.Provider,
		Ciphertext:     ciphertext,
		KeyVersion:     1,
		ModelAllowlist: req.ModelAllowlist,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if row.ModelAllowlist == nil {
		row.ModelAllowlist = []string{}
	}

	if err := h.store.CreateAdminCredential(c.Request.Context(), row); err != nil {
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
		BaseURL:        req.BaseURL,
		ModelAllowlist: row.ModelAllowlist,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
	})
}

// List handles GET /api/v1/admin/provider-credentials.
func (h *AdminProviderCredentialsHandler) List(c *gin.Context) {
	rows, err := h.store.ListAdminCredentials(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list credentials"})
		return
	}

	kek := h.kek()
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
		if kek != nil {
			if plain, decErr := secrets.DecryptSecret(kek, row.Ciphertext); decErr == nil {
				var pd secrets.LLMProviderData
				if json.Unmarshal(plain, &pd) == nil {
					r.BaseURL = pd.BaseURL
				}
			}
		}
		resp = append(resp, r)
	}
	c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Get(c *gin.Context) {
	id := c.Param("id")
	row, err := h.store.GetAdminCredential(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

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
	if kek := h.kek(); kek != nil {
		if plain, decErr := secrets.DecryptSecret(kek, row.Ciphertext); decErr == nil {
			var pd secrets.LLMProviderData
			if json.Unmarshal(plain, &pd) == nil {
				r.BaseURL = pd.BaseURL
			}
		}
	}
	c.JSON(http.StatusOK, r)
}

// Update handles PUT /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Update(c *gin.Context) {
	id := c.Param("id")
	existing, err := h.store.GetAdminCredential(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	var req createAdminCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	kek := h.kek()
	if kek == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "master secret not configured"})
		return
	}

	plaintext, marshalErr := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // marshaling for encryption, not API response
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
	if marshalErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}
	ciphertext, err := secrets.EncryptSecret(kek, plaintext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}

	existing.Name = req.Name
	existing.Provider = req.Provider
	existing.Ciphertext = ciphertext
	existing.UpdatedAt = time.Now()
	if req.ModelAllowlist != nil {
		existing.ModelAllowlist = req.ModelAllowlist
	}
	if existing.ModelAllowlist == nil {
		existing.ModelAllowlist = []string{}
	}

	if err := h.store.UpdateAdminCredential(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update credential"})
		return
	}

	c.JSON(http.StatusOK, AdminCredentialResponse{
		ID:             existing.ID,
		Name:           existing.Name,
		Provider:       existing.Provider,
		BaseURL:        req.BaseURL,
		ModelAllowlist: existing.ModelAllowlist,
		CreatedAt:      existing.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      existing.UpdatedAt.Format(time.RFC3339),
	})
}

// Delete handles DELETE /api/v1/admin/provider-credentials/:id.
func (h *AdminProviderCredentialsHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.DeleteAdminCredential(c.Request.Context(), id); err != nil {
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "auto-apply not configured"})
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
		c.JSON(http.StatusOK, []autoApplyResponse{})
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
		c.Status(http.StatusNoContent)
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
