// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// fakeFetcher is a deterministic in-memory parent fetcher for unit tests.
type fakeFetcher struct {
	parents     map[string]map[string]string // workspaceID → sessionID → parentID
	calls       atomic.Int32
	failOn      map[string]bool // sessionIDs that should return an error
	failCounter atomic.Int32
}

func (f *fakeFetcher) fetch(ctx context.Context, workspaceID, sessionID string) (string, error) {
	f.calls.Add(1)
	if f.failOn[sessionID] {
		f.failCounter.Add(1)
		return "", errors.New("fetch failed")
	}
	if ws, ok := f.parents[workspaceID]; ok {
		return ws[sessionID], nil
	}
	return "", nil
}

func TestSessionParentCache_TopLevelSession(t *testing.T) {
	// A session with no parent is its own root.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {"ses_root": ""},
	}}
	c := newSessionParentCache(f.fetch)
	root := c.resolveRoot(context.Background(), "ws-1", "ses_root")
	if root != "ses_root" {
		t.Fatalf("expected root=ses_root, got %q", root)
	}
}

func TestSessionParentCache_DirectChild(t *testing.T) {
	// A subagent session has parentID pointing at the root.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {
			"ses_child": "ses_root",
			"ses_root":  "",
		},
	}}
	c := newSessionParentCache(f.fetch)
	root := c.resolveRoot(context.Background(), "ws-1", "ses_child")
	if root != "ses_root" {
		t.Fatalf("expected root=ses_root, got %q", root)
	}
}

func TestSessionParentCache_NestedSubtask(t *testing.T) {
	// Grandchild session walks up two levels.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {
			"ses_grandchild": "ses_child",
			"ses_child":      "ses_root",
			"ses_root":       "",
		},
	}}
	c := newSessionParentCache(f.fetch)
	root := c.resolveRoot(context.Background(), "ws-1", "ses_grandchild")
	if root != "ses_root" {
		t.Fatalf("expected root=ses_root, got %q", root)
	}
}

func TestSessionParentCache_CachesEntries(t *testing.T) {
	// Repeated lookups on the same session must not refetch.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {
			"ses_child": "ses_root",
			"ses_root":  "",
		},
	}}
	c := newSessionParentCache(f.fetch)

	for i := 0; i < 5; i++ {
		_ = c.resolveRoot(context.Background(), "ws-1", "ses_child")
	}

	// Each resolveRoot walks ses_child → ses_root → end. First call fetches
	// twice (ses_child, ses_root); subsequent calls hit cache for both.
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("expected 2 fetches across 5 resolveRoot calls (cached), got %d", got)
	}
}

func TestSessionParentCache_FetchErrorReturnsLastKnownAncestor(t *testing.T) {
	// If lookup fails midway, resolution returns the deepest known ancestor
	// rather than dropping the prompt entirely. Better to bubble to the
	// wrong (deeper) session occasionally than silently lose user-facing
	// permission requests.
	f := &fakeFetcher{
		parents: map[string]map[string]string{
			"ws-1": {
				"ses_child": "ses_intermediate",
			},
		},
		failOn: map[string]bool{"ses_intermediate": true},
	}
	c := newSessionParentCache(f.fetch)
	root := c.resolveRoot(context.Background(), "ws-1", "ses_child")
	if root != "ses_intermediate" {
		t.Fatalf("expected fallback to deepest known ancestor (ses_intermediate), got %q", root)
	}
}

func TestSessionParentCache_FetchErrorOnInitialSessionReturnsItself(t *testing.T) {
	// If the very first lookup fails, the input session is itself returned
	// (so the event still carries something the frontend can match on).
	f := &fakeFetcher{
		parents: map[string]map[string]string{},
		failOn:  map[string]bool{"ses_unknown": true},
	}
	c := newSessionParentCache(f.fetch)
	root := c.resolveRoot(context.Background(), "ws-1", "ses_unknown")
	if root != "ses_unknown" {
		t.Fatalf("expected ses_unknown returned as root, got %q", root)
	}
}

func TestSessionParentCache_CycleProtection(t *testing.T) {
	// A malformed parent chain that loops must not hang or recurse forever.
	// resolveRoot bounds depth to 16; after that it returns the current node.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {
			"ses_a": "ses_b",
			"ses_b": "ses_a", // cycle
		},
	}}
	c := newSessionParentCache(f.fetch)
	done := make(chan struct{})
	go func() {
		_ = c.resolveRoot(context.Background(), "ws-1", "ses_a")
		close(done)
	}()
	select {
	case <-done:
		// success: returned within reasonable time
	case <-context.Background().Done():
		t.Fatal("resolveRoot hung on cyclic parent chain")
	}
}

func TestSessionParentCache_Invalidate(t *testing.T) {
	// invalidate() drops cached entries so the next lookup refetches.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {
			"ses_child": "ses_root",
			"ses_root":  "",
		},
	}}
	c := newSessionParentCache(f.fetch)
	_ = c.resolveRoot(context.Background(), "ws-1", "ses_child")
	beforeInvalidate := f.calls.Load()

	c.invalidate("ws-1")

	_ = c.resolveRoot(context.Background(), "ws-1", "ses_child")
	afterInvalidate := f.calls.Load()

	if afterInvalidate-beforeInvalidate != 2 {
		t.Fatalf("expected 2 refetches after invalidate, got %d", afterInvalidate-beforeInvalidate)
	}
}

func TestSessionParentCache_PerWorkspaceIsolation(t *testing.T) {
	// Cache entries from one workspace must not leak into another.
	f := &fakeFetcher{parents: map[string]map[string]string{
		"ws-1": {"ses_x": "ses_root1", "ses_root1": ""},
		"ws-2": {"ses_x": "ses_root2", "ses_root2": ""},
	}}
	c := newSessionParentCache(f.fetch)

	root1 := c.resolveRoot(context.Background(), "ws-1", "ses_x")
	root2 := c.resolveRoot(context.Background(), "ws-2", "ses_x")

	if root1 != "ses_root1" {
		t.Fatalf("ws-1 root: expected ses_root1, got %q", root1)
	}
	if root2 != "ses_root2" {
		t.Fatalf("ws-2 root: expected ses_root2, got %q", root2)
	}
}
