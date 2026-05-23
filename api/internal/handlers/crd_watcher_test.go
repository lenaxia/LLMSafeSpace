package handlers

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

func setupWatcherMocks(t *testing.T) (*k8smocks.MockKubernetesClient, *k8smocks.MockLLMSafespaceV1Interface, *k8smocks.MockSandboxInterface) {
	t.Helper()
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	sbMock := k8smocks.NewMockSandboxInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Sandboxes", "default").Return(sbMock)
	// SandboxWatcher.Start does an initial List to seed resourceVersion.
	// Tests that don't care about it can use this default; tests that need
	// to assert the initial RV can override with their own .On("List", ...).
	sbMock.On("List", mock.Anything).
		Return(&v1.SandboxList{ListMeta: metav1.ListMeta{ResourceVersion: "100"}}, nil).Maybe()
	return k8sMock, llmMock, sbMock
}

func TestNewSandboxWatcher_Validation(t *testing.T) {
	tests := []struct {
		name      string
		k8sClient pkginterfaces.KubernetesClient
		callback  PhaseChangeCallback
		expectErr string
	}{
		{
			name:      "nil kubernetes client",
			k8sClient: nil,
			callback:  func(s *v1.Sandbox) {},
			expectErr: "kubernetes client cannot be nil",
		},
		{
			name:      "nil phase change callback",
			k8sClient: k8smocks.NewMockKubernetesClient(),
			callback:  nil,
			expectErr: "phase change callback cannot be nil",
		},
		{
			name:      "valid arguments",
			k8sClient: k8smocks.NewMockKubernetesClient(),
			callback:  func(s *v1.Sandbox) {},
			expectErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := NewSandboxWatcher(tt.k8sClient, &testLogger{}, "default", tt.callback)
			if tt.expectErr != "" {
				assert.EqualError(t, err, tt.expectErr)
				assert.Nil(t, w)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, w)
			}
		})
	}
}

func TestSandboxWatcher_InitialAddRecordsPhase(t *testing.T) {
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	sb := makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")
	w.handleEvent(watch.Event{Type: watch.Added, Object: sb})

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok)
	assert.Equal(t, "Running", phase)
}

func TestSandboxWatcher_InitialModifiedRecordsPhase(t *testing.T) {
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	sb := makeSandboxCRD("sb-1", "10.0.0.1", "Pending", "ws-1")
	w.handleEvent(watch.Event{Type: watch.Modified, Object: sb})

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok)
	assert.Equal(t, "Pending", phase)
}

func TestSandboxWatcher_PhaseChangeFiresCallback(t *testing.T) {
	var mu sync.Mutex
	var captured *v1.Sandbox
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		captured = s
	}
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Suspended", "ws-1")})

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, captured)
	assert.Equal(t, "sb-1", captured.Name)
	assert.Equal(t, "Suspended", captured.Status.Phase)
}

func TestSandboxWatcher_SamePhaseNoCallback(t *testing.T) {
	var mu sync.Mutex
	var callCount int
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
	}
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 0, callCount, "callback should not fire when phase is unchanged")
}

func TestSandboxWatcher_MultiplePhaseChanges(t *testing.T) {
	var mu sync.Mutex
	var phases []string
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, s.Status.Phase)
	}
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-1", "", "Pending", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Suspending", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Suspended", "ws-1")})

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Running", "Suspending", "Suspended"}, phases)
}

func TestSandboxWatcher_NonSandboxObjectIgnored(t *testing.T) {
	var called bool
	cb := func(s *v1.Sandbox) { called = true }
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
	}
	w.handleEvent(watch.Event{Type: watch.Added, Object: ws})

	assert.False(t, called, "callback should not fire for non-Sandbox objects")
	_, ok := w.GetKnownPhase("ws-1")
	assert.False(t, ok, "non-Sandbox objects should not be tracked")
}

