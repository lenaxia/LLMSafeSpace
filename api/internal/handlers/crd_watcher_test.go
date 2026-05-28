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
