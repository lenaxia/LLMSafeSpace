// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package prompt

import (
	"context"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespaces/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// mockPromptStore implements promptStore for testing.
type mockPromptStore struct {
	mock.Mock
}

func (m *mockPromptStore) GetPlatformSetting(ctx context.Context, key types.PlatformSettingKey) (*types.PlatformSetting, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.PlatformSetting), args.Error(1)
}

func (m *mockPromptStore) GetOrgPolicies(ctx context.Context, orgID string) ([]*types.OrgPolicy, error) {
	args := m.Called(ctx, orgID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.OrgPolicy), args.Error(1)
}

func (m *mockPromptStore) GetWorkspacePrompt(ctx context.Context, workspaceID string) (*types.WorkspacePrompt, error) {
	args := m.Called(ctx, workspaceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.WorkspacePrompt), args.Error(1)
}

func (m *mockPromptStore) GetWorkspaceOrgID(ctx context.Context, workspaceID string) (string, error) {
	args := m.Called(ctx, workspaceID)
	return args.String(0), args.Error(1)
}

func (m *mockPromptStore) GetAgentRole(ctx context.Context, roleID string) (*types.AgentRole, error) {
	args := m.Called(ctx, roleID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.AgentRole), args.Error(1)
}

type mockCache struct {
	mock.Mock
	deletedKeys []string
}

func (m *mockCache) GetObject(ctx context.Context, key string, value interface{}) error {
	return m.Called(ctx, key, value).Error(0)
}

func (m *mockCache) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return m.Called(ctx, key, value, expiration).Error(0)
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	m.deletedKeys = append(m.deletedKeys, key)
	return m.Called(ctx, key).Error(0)
}

func (m *mockCache) DeleteByPrefix(ctx context.Context, prefix string) error {
	return m.Called(ctx, prefix).Error(0)
}

// --- Tests ---

func TestResolveEffective_StandaloneUser_AllowUserPrompt(t *testing.T) {
	store := new(mockPromptStore)
	store.On("GetPlatformSetting", mock.Anything, types.SettingSysPromptPlatform).Return(&types.PlatformSetting{
		Value: []byte(`"Follow security policy at all times."`),
	}, nil)
	store.On("GetWorkspaceOrgID", mock.Anything, "ws-1").Return("", nil)
	store.On("GetWorkspacePrompt", mock.Anything, "ws-1").Return(&types.WorkspacePrompt{
		Prompt: "Focus on tests today",
	}, nil)

	svc := New(store, nil)
	result, err := svc.ResolveEffective(context.Background(), "ws-1")
	assert.NoError(t, err)
	assert.Contains(t, result.Resolved, "Follow security policy")
	assert.Contains(t, result.Resolved, "Focus on tests today")
	assert.True(t, result.AllowUserPrompt)
}

func TestResolveEffective_OrgLocked_OmitsUserPrompt(t *testing.T) {
	orgID := "org-1"
	locked := false
	store := new(mockPromptStore)
	store.On("GetPlatformSetting", mock.Anything, types.SettingSysPromptPlatform).Return(&types.PlatformSetting{
		Value: []byte(`"Platform rules"`),
	}, nil)
	store.On("GetWorkspaceOrgID", mock.Anything, "ws-1").Return(orgID, nil)
	store.On("GetOrgPolicies", mock.Anything, orgID).Return([]*types.OrgPolicy{
		{Key: types.PolicySysPromptOrg, Value: []byte(`"Org overlay"`)},
		{Key: types.PolicyAllowUserPrompt, Value: mustMarshalBool(&locked)},
	}, nil)
	// GetWorkspacePrompt should NOT be called when locked

	svc := New(store, nil)
	result, err := svc.ResolveEffective(context.Background(), "ws-1")
	assert.NoError(t, err)
	assert.Contains(t, result.Resolved, "Platform rules")
	assert.Contains(t, result.Resolved, "Org overlay")
	assert.NotContains(t, result.Resolved, "user instructions")
	assert.False(t, result.AllowUserPrompt)
	store.AssertNotCalled(t, "GetWorkspacePrompt")
}

func TestResolveEffective_OrgUnlocked_IncludesUserPrompt(t *testing.T) {
	orgID := "org-1"
	unlocked := true
	store := new(mockPromptStore)
	store.On("GetPlatformSetting", mock.Anything, types.SettingSysPromptPlatform).Return(&types.PlatformSetting{
		Value: []byte(`"Platform"`),
	}, nil)
	store.On("GetWorkspaceOrgID", mock.Anything, "ws-1").Return(orgID, nil)
	store.On("GetOrgPolicies", mock.Anything, orgID).Return([]*types.OrgPolicy{
		{Key: types.PolicyAllowUserPrompt, Value: mustMarshalBool(&unlocked)},
	}, nil)
	store.On("GetWorkspacePrompt", mock.Anything, "ws-1").Return(&types.WorkspacePrompt{
		Prompt: "My custom instructions",
	}, nil)

	svc := New(store, nil)
	result, err := svc.ResolveEffective(context.Background(), "ws-1")
	assert.NoError(t, err)
	assert.Contains(t, result.Resolved, "Platform")
	assert.Contains(t, result.Resolved, "My custom instructions")
	assert.True(t, result.AllowUserPrompt)
}

func TestResolveEffective_IncludesRoleSystemPrompt(t *testing.T) {
	orgID := "org-1"
	unlocked := true
	roleID := "role-1"
	roleSys := "You are a code reviewer."
	store := new(mockPromptStore)
	store.On("GetPlatformSetting", mock.Anything, types.SettingSysPromptPlatform).Return(&types.PlatformSetting{
		Value: []byte(`"Platform"`),
	}, nil)
	store.On("GetWorkspaceOrgID", mock.Anything, "ws-1").Return(orgID, nil)
	store.On("GetOrgPolicies", mock.Anything, orgID).Return([]*types.OrgPolicy{
		{Key: types.PolicyAllowUserPrompt, Value: mustMarshalBool(&unlocked)},
	}, nil)
	store.On("GetWorkspacePrompt", mock.Anything, "ws-1").Return(&types.WorkspacePrompt{
		Prompt:      "Custom",
		AgentRoleID: &roleID,
	}, nil)
	store.On("GetAgentRole", mock.Anything, roleID).Return(&types.AgentRole{
		Config: types.RoleConfig{System: &roleSys},
	}, nil)

	svc := New(store, nil)
	result, err := svc.ResolveEffective(context.Background(), "ws-1")
	assert.NoError(t, err)
	assert.Contains(t, result.Resolved, "You are a code reviewer.")
	assert.Equal(t, roleSys, result.RolePrompt)
}

func TestInvalidateOrgWorkspacesCache_CallsDeleteByPrefix(t *testing.T) {
	cache := new(mockCache)
	cache.On("DeleteByPrefix", mock.Anything, "ws:prompt:").Return(nil)

	svc := New(new(mockPromptStore), cache)
	svc.InvalidateOrgWorkspacesCache(context.Background(), "org-1")
	cache.AssertCalled(t, "DeleteByPrefix", mock.Anything, "ws:prompt:")
}

func mustMarshalBool(b *bool) []byte {
	if *b {
		return []byte("true")
	}
	return []byte("false")
}
