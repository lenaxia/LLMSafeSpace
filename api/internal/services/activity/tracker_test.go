// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package activity

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type testLogger struct{}

func (l *testLogger) Debug(msg string, kv ...interface{})                  {}
func (l *testLogger) Info(msg string, kv ...interface{})                   {}
func (l *testLogger) Warn(msg string, kv ...interface{})                   {}
func (l *testLogger) Error(msg string, err error, kv ...interface{})       {}
func (l *testLogger) Fatal(msg string, err error, kv ...interface{})       {}
func (l *testLogger) With(kv ...interface{}) pkginterfaces.LoggerInterface { return l }
func (l *testLogger) Sync() error                                          { return nil }

func makeWorkspaceCRD(name string, maxActiveSessions int) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Owner:             v1.WorkspaceOwner{UserID: "user-1"},
			MaxActiveSessions: int32(maxActiveSessions),
		},
		Status: v1.WorkspaceStatus{
			Phase: v1.WorkspacePhaseActive,
			PodIP: "10.0.0.1",
		},
	}
}

func newTestTracker(wsMock *k8smocks.MockWorkspaceInterface) *ActivityTracker {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock, nil)
	llmMock.On("Workspaces", "default").Return(wsMock)
	return NewActivityTracker(k8sMock, &testLogger{}, "default")
}

func TestActivityTracker_RecordStoresTimestamp(t *testing.T) {
	tracker := newTestTracker(k8smocks.NewMockWorkspaceInterface())

	before := time.Now()
	tracker.Record("ws-1")
	after := time.Now()

	assert.Equal(t, 1, tracker.PendingCount())

	tracker.mu.Lock()
	ts, ok := tracker.activity["ws-1"]
	tracker.mu.Unlock()

	assert.True(t, ok)
	assert.False(t, ts.IsZero())
	assert.True(t, !ts.Before(before) && !ts.After(after))
}

func TestActivityTracker_RecordEmptyWorkspaceID(t *testing.T) {
	tracker := newTestTracker(k8smocks.NewMockWorkspaceInterface())

	tracker.Record("")

	assert.Equal(t, 0, tracker.PendingCount())
}

func TestActivityTracker_Flush_UpdatesWorkspaceStatus(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Once()

	var captured *v1.Workspace
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(1).(*v1.Workspace)
	}).Return(ws, nil).Once()

	tracker.Record("ws-1")
	tracker.Flush()

	wsMock.AssertExpectations(t)
	require.NotNil(t, captured)
	require.NotNil(t, captured.Status.LastActivityAt)
	assert.WithinDuration(t, time.Now(), captured.Status.LastActivityAt.Time, 2*time.Second)
}

func TestActivityTracker_Flush_SkipsStaleWorkspace(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(ws, nil).Once()

	tracker.Record("ws-1")
	tracker.Flush()
	tracker.Flush()

	wsMock.AssertExpectations(t)
}

func TestActivityTracker_Flush_CoalescesRecords(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Once()

	var captured *v1.Workspace
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(1).(*v1.Workspace)
	}).Return(ws, nil).Once()

	tracker.Record("ws-1")
	time.Sleep(50 * time.Millisecond)
	cutoff := time.Now()
	tracker.Record("ws-1")

	tracker.Flush()

	wsMock.AssertExpectations(t)
	require.NotNil(t, captured)
	require.NotNil(t, captured.Status.LastActivityAt)
	assert.True(t, captured.Status.LastActivityAt.After(cutoff.Add(-1*time.Second)))
	assert.Equal(t, 1, tracker.PendingCount())
}

func TestActivityTracker_Flush_MultipleWorkspaces(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws1 := makeWorkspaceCRD("ws-1", 5)
	ws2 := makeWorkspaceCRD("ws-2", 3)
	ws3 := makeWorkspaceCRD("ws-3", 10)

	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws1, nil).Once()
	wsMock.On("Get", mock.Anything, "ws-2", metav1.GetOptions{}).Return(ws2, nil).Once()
	wsMock.On("Get", mock.Anything, "ws-3", metav1.GetOptions{}).Return(ws3, nil).Once()

	wsMock.On("UpdateStatus", mock.Anything, mock.MatchedBy(func(w *v1.Workspace) bool {
		return w.Name == "ws-1"
	})).Return(ws1, nil).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.MatchedBy(func(w *v1.Workspace) bool {
		return w.Name == "ws-2"
	})).Return(ws2, nil).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.MatchedBy(func(w *v1.Workspace) bool {
		return w.Name == "ws-3"
	})).Return(ws3, nil).Once()

	tracker.Record("ws-1")
	tracker.Record("ws-2")
	tracker.Record("ws-3")
	tracker.Flush()

	wsMock.AssertExpectations(t)
}

