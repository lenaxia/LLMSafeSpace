// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

const (
	testTimeout      = 2 * time.Second
	testPollInterval = 50 * time.Millisecond
)

func setupWatcherMocks(t *testing.T) (*k8smocks.MockKubernetesClient, *k8smocks.MockWorkspaceInterface, *watch.FakeWatcher) {
	k8s := k8smocks.NewMockKubernetesClient()
	llm := k8smocks.NewMockLLMSafespaceV1Interface()
	ws := k8smocks.NewMockWorkspaceInterface()
	k8s.On("LlmsafespaceV1").Return(llm)
	llm.On("Workspaces", "default").Return(ws)
	fakeWatch := watch.NewFake()
	ws.On("List", mock.Anything).Return(&v1.WorkspaceList{}, nil).Maybe()
	ws.On("Watch", mock.Anything).Return(fakeWatch, nil).Maybe()
	return k8s, ws, fakeWatch
}

func TestWorkspaceWatcher_NilCallback_ReturnsError(t *testing.T) {
	k8s, _, _ := setupWatcherMocks(t)
	_, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", nil)
	assert.Error(t, err)
}

func TestWorkspaceWatcher_GetKnownPhase_Empty(t *testing.T) {
	k8s, _, _ := setupWatcherMocks(t)
	noop := func(*v1.Workspace) {}
	w, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)

	_, ok := w.GetKnownPhase("nonexistent")
	assert.False(t, ok)
}

func TestWorkspaceWatcher_PhaseChangeCallback(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	var callbackCalled atomic.Bool
	callback := func(workspace *v1.Workspace) {
		callbackCalled.Store(true)
	}

	w, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", callback)
	require.NoError(t, err)
	require.NoError(t, w.Start())
	defer w.Stop()

	// Send a workspace event
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}
	fakeWatch.Add(ws)

	// Then modify it to trigger phase change
	ws2 := ws.DeepCopy()
	ws2.Status.Phase = v1.WorkspacePhaseSuspending
	ws2.ResourceVersion = "2"
	fakeWatch.Modify(ws2)

	assert.Eventually(t, func() bool { return callbackCalled.Load() }, testTimeout, testPollInterval)
}

func TestWorkspaceWatcher_SeedResourceVersion_PopulatesKnownPhases(t *testing.T) {
	k8s := k8smocks.NewMockKubernetesClient()
	llm := k8smocks.NewMockLLMSafespaceV1Interface()
	ws := k8smocks.NewMockWorkspaceInterface()
	k8s.On("LlmsafespaceV1").Return(llm)
	llm.On("Workspaces", "default").Return(ws)

	ws.On("List", mock.Anything).Return(&v1.WorkspaceList{
		ListMeta: metav1.ListMeta{ResourceVersion: "100"},
		Items: []v1.Workspace{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ws-1"},
				Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "user-1"}},
				Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ws-2"},
				Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "user-2"}},
				Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspended},
			},
		},
	}, nil)

	noop := func(*v1.Workspace) {}
	w, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)

	broker := NewUserEventBroker()
	w.SetUserBroker(broker)

	err = w.seedResourceVersion()
	require.NoError(t, err)

	// Verify knownPhases populated
	phase, ok := w.GetKnownPhase("ws-1")
	assert.True(t, ok)
	assert.Equal(t, "Active", phase)

	phase, ok = w.GetKnownPhase("ws-2")
	assert.True(t, ok)
	assert.Equal(t, "Suspended", phase)

	// Verify broker ownership recorded
	assert.Equal(t, "user-1", broker.WorkspaceOwner("ws-1"))
	assert.Equal(t, "user-2", broker.WorkspaceOwner("ws-2"))
}

func TestWorkspaceWatcher_HandleEvent_Deleted(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	noop := func(*v1.Workspace) {}
	w, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)

	broker := NewUserEventBroker()
	w.SetUserBroker(broker)

	require.NoError(t, w.Start())
	defer w.Stop()

	// Add a workspace so it's known
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-del", ResourceVersion: "1"},
		Spec:       v1.WorkspaceSpec{Owner: v1.WorkspaceOwner{UserID: "user-del"}},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}
	fakeWatch.Add(ws)

	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-del")
		return ok
	}, testTimeout, testPollInterval)

	// Manually record ownership (normally done by seedResourceVersion)
	broker.RecordWorkspaceOwner("ws-del", "user-del")

	// Delete the workspace
	fakeWatch.Delete(ws)

	assert.Eventually(t, func() bool {
		_, ok := w.GetKnownPhase("ws-del")
		return !ok
	}, testTimeout, testPollInterval)

	// Verify broker ownership cleaned up
	assert.Equal(t, "", broker.WorkspaceOwner("ws-del"))
}

func TestWorkspaceWatcher_GetAllKnownPhases(t *testing.T) {
	k8s, _, fakeWatch := setupWatcherMocks(t)

	noop := func(*v1.Workspace) {}
	w, err := NewWorkspaceWatcher(k8s, &testLogger{}, "default", noop)
	require.NoError(t, err)
	require.NoError(t, w.Start())
	defer w.Stop()

	// Add two workspaces
	fakeWatch.Add(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-a", ResourceVersion: "1"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	})
	fakeWatch.Add(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-b", ResourceVersion: "2"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspended},
	})

	assert.Eventually(t, func() bool {
		phases := w.GetAllKnownPhases()
		return len(phases) >= 2
	}, testTimeout, testPollInterval)

	phases := w.GetAllKnownPhases()
	assert.Equal(t, "Active", phases["ws-a"])
	assert.Equal(t, "Suspended", phases["ws-b"])

	// Verify it's a copy — mutating doesn't affect watcher
	phases["ws-a"] = "Terminated"
	realPhase, _ := w.GetKnownPhase("ws-a")
	assert.Equal(t, "Active", realPhase)
}
