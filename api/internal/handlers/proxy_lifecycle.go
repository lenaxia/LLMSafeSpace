// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"fmt"
)

func (h *ProxyHandler) EnableSessionParentResolution() {
	if h.sessionParents != nil {
		return
	}
	h.sessionParents = newSessionParentCache(h.fetchSessionParent)
}

func (h *ProxyHandler) Start() error {
	var startErr error
	h.startOnce.Do(func() {
		h.broker = NewWorkspaceEventBroker()
		h.userBroker = NewUserEventBroker()

		h.activityTracker = NewActivityTracker(h.k8sClient, h.logger, h.namespace)
		if err := h.activityTracker.Start(); err != nil {
			startErr = fmt.Errorf("starting activity tracker: %w", err)
			return
		}

		h.sseTracker = NewSSETracker(h.httpClient, h.logger, h.onSessionIdle)
		h.sseTracker.SetPasswordGetter(h.getPassword)
		h.sseTracker.SetPodIPResolver(h.getPodIPForSSE)
		h.sseTracker.SetOnSessionActive(h.onSessionActive)
		h.sseTracker.SetOnRawEvent(h.onRawEvent)

		watcher, err := NewWorkspaceWatcher(h.k8sClient, h.logger, h.namespace, h.onPhaseChange)
		if err != nil {
			_ = h.activityTracker.Stop()
			startErr = fmt.Errorf("creating CRD watcher: %w", err)
			return
		}
		watcher.SetUserBroker(h.userBroker)
		if err := watcher.Start(); err != nil {
			_ = h.activityTracker.Stop()
			startErr = fmt.Errorf("starting CRD watcher: %w", err)
			return
		}
		h.watcher = watcher

		if h.sseTracker != nil {
			for wsName, phase := range watcher.GetAllKnownPhases() {
				if phase == string(phaseActive) {
					h.sseTracker.EnsureWatching(wsName)
				}
			}
		}
	})
	return startErr
}

func (h *ProxyHandler) Stop() error {
	h.stopOnce.Do(func() {
		if h.sseTracker != nil {
			h.sseTracker.Stop()
		}
		if h.watcher != nil {
			h.watcher.Stop()
		}
		if h.activityTracker != nil {
			_ = h.activityTracker.Stop()
		}
	})
	return nil
}

func (h *ProxyHandler) GetSSETracker() *SSETracker {
	return h.sseTracker
}

func (h *ProxyHandler) GetPasswordGetter() func(ctx context.Context, workspaceID string) (string, error) {
	return h.getPassword
}

func (h *ProxyHandler) SetAgentStateChecker(c AgentStateChecker) {
	h.agentStateChecker = c
}
