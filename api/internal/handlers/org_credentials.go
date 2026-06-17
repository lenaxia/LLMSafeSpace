// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// orgCredentialStore is the minimal OrgCredentialStore interface used by OrgCredentialsHandler.
type orgCredentialStore interface {
	CreateOrgCredential(ctx context.Context, orgID, name, provider string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int) (string, error)
	ListOrgCredentials(ctx context.Context, orgID string) ([]*secrets.CredentialRow, error)
	GetOrgCredential(ctx context.Context, orgID, credID string) (*secrets.CredentialRow, error)
	UpdateOrgCredential(ctx context.Context, orgID, credID string, name *string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int, keyVersion int) error
	DeleteOrgCredential(ctx context.Context, orgID, credID string) error
	BindCredentialToAllOrgWorkspaces(ctx context.Context, credentialID, orgID string) error
	CreateOrgAutoApply(ctx context.Context, credentialID, orgID string, withinPriority int) error
	ListOrgAutoApply(ctx context.Context, orgID string) ([]*secrets.AutoApplyRule, error)
	DeleteOrgAutoApply(ctx context.Context, credentialID, orgID string) error
}

// OrgCredentialsHandler handles org credential endpoints.
type OrgCredentialsHandler struct {
	credStore     orgCredentialStore
	orgKeyDeriver secrets.AdminKeyDeriver
	authSvc       orgAuthService
}

// NewOrgCredentialsHandler creates a new OrgCredentialsHandler.
func NewOrgCredentialsHandler(store orgCredentialStore, orgKeyDeriver secrets.AdminKeyDeriver, authSvc orgAuthService) *OrgCredentialsHandler {
	return &OrgCredentialsHandler{credStore: store, orgKeyDeriver: orgKeyDeriver, authSvc: authSvc}
}

type createOrgCredentialRequest struct {
	Name               string         `json:"name"           binding:"required,min=1,max=128"`
	Provider           string         `json:"provider"       binding:"required"`
	APIKey             string         `json:"apiKey"         binding:"required"              log:"-"` //nolint:gosec // G117 false positive — field has log:"-" tag, never marshaled to response
	BaseURL            string         `json:"baseURL"`
	ModelAllowlist     []string       `json:"modelAllowlist"`
	ModelContextLimits map[string]int `json:"modelContextLimits"`
}

type updateOrgCredentialRequest struct {
	Name               *string        `json:"name"`
	APIKey             *string        `json:"apiKey"          log:"-"` //nolint:gosec // G117 false positive — field has log:"-" tag, never marshaled to response
	BaseURL            *string        `json:"baseURL"`
	ModelAllowlist     []string       `json:"modelAllowlist"`
	ModelContextLimits map[string]int `json:"modelContextLimits"`
}

// orgKEK returns the server KEK used to encrypt org credentials, or nil if the
// key deriver is not configured.
func (h *OrgCredentialsHandler) orgKEK() []byte {
	if h.orgKeyDeriver == nil {
		return nil
	}
	return h.orgKeyDeriver("org-credentials")
}

// Create handles POST /api/v1/orgs/:id/credentials.
func (h *OrgCredentialsHandler) Create(c *gin.Context) {
	orgID := c.Param("id")
	ctx := c.Request.Context()

	var req createOrgCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	orgKEK := h.orgKEK()
	if orgKEK == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server key not configured"})
		return
	}

	plaintext, err := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // G117 false positive — APIKey is encrypted before storage
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}
	ciphertext, err := secrets.EncryptSecret(orgKEK, plaintext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}
	zeroBytes(plaintext)

	allowlist := req.ModelAllowlist
	if allowlist == nil {
		allowlist = []string{}
	}

	credID, err := h.credStore.CreateOrgCredential(ctx, orgID, req.Name, req.Provider, ciphertext, allowlist, req.ModelContextLimits)
	if err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential) {
			c.JSON(http.StatusConflict, gin.H{"error": "credential for this provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create credential"})
		return
	}

	// Fetch the freshly-created row so the response reflects the DB-generated
	// timestamps and the stored ciphertext (for baseURL extraction).
	created, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil || created == nil {
		// Credential was stored but unreadable — surface a minimal response.
		c.JSON(http.StatusCreated, CredentialResponse{
			ID: credID, OrgID: orgID, Name: req.Name, Provider: req.Provider,
			ModelAllowlist: allowlist, ModelContextLimits: req.ModelContextLimits,
		})
		return
	}

	resp := buildCredentialResponse(created, orgKEK)

	if err := h.credStore.BindCredentialToAllOrgWorkspaces(ctx, credID, orgID); err != nil {
		resp.BindWarning = "credential created but auto-bind to existing org workspaces failed"
	}

	c.JSON(http.StatusCreated, resp)
}

// List handles GET /api/v1/orgs/:id/credentials.
func (h *OrgCredentialsHandler) List(c *gin.Context) {
	orgID := c.Param("id")
	rows, err := h.credStore.ListOrgCredentials(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list credentials"})
		return
	}
	kek := h.orgKEK()
	resp := make([]CredentialResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, buildCredentialResponse(row, kek))
	}
	c.JSON(http.StatusOK, resp)
}

