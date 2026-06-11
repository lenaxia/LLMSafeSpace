// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type PhaseChangeCallback func(workspace *v1.Workspace)

// Watch tuning. The apiserver enforces a max watch lifetime of about 60 minutes
// (default). We pick a shorter explicit timeout so reconnects happen at
// predictable intervals; bookmarks keep us in sync with resourceVersion in the
// meantime so reconnects are O(1).
const (
	watchTimeoutSeconds    = 290 // ~5 min, leaves slack under apiserver's 5-10m default
	watchBackoffInitial    = 2 * time.Second
	watchBackoffMax        = 30 * time.Second
	watchBackoffMultiplier = 2
)

type WorkspaceWatcher struct {
	k8sClient            pkginterfaces.KubernetesClient
	logger               pkginterfaces.LoggerInterface
	namespace            string
	onPhaseChange        PhaseChangeCallback
	userBroker           *UserEventBroker
	stopCh               chan struct{}
	stopOnce             sync.Once
	knownPhases          map[string]string
	knownPhasesMu        sync.RWMutex
	watchRestartMu       sync.Mutex
	lastResourceVersion  string
	lastResourceVersionM sync.Mutex
}

func NewWorkspaceWatcher(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
	onPhaseChange PhaseChangeCallback,
) (*WorkspaceWatcher, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client cannot be nil")
	}
	if onPhaseChange == nil {
		return nil, fmt.Errorf("phase change callback cannot be nil")
	}
	return &WorkspaceWatcher{
		k8sClient:     k8sClient,
		logger:        logger,
		namespace:     namespace,
		onPhaseChange: onPhaseChange,
		stopCh:        make(chan struct{}),
		knownPhases:   make(map[string]string),
	}, nil
}

// SetUserBroker sets the user event broker for ownership tracking.
// Must be called before Start().
func (w *WorkspaceWatcher) SetUserBroker(broker *UserEventBroker) {
	w.userBroker = broker
}

func (w *WorkspaceWatcher) Start() error {
	go w.runWatchLoop()
	return nil
}

