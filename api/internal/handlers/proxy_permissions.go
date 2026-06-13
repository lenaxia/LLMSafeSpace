// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func (h *ProxyHandler) shouldAutoApprovePermissions(workspaceID string) bool {
	h.wsConfigMu.RLock()
	if cfg, ok := h.wsConfig[workspaceID]; ok {
		h.wsConfigMu.RUnlock()
		return cfg.autoApprovePermissions
	}
	h.wsConfigMu.RUnlock()

	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return false
	}
	workspace, err := v1Client.Workspaces(h.namespace).Get(context.Background(), workspaceID, metav1.GetOptions{})
	if err != nil {
		return false
	}

	h.wsConfigMu.Lock()
	cfg := h.wsConfig[workspaceID]
	cfg.autoApprovePermissions = workspace.Spec.AutoApprovePermissions
	cfg.maxActiveSessions = int(workspace.Spec.MaxActiveSessions)
	h.wsConfig[workspaceID] = cfg
	h.wsConfigMu.Unlock()

	return workspace.Spec.AutoApprovePermissions
}

func (h *ProxyHandler) autoApprovePermission(workspaceID, requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	v1Client, v1Err := h.k8sClient.LlmsafespaceV1()
	workspace, err := func() (*v1.Workspace, error) {
		if v1Err != nil {
			return nil, v1Err
		}
		return v1Client.Workspaces(h.namespace).Get(ctx, workspaceID, metav1.GetOptions{})
	}()
	if err != nil || workspace.Status.PodIP == "" {
		h.logger.Warn("Cannot auto-approve permission: workspace not reachable",
			"workspaceID", workspaceID, "requestID", requestID)
		return
	}

	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		h.logger.Warn("Cannot auto-approve permission: password unavailable",
			"workspaceID", workspaceID, "requestID", requestID)
		return
	}

	targetPath := h.dialect.PermissionReplyPath(requestID)
	targetURL := fmt.Sprintf("http://%s:%d%s", workspace.Status.PodIP, opencodePort, targetPath)

	body := []byte(`{"reply":"always"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.SetBasicAuth("opencode", password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Warn("Auto-approve permission failed", "error", err,
			"workspaceID", workspaceID, "requestID", requestID)
		return
	}
	_ = resp.Body.Close()

	h.logger.Info("Auto-approved permission",
		"workspaceID", workspaceID, "requestID", requestID)
}
