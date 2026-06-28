// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package role

import (
	"context"
	"fmt"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

const maxChainDepth = 10

// roleStore is the data-access surface for the RoleService.
type roleStore interface {
	GetAgentRole(ctx context.Context, roleID string) (*types.AgentRole, error)
	ListAgentRoles(ctx context.Context, scope string, orgID string) ([]*types.AgentRole, error)
	CreateAgentRole(ctx context.Context, role *types.AgentRole, configJSON []byte) (*types.AgentRole, error)
	UpdateAgentRole(ctx context.Context, roleID string, role *types.AgentRole, configJSON []byte) (*types.AgentRole, error)
	DeleteAgentRole(ctx context.Context, roleID string) error
	GetRoleDependents(ctx context.Context, roleID string) ([]*types.AgentRole, error)
	HasRoleWorkspaceUsage(ctx context.Context, roleID string) (bool, error)
	SetOrgDefaultRole(ctx context.Context, orgID, roleID string) error
}

// Service handles agent role CRUD, inheritance resolution, and validation.
type Service struct {
	store roleStore
}

func New(store roleStore) *Service {
	return &Service{store: store}
}

// ResolveEffective walks the extends chain from leaf to root and merges configs.
// Returns the fully resolved role with the merged config.
func (s *Service) ResolveEffective(ctx context.Context, roleID string) (*types.EffectiveAgentRole, error) {
	chain, err := s.walkChain(ctx, roleID)
	if err != nil {
		return nil, err
	}

	var merged *types.RoleConfig
	chainIDs := make([]string, 0, len(chain))

	for i := len(chain) - 1; i >= 0; i-- {
		role := chain[i]
		chainIDs = append(chainIDs, role.ID)
		if merged == nil {
			cp := role.Config
			merged = &cp
		} else {
			merged = types.MergeRoleConfigs(merged, &role.Config)
		}
	}

	if merged == nil {
		merged = &types.RoleConfig{Version: types.RoleConfigVersion}
	}

	leaf := chain[0]
	return &types.EffectiveAgentRole{
		AgentRole:        *leaf,
		EffectiveConfig:  *merged,
		InheritanceChain: chainIDs,
	}, nil
}

// walkChain walks from the given role up to its root, returning the chain
// in leaf-to-root order. Validates depth and detects cycles.
func (s *Service) walkChain(ctx context.Context, roleID string) ([]*types.AgentRole, error) {
	var chain []*types.AgentRole
	visited := make(map[string]bool)
	current := roleID

	for current != "" {
		if len(chain) >= maxChainDepth {
			return nil, fmt.Errorf("inheritance chain exceeds max depth %d", maxChainDepth)
		}
		if visited[current] {
			return nil, fmt.Errorf("inheritance cycle detected at role %s", current)
		}
		visited[current] = true

		role, err := s.store.GetAgentRole(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("get role %s: %w", current, err)
		}
		if role == nil {
			return nil, fmt.Errorf("role %s not found", current)
		}

		chain = append(chain, role)

		if role.Extends == nil || *role.Extends == "" {
			break
		}
		current = *role.Extends
	}

	if len(chain) == 0 {
		return nil, fmt.Errorf("role %s not found", roleID)
	}
	return chain, nil
}

// ValidateExtends checks that the extends target is valid for the given scope/org.
// (Stress test 1.2: cross-org extends guard)
func (s *Service) ValidateExtends(ctx context.Context, scope, orgID string, extendsID string) error {
	if extendsID == "" {
		return nil
	}

	target, err := s.store.GetAgentRole(ctx, extendsID)
	if err != nil {
		return fmt.Errorf("get extends target: %w", err)
	}
	if target == nil {
		return fmt.Errorf("extends target %s not found", extendsID)
	}

	if scope == "org" {
		if target.Scope == "platform" {
			return nil
		}
		if target.Scope == "org" {
			if target.OrgID == nil || (orgID != "" && *target.OrgID != orgID) {
				return fmt.Errorf("cannot extend role from a different org")
			}
		}
	}

	if scope == "platform" && target.Scope != "platform" {
		return fmt.Errorf("platform roles can only extend other platform roles")
	}

	return nil
}

// CheckDelete validates that a role can be safely deleted.
// (Stress test 3.2: dependent-role check + workspace usage check)
func (s *Service) CheckDelete(ctx context.Context, roleID string) error {
	dependents, err := s.store.GetRoleDependents(ctx, roleID)
	if err != nil {
		return fmt.Errorf("check dependents: %w", err)
	}
	if len(dependents) > 0 {
		names := make([]string, len(dependents))
		for i, d := range dependents {
			names[i] = d.Name
		}
		return &DependentRolesError{RoleID: roleID, Dependents: names}
	}

	inUse, err := s.store.HasRoleWorkspaceUsage(ctx, roleID)
	if err != nil {
		return fmt.Errorf("check workspace usage: %w", err)
	}
	if inUse {
		return &RoleInUseError{RoleID: roleID}
	}

	return nil
}

// DependentRolesError indicates a role cannot be deleted because other roles extend it.
type DependentRolesError struct {
	RoleID     string
	Dependents []string
}

func (e *DependentRolesError) Error() string {
	return fmt.Sprintf("role %s has dependent roles: %v", e.RoleID, e.Dependents)
}

// RoleInUseError indicates a role cannot be deleted because one or more
// workspaces currently have it assigned.
type RoleInUseError struct {
	RoleID string
}

func (e *RoleInUseError) Error() string {
	return fmt.Sprintf("role %s is assigned to one or more workspaces", e.RoleID)
}