func TestSandboxWatcher_MultipleSandboxesIndependent(t *testing.T) {
	var mu sync.Mutex
	var changed []*v1.Sandbox
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, s)
	}
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-2", "10.0.0.2", "Pending", "ws-2")})
	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-3", "10.0.0.3", "Creating", "ws-3")})

	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Suspended", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-2", "10.0.0.2", "Running", "ws-2")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-3", "10.0.0.3", "Running", "ws-3")})

	phase1, ok1 := w.GetKnownPhase("sb-1")
	assert.True(t, ok1)
	assert.Equal(t, "Suspended", phase1)

	phase2, ok2 := w.GetKnownPhase("sb-2")
	assert.True(t, ok2)
	assert.Equal(t, "Running", phase2)

	phase3, ok3 := w.GetKnownPhase("sb-3")
	assert.True(t, ok3)
	assert.Equal(t, "Running", phase3)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, changed, 3)
	assert.Equal(t, "sb-1", changed[0].Name)
	assert.Equal(t, "Suspended", changed[0].Status.Phase)
	assert.Equal(t, "sb-2", changed[1].Name)
	assert.Equal(t, "Running", changed[1].Status.Phase)
	assert.Equal(t, "sb-3", changed[2].Name)
	assert.Equal(t, "Running", changed[2].Status.Phase)
}

func TestSandboxWatcher_OneSandboxSamePhaseDoesNotAffectOthers(t *testing.T) {
	var mu sync.Mutex
	var changed []*v1.Sandbox
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, s)
	}
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", cb)
	require.NoError(t, err)

	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Added, Object: makeSandboxCRD("sb-2", "10.0.0.2", "Running", "ws-2")})

	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")})
	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-2", "10.0.0.2", "Suspended", "ws-2")})

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, changed, 1)
	assert.Equal(t, "sb-2", changed[0].Name)
	assert.Equal(t, "Suspended", changed[0].Status.Phase)
}

func TestSandboxWatcher_StopClosesChannel(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)
	mockWatch := k8smocks.NewMockWatch()
	mockWatch.On("Stop").Return()
	sbMock.On("Watch", mock.Anything).Return(mockWatch, nil)

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	time.Sleep(100 * time.Millisecond)

	w.Stop()
	time.Sleep(100 * time.Millisecond)

	mockWatch.AssertCalled(t, "Stop")
}

func TestSandboxWatcher_StartRunsWatchLoop(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)
	mockWatch := k8smocks.NewMockWatch()
	mockWatch.On("Stop").Return()
	sbMock.On("Watch", mock.Anything).Return(mockWatch, nil)

	var mu sync.Mutex
	var captured *v1.Sandbox
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		captured = s
	}

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", cb)
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	mockWatch.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	time.Sleep(100 * time.Millisecond)

	mockWatch.SendEvent(watch.Modified, makeSandboxCRD("sb-1", "10.0.0.1", "Suspended", "ws-1"))
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, captured)
	assert.Equal(t, "Suspended", captured.Status.Phase)

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok)
	assert.Equal(t, "Suspended", phase)
}

func TestSandboxWatcher_RestartsOnWatchError(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	mockWatch := k8smocks.NewMockWatch()
	mockWatch.On("Stop").Return()

	sbMock.On("Watch", mock.Anything).Return(nil, fmt.Errorf("transient watch error")).Once()
	sbMock.On("Watch", mock.Anything).Return(mockWatch, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(3 * time.Second)

	mockWatch.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	time.Sleep(100 * time.Millisecond)

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok, "watcher should recover and track phases after restart")
	assert.Equal(t, "Running", phase)
}

func TestSandboxWatcher_RestartsOnChannelClose(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()

	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	sbMock.On("Watch", mock.Anything).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.Anything).Return(mockWatch2, nil).Once()

	var mu sync.Mutex
	var captured *v1.Sandbox
	cb := func(s *v1.Sandbox) {
		mu.Lock()
		defer mu.Unlock()
		captured = s
	}

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", cb)
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	mockWatch1.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	time.Sleep(100 * time.Millisecond)

	mockWatch1.Stop()

	time.Sleep(3 * time.Second)

	mockWatch2.SendEvent(watch.Modified, makeSandboxCRD("sb-1", "10.0.0.1", "Suspended", "ws-1"))
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, captured, "callback should fire after watch channel close triggers restart")
	assert.Equal(t, "Suspended", captured.Status.Phase)
}

