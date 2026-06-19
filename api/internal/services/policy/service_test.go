// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type fakePolicyStore struct {
	mu       sync.Mutex
	policies map[string][]*types.OrgPolicy
	err      error
}

func newFakePolicyStore() *fakePolicyStore {
	return &fakePolicyStore{policies: make(map[string][]*types.OrgPolicy)}
}

func (f *fakePolicyStore) GetOrgPolicies(_ context.Context, orgID string) ([]*types.OrgPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*types.OrgPolicy, len(f.policies[orgID]))
	copy(out, f.policies[orgID])
	return out, nil
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestGetEffectivePolicy_NoPolicies_Unrestricted(t *testing.T) {
	store := newFakePolicyStore()
	svc := New(store, nil)

	pol, err := svc.GetEffectivePolicy(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pol == nil {
		t.Fatal("expected non-nil policy")
	}
	if !pol.IsModelAllowed("anything") {
		t.Error("expected all models allowed when no policy set")
	}
	if !pol.IsProviderAllowed("anything") {
		t.Error("expected all providers allowed")
	}
	if pol.MaxWorkspaces() != -1 {
		t.Error("expected unlimited workspaces")
	}
}

func TestGetEffectivePolicy_AllowedModels_FiltersCorrectly(t *testing.T) {
	store := newFakePolicyStore()
	store.policies["org-1"] = []*types.OrgPolicy{
		{Key: types.PolicyAllowedModels, Value: mustJSON(t, []string{"gpt-4o", "claude-3"})},
	}
	svc := New(store, nil)

	pol, err := svc.GetEffectivePolicy(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pol.IsModelAllowed("gpt-4o") {
		t.Error("gpt-4o should be allowed")
	}
	if pol.IsModelAllowed("gpt-3.5") {
		t.Error("gpt-3.5 should be blocked")
	}
}

func TestGetEffectivePolicy_AllowedProviders_FiltersCorrectly(t *testing.T) {
	store := newFakePolicyStore()
	store.policies["org-1"] = []*types.OrgPolicy{
		{Key: types.PolicyAllowedProviders, Value: mustJSON(t, []string{"openai"})},
	}
	svc := New(store, nil)

	pol, _ := svc.GetEffectivePolicy(context.Background(), "org-1")
	if !pol.IsProviderAllowed("openai") {
		t.Error("openai should be allowed")
	}
	if pol.IsProviderAllowed("anthropic") {
		t.Error("anthropic should be blocked")
	}
}

func TestGetEffectivePolicy_WorkspaceQuotas(t *testing.T) {
	store := newFakePolicyStore()
	store.policies["org-1"] = []*types.OrgPolicy{
		{Key: types.PolicyMaxWorkspacesPerMember, Value: mustJSON(t, 5)},
		{Key: types.PolicyMaxActiveWorkspacesPerMem, Value: mustJSON(t, 3)},
	}
	svc := New(store, nil)

	pol, _ := svc.GetEffectivePolicy(context.Background(), "org-1")
	if pol.MaxWorkspaces() != 5 {
		t.Errorf("expected max 5, got %d", pol.MaxWorkspaces())
	}
	if pol.MaxActive() != 3 {
		t.Errorf("expected max active 3, got %d", pol.MaxActive())
	}
}

func TestGetEffectivePolicy_PlatformIntersection(t *testing.T) {
	store := newFakePolicyStore()
	platformModels := []string{"gpt-4o", "gpt-4o-mini", "claude-3"}
	orgModels := []string{"gpt-4o", "claude-3"}

	svc := New(store, nil)
	svc.SetPlatformPolicy(types.OrgPolicyValues{
		AllowedModels: &platformModels,
	})

	store.policies["org-1"] = []*types.OrgPolicy{
		{Key: types.PolicyAllowedModels, Value: mustJSON(t, orgModels)},
	}

	pol, _ := svc.GetEffectivePolicy(context.Background(), "org-1")
	// Intersection: gpt-4o, claude-3 — but NOT gpt-4o-mini (org doesn't allow)
	if !pol.IsModelAllowed("gpt-4o") {
		t.Error("gpt-4o should be in intersection")
	}
	if !pol.IsModelAllowed("claude-3") {
		t.Error("claude-3 should be in intersection")
	}
	if pol.IsModelAllowed("gpt-4o-mini") {
		t.Error("gpt-4o-mini should NOT be in intersection (org restricts)")
	}
}

func TestGetEffectivePolicy_PlatformMinQuota(t *testing.T) {
	store := newFakePolicyStore()
	svc := New(store, nil)
	platformMax := 10
	svc.SetPlatformPolicy(types.OrgPolicyValues{
		MaxWorkspacesPerMember: &platformMax,
	})
	store.policies["org-1"] = []*types.OrgPolicy{
		{Key: types.PolicyMaxWorkspacesPerMember, Value: mustJSON(t, 5)},
	}

	pol, _ := svc.GetEffectivePolicy(context.Background(), "org-1")
	// min(10, 5) = 5
	if pol.MaxWorkspaces() != 5 {
		t.Errorf("expected min(10,5)=5, got %d", pol.MaxWorkspaces())
	}
}

func TestGetEffectivePolicy_DBError_NoFallback(t *testing.T) {
	store := newFakePolicyStore()
	store.err = errors.New("db down")
	svc := New(store, nil)

	_, err := svc.GetEffectivePolicy(context.Background(), "org-1")
	if err == nil {
		t.Fatal("expected error on DB failure; must NOT silently fall back to unrestricted")
	}
}

func TestGetEffectivePolicy_CacheHit(t *testing.T) {
	store := newFakePolicyStore()
	cache := &fakeCache{data: make(map[string]any)}

	cachedModels := []string{"cached-model"}
	cached := types.OrgPolicyValues{AllowedModels: &cachedModels}
	cache.data[cacheKey("org-1")] = cached

	svc := New(store, cache)
	pol, err := svc.GetEffectivePolicy(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pol.IsModelAllowed("cached-model") {
		t.Error("cache hit should return cached policy")
	}
	if pol.IsModelAllowed("not-cached") {
		t.Error("cache hit should not allow models outside cached list")
	}
}

func TestInvalidateCache(t *testing.T) {
	store := newFakePolicyStore()
	cache := &fakeCache{data: make(map[string]any)}
	svc := New(store, cache)

	cache.data[cacheKey("org-1")] = "something"
	svc.InvalidateCache(context.Background(), "org-1")

	if _, ok := cache.data[cacheKey("org-1")]; ok {
		t.Error("cache should be invalidated")
	}
}

// fakeCache is a simple in-memory cache for testing.
type fakeCache struct {
	data map[string]any
}

func (f *fakeCache) GetObject(_ context.Context, key string, value any) error {
	v, ok := f.data[key]
	if !ok {
		return errors.New("not found")
	}
	// Simple copy via JSON roundtrip for struct types
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, value)
	return nil
}

func (f *fakeCache) SetObject(_ context.Context, key string, value any, _ time.Duration) error {
	f.data[key] = value
	return nil
}

func (f *fakeCache) Delete(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}
