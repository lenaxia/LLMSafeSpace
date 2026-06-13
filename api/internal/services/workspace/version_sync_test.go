// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// VersionSyncCallback is called with (workspaceID, imageTag, agentVersion)
// whenever a workspace transitions to Active or is seeded at startup with
// a non-empty imageTag. It is the authoritative trigger for syncing runtime
// version info to the DB, replacing the lazy HTTP-poll side-effect in
// GetWorkspaceStatus.

// --- VersionSyncCallback fires on Creating→Active transition ---

func TestWorkspaceWatcher_VersionSync_FiredOnCreatingToActive(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	type syncCall struct {
		id           string
		imageTag     string
		agentVersion string
	}
	var mu sync.Mutex
	var calls []syncCall

	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		mu.Lock()
		calls = append(calls, syncCall{id, tag, av})
		mu.Unlock()
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	// Seed workspace as Creating (no callback expected yet)
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-sync-1", ResourceVersion: "1"},
		Status: v1.WorkspaceStatus{
			Phase:    v1.WorkspacePhaseCreating,
			ImageTag: "",
		},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-sync-1")
		return ok
	}, testTimeout, testPollInterval)

	// Transition to Active with imageTag set
	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseActive
	ws2.Status.ImageTag = "ts-1781332002"
	fakeWatch.Modify(ws2)

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1
	}, testTimeout, testPollInterval)

	mu.Lock()
	got := calls[0]
	mu.Unlock()
	assert.Equal(t, "ws-sync-1", got.id)
	assert.Equal(t, "ts-1781332002", got.imageTag)
}

// --- VersionSyncCallback fires on Resuming→Active transition ---

func TestWorkspaceWatcher_VersionSync_FiredOnResumingToActive(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	var called atomic.Bool
	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		called.Store(true)
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-resume", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseResuming},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-resume")
		return ok
	}, testTimeout, testPollInterval)

	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseActive
	ws2.Status.ImageTag = "ts-1781332002"
	fakeWatch.Modify(ws2)

	assert.Eventually(t, called.Load, testTimeout, testPollInterval)
}

// --- VersionSyncCallback NOT fired when imageTag is empty ---

func TestWorkspaceWatcher_VersionSync_NotFiredWhenImageTagEmpty(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	var called atomic.Bool
	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		called.Store(true)
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-notag", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseCreating},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-notag")
		return ok
	}, testTimeout, testPollInterval)

	// Transition to Active with no imageTag
	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseActive
	ws2.Status.ImageTag = ""
	fakeWatch.Modify(ws2)

	// Wait for phase to update, then verify no sync call
	assert.Eventually(t, func() bool {
		p, _ := w.GetKnownPhase("ws-notag")
		return p == "Active"
	}, testTimeout, testPollInterval)
	time.Sleep(50 * time.Millisecond)
	assert.False(t, called.Load(), "must not call version sync when imageTag is empty")
}

// --- VersionSyncCallback NOT fired when imageTag unchanged on phase transition ---

func TestWorkspaceWatcher_VersionSync_NotFiredOnSuspend(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	var callCount atomic.Int32
	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		callCount.Add(1)
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	// Add workspace as Active with imageTag — this fires one sync call (tag "" → "ts-old")
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-suspend", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive, ImageTag: "ts-old"},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		return callCount.Load() == 1
	}, testTimeout, testPollInterval)

	// Transition to Suspending with the SAME imageTag — no new sync call expected
	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseSuspending
	// imageTag unchanged: "ts-old"
	fakeWatch.Modify(ws2)

	assert.Eventually(t, func() bool {
		p, _ := w.GetKnownPhase("ws-suspend")
		return p == "Suspending"
	}, testTimeout, testPollInterval)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), callCount.Load(), "must not fire additional sync when imageTag unchanged")
}

// --- Seed: VersionSyncCallback fires during seedResourceVersion for Active workspaces ---