func TestSandboxWatcher_RestartPreservesKnownPhases(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()

	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	sbMock.On("Watch", mock.Anything).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.Anything).Return(mockWatch2, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	mockWatch1.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	mockWatch1.SendEvent(watch.Added, makeSandboxCRD("sb-2", "10.0.0.2", "Pending", "ws-2"))
	time.Sleep(200 * time.Millisecond)

	mockWatch1.Stop()

	time.Sleep(3 * time.Second)

	mockWatch2.SendEvent(watch.Modified, makeSandboxCRD("sb-2", "10.0.0.2", "Running", "ws-2"))
	time.Sleep(200 * time.Millisecond)

	phase1, ok1 := w.GetKnownPhase("sb-1")
	assert.True(t, ok1)
	assert.Equal(t, "Running", phase1, "phases from first watch should persist across restart")

	phase2, ok2 := w.GetKnownPhase("sb-2")
	assert.True(t, ok2)
	assert.Equal(t, "Running", phase2)
}

func TestSandboxWatcher_GetKnownPhase_NotFound(t *testing.T) {
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	phase, ok := w.GetKnownPhase("nonexistent")
	assert.False(t, ok)
	assert.Equal(t, "", phase)
}

func TestSandboxWatcher_EmptyPhaseStillTracked(t *testing.T) {
	w, err := NewSandboxWatcher(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	sb := makeSandboxCRD("sb-1", "", "", "")
	w.handleEvent(watch.Event{Type: watch.Added, Object: sb})

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok)
	assert.Equal(t, "", phase)

	w.handleEvent(watch.Event{Type: watch.Modified, Object: makeSandboxCRD("sb-1", "10.0.0.1", "Running", "")})

	phase, ok = w.GetKnownPhase("sb-1")
	assert.True(t, ok)
	assert.Equal(t, "Running", phase)
}

// ============================================================================
// New tests for the resourceVersion + bookmark + error-event contract.
// These tests lock in behavior that does not exist in the legacy watcher
// (no RV threading, no bookmarks, no error event handling).
// ============================================================================

// TestSandboxWatcher_InitialListSeedsResourceVersion verifies that Start()
// performs an initial List and uses the returned resourceVersion as the
// starting point for the first Watch call. This avoids replaying every
// existing sandbox as an Added event on every restart.
func TestSandboxWatcher_InitialListSeedsResourceVersion(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	// Override the default List from setupWatcherMocks with a specific RV.
	sbMock.ExpectedCalls = nil
	sbMock.On("List", mock.Anything).
		Return(&v1.SandboxList{ListMeta: metav1.ListMeta{ResourceVersion: "12345"}}, nil)

	mockWatch := k8smocks.NewMockWatch()
	mockWatch.On("Stop").Return()

	var optsMu sync.Mutex
	var capturedOpts metav1.ListOptions
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		capturedOpts = opts
		optsMu.Unlock()
		return true
	})).Return(mockWatch, nil)

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	// Give the loop time to call List and start the first Watch.
	time.Sleep(200 * time.Millisecond)

	optsMu.Lock()
	defer optsMu.Unlock()
	assert.Equal(t, "12345", capturedOpts.ResourceVersion,
		"Watch must use resourceVersion from the initial List")
	require.NotNil(t, capturedOpts.TimeoutSeconds, "TimeoutSeconds must be set")
	assert.True(t, *capturedOpts.TimeoutSeconds > 0, "TimeoutSeconds must be positive")
	assert.True(t, capturedOpts.AllowWatchBookmarks,
		"AllowWatchBookmarks must be true so bookmarks deliver RV updates")
}