// Update handles PUT /api/v1/orgs/:id/credentials/:credID.
func (h *OrgCredentialsHandler) Update(c *gin.Context) {
	orgID := c.Param("id")
	credID := c.Param("credID")
	ctx := c.Request.Context()

	var req updateOrgCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	existing, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve credential"})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	var newCiphertext []byte
	newKeyVersion := existing.KeyVersion
	// Re-encrypt whenever an encrypted field (apiKey OR baseURL) changes.
	// A baseURL-only update must still rewrite the ciphertext, since baseURL
	// lives inside the encrypted LLMProviderData blob — matching the admin
	// handler (admin_provider_credentials.go:267).
	if req.APIKey != nil || req.BaseURL != nil {
		orgKEK := h.orgKEK()
		if orgKEK == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server key not configured"})
			return
		}

		oldPlaintext, err := secrets.DecryptSecret(orgKEK, existing.Ciphertext)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt existing credential"})
			return
		}
		var pd secrets.LLMProviderData
		if err := json.Unmarshal(oldPlaintext, &pd); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode credential"})
			return
		}
		if req.APIKey != nil {
			pd.APIKey = *req.APIKey
		}
		if req.BaseURL != nil {
			pd.BaseURL = *req.BaseURL
		}
		newPlaintext, err := json.Marshal(pd) //nolint:gosec // G117 false positive — pd contains encrypted credential data
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
			return
		}
		newCiphertext, err = secrets.EncryptSecret(orgKEK, newPlaintext)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "re-encryption failed"})
			return
		}
		zeroBytes(newPlaintext)
		zeroBytes(oldPlaintext)
		newKeyVersion++
	}

	if err := h.credStore.UpdateOrgCredential(ctx, orgID, credID, req.Name, newCiphertext, req.ModelAllowlist, req.ModelContextLimits, newKeyVersion); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update credential"})
		return
	}

	// Fetch the updated row so the response carries the DB-generated updated_at
	// and the (possibly re-encrypted) ciphertext for baseURL extraction.
	updated, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil || updated == nil {
		c.JSON(http.StatusOK, CredentialResponse{ID: credID, OrgID: orgID})
		return
	}
	c.JSON(http.StatusOK, buildCredentialResponse(updated, h.orgKEK()))
}

// Delete handles DELETE /api/v1/orgs/:id/credentials/:credID.
func (h *OrgCredentialsHandler) Delete(c *gin.Context) {
	orgID := c.Param("id")
	credID := c.Param("credID")
	if err := h.credStore.DeleteOrgCredential(c.Request.Context(), orgID, credID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete credential"})
		return
	}
	c.Status(http.StatusNoContent)
}

// ProbeModels handles GET /api/v1/orgs/:id/credentials/:credID/models.
// It decrypts the stored credential and calls the provider's /v1/models
// (OpenAI-compatible) to discover available model IDs, merged with any saved
// context limits so the UI can pre-populate the config table.
func (h *OrgCredentialsHandler) ProbeModels(c *gin.Context) {
	orgID := c.Param("id")
	credID := c.Param("credID")
	ctx := c.Request.Context()

	row, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	kek := h.orgKEK()
	if kek == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server key not configured"})
		return
	}
	plaintext, err := secrets.DecryptSecret(kek, row.Ciphertext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt credential"})
		return
	}

	c.JSON(http.StatusOK, probeCredentialModels(ctx, plaintext, row.ModelContextLimits))
}

// CreateAutoApply handles POST /api/v1/orgs/:id/credentials/:credID/auto-apply.
func (h *OrgCredentialsHandler) CreateAutoApply(c *gin.Context) {
	orgID := c.Param("id")
	credID := c.Param("credID")
	ctx := c.Request.Context()

	cred, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
		return
	}
	if cred == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found in this organization"})
		return
	}

	if err := h.credStore.CreateOrgAutoApply(ctx, credID, orgID, 5); err != nil {
		if errors.Is(ClassifyPostgresError(err), ErrDuplicateCredential) {
			c.JSON(http.StatusConflict, gin.H{"error": "auto-apply rule already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create auto-apply rule"})
		return
	}
	c.Status(http.StatusCreated)
}

// ListAutoApply handles GET /api/v1/orgs/:id/credentials/:credID/auto-apply.
func (h *OrgCredentialsHandler) ListAutoApply(c *gin.Context) {
	orgID := c.Param("id")
	ctx := c.Request.Context()

	credID := c.Param("credID")
	if credID != "" {
		cred, err := h.credStore.GetOrgCredential(ctx, orgID, credID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify credential"})
			return
		}
		if cred == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "credential not found in this organization"})
			return
		}
	}

	rules, err := h.credStore.ListOrgAutoApply(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list auto-apply rules"})
		return
	}
	if rules == nil {
		rules = []*secrets.AutoApplyRule{}
	}
	c.JSON(http.StatusOK, rules)
}

// DeleteAutoApply handles DELETE /api/v1/orgs/:id/credentials/:credID/auto-apply.
func (h *OrgCredentialsHandler) DeleteAutoApply(c *gin.Context) {
	orgID := c.Param("id")
	credID := c.Param("credID")
	if err := h.credStore.DeleteOrgAutoApply(c.Request.Context(), credID, orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete auto-apply rule"})
		return
	}
	c.Status(http.StatusNoContent)
}
