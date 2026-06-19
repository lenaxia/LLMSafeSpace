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
	"time"

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

	// US-27b.5: wire chat-error enrichment. The closure captures wid + the
	// agent-state checker so doProxy can rewrite the response body on 4xx
	// with agentNeedsRefresh / hint fields. On 2xx the closure is never
	// invoked (doProxy only buffers on status >= 400).
	var errBodyTransform func(statusCode int, body []byte) []byte
	if h.agentStateChecker != nil {
		errBodyTransform = func(_ int, body []byte) []byte {
			changedAt, checkerErr := h.agentStateChecker.GetLastCredentialChangedAt(c.Request.Context(), wid)
			if checkerErr != nil || changedAt.IsZero() {
				// No pending credentials — pass body through the allowlist
				// (EnrichChatErrorBody with needsRefresh=false just filters
				// unknown fields; no hint added).
				return EnrichChatErrorBody(body, false, time.Time{}, wid)
			}
			h.logger.Info("Chat error enriched with pending-credential hint",
				"workspaceID", wid, "credentialsPendingSince", changedAt.Format("2006-01-02T15:04:05Z"))
			return EnrichChatErrorBody(body, true, changedAt, wid)
		}
	}

	h.proxyToWorkspaceWithErrBody(c, "/session/"+sid+"/message", true, sid, errBodyTransform, true)

	status := c.Writer.Status()
	if status < 300 && h.sessionIndex != nil {
		go h.fetchAndPersistTitle(wid, sid)
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
	wid := c.Param("id")

	// Proxy the abort to opencode first. Only if that succeeds do we take
	// ownership of queued messages — this avoids clearing the queue when the
	// abort itself fails (network error, workspace not active, etc.).
	h.proxyToWorkspace(c, "/session/"+sid+"/abort", false, sid)

	if h.queueSvc == nil || c.Writer.Status() >= 400 {
		return
	}

	// Abort succeeded. Peek then clear the session queue. Note: PeekAll and
	// Clear are separate Redis commands — a message enqueued between them will
	// be cleared without a dismissed SSE event. This is acceptable: the message
	// is still discarded (the intent of abort), just silently.
	flushed, err := h.queueSvc.PeekAll(c.Request.Context(), wid, sid)
	if err != nil {
		h.logger.Error("AbortSession: failed to peek queue after abort", err, "workspaceID", wid, "sessionID", sid)
		return
	}
	if len(flushed) == 0 {
		return
	}
	if err := h.queueSvc.Clear(c.Request.Context(), wid, sid); err != nil {
		h.logger.Error("AbortSession: failed to clear queue after abort", err, "workspaceID", wid, "sessionID", sid)
		return
	}
	// Publish dismissed SSE so UIs remove the pills immediately.
	for _, msg := range flushed {
		h.publishQueueEvent(wid, sid, "dismissed", msg.ID, "")
	}

	// In the background: wait for idle, then send each flushed message one at a
	// time (with an idle-wait between each) and abort again at the end. This
	// ensures messages appear in the transcript without being processed.
	go h.flushAndAbortAfterIdle(wid, sid, flushed)
}