func (w *WorkspaceWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

func (w *WorkspaceWatcher) GetKnownPhase(name string) (string, bool) {
	w.knownPhasesMu.RLock()
	defer w.knownPhasesMu.RUnlock()
	phase, ok := w.knownPhases[name]
	return phase, ok
}

// GetAllKnownPhases returns a copy of the full known phases map.
// One RLock acquisition for the entire read — O(N) with a single lock (G8).
func (w *WorkspaceWatcher) GetAllKnownPhases() map[string]string {
	w.knownPhasesMu.RLock()
	defer w.knownPhasesMu.RUnlock()
	result := make(map[string]string, len(w.knownPhases))
	for k, v := range w.knownPhases {
		result[k] = v
	}
	return result
}

// runWatchLoop drives the Watch lifecycle: List once to seed
// lastResourceVersion, then loop calling watchOnce() and reconnecting on clean
// close or error. Backoff is exponential on error and immediate on clean close
// (apiserver-driven cycling is the common case and not an error).
func (w *WorkspaceWatcher) runWatchLoop() {
	if err := w.seedResourceVersion(); err != nil {
		w.logger.Warn("Initial List for workspace watcher failed; will rely on Watch alone",
			"error", err.Error())
	}

	backoff := watchBackoffInitial
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		cleanClose, err := w.watchOnce()
		if err != nil {
			w.logger.Warn("Workspace watch error; will retry",
				"error", err.Error(),
				"backoff", backoff.String())
			if !sleepCancellable(w.stopCh, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		// Clean close. apiserver cycles long-lived watches roughly every
		// 5–10 minutes; this is normal. Reconnect immediately and reset
		// backoff. Log at debug so it doesn't clutter normal operation.
		if cleanClose {
			w.logger.Debug("Workspace watch closed cleanly, reconnecting")
			backoff = watchBackoffInitial
		}
	}
}

// seedResourceVersion does an initial List to populate lastResourceVersion and
// knownPhases so the first Watch starts from a known position. Also records
// workspace ownership in the user broker for snapshot delivery (FM3, FM7).
func (w *WorkspaceWatcher) seedResourceVersion() error {
	list, err := w.k8sClient.LlmsafespaceV1().Workspaces(w.namespace).List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	w.setResourceVersion(list.ResourceVersion)

	w.knownPhasesMu.Lock()
	for i := range list.Items {
		ws := &list.Items[i]
		phase := string(ws.Status.Phase)
		if phase != "" {
			w.knownPhases[ws.Name] = phase
		}
		if w.userBroker != nil && ws.Spec.Owner.UserID != "" {
			w.userBroker.RecordWorkspaceOwner(ws.Name, ws.Spec.Owner.UserID)
		}
	}
	w.knownPhasesMu.Unlock()

	return nil
}

// watchOnce runs a single Watch session until it ends. Returns (cleanClose,
// error): cleanClose==true means the channel closed without observing an
// error event; error!=nil means the Watch couldn't start or an apiserver
// error event was observed.
func (w *WorkspaceWatcher) watchOnce() (bool, error) {
	w.watchRestartMu.Lock()
	defer w.watchRestartMu.Unlock()

	timeoutSeconds := int64(watchTimeoutSeconds)
	allowBookmarks := true
	opts := metav1.ListOptions{
		ResourceVersion:     w.getResourceVersion(),
		TimeoutSeconds:      &timeoutSeconds,
		AllowWatchBookmarks: allowBookmarks,
	}

	startedAt := time.Now()
	watcher, err := w.k8sClient.LlmsafespaceV1().Workspaces(w.namespace).Watch(opts)
	if err != nil {
		return false, fmt.Errorf("starting workspace watch: %w", err)
	}
	defer watcher.Stop()

	eventCount := 0
	for {
		select {
		case <-w.stopCh:
			return true, nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				w.logger.Debug("Workspace watch channel closed",
					"livedFor", time.Since(startedAt).String(),
					"eventCount", eventCount,
					"resourceVersion", w.getResourceVersion())
				return true, nil
			}
			eventCount++

			if event.Type == watch.Error {
				// apiserver returned an error event (often Status with code
				// 410 Gone — resource version too old). Drop the cached
				// version so the next Watch re-Lists from current state.
				status, _ := event.Object.(*metav1.Status)
				w.handleWatchError(status)
				if status != nil && status.Code == 410 {
					w.setResourceVersion("")
				}
				return false, fmt.Errorf("watch error event: %s", statusMessage(status))
			}

			w.handleEvent(event)
		}
	}
}

// handleEvent updates phase tracking. Bookmark events carry only
// resourceVersion; we record it and otherwise skip them.
func (w *WorkspaceWatcher) handleEvent(event watch.Event) {
	if event.Type == watch.Bookmark {
		if obj, ok := event.Object.(*v1.Workspace); ok && obj.ResourceVersion != "" {
			w.setResourceVersion(obj.ResourceVersion)
		}
		return
	}

	workspace, ok := event.Object.(*v1.Workspace)
	if !ok {
		return
	}

	if workspace.ResourceVersion != "" {
		w.setResourceVersion(workspace.ResourceVersion)
	}

	name := workspace.Name

	// C5: handle deletion — remove from knownPhases and clean up broker ownership
	if event.Type == watch.Deleted {
		w.knownPhasesMu.Lock()
		delete(w.knownPhases, name)
		w.knownPhasesMu.Unlock()
		if w.userBroker != nil {
			w.userBroker.CleanupWorkspace(name)
		}
		return
	}

	newPhase := string(workspace.Status.Phase)

	// FM7: record workspace ownership on every event
	if w.userBroker != nil && workspace.Spec.Owner.UserID != "" {
		w.userBroker.RecordWorkspaceOwner(name, workspace.Spec.Owner.UserID)
	}

	w.knownPhasesMu.Lock()
	oldPhase, existed := w.knownPhases[name]
	w.knownPhases[name] = newPhase
	w.knownPhasesMu.Unlock()

	if existed && oldPhase != newPhase {
		metrics.RecordWorkspacePhaseTransition(oldPhase, newPhase)
		w.onPhaseChange(workspace)
	}
}

func (w *WorkspaceWatcher) handleWatchError(status *metav1.Status) {
	if status == nil {
		w.logger.Warn("Workspace watch returned error event with nil status")
		return
	}
	w.logger.Warn("Workspace watch returned error event",
		"reason", string(status.Reason),
		"message", status.Message,
		"code", status.Code)
}

func (w *WorkspaceWatcher) getResourceVersion() string {
	w.lastResourceVersionM.Lock()
	defer w.lastResourceVersionM.Unlock()
	return w.lastResourceVersion
}

func (w *WorkspaceWatcher) setResourceVersion(rv string) {
	w.lastResourceVersionM.Lock()
	defer w.lastResourceVersionM.Unlock()
	w.lastResourceVersion = rv
}

func statusMessage(s *metav1.Status) string {
	if s == nil {
		return "<nil status>"
	}
	return s.Message
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * watchBackoffMultiplier
	if next > watchBackoffMax {
		return watchBackoffMax
	}
	return next
}

// sleepCancellable sleeps for d or until stopCh closes. Returns true if the
// full duration elapsed, false if stopCh closed first.
func sleepCancellable(stopCh <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-stopCh:
		return false
	case <-timer.C:
		return true
	}
}
