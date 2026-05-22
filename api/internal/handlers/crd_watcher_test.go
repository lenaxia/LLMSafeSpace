package handlers

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil)

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
	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil)

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

	sbMock.On("Watch", metav1.ListOptions{}).Return(nil, fmt.Errorf("transient watch error")).Once()
	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil).Once()

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

	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch2, nil).Once()

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

	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch1, nil).Once()
	sbMock.On("Watch", metav1.ListOptions{}).Return(mockWatch2, nil).Once()

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
