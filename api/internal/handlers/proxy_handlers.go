// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/services/msgqueue"
	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
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

	h.markSessionDeleted(workspaceID, sid)

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

// markSessionDeleted records that a session was explicitly deleted so that
// late SSE events from opencode don't re-insert it into session_index.
func (h *ProxyHandler) markSessionDeleted(workspaceID, sessionID string) {
	h.deletedSessionsMu.Lock()
	h.deletedSessions[workspaceID+"/"+sessionID] = struct{}{}
	// Bounded: if the set grows beyond a reasonable size, evict a batch.
	// In practice this never triggers — sessions are deleted rarely and the
	// set is cleared on workspace suspend/delete.
	if len(h.deletedSessions) > 500 {
		count := 0
		for k := range h.deletedSessions {
			delete(h.deletedSessions, k)
			count++
			if count >= 250 {
				break
			}
		}
	}
	h.deletedSessionsMu.Unlock()
}

// isSessionDeleted returns true if the session was recently deleted via the
// API and late events should be suppressed.
func (h *ProxyHandler) isSessionDeleted(workspaceID, sessionID string) bool {
	h.deletedSessionsMu.RLock()
	_, ok := h.deletedSessions[workspaceID+"/"+sessionID]
	h.deletedSessionsMu.RUnlock()
	return ok
}

// clearDeletedSessions removes all deleted-session markers for a workspace.
func (h *ProxyHandler) clearDeletedSessions(workspaceID string) {
	h.deletedSessionsMu.Lock()
	prefix := workspaceID + "/"
	for k := range h.deletedSessions {
		if strings.HasPrefix(k, prefix) {
			delete(h.deletedSessions, k)
		}
	}
	h.deletedSessionsMu.Unlock()
}

func (h *ProxyHandler) GetWorkspaceCRD(workspaceID string) (*v1.Workspace, error) {
	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return nil, fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
	}
	return v1Client.Workspaces(h.namespace).Get(context.Background(), workspaceID, metav1.GetOptions{})
}

// RenameSessionInAgent sends a title update to the opencode agent running on
// the workspace pod so that the agent's in-memory session title matches the
// user-assigned title. Without this, the periodic title fetch (useSessionTitle
// hook in the frontend) retrieves the old agent-side title and overwrites the
// user's rename in PostgreSQL.
func (h *ProxyHandler) RenameSessionInAgent(ctx context.Context, workspaceID, sessionID, title string) error {
	if err := validateSessionID(sessionID); err != nil {
		return fmt.Errorf("invalid sessionId: %w", err)
	}

	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
	}
	ws, err := v1Client.Workspaces(h.namespace).Get(ctx, workspaceID, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get workspace CRD: %w", err)
	}
	if ws.Status.Phase != phaseActive || ws.Status.PodIP == "" {
		return fmt.Errorf("workspace not active")
	}

	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("get password: %w", err)
	}

	type sessionUpdate struct {
		Title string `json:"title"`
	}
	payload := sessionUpdate{Title: title}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	targetURL := fmt.Sprintf("http://%s:%d/session/%s", ws.Status.PodIP, opencodePort, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(agentd.AuthUsername, password)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agent returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
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

type enqueueRequest struct {
	Text string `json:"text" binding:"required"`
}

func (h *ProxyHandler) EnqueueMessage(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	wid := c.Param("id")

	var req enqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if len(req.Text) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text must not be empty"})
		return
	}
	if len(req.Text) > 100_000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text exceeds 100KB limit"})
		return
	}

	if h.queueSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "message queue not available"})
		return
	}

	msgID, err := h.queueSvc.Enqueue(c.Request.Context(), wid, sid, req.Text)
	if err != nil {
		h.logger.Error("Failed to enqueue message", err, "workspaceID", wid, "sessionID", sid)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue message"})
		return
	}

	if h.broker != nil {
		h.broker.Publish(wid, apitypes.WorkspaceSSEEvent{
			Type:      "queue.update",
			SessionID: sid,
			Data: queueUpdateData{
				Event:     "enqueued",
				MessageID: msgID,
			},
		})
	}

	c.JSON(http.StatusAccepted, gin.H{"messageID": msgID})
}

func (h *ProxyHandler) ListQueue(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	wid := c.Param("id")

	if h.queueSvc == nil {
		c.JSON(http.StatusOK, gin.H{"messages": []msgqueue.QueuedMessage{}})
		return
	}

	msgs, err := h.queueSvc.PeekAll(c.Request.Context(), wid, sid)
	if err != nil {
		h.logger.Error("Failed to list queue", err, "workspaceID", wid, "sessionID", sid)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list queue"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

func (h *ProxyHandler) DeleteQueueMessage(c *gin.Context) {
	sid := c.Param("sessionId")
	if err := validateSessionID(sid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
		return
	}
	wid := c.Param("id")
	msgID := c.Param("messageId")
	if msgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messageId required"})
		return
	}

	if h.queueSvc == nil {
		c.Status(http.StatusNoContent)
		return
	}

	if err := h.queueSvc.Remove(c.Request.Context(), wid, sid, msgID); err != nil {
		h.logger.Error("Failed to remove queue message", err, "workspaceID", wid, "sessionID", sid, "messageID", msgID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove message"})
		return
	}

	if h.broker != nil {
		h.broker.Publish(wid, apitypes.WorkspaceSSEEvent{
			Type:      "queue.update",
			SessionID: sid,
			Data: queueUpdateData{
				Event:     "dismissed",
				MessageID: msgID,
			},
		})
	}

	c.Status(http.StatusNoContent)
}