// TestSandboxWatcher_FailedInitialListStillStarts verifies that the watcher
// degrades gracefully when the initial List fails: Watch is still attempted
// (with empty RV — apiserver will deliver initial Added events), and
// subsequent normal operation works.
func TestSandboxWatcher_FailedInitialListStillStarts(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)
	sbMock.ExpectedCalls = nil
	sbMock.On("List", mock.Anything).
		Return((*v1.SandboxList)(nil), fmt.Errorf("apiserver unavailable"))

	mockWatch := k8smocks.NewMockWatch()
	mockWatch.On("Stop").Return()

	var optsMu sync.Mutex
	var capturedOpts metav1.ListOptions
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		capturedOpts = opts
		optsMu.Unlock()
		return true
	})).Return(mockWatch, nil)

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	optsMu.Lock()
	rv := capturedOpts.ResourceVersion
	optsMu.Unlock()
	assert.Equal(t, "", rv,
		"Failed List should leave RV empty so Watch starts from scratch")

	mockWatch.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	time.Sleep(100 * time.Millisecond)

	phase, ok := w.GetKnownPhase("sb-1")
	assert.True(t, ok, "watcher should still process events after failed initial List")
	assert.Equal(t, "Running", phase)
}

// TestSandboxWatcher_BookmarkEventUpdatesRVWithoutCallback verifies that a
// Bookmark event updates the cached resourceVersion but does NOT fire the
// phase-change callback (bookmarks have no phase data).
func TestSandboxWatcher_BookmarkEventUpdatesRVWithoutCallback(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()
	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	var watchOpts []metav1.ListOptions
	var optsMu sync.Mutex
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch2, nil).Once()

	var callbackCount int
	var cbMu sync.Mutex
	cb := func(s *v1.Sandbox) {
		cbMu.Lock()
		callbackCount++
		cbMu.Unlock()
	}

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", cb)
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	// Send a Bookmark event with RV=999. This should update the cached RV
	// without invoking the phase-change callback.
	bookmarkObj := &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "999"},
	}
	mockWatch1.SendEvent(watch.Bookmark, bookmarkObj)
	time.Sleep(100 * time.Millisecond)

	// Force a reconnect by closing watch1's channel.
	mockWatch1.Stop()
	time.Sleep(300 * time.Millisecond)

	cbMu.Lock()
	assert.Equal(t, 0, callbackCount, "Bookmark events must NOT fire the phase-change callback")
	cbMu.Unlock()

	optsMu.Lock()
	require.GreaterOrEqual(t, len(watchOpts), 2, "should have made at least 2 Watch calls")
	assert.Equal(t, "999", watchOpts[1].ResourceVersion,
		"second Watch must resume from the bookmarked resourceVersion")
	optsMu.Unlock()
}

// TestSandboxWatcher_410ErrorResetsResourceVersion verifies that a 410 Gone
// error event causes the cached resourceVersion to be cleared, so the next
// Watch starts fresh (otherwise it would loop forever on the stale RV).
func TestSandboxWatcher_410ErrorResetsResourceVersion(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	// Seed an initial RV via List.
	sbMock.ExpectedCalls = nil
	sbMock.On("List", mock.Anything).
		Return(&v1.SandboxList{ListMeta: metav1.ListMeta{ResourceVersion: "100"}}, nil)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()
	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	var watchOpts []metav1.ListOptions
	var optsMu sync.Mutex
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch2, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	// Send a 410 Gone error event.
	mockWatch1.SendEvent(watch.Error, &metav1.Status{
		Status:  metav1.StatusFailure,
		Reason:  metav1.StatusReasonGone,
		Code:    410,
		Message: "too old resource version: 100 (current: 5000)",
	})

	// Wait for backoff + reconnect (2s + slack).
	time.Sleep(3 * time.Second)

	optsMu.Lock()
	defer optsMu.Unlock()
	require.GreaterOrEqual(t, len(watchOpts), 2)
	assert.Equal(t, "100", watchOpts[0].ResourceVersion,
		"first Watch should use seeded RV")
	assert.Equal(t, "", watchOpts[1].ResourceVersion,
		"410 Gone must reset RV to empty so next Watch re-syncs from current state")
}

