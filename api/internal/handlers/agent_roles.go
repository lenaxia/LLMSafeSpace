// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespaces/api/internal/services/role"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// roleStore is the data-access surface for agent role CRUD endpoints.
type roleStore interface {
	GetAgentRole(ctx context.Context, roleID string) (*types.AgentRole, error)
	ListAgentRoles(ctx context.Context, scope string, orgID string) ([]*types.AgentRole, error)
	CreateAgentRole(ctx context.Context, role *types.AgentRole, configJSON []byte, updatedBy string) (*types.AgentRole, error)
	UpdateAgentRole(ctx context.Context, roleID string, role *types.AgentRole, configJSON []byte, expectedUpdatedAt interface{}) (*types.AgentRole, error)
	DeleteAgentRole(ctx context.Context, roleID string) error
	GetRoleDependents(ctx context.Context, roleID string) ([]*types.AgentRole, error)
	HasRoleWorkspaceUsage(ctx context.Context, roleID string) (bool, error)
	SetOrgDefaultRole(ctx context.Context, orgID, roleID string) error
	GetWorkspaceAgentRole(ctx context.Context, workspaceID string) (*types.AgentRole, error)
	SetWorkspaceAgentRole(ctx context.Context, workspaceID, roleID, userID string) error
	GetWorkspaceOrgID(ctx context.Context, workspaceID string) (string, error)
	GetOrgPolicies(ctx context.Context, orgID string) ([]*types.OrgPolicy, error)
	LogOrgEvent(ctx context.Context, orgID, actorID, action, targetID string, metadata map[string]any) error
	LogAuditEvent(ctx context.Context, domain, actorID, action, targetID string, orgID *string, metadata map[string]any) error
}

// AgentRoleHandler handles platform and org agent role CRUD.
type AgentRoleHandler struct {
	store   roleStore
	svc     *role.Service
	authSvc orgAuthService
	logger  policyLogger
}

func NewAgentRoleHandler(store roleStore, svc *role.Service, authSvc orgAuthService, logger policyLogger) *AgentRoleHandler {
	return &AgentRoleHandler{store: store, svc: svc, authSvc: authSvc, logger: logger}
}

type createRoleRequest struct {
	Name        string             `json:"name" binding:"required,max=100"`
	Slug        string             `json:"slug" binding:"required,max=50"`
	Description string             `json:"description"`
	Extends     *string            `json:"extends,omitempty"`
	IsDefault   bool               `json:"isDefault"`
	Config      *types.RoleConfig  `json:"config,omitempty"`
}

type updateRoleRequest struct {
	Name        *string            `json:"name,omitempty"`
	Slug        *string            `json:"slug,omitempty"`
	Description *string            `json:"description,omitempty"`
	Extends     *string            `json:"extends,omitempty"`
	IsDefault   *bool              `json:"isDefault,omitempty"`
	Config      *types.RoleConfig  `json:"config,omitempty"`
}

// --- Platform Roles ---

func (h *AgentRoleHandler) ListPlatform(c *gin.Context) {
	roles, err := h.store.ListAgentRoles(c.Request.Context(), "platform", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list platform roles"})
		return
	}
	c.JSON(http.StatusOK, roles)
}