func TestWorkspaceWatcher_VersionSync_FiredDuringSeedForActiveWorkspaces(t *testing.T) {
	k8s := k8smocks.NewMockKubernetesClient()
	llm := k8smocks.NewMockLLMSafespaceV1Interface()
	ws := k8smocks.NewMockWorkspaceInterface()
	k8s.On("LlmsafespaceV1").Return(llm, nil)
	llm.On("Workspaces", "default").Return(ws)

	ws.On("List", mock.Anything, mock.Anything).Return(&v1.WorkspaceList{
		ListMeta: metav1.ListMeta{ResourceVersion: "100"},
		Items: []v1.Workspace{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ws-active-seed"},
				Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "u1"}},
				Status: v1.WorkspaceStatus{
					Phase:    v1.WorkspacePhaseActive,
					ImageTag: "ts-1781332002",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ws-suspended-seed"},
				Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "u2"}},
				Status: v1.WorkspaceStatus{
					Phase:    v1.WorkspacePhaseSuspended,
					ImageTag: "ts-1781332002",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ws-active-notag"},
				Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "u3"}},
				Status: v1.WorkspaceStatus{
					Phase:    v1.WorkspacePhaseActive,
					ImageTag: "",
				},
			},
		},
	}, nil)

	type syncCall struct{ id, tag string }
	var mu sync.Mutex
	var calls []syncCall

	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		mu.Lock()
		calls = append(calls, syncCall{id, tag})
		mu.Unlock()
	})

	err = w.seedResourceVersion()
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	// Only the Active workspace with a non-empty imageTag should be synced.
	require.Len(t, calls, 1, "expected exactly one sync call")
	assert.Equal(t, "ws-active-seed", calls[0].id)
	assert.Equal(t, "ts-1781332002", calls[0].tag)
}

// --- Seed: no panic / no call when callback is nil ---

func TestWorkspaceWatcher_VersionSync_NilCallbackIsSafe(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	// Intentionally do NOT call SetVersionSyncCallback

	require.NoError(t, w.Start())
	defer w.Stop()

	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-nil-cb", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseCreating},
	}
	fakeWatch.Add(ws)

	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseActive
	ws2.Status.ImageTag = "ts-1781332002"
	fakeWatch.Modify(ws2)

	// Just verify no panic and phase is tracked
	assert.Eventually(t, func() bool {
		p, _ := w.GetKnownPhase("ws-nil-cb")
		return p == "Active"
	}, testTimeout, testPollInterval)
}

// --- Active→Active event: VersionSyncCallback still fires (imageTag update without phase change) ---

func TestWorkspaceWatcher_VersionSync_FiredOnActiveToActiveWithNewImageTag(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	type syncCall struct{ tag string }
	var mu sync.Mutex
	var calls []syncCall

	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		mu.Lock()
		calls = append(calls, syncCall{tag})
		mu.Unlock()
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	// Seed as Active with old tag — fires one sync call (tag "" → "ts-old")
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-aa", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive, ImageTag: "ts-old"},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1 && calls[0].tag == "ts-old"
	}, testTimeout, testPollInterval)

	// Controller updates imageTag while still Active (e.g. after an in-place image refresh).
	// Phase stays Active — NOT a phase transition, but imageTag changed — must fire again.
	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.ImageTag = "ts-1781332002"
	fakeWatch.Modify(ws2)

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) >= 2 && calls[len(calls)-1].tag == "ts-1781332002"
	}, testTimeout, testPollInterval, "Active→Active imageTag change must fire version sync")
}

// --- knownImageTags is cleaned up on workspace deletion ---
// Regression: if not cleaned up, a re-created workspace with the same name
// and same imageTag would silently skip the sync (stale oldTag == newImageTag).

func TestWorkspaceWatcher_VersionSync_KnownImageTagsClearedOnDelete(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	var mu sync.Mutex
	var calls []string

	noop := func(*v1.Workspace) {}
	w, err := NewWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	w.SetVersionSyncCallback(func(id, tag, av string) {
		mu.Lock()
		calls = append(calls, tag)
		mu.Unlock()
	})
	require.NoError(t, w.Start())
	defer w.Stop()

	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-recycle", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseCreating},
	}
	fakeWatch.Add(ws)
	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-recycle")
		return ok
	}, testTimeout, testPollInterval)

	// Transition to Active with imageTag — 1 sync call
	ws2 := ws.DeepCopy()
	ws2.ResourceVersion = "2"
	ws2.Status.Phase = v1.WorkspacePhaseActive
	ws2.Status.ImageTag = "ts-1781332002"
	fakeWatch.Modify(ws2)
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1
	}, testTimeout, testPollInterval)

	// Delete the workspace — knownImageTags entry must be removed
	fakeWatch.Delete(ws2)
	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-recycle")
		return !ok
	}, testTimeout, testPollInterval)

	// Re-add with the same name and same imageTag — must fire sync again
	// (if knownImageTags was not cleared, oldTag == newImageTag and sync is skipped)
	ws3 := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-recycle", ResourceVersion: "10"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseCreating},
	}
	fakeWatch.Add(ws3)
	ws4 := ws3.DeepCopy()
	ws4.ResourceVersion = "11"
	ws4.Status.Phase = v1.WorkspacePhaseActive
	ws4.Status.ImageTag = "ts-1781332002" // same tag as before
	fakeWatch.Modify(ws4)

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	}, testTimeout, testPollInterval, "re-created workspace with same imageTag must fire sync again after delete")
}
