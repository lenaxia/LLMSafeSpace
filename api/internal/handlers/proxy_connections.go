// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *ProxyHandler) getPassword(ctx context.Context, workspaceID string) (string, error) {
	h.pwCacheMu.RLock()
	if pw, ok := h.pwCache[workspaceID]; ok {
		h.pwCacheMu.RUnlock()
		return pw, nil
	}
	h.pwCacheMu.RUnlock()

	secretName := fmt.Sprintf("workspace-pw-%s", workspaceID)
	secret, err := h.k8sClient.Clientset().CoreV1().Secrets(h.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading password secret %s: %w", secretName, err)
	}

	pw := string(secret.Data["password"])
	if pw == "" {
		return "", fmt.Errorf("password secret %s has empty password key", secretName)
	}

	h.pwCacheMu.Lock()
	h.pwCache[workspaceID] = pw
	h.pwCacheMu.Unlock()

	return pw, nil
}

func (h *ProxyHandler) checkAndAddActiveSession(workspaceID, sessionID string, maxSessions int) bool {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()

	if h.activeSess[workspaceID] == nil {
		h.activeSess[workspaceID] = make(map[string]bool)
	}

	if h.activeSess[workspaceID][sessionID] {
		return true
	}

	if len(h.activeSess[workspaceID]) >= maxSessions {
		return false
	}

	h.activeSess[workspaceID][sessionID] = true
	return true
}

func (h *ProxyHandler) removeActiveSession(workspaceID, sessionID string) {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	if sessions, ok := h.activeSess[workspaceID]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(h.activeSess, workspaceID)
		}
	}
}

func (h *ProxyHandler) isSessionActive(workspaceID, sessionID string) bool {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	sessions, ok := h.activeSess[workspaceID]
	if !ok {
		return false
	}
	return sessions[sessionID]
}

func (h *ProxyHandler) activeSessionCount(workspaceID string) int {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	return len(h.activeSess[workspaceID])
}

func (h *ProxyHandler) acquireConnection(workspaceID string) bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[workspaceID] >= maxConnectionsPerWorkspace {
		return false
	}
	h.connCount[workspaceID]++
	return true
}

func (h *ProxyHandler) releaseConnection(workspaceID string) {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[workspaceID] > 0 {
		h.connCount[workspaceID]--
	}
	if h.connCount[workspaceID] == 0 {
		delete(h.connCount, workspaceID)
	}
}

func (h *ProxyHandler) connectionCount(workspaceID string) int {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	return h.connCount[workspaceID]
}

func (h *ProxyHandler) invalidateCaches(workspaceID string) {
	h.pwCacheMu.Lock()
	delete(h.pwCache, workspaceID)
	h.pwCacheMu.Unlock()

	h.wsConfigMu.Lock()
	delete(h.wsConfig, workspaceID)
	h.wsConfigMu.Unlock()

	h.activeMu.Lock()
	delete(h.activeSess, workspaceID)
	h.activeMu.Unlock()

	if h.sessionParents != nil {
		h.sessionParents.invalidate(workspaceID)
	}

	h.parentBackfilledMu.Lock()
	delete(h.parentBackfilled, workspaceID)
	h.parentBackfilledMu.Unlock()
}

func (h *ProxyHandler) GetActiveSessions(workspaceID string) []string {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	sessions := h.activeSess[workspaceID]
	if sessions == nil {
		return nil
	}
	result := make([]string, 0, len(sessions))
	for sid := range sessions {
		result = append(result, sid)
	}
	return result
}

func (h *ProxyHandler) SetActiveSessionsForTest(workspaceID string, sessionIDs []string) {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	m := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		m[id] = true
	}
	h.activeSess[workspaceID] = m
}