// TestSandboxWatcher_NonGoneErrorPreservesRV verifies that a non-410 error
// (e.g. transient apiserver issue) does NOT clobber the cached RV — we want
// to resume from where we were, not replay from scratch.
func TestSandboxWatcher_NonGoneErrorPreservesRV(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	sbMock.ExpectedCalls = nil
	sbMock.On("List", mock.Anything).
		Return(&v1.SandboxList{ListMeta: metav1.ListMeta{ResourceVersion: "200"}}, nil)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()
	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	var watchOpts []metav1.ListOptions
	var optsMu sync.Mutex
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch2, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	// Send a 500 InternalError — transient, RV should be preserved.
	mockWatch1.SendEvent(watch.Error, &metav1.Status{
		Status:  metav1.StatusFailure,
		Reason:  metav1.StatusReasonInternalError,
		Code:    500,
		Message: "etcd hiccup",
	})

	time.Sleep(3 * time.Second)

	optsMu.Lock()
	defer optsMu.Unlock()
	require.GreaterOrEqual(t, len(watchOpts), 2)
	assert.Equal(t, "200", watchOpts[1].ResourceVersion,
		"non-410 error must preserve cached RV so Watch resumes from where it was")
}

// TestSandboxWatcher_NormalEventAdvancesRV verifies that normal Added/Modified
// events advance the cached resourceVersion so a subsequent reconnect resumes
// from the latest known position rather than the original List RV.
func TestSandboxWatcher_NormalEventAdvancesRV(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	sbMock.ExpectedCalls = nil
	sbMock.On("List", mock.Anything).
		Return(&v1.SandboxList{ListMeta: metav1.ListMeta{ResourceVersion: "100"}}, nil)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()
	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	var watchOpts []metav1.ListOptions
	var optsMu sync.Mutex
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		optsMu.Lock()
		watchOpts = append(watchOpts, opts)
		optsMu.Unlock()
		return true
	})).Return(mockWatch2, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)

	// Build a sandbox CRD with explicit RV=500 to simulate apiserver delivery.
	sb := makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1")
	sb.ResourceVersion = "500"
	mockWatch1.SendEvent(watch.Added, sb)
	time.Sleep(100 * time.Millisecond)

	// Trigger reconnect via clean close.
	mockWatch1.Stop()
	time.Sleep(300 * time.Millisecond)

	optsMu.Lock()
	defer optsMu.Unlock()
	require.GreaterOrEqual(t, len(watchOpts), 2)
	assert.Equal(t, "500", watchOpts[1].ResourceVersion,
		"second Watch must resume from the highest RV observed in events")
}

// TestSandboxWatcher_CleanCloseImmediateReconnect verifies that a clean
// channel close (apiserver cycling the watch — the normal case after ~5min)
// triggers an immediate reconnect with no backoff, so users don't see gaps
// in phase tracking.
func TestSandboxWatcher_CleanCloseImmediateReconnect(t *testing.T) {
	k8sMock, _, sbMock := setupWatcherMocks(t)

	mockWatch1 := k8smocks.NewMockWatch()
	mockWatch1.On("Stop").Return()
	mockWatch2 := k8smocks.NewMockWatch()
	mockWatch2.On("Stop").Return()

	sbMock.On("Watch", mock.Anything).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", mock.Anything).Return(mockWatch2, nil).Once()

	w, err := NewSandboxWatcher(k8sMock, &testLogger{}, "default", func(s *v1.Sandbox) {})
	require.NoError(t, err)

	require.NoError(t, w.Start())
	defer w.Stop()

	time.Sleep(200 * time.Millisecond)
	closedAt := time.Now()
	mockWatch1.Stop()

	// Poll for reconnect by sending events until one is processed. The
	// MockWatch buffered channel will accept events even before the loop
	// has reconnected, but the watcher will only see them once it's
	// consuming from mockWatch2. A clean close must reconnect well under
	// the 2s error backoff (target: <500ms).
	mockWatch2.SendEvent(watch.Added, makeSandboxCRD("sb-1", "10.0.0.1", "Running", "ws-1"))
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := w.GetKnownPhase("sb-1"); ok {
			elapsed := time.Since(closedAt)
			assert.Less(t, elapsed, 1*time.Second,
				"reconnect after clean close should be much faster than error backoff")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher did not reconnect quickly after clean close")
}
