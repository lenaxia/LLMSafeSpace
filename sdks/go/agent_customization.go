// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package llmsafespaces

import (
	"context"
	"fmt"
	"time"
)

// --- Prompt types ---

type PlatformPrompt struct {
	Prompt string `json:"prompt"`
}

type OrgPrompt struct {
	Prompt          string `json:"prompt"`
	AllowUserPrompt bool   `json:"allowUserPrompt"`
}

type SetPlatformPromptRequest struct {
	Prompt string `json:"prompt"`
}

type SetOrgPromptRequest struct {
	Prompt          *string `json:"prompt,omitempty"`
	AllowUserPrompt *bool   `json:"allowUserPrompt,omitempty"`
}

type WorkspacePromptResponse struct {
	Prompt string `json:"prompt"`
}

// --- Agent role types ---

type RoleConfig struct {
	Version     int              `json:"version"`
	System      *string          `json:"system,omitempty"`
	Description *string          `json:"description,omitempty"`
	Color       *string          `json:"color,omitempty"`
	Model       *string          `json:"model,omitempty"`
	Mode        *string          `json:"mode,omitempty"`
	Hidden      *bool            `json:"hidden,omitempty"`
	Permissions []PermissionRule `json:"permissions,omitempty"`
}

type PermissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

type AgentRole struct {
	ID          string     `json:"id"`
	Scope       string     `json:"scope"`
	OrgID       *string    `json:"orgId,omitempty"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description string     `json:"description"`
	Extends     *string    `json:"extends,omitempty"`
	IsDefault   bool       `json:"isDefault"`
	Config      RoleConfig `json:"config"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type EffectiveAgentRole struct {
	AgentRole
	EffectiveConfig  RoleConfig `json:"effectiveConfig"`
	InheritanceChain []string   `json:"inheritanceChain"`
}

type CreateAgentRoleRequest struct {
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	Extends     *string     `json:"extends,omitempty"`
	IsDefault   bool        `json:"isDefault"`
	Config      *RoleConfig `json:"config,omitempty"`
}

type UpdateAgentRoleRequest struct {
	Name        *string     `json:"name,omitempty"`
	Slug        *string     `json:"slug,omitempty"`
	Description *string     `json:"description,omitempty"`
	Extends     *string     `json:"extends,omitempty"`
	IsDefault   *bool       `json:"isDefault,omitempty"`
	Config      *RoleConfig `json:"config,omitempty"`
}

// --- PromptsService ---

type PromptsService struct{ c *Client }

func (s *PromptsService) GetPlatform(ctx context.Context) (*PlatformPrompt, error) {
	var resp PlatformPrompt
	err := s.c.do(ctx, "GET", "/admin/prompt", nil, &resp)
	return &resp, err
}

func (s *PromptsService) SetPlatform(ctx context.Context, prompt string) error {
	return s.c.do(ctx, "PUT", "/admin/prompt", &SetPlatformPromptRequest{Prompt: prompt}, nil)
}

func (s *PromptsService) GetOrg(ctx context.Context, orgID string) (*OrgPrompt, error) {
	var resp OrgPrompt
	err := s.c.do(ctx, "GET", fmt.Sprintf("/orgs/%s/prompt", orgID), nil, &resp)
	return &resp, err
}

func (s *PromptsService) SetOrg(ctx context.Context, orgID string, req *SetOrgPromptRequest) error {
	return s.c.do(ctx, "PUT", fmt.Sprintf("/orgs/%s/prompt", orgID), req, nil)
}

func (s *PromptsService) GetWorkspacePrompt(ctx context.Context, workspaceID string) (*WorkspacePromptResponse, error) {
	var resp WorkspacePromptResponse
	err := s.c.do(ctx, "GET", fmt.Sprintf("/workspaces/%s/prompt", workspaceID), nil, &resp)
	return &resp, err
}

func (s *PromptsService) SetWorkspacePrompt(ctx context.Context, workspaceID, prompt string) error {
	return s.c.do(ctx, "PUT", fmt.Sprintf("/workspaces/%s/prompt", workspaceID),
		map[string]string{"prompt": prompt}, nil)
}

// --- AgentRolesService ---

type AgentRolesService struct{ c *Client }

// Platform roles

func (s *AgentRolesService) ListPlatform(ctx context.Context) ([]AgentRole, error) {
	var roles []AgentRole
	err := s.c.do(ctx, "GET", "/admin/agent-roles", nil, &roles)
	return roles, err
}

func (s *AgentRolesService) CreatePlatform(ctx context.Context, req *CreateAgentRoleRequest) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "POST", "/admin/agent-roles", req, &resp)
	return &resp, err
}

func (s *AgentRolesService) GetPlatform(ctx context.Context, roleID string) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "GET", fmt.Sprintf("/admin/agent-roles/%s", roleID), nil, &resp)
	return &resp, err
}

func (s *AgentRolesService) UpdatePlatform(ctx context.Context, roleID string, req *UpdateAgentRoleRequest) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "PUT", fmt.Sprintf("/admin/agent-roles/%s", roleID), req, &resp)
	return &resp, err
}

func (s *AgentRolesService) DeletePlatform(ctx context.Context, roleID string) error {
	return s.c.do(ctx, "DELETE", fmt.Sprintf("/admin/agent-roles/%s", roleID), nil, nil)
}

// Org roles

func (s *AgentRolesService) ListOrg(ctx context.Context, orgID string) ([]AgentRole, error) {
	var roles []AgentRole
	err := s.c.do(ctx, "GET", fmt.Sprintf("/orgs/%s/agent-roles", orgID), nil, &roles)
	return roles, err
}

func (s *AgentRolesService) CreateOrg(ctx context.Context, orgID string, req *CreateAgentRoleRequest) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "POST", fmt.Sprintf("/orgs/%s/agent-roles", orgID), req, &resp)
	return &resp, err
}

func (s *AgentRolesService) GetOrg(ctx context.Context, orgID, roleID string) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "GET", fmt.Sprintf("/orgs/%s/agent-roles/%s", orgID, roleID), nil, &resp)
	return &resp, err
}

func (s *AgentRolesService) UpdateOrg(ctx context.Context, orgID, roleID string, req *UpdateAgentRoleRequest) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "PUT", fmt.Sprintf("/orgs/%s/agent-roles/%s", orgID, roleID), req, &resp)
	return &resp, err
}

func (s *AgentRolesService) DeleteOrg(ctx context.Context, orgID, roleID string) error {
	return s.c.do(ctx, "DELETE", fmt.Sprintf("/orgs/%s/agent-roles/%s", orgID, roleID), nil, nil)
}

// Workspace role selection

func (s *AgentRolesService) GetWorkspaceRole(ctx context.Context, workspaceID string) (*AgentRole, error) {
	var resp AgentRole
	err := s.c.do(ctx, "GET", fmt.Sprintf("/workspaces/%s/agent-role", workspaceID), nil, &resp)
	return &resp, err
}

func (s *AgentRolesService) SetWorkspaceRole(ctx context.Context, workspaceID, roleID string) error {
	return s.c.do(ctx, "PUT", fmt.Sprintf("/workspaces/%s/agent-role", workspaceID),
		map[string]string{"roleId": roleID}, nil)
}

func (s *AgentRolesService) GetEffectiveWorkspaceRole(ctx context.Context, workspaceID string) (*EffectiveAgentRole, error) {
	var resp EffectiveAgentRole
	err := s.c.do(ctx, "GET", fmt.Sprintf("/workspaces/%s/effective-agent-role", workspaceID), nil, &resp)
	return &resp, err
}