func (h *AgentRoleHandler) CreatePlatform(c *gin.Context) {
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	actorID := h.authSvc.GetUserID(c)

	if req.Extends != nil && *req.Extends != "" {
		if err := h.svc.ValidateExtends(c.Request.Context(), "platform", "", *req.Extends); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	configJSON, _ := types.MarshalRoleConfig(roleConfigOrDefault(req.Config))
	created, err := h.store.CreateAgentRole(c.Request.Context(), &types.AgentRole{
		Scope:       "platform",
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Extends:     req.Extends,
		IsDefault:   req.IsDefault,
	}, configJSON, actorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create role"})
		return
	}

	h.store.LogAuditEvent(c.Request.Context(), "admin", actorID, "role.platform.create", created.ID, nil, map[string]any{"name": req.Name})
	c.JSON(http.StatusCreated, created)
}

func (h *AgentRoleHandler) GetPlatform(c *gin.Context) {
	r, err := h.store.GetAgentRole(c.Request.Context(), c.Param("id"))
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *AgentRoleHandler) GetOrg(c *gin.Context) {
	roleID := c.Param("roleId")
	if roleID == "" {
		roleID = c.Param("id")
	}
	r, err := h.store.GetAgentRole(c.Request.Context(), roleID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *AgentRoleHandler) UpdatePlatform(c *gin.Context) {
	roleID := c.Param("id")
	var req updateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	actorID := h.authSvc.GetUserID(c)
	existing, err := h.store.GetAgentRole(c.Request.Context(), roleID)
	if err != nil || existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	if req.Extends != nil && *req.Extends != "" {
		if err := h.svc.ValidateExtends(c.Request.Context(), "platform", "", *req.Extends); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	updated := applyUpdates(existing, &req)
	configJSON, _ := types.MarshalRoleConfig(&updated.Config)

	result, err := h.store.UpdateAgentRole(c.Request.Context(), roleID, updated, configJSON, nil)
	if err != nil || result == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update role"})
		return
	}

	h.store.LogAuditEvent(c.Request.Context(), "admin", actorID, "role.platform.update", roleID, nil, nil)
	c.JSON(http.StatusOK, result)
}

func (h *AgentRoleHandler) DeletePlatform(c *gin.Context) {
	roleID := c.Param("id")
	actorID := h.authSvc.GetUserID(c)

	if err := h.svc.CheckDelete(c.Request.Context(), roleID); err != nil {
		if _, ok := err.(*role.DependentRolesError); ok {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		}
		return
	}

	if err := h.store.DeleteAgentRole(c.Request.Context(), roleID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete role"})
		return
	}

	h.store.LogAuditEvent(c.Request.Context(), "admin", actorID, "role.platform.delete", roleID, nil, nil)
	c.Status(http.StatusNoContent)
}

// --- Org Roles ---

func (h *AgentRoleHandler) ListOrg(c *gin.Context) {
	orgID := c.Param("id")
	roles, err := h.store.ListAgentRoles(c.Request.Context(), "org", orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list org roles"})
		return
	}
	c.JSON(http.StatusOK, roles)
}

func (h *AgentRoleHandler) CreateOrg(c *gin.Context) {
	orgID := c.Param("id")
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	actorID := h.authSvc.GetUserID(c)

	if req.Extends != nil && *req.Extends != "" {
		if err := h.svc.ValidateExtends(c.Request.Context(), "org", orgID, *req.Extends); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	configJSON, _ := types.MarshalRoleConfig(roleConfigOrDefault(req.Config))
	created, err := h.store.CreateAgentRole(c.Request.Context(), &types.AgentRole{
		Scope:       "org",
		OrgID:       &orgID,
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Extends:     req.Extends,
		IsDefault:   req.IsDefault,
	}, configJSON, actorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create org role"})
		return
	}

	if req.IsDefault {
		_ = h.store.SetOrgDefaultRole(c.Request.Context(), orgID, created.ID)
	}

	h.store.LogOrgEvent(c.Request.Context(), orgID, actorID, "role.org.create", created.ID, map[string]any{"name": req.Name})
	c.JSON(http.StatusCreated, created)
}

func (h *AgentRoleHandler) UpdateOrg(c *gin.Context) {
	orgID := c.Param("id")
	roleID := c.Param("roleId")
	var req updateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	actorID := h.authSvc.GetUserID(c)
	existing, err := h.store.GetAgentRole(c.Request.Context(), roleID)
	if err != nil || existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	if req.Extends != nil && *req.Extends != "" {
		if err := h.svc.ValidateExtends(c.Request.Context(), "org", orgID, *req.Extends); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	updated := applyUpdates(existing, &req)
	configJSON, _ := types.MarshalRoleConfig(&updated.Config)

	result, err := h.store.UpdateAgentRole(c.Request.Context(), roleID, updated, configJSON, nil)
	if err != nil || result == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update role"})
		return
	}

	if req.IsDefault != nil && *req.IsDefault {
		_ = h.store.SetOrgDefaultRole(c.Request.Context(), orgID, roleID)
	}

	h.store.LogOrgEvent(c.Request.Context(), orgID, actorID, "role.org.update", roleID, nil)
	c.JSON(http.StatusOK, result)
}

func (h *AgentRoleHandler) DeleteOrg(c *gin.Context) {
	orgID := c.Param("id")
	roleID := c.Param("roleId")
	actorID := h.authSvc.GetUserID(c)

	if err := h.svc.CheckDelete(c.Request.Context(), roleID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.DeleteAgentRole(c.Request.Context(), roleID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete role"})
		return
	}

	h.store.LogOrgEvent(c.Request.Context(), orgID, actorID, "role.org.delete", roleID, nil)
	c.Status(http.StatusNoContent)
}

// --- Workspace Role Selection ---

func (h *AgentRoleHandler) GetWorkspaceRole(c *gin.Context) {
	wsID := c.Param("id")
	r, err := h.store.GetWorkspaceAgentRole(c.Request.Context(), wsID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get workspace role"})
		return
	}
	if r == nil {
		c.JSON(http.StatusOK, nil)
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *AgentRoleHandler) SetWorkspaceRole(c *gin.Context) {
	wsID := c.Param("id")
	var req struct {
		RoleID string `json:"roleId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "roleId required"})
		return
	}

	actorID := h.authSvc.GetUserID(c)

	r, err := h.store.GetAgentRole(c.Request.Context(), req.RoleID)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	orgID, err := h.store.GetWorkspaceOrgID(c.Request.Context(), wsID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve workspace org"})
		return
	}

	// Enforce allow_user_prompt toggle (same as SetWorkspacePrompt)
	if orgID != "" {
		policies, err := h.store.GetOrgPolicies(c.Request.Context(), orgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check org policy"})
			return
		}
		for _, p := range policies {
			if p.Key == types.PolicyAllowUserPrompt {
				var allowed bool
				if json.Unmarshal(p.Value, &allowed) == nil && !allowed {
					c.JSON(http.StatusForbidden, gin.H{"error": "org admin has disabled member role customization"})
					return
				}
			}
		}
	}

	// Stress test 1.3: scope validation
	if r.Scope == "org" {
		if r.OrgID == nil || *r.OrgID != orgID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot select role from a different org"})
			return
		}
	}

	if err := h.store.SetWorkspaceAgentRole(c.Request.Context(), wsID, req.RoleID, actorID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set workspace role"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *AgentRoleHandler) GetEffectiveWorkspaceRole(c *gin.Context) {
	wsID := c.Param("id")
	r, err := h.store.GetWorkspaceAgentRole(c.Request.Context(), wsID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get workspace role"})
		return
	}
	if r == nil {
		c.JSON(http.StatusOK, gin.H{"effective": nil})
		return
	}

	effective, err := h.svc.ResolveEffective(c.Request.Context(), r.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve effective role"})
		return
	}
	c.JSON(http.StatusOK, effective)
}

// --- Helpers ---

func roleConfigOrDefault(cfg *types.RoleConfig) *types.RoleConfig {
	if cfg == nil {
		return &types.RoleConfig{Version: types.RoleConfigVersion}
	}
	if cfg.Version == 0 {
		cfg.Version = types.RoleConfigVersion
	}
	return cfg
}

func applyUpdates(existing *types.AgentRole, req *updateRoleRequest) *types.AgentRole {
	updated := *existing
	if req.Name != nil {
		updated.Name = *req.Name
	}
	if req.Slug != nil {
		updated.Slug = *req.Slug
	}
	if req.Description != nil {
		updated.Description = *req.Description
	}
	if req.Extends != nil {
		updated.Extends = req.Extends
	}
	if req.IsDefault != nil {
		updated.IsDefault = *req.IsDefault
	}
	if req.Config != nil {
		updated.Config = *req.Config
	}
	return &updated
}

var _ = time.Now
