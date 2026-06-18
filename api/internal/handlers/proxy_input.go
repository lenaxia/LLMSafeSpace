// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apitypes "github.com/lenaxia/llmsafespaces/api/internal/types"
	"github.com/lenaxia/llmsafespaces/pkg/agent"
)

var (
	questionIDPattern   = regexp.MustCompile(`^que_[a-zA-Z0-9]+$`)
	permissionIDPattern = regexp.MustCompile(`^per_[a-zA-Z0-9_]+$`)
)

// ListQuestions proxies GET /question to the workspace pod.
func (h *ProxyHandler) ListQuestions(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionListPath(), false, "")
}

// QuestionReply proxies POST /question/:requestID/reply to the workspace pod.
func (h *ProxyHandler) QuestionReply(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !questionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionReplyPath(requestID), false, "")
}

// QuestionReject proxies POST /question/:requestID/reject to the workspace pod.
func (h *ProxyHandler) QuestionReject(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !questionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionRejectPath(requestID), false, "")
}

// ListPermissions proxies GET /permission to the workspace pod.
func (h *ProxyHandler) ListPermissions(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.PermissionListPath(), false, "")
}

// PermissionReply proxies POST /permission/:requestID/reply to the workspace pod.
func (h *ProxyHandler) PermissionReply(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !permissionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid permission request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.PermissionReplyPath(requestID), false, "")
}

// emitPendingInputRequests fetches pending questions and permissions from the pod
// and publishes them as synthetic events so reconnecting browsers see them immediately.
func (h *ProxyHandler) emitPendingInputRequests(workspaceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v1Client, err := h.k8sClient.LlmsafespacesV1()
	if err != nil {
		return
	}
	workspace, err := v1Client.Workspaces(h.namespace).Get(ctx, workspaceID, metav1.GetOptions{})
	if err != nil || workspace.Status.Phase != phaseActive || workspace.Status.PodIP == "" {
		return
	}

	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		return
	}

	podIP := workspace.Status.PodIP

	// Fetch and emit pending questions
	if body, err := h.fetchFromPod(ctx, podIP, password, h.dialect.QuestionListPath()); err == nil {
		for _, req := range h.parseQuestionList(body) {
			if h.sessionParents != nil {
				req.RootSessionID = h.sessionParents.resolveRoot(ctx, workspaceID, req.SessionID)
			} else {
				req.RootSessionID = req.SessionID
			}
			h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{Type: "agent.question", Data: req})
		}
	}

	// Fetch and emit pending permissions (only if not auto-approving)
	if !h.shouldAutoApprovePermissions(workspaceID) {
		if body, err := h.fetchFromPod(ctx, podIP, password, h.dialect.PermissionListPath()); err == nil {
			for _, req := range h.parsePermissionList(body) {
				if h.sessionParents != nil {
					req.RootSessionID = h.sessionParents.resolveRoot(ctx, workspaceID, req.SessionID)
				} else {
					req.RootSessionID = req.SessionID
				}
				h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{Type: "agent.permission", Data: req})
			}
		}
	}
}

// fetchFromPod makes a GET request to the workspace pod.
func (h *ProxyHandler) fetchFromPod(ctx context.Context, podIP, password, path string) ([]byte, error) {
	url := "http://" + podIP + ":" + "4096" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", password)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Warn("Failed to fetch pending input requests", "error", err, "path", path)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// parseQuestionList parses the response from GET /question into normalized requests.
func (h *ProxyHandler) parseQuestionList(body []byte) []*agent.QuestionRequest {
	var raw []json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	var results []*agent.QuestionRequest
	for _, r := range raw {
		if req, err := h.dialect.ParseQuestionRequest("question.asked", r); err == nil {
			results = append(results, req)
		}
	}
	return results
}

// parsePermissionList parses the response from GET /permission into normalized requests.
func (h *ProxyHandler) parsePermissionList(body []byte) []*agent.PermissionRequest {
	var raw []json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	var results []*agent.PermissionRequest
	for _, r := range raw {
		if req, err := h.dialect.ParsePermissionRequest("permission.asked", r); err == nil {
			results = append(results, req)
		}
	}
	return results
}
