// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type ssoConfigStore interface {
	GetSSOConfig(ctx context.Context, orgID string) (*types.OrgSSOConfig, error)
	UpsertSSOConfig(ctx context.Context, cfg *types.OrgSSOConfig) error
	DeleteSSOConfig(ctx context.Context, orgID string) error
}

// SSOHandler handles GET/PUT/DELETE /api/v1/orgs/:id/sso (admin-only).
type SSOHandler struct {
	store   ssoConfigStore
	authSvc orgAuthService
}

func NewSSOHandler(store ssoConfigStore, authSvc orgAuthService) *SSOHandler {
	return &SSOHandler{store: store, authSvc: authSvc}
}

// Get handles GET /api/v1/orgs/:id/sso — returns config without the secret.
func (h *SSOHandler) Get(c *gin.Context) {
	orgID := c.Param("id")
	cfg, err := h.store.GetSSOConfig(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get SSO config"})
		return
	}
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{"configured": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"configured": true,
		"config": types.OrgSSOConfigResponse{
			DiscoveryURL:     cfg.DiscoveryURL,
			ClientID:         cfg.ClientID,
			GroupAdminClaim:  cfg.GroupAdminClaim,
			GroupMemberClaim: cfg.GroupMemberClaim,
			AutoProvision:    cfg.AutoProvision,
			Enabled:          cfg.Enabled,
		},
	})
}

// Put handles PUT /api/v1/orgs/:id/sso.
func (h *SSOHandler) Put(c *gin.Context) {
	orgID := c.Param("id")
	userID := h.authSvc.GetUserID(c)

	var req types.SetOrgSSOConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	groupAdmin := req.GroupAdminClaim
	if groupAdmin == "" {
		groupAdmin = "llmsafespace-admins"
	}
	groupMember := req.GroupMemberClaim
	if groupMember == "" {
		groupMember = "llmsafespace-members"
	}
	autoProvision := true
	if req.AutoProvision != nil {
		autoProvision = *req.AutoProvision
	}
	enabled := false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	cfg := &types.OrgSSOConfig{
		OrgID:            orgID,
		DiscoveryURL:     strings.TrimSpace(req.DiscoveryURL),
		ClientID:         req.ClientID,
		EncryptedSecret:  []byte(req.ClientSecret),
		GroupAdminClaim:  groupAdmin,
		GroupMemberClaim: groupMember,
		AutoProvision:    autoProvision,
		Enabled:          enabled,
		ConfiguredBy:     userID,
	}

	if err := h.store.UpsertSSOConfig(c.Request.Context(), cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save SSO config"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Delete handles DELETE /api/v1/orgs/:id/sso.
func (h *SSOHandler) Delete(c *gin.Context) {
	orgID := c.Param("id")
	if err := h.store.DeleteSSOConfig(c.Request.Context(), orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete SSO config"})
		return
	}
	c.Status(http.StatusNoContent)
}