// flushAndAbortAfterIdle waits for the session to become idle (after an abort),
// then sends each flushed message one at a time to opencode. Between each send
// it waits for the session to go idle again before sending the next, ensuring
// no 409 "session busy" errors. After all messages are sent it aborts once more
// so they appear in the transcript but are not processed further.
func (h *ProxyHandler) flushAndAbortAfterIdle(workspaceID, sessionID string, msgs []msgqueue.QueuedMessage) {
	if h.sseTracker == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// waitIdle subscribes to the SSE drain and returns once this session signals idle,
	// or when ctx is done.
	waitIdle := func() bool {
		idleCh := make(chan struct{}, 1)
		unsub := h.sseTracker.SubscribeDrain(workspaceID,
			func(_, sid string) {
				if sid == sessionID {
					select {
					case idleCh <- struct{}{}:
					default:
					}
				}
			},
			func(_, _ string) {},
		)
		defer unsub()
		select {
		case <-idleCh:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// Wait for idle from the initial abort before sending anything.
	if !waitIdle() {
		h.logger.Warn("flushAndAbortAfterIdle: timed out waiting for initial idle",
			"workspaceID", workspaceID, "sessionID", sessionID)
		return
	}

	// Send each message one at a time, waiting for idle after each send.
	for i, msg := range msgs {
		if err := h.sendQueuedToOpencode(ctx, workspaceID, sessionID, &msg); err != nil {
			h.logger.Warn("flushAndAbortAfterIdle: failed to send flushed message",
				"workspaceID", workspaceID, "sessionID", sessionID,
				"messageID", msg.ID, "index", i, "error", err)
			// Stop on first error — remaining messages would also fail.
			break
		}
		// Wait for this message's turn to complete before sending the next.
		if i < len(msgs)-1 {
			if !waitIdle() {
				h.logger.Warn("flushAndAbortAfterIdle: timed out waiting for idle between messages",
					"workspaceID", workspaceID, "sessionID", sessionID, "sentSoFar", i+1)
				break
			}
		}
	}

	// Abort again to stop processing the flushed messages.
	podIP, password, err := h.getPodIPAndPassword(ctx, workspaceID)
	if err != nil {
		return
	}
	abortURL := fmt.Sprintf("http://%s:%d/session/%s/abort", podIP, opencodePort, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, abortURL, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(agentd.AuthUsername, password)
	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Warn("flushAndAbortAfterIdle: second abort failed",
			"workspaceID", workspaceID, "sessionID", sessionID, "error", err)
		return
	}
	_ = resp.Body.Close()
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

	h.state().MarkSessionDeleted(workspaceID, sid)

	if h.sessionIndex != nil {
		// Use context.Background() so a client disconnect after the agent
		// has already deleted the session doesn't leave the index in an
		// inconsistent state (agent deleted, index still has it).
		if err := h.sessionIndex.DeleteSession(context.Background(), workspaceID, sid); err != nil { //nolint:contextcheck
			h.logger.Error("failed to delete session from index", err, "workspaceID", workspaceID, "sessionID", sid)
		}
	}

	go func() {
		h.removeActiveSession(workspaceID, sid)
		if h.sessionParents != nil {
			h.sessionParents.invalidate(workspaceID)
		}
		if h.userBroker != nil {
			h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
				Type:      "session.status",
				SessionID: sid,
				Status:    "deleted",
			})
		}
	}()
}

// isSessionDeleted returns true if the session was recently deleted via the
// API and late events should be suppressed. Delegates to the state store —
// the store's in-memory implementation matches the prior ProxyHandler
// behavior exactly; a future Redis-backed implementation will move
// tombstones to a shared key so the suppression is cluster-wide.
func (h *ProxyHandler) isSessionDeleted(workspaceID, sessionID string) bool {
	return h.state().IsSessionDeleted(workspaceID, sessionID)
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

// getPodIPAndPassword returns the pod IP and opencode password for the given
// workspace. It is a convenience helper shared by several background goroutines.
func (h *ProxyHandler) getPodIPAndPassword(ctx context.Context, workspaceID string) (podIP, password string, err error) {
	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return "", "", fmt.Errorf("getting v1 client: %w", err)
	}
	ws, err := v1Client.Workspaces(h.namespace).Get(ctx, workspaceID, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("getting workspace: %w", err)
	}
	if ws.Status.Phase != phaseActive || ws.Status.PodIP == "" {
		return "", "", fmt.Errorf("workspace not active")
	}
	pw, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("getting password: %w", err)
	}
	return ws.Status.PodIP, pw, nil
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

	if h.userBroker != nil {
		h.publishWorkspaceEvent(wid, apitypes.WorkspaceSSEEvent{
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

	if h.userBroker != nil {
		h.publishWorkspaceEvent(wid, apitypes.WorkspaceSSEEvent{
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
