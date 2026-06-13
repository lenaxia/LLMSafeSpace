// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func (h *ProxyHandler) CreateSession(c *gin.Context) {
	h.proxyToWorkspace(c, "/session", false, "")
}

func (h *ProxyHandler) ListSessions(c *gin.Context) {
	h.proxyToWorkspace(c, "/session", false, "")
}

func (h *ProxyHandler) SendMessage(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	wid := c.Param("id")
	h.proxyToWorkspace(c, "/session/"+sid+"/message", true, sid)

	status := c.Writer.Status()
	if status < 300 && h.sessionIndex != nil {
		go h.fetchAndPersistTitle(wid, sid)
	}

	if status >= 400 && h.agentStateChecker != nil {
		changedAt, checkerErr := h.agentStateChecker.GetLastCredentialChangedAt(c.Request.Context(), wid)
		if checkerErr == nil && !changedAt.IsZero() {
			h.logger.Info("Proxied message failed with staged credentials — client should call agent/reload",
				"workspaceID", wid, "credentialsPendingSince", changedAt.Format("2006-01-02T15:04:05Z"))
		}
	}
}

func (h *ProxyHandler) SendPromptAsync(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	wid := c.Param("id")
	if h.isSessionActive(wid, sid) {
		c.Header("Retry-After", "1")
		c.JSON(http.StatusConflict, gin.H{
			"error":      "session is busy; retry after idle",
			"retryAfter": 1,
		})
		return
	}
	h.proxyToWorkspace(c, "/session/"+sid+"/prompt_async", true, sid)
}

func (h *ProxyHandler) GetHistory(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	h.proxyToWorkspace(c, "/session/"+sid+"/message", false, sid)
}

func (h *ProxyHandler) GetSession(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	h.proxyToWorkspace(c, "/session/"+sid, false, sid)
}

func (h *ProxyHandler) AbortSession(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	h.proxyToWorkspace(c, "/session/"+sid+"/abort", false, sid)
}

func (h *ProxyHandler) DeleteSession(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	workspaceID := c.Param("id")
	h.proxyToWorkspace(c, "/session/"+sid, false, sid)

	if c.Writer.Status() >= 400 {
		return
	}

	if h.sessionIndex != nil {
		if err := h.sessionIndex.DeleteSession(context.Background(), workspaceID, sid); err != nil {
			h.logger.Error("failed to delete session from index", err, "workspaceID", workspaceID, "sessionID", sid)
		}
	}

	go func() {
		h.removeActiveSession(workspaceID, sid)
		if h.sessionParents != nil {
			h.sessionParents.invalidate(workspaceID)
		}
		if h.broker != nil {
			h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
				Type:      "session.status",
				SessionID: sid,
				Status:    "deleted",
			})
		}
	}()
}

func (h *ProxyHandler) GetWorkspaceCRD(workspaceID string) (*v1.Workspace, error) {
	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return nil, fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
	}
	return v1Client.Workspaces(h.namespace).Get(context.Background(), workspaceID, metav1.GetOptions{})
}

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func validateSessionID(s string) error {
	if s == "" {
		return errors.New("sessionId must not be empty")
	}
	if len(s) > 128 {
		return errors.New("sessionId exceeds the 128-character limit")
	}
	if strings.Contains(s, "..") {
		return errors.New("sessionId contains forbidden '..' (path traversal)")
	}
	if !sessionIDPattern.MatchString(s) {
		return errors.New("sessionId contains characters outside [a-zA-Z0-9._-]")
	}
	return nil
}
