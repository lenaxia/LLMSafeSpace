// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package role

import (
	"context"
	"fmt"
	"testing"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// mockStore implements roleStore for testing.
type mockStore struct {
	roles      map[string]*types.AgentRole
	dependents map[string][]*types.AgentRole
	usage      map[string]bool
}

func newMockStore() *mockStore {
	return &mockStore{
		roles:      make(map[string]*types.AgentRole),
		dependents: make(map[string][]*types.AgentRole),
		usage:      make(map[string]bool),
	}
}

func (m *mockStore) addRole(r *types.AgentRole) {
	m.roles[r.ID] = r
}

func (m *mockStore) GetAgentRole(_ context.Context, roleID string) (*types.AgentRole, error) {
	r, ok := m.roles[roleID]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *mockStore) ListAgentRoles(_ context.Context, scope, orgID string) ([]*types.AgentRole, error) {
	return nil, nil
}
func (m *mockStore) CreateAgentRole(_ context.Context, _ *types.AgentRole, _ []byte) (*types.AgentRole, error) {
	return nil, nil
}
func (m *mockStore) UpdateAgentRole(_ context.Context, _ string, _ *types.AgentRole, _ []byte) (*types.AgentRole, error) {
	return nil, nil
}
func (m *mockStore) DeleteAgentRole(_ context.Context, _ string) error      { return nil }
func (m *mockStore) SetOrgDefaultRole(_ context.Context, _, _ string) error { return nil }

func (m *mockStore) GetRoleDependents(_ context.Context, roleID string) ([]*types.AgentRole, error) {
	return m.dependents[roleID], nil
}

func (m *mockStore) HasRoleWorkspaceUsage(_ context.Context, roleID string) (bool, error) {
	return m.usage[roleID], nil
}

func makeRole(id, scope string, orgID *string, extends *string, system string) *types.AgentRole {
	sys := system
	return &types.AgentRole{
		ID:      id,
		Scope:   scope,
		OrgID:   orgID,
		Name:    id,
		Slug:    id,
		Extends: extends,
		Config:  types.RoleConfig{Version: 1, System: &sys},
	}
}

func strPtr(s string) *string { return &s }

// --- walkChain / ResolveEffective ---

func TestResolveEffective_MergesParentChild(t *testing.T) {
	store := newMockStore()
	platSys := "You are a code reviewer."
	orgSys := "Additional: check for SQL injection."

	store.addRole(makeRole("child", "org", strPtr("org-1"), strPtr("parent"), orgSys))
	store.addRole(makeRole("parent", "platform", nil, nil, platSys))

	svc := New(store)
	result, err := svc.ResolveEffective(context.Background(), "child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.EffectiveConfig.System == nil {
		t.Fatal("expected non-nil system")
	}
	// Child should override parent
	if *result.EffectiveConfig.System != orgSys {
		t.Errorf("expected child system to win, got %q", *result.EffectiveConfig.System)
	}
	// Chain should be [child, parent]
	if len(result.InheritanceChain) != 2 {
		t.Errorf("expected chain length 2, got %d", len(result.InheritanceChain))
	}
}

func TestWalkChain_DetectsCycle(t *testing.T) {
	store := newMockStore()
	store.addRole(makeRole("a", "platform", nil, strPtr("b"), "A"))
	store.addRole(makeRole("b", "platform", nil, strPtr("a"), "B"))

	svc := New(store)
	_, err := svc.ResolveEffective(context.Background(), "a")
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestWalkChain_EnforcesDepthLimit(t *testing.T) {
	store := newMockStore()
	// Create a chain of 12 roles: r0 -> r1 -> ... -> r11
	for i := 0; i < 12; i++ {
		var extends *string
		if i < 11 {
			next := fmt.Sprintf("r%d", i+1)
			extends = &next
		}
		store.addRole(makeRole(fmt.Sprintf("r%d", i), "platform", nil, extends, fmt.Sprintf("role %d", i)))
	}

	svc := New(store)
	_, err := svc.ResolveEffective(context.Background(), "r0")
	if err == nil {
		t.Fatal("expected depth limit error")
	}
}

// --- ValidateExtends ---

func TestValidateExtends_OrgCanExtendPlatform(t *testing.T) {
	store := newMockStore()
	store.addRole(makeRole("plat-role", "platform", nil, nil, "base"))

	svc := New(store)
	err := svc.ValidateExtends(context.Background(), "org", "org-1", "plat-role")
	if err != nil {
		t.Errorf("org extending platform should be allowed: %v", err)
	}
}

func TestValidateExtends_OrgCanExtendSameOrg(t *testing.T) {
	store := newMockStore()
	store.addRole(makeRole("org-role-a", "org", strPtr("org-1"), nil, "A"))

	svc := New(store)
	err := svc.ValidateExtends(context.Background(), "org", "org-1", "org-role-a")
	if err != nil {
		t.Errorf("org extending same org should be allowed: %v", err)
	}
}

func TestValidateExtends_OrgCannotExtendDifferentOrg(t *testing.T) {
	store := newMockStore()
	store.addRole(makeRole("org-b-role", "org", strPtr("org-b"), nil, "B"))

	svc := New(store)
	err := svc.ValidateExtends(context.Background(), "org", "org-a", "org-b-role")
	if err == nil {
		t.Fatal("org extending different org should be rejected")
	}
}

func TestValidateExtends_PlatformCannotExtendOrg(t *testing.T) {
	store := newMockStore()
	store.addRole(makeRole("org-role", "org", strPtr("org-1"), nil, "org"))

	svc := New(store)
	err := svc.ValidateExtends(context.Background(), "platform", "", "org-role")
	if err == nil {
		t.Fatal("platform extending org role should be rejected")
	}
}

// --- CheckDelete ---

func TestCheckDelete_BlocksWhenDependentsExist(t *testing.T) {
	store := newMockStore()
	store.dependents["target"] = []*types.AgentRole{
		makeRole("child", "org", strPtr("org-1"), strPtr("target"), ""),
	}

	svc := New(store)
	err := svc.CheckDelete(context.Background(), "target")
	if err == nil {
		t.Fatal("expected error when dependents exist")
	}
	if _, ok := err.(*DependentRolesError); !ok {
		t.Errorf("expected DependentRolesError, got %T: %v", err, err)
	}
}

func TestCheckDelete_BlocksWhenWorkspaceUsageExists(t *testing.T) {
	store := newMockStore()
	store.usage["target"] = true

	svc := New(store)
	err := svc.CheckDelete(context.Background(), "target")
	if err == nil {
		t.Fatal("expected error when workspace usage exists")
	}
}

func TestCheckDelete_PassesWhenClean(t *testing.T) {
	store := newMockStore()

	svc := New(store)
	err := svc.CheckDelete(context.Background(), "clean-role")
	if err != nil {
		t.Errorf("expected no error for clean role: %v", err)
	}
}
