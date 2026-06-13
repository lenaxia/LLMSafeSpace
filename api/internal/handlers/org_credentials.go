// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// orgCredentialStore is the minimal OrgCredentialStore interface used by OrgCredentialsHandler.
type orgCredentialStore interface {
	CreateOrgCredential(ctx context.Context, orgID, name, provider string, ciphertext []byte, modelAllowlist []string) (string, error)
	ListOrgCredentials(ctx context.Context, orgID string) ([]*secrets.OrgCredentialMetadata, error)
	GetOrgCredential(ctx context.Context, orgID, credID string) (*secrets.OrgCredentialRow, error)
	UpdateOrgCredential(ctx context.Context, orgID, credID string, name *string, ciphertext []byte, modelAllowlist []string, keyVersion int) error
	DeleteOrgCredential(ctx context.Context, orgID, credID string) error
	BindCredentialToAllOrgWorkspaces(ctx context.Context, credentialID, orgID string) error
	CreateOrgAutoApply(ctx context.Context, credentialID, orgID string, withinPriority int) error
	ListOrgAutoApply(ctx context.Context, orgID string) ([]*secrets.AutoApplyRule, error)
	DeleteOrgAutoApply(ctx context.Context, credentialID, orgID string) error
}

// OrgCredentialsHandler handles org credential endpoints.
type OrgCredentialsHandler struct {
	credStore orgCredentialStore
	orgKeySvc *secrets.OrgKeyService
	authSvc   orgAuthService
}

// NewOrgCredentialsHandler creates a new OrgCredentialsHandler.
func NewOrgCredentialsHandler(store orgCredentialStore, orgKeySvc *secrets.OrgKeyService, authSvc orgAuthService) *OrgCredentialsHandler {
	return &OrgCredentialsHandler{credStore: store, orgKeySvc: orgKeySvc, authSvc: authSvc}
}

type createOrgCredentialRequest struct {
	Name           string   `json:"name"           binding:"required,min=1,max=128"`
	Provider       string   `json:"provider"       binding:"required"`
	APIKey         string   `json:"apiKey"         binding:"required"              log:"-"` //nolint:gosec // G117 false positive — field has log:"-" tag, never marshaled to response
	BaseURL        string   `json:"baseURL"`
	ModelAllowlist []string `json:"modelAllowlist"`
}

type updateOrgCredentialRequest struct {
	Name           *string  `json:"name"`
	APIKey         *string  `json:"apiKey"          log:"-"` //nolint:gosec // G117 false positive — field has log:"-" tag, never marshaled to response
	BaseURL        *string  `json:"baseURL"`
	ModelAllowlist []string `json:"modelAllowlist"`
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

	orgDEK, err := h.orgKeySvc.GetOrgDEK(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "org DEK not available — please log out and back in to refresh your org key"})
		return
	}

	plaintext, err := json.Marshal(secrets.LLMProviderData{
		Provider: req.Provider,
		APIKey:   req.APIKey,
		BaseURL:  req.BaseURL,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
		return
	}
	ciphertext, err := secrets.EncryptSecret(orgDEK, plaintext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption failed"})
		return
	}
	zeroBytes(plaintext)

	allowlist := req.ModelAllowlist
	if allowlist == nil {
		allowlist = []string{}
	}

	credID, err := h.credStore.CreateOrgCredential(ctx, orgID, req.Name, req.Provider, ciphertext, allowlist)
	if err != nil {
		if isDuplicateErr(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "credential for this provider already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create credential"})
		return
	}

	_ = h.credStore.BindCredentialToAllOrgWorkspaces(ctx, credID, orgID)

	c.JSON(http.StatusCreated, gin.H{"id": credID, "orgId": orgID, "name": req.Name, "provider": req.Provider})
}

// List handles GET /api/v1/orgs/:id/credentials.
func (h *OrgCredentialsHandler) List(c *gin.Context) {
	orgID := c.Param("id")
	creds, err := h.credStore.ListOrgCredentials(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list credentials"})
		return
	}
	if creds == nil {
		creds = []*secrets.OrgCredentialMetadata{}
	}
	c.JSON(http.StatusOK, creds)
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
	if req.APIKey != nil {
		orgDEK, err := h.orgKeySvc.GetOrgDEK(ctx, orgID)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "org DEK not available"})
			return
		}

		oldPlaintext, err := secrets.DecryptSecret(orgDEK, existing.Ciphertext)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt existing credential"})
			return
		}
		var pd secrets.LLMProviderData
		if err := json.Unmarshal(oldPlaintext, &pd); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode credential"})
			return
		}
		pd.APIKey = *req.APIKey
		if req.BaseURL != nil {
			pd.BaseURL = *req.BaseURL
		}
		newPlaintext, err := json.Marshal(pd)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode credential"})
			return
		}
		newCiphertext, err = secrets.EncryptSecret(orgDEK, newPlaintext)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "re-encryption failed"})
			return
		}
		zeroBytes(newPlaintext)
		zeroBytes(oldPlaintext)
		newKeyVersion++
	}

	if err := h.credStore.UpdateOrgCredential(ctx, orgID, credID, req.Name, newCiphertext, req.ModelAllowlist, newKeyVersion); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update credential"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": credID, "message": "credential updated"})
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
		if isDuplicateErr(err) {
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