func TestActivityTracker_StartBeginsFlushLoop(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	err := tracker.Start()
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(ws, nil).Once()

	tracker.Record("ws-1")

	err = tracker.Stop()
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	wsMock.AssertExpectations(t)
}

func TestActivityTracker_Stop_FinalFlush(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	err := tracker.Start()
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(ws, nil).Once()

	tracker.Record("ws-1")

	err = tracker.Stop()
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	wsMock.AssertExpectations(t)
}

func TestActivityTracker_PendingCount(t *testing.T) {
	tracker := newTestTracker(k8smocks.NewMockWorkspaceInterface())

	assert.Equal(t, 0, tracker.PendingCount())

	tracker.Record("ws-1")
	assert.Equal(t, 1, tracker.PendingCount())

	tracker.Record("ws-2")
	assert.Equal(t, 2, tracker.PendingCount())

	tracker.Record("ws-1")
	assert.Equal(t, 2, tracker.PendingCount())
}

func TestActivityTracker_ConcurrentRecord(t *testing.T) {
	tracker := newTestTracker(k8smocks.NewMockWorkspaceInterface())

	var wg sync.WaitGroup
	const goroutines = 100
	const workspaces = 10
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			tracker.Record(fmt.Sprintf("ws-%d", id%workspaces))
		}(i)
	}

	wg.Wait()

	assert.Equal(t, workspaces, tracker.PendingCount())
}

func TestActivityTracker_ConcurrentRecordAndFlush(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil)
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(ws, nil)

	var wg sync.WaitGroup
	const recorders = 50
	wg.Add(recorders + 1)

	for i := 0; i < recorders; i++ {
		go func() {
			defer wg.Done()
			tracker.Record("ws-1")
		}()
	}

	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		tracker.Flush()
	}()

	wg.Wait()
}

func TestActivityTracker_Flush_RetryOnConflict(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).Return(ws, nil).Twice()

	conflictErr := apierrors.NewConflict(
		schema.GroupResource{Group: "llmsafespace.dev", Resource: "workspaces"},
		"ws-1",
		fmt.Errorf("object has been modified"),
	)
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(nil, conflictErr).Once()
	wsMock.On("UpdateStatus", mock.Anything, mock.Anything).Return(ws, nil).Once()

	tracker.Record("ws-1")
	tracker.Flush()

	wsMock.AssertExpectations(t)
	wsMock.AssertNumberOfCalls(t, "UpdateStatus", 2)
	wsMock.AssertNumberOfCalls(t, "Get", 2)
}

func TestActivityTracker_Flush_GetError(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	wsMock.On("Get", mock.Anything, "ws-missing", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found")).Once()

	tracker.Record("ws-missing")
	tracker.Flush()

	wsMock.AssertExpectations(t)
	wsMock.AssertNotCalled(t, "UpdateStatus")
}

func TestActivityTracker_Flush_Empty(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	tracker.Flush()

	wsMock.AssertNotCalled(t, "Get")
	wsMock.AssertNotCalled(t, "UpdateStatus")
}

func TestActivityTracker_NewActivityTracker(t *testing.T) {
	wsMock := k8smocks.NewMockWorkspaceInterface()
	tracker := newTestTracker(wsMock)

	assert.NotNil(t, tracker)
	assert.Equal(t, "default", tracker.namespace)
	assert.Equal(t, 0, tracker.PendingCount())
}

func TestActivityTracker_Delete_RemovesLastFlushEntry(t *testing.T) {
	tracker := newTestTracker(k8smocks.NewMockWorkspaceInterface())

	tracker.Record("ws-1")
	require.Equal(t, 1, tracker.PendingCount())

	tracker.mu.Lock()
	tracker.lastFlush["ws-1"] = time.Now()
	tracker.mu.Unlock()

	tracker.Delete("ws-1")

	assert.Equal(t, 0, tracker.PendingCount(), "Delete must remove the activity entry")
	tracker.mu.Lock()
	_, inLastFlush := tracker.lastFlush["ws-1"]
	tracker.mu.Unlock()
	assert.False(t, inLastFlush, "Delete must remove the lastFlush entry")
}
