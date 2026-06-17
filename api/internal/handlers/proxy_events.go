// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/services/msgqueue"
	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
	phase := workspace.Status.Phase

	prior, hadPrior := h.state().GetPriorPhase(workspace.Name)
	h.state().SetPriorPhase(workspace.Name, string(phase))

	if h.userBroker != nil && workspace.Spec.Owner.UserID != "" {
		h.userBroker.RecordWorkspaceOwner(workspace.Name, workspace.Spec.Owner.UserID)
		h.userBroker.PublishToUser(workspace.Spec.Owner.UserID, apitypes.WorkspaceSSEEvent{
			Type:        "workspace.phase",
			WorkspaceID: workspace.Name,
			Phase:       string(phase),
		})
	}

	if h.meteringSvc != nil && workspace.Spec.Owner.UserID != "" {
		// RecordLifecycleEvent is called unconditionally — including on seed calls
		// (prior=="") that fire when the API restarts with already-Active workspaces.
		// Seed calls produce a phantom lifecycle record with from_phase="" and
		// to_phase="Active". This was a deliberate tradeoff: the alternative (guarding
		// with prior!="") silently drops Creating→Active events for workspaces that
		// transition while the API is restarting, which corrupts billing data worse than
		// a phantom record. The metering service is expected to handle from_phase="" as
		// a no-op or a restart-artifact marker.
		if err := h.meteringSvc.RecordLifecycleEvent(
			context.Background(),
			workspace.Name,
			workspace.Spec.Owner.UserID,
			types.OwnerTypeUser,
			prior,
			string(phase),
			workspace.Spec.SecurityLevel,
			time.Now(),
		); err != nil {
			h.logger.Error("Failed to record lifecycle event", err,
				"workspace_id", workspace.Name,
				"phase", string(phase),
			)
		}
	}

	if phase == phaseSuspending || phase == phaseSuspended || phase == phaseTerminating || phase == phaseTerminated {
		h.invalidateCaches(workspace.Name)
		if h.sseTracker != nil {
			h.sseTracker.StopWatching(workspace.Name)
		}
		if h.queueSvc != nil {
			h.publishDismissedForWorkspace(context.Background(), workspace.Name)
			if err := h.queueSvc.ClearWorkspace(context.Background(), workspace.Name); err != nil {
				h.logger.Error("Failed to clear message queue on terminate/suspend", err, "workspaceID", workspace.Name)
			}
		}
		if phase == phaseTerminated || phase == phaseTerminating {
			h.state().DeletePriorPhase(workspace.Name)

			if h.activityTracker != nil {
				h.activityTracker.Delete(workspace.Name)
			}
		}
		return
	}

	if phase == v1.WorkspacePhaseFailed {
		h.invalidateCaches(workspace.Name)
		return
	}

	if phase == phaseActive {
		// hadPrior==false means this is the first invocation for this
		// workspace in the handler — either a seed call (workspace was
		// already Active on API restart) or a real transition from a
		// phase not yet seen by the handler (e.g. Creating→Active on a
		// new workspace whose Creating event arrived before the handler
		// was aware of it). prior != phaseActive means a real transition
		// into Active (e.g. Creating → Active, Resuming → Active). Both
		// cases require starting the SSE subscription. prior == phaseActive
		// means a watch event with no phase change — only clear cached config.
		if !hadPrior || prior != string(phaseActive) {
			h.invalidateCaches(workspace.Name)
			if h.sseTracker != nil {
				h.sseTracker.StopWatching(workspace.Name)
				h.sseTracker.EnsureWatching(workspace.Name)
			}
		} else {
			h.state().InvalidateWorkspaceConfig(workspace.Name)
		}
	}
}

func (h *ProxyHandler) onSessionIdle(workspaceID, sessionID string) {
	h.removeActiveSession(workspaceID, sessionID)

	if h.broker != nil {
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "idle",
		})
	}

	if h.userBroker != nil {
		if userID := h.userBroker.WorkspaceOwner(workspaceID); userID != "" {
			h.userBroker.PublishToUser(userID, apitypes.WorkspaceSSEEvent{
				Type:        "session.status",
				WorkspaceID: workspaceID,
				SessionID:   sessionID,
				Status:      "idle",
			})
		}
	}

	if h.activityTracker != nil {
		h.activityTracker.Record(workspaceID)
	}
	if h.sessionIndex != nil && !h.isSessionDeleted(workspaceID, sessionID) {
		h.sessionIndex.RecordMessage(workspaceID, sessionID, "", time.Now())
		go h.fetchAndPersistTitle(workspaceID, sessionID)
	}
	if h.queueSvc != nil && !h.isSessionDeleted(workspaceID, sessionID) {
		go h.drainQueuedMessage(workspaceID, sessionID)
	}
}

func (h *ProxyHandler) onSessionActive(workspaceID, sessionID string) {
	cfg, ok := h.state().GetWorkspaceConfig(workspaceID)
	maxSessions := defaultMaxActiveSessions
	if ok && cfg.MaxActiveSessions > 0 {
		maxSessions = cfg.MaxActiveSessions
	}
	h.checkAndAddActiveSession(workspaceID, sessionID, maxSessions)

	if h.broker != nil {
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "busy",
		})
	}

	if h.userBroker != nil {
		if userID := h.userBroker.WorkspaceOwner(workspaceID); userID != "" {
			h.userBroker.PublishToUser(userID, apitypes.WorkspaceSSEEvent{
				Type:        "session.status",
				WorkspaceID: workspaceID,
				SessionID:   sessionID,
				Status:      "busy",
			})
		}
	}
}

func (h *ProxyHandler) onRawEvent(workspaceID, eventType, rawData string) {
	if h.broker != nil {
		var parsed interface{}
		_ = json.Unmarshal([]byte(rawData), &parsed)
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type:      "opencode.event",
			EventType: eventType,
			Data:      parsed,
		})
	}

	if eventType == "session.updated" && h.sessionIndex != nil {
		h.persistTitleFromEvent(workspaceID, rawData)
	}

	if eventType == "session.next.step.ended" {
		h.persistContextFromEvent(workspaceID, rawData)
	}

	if h.dialect != nil {
		h.emitNormalizedInputEvent(workspaceID, eventType, rawData)
	}
}

func (h *ProxyHandler) emitNormalizedInputEvent(workspaceID, eventType, rawData string) {
	if h.broker == nil {
		return
	}
	var envelope struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(rawData), &envelope); err != nil || len(envelope.Properties) == 0 {
		return
	}
	properties := envelope.Properties

	if h.dialect.IsQuestionAsked(eventType) {
		req, err := h.dialect.ParseQuestionRequest(eventType, properties)
		if err != nil {
			h.logger.Warn("Failed to parse question event", "error", err, "workspaceID", workspaceID)
			return
		}
		req.RootSessionID = h.resolveRootSessionID(workspaceID, req.SessionID)
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type: "agent.question",
			Data: req,
		})
	} else if h.dialect.IsQuestionResolved(eventType) {
		var resolution struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(properties, &resolution)
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type: "agent.question.resolved",
			Data: map[string]string{
				"request_id": resolution.ID,
				"session_id": resolution.SessionID,
			},
		})
	} else if h.dialect.IsPermissionAsked(eventType) {
		req, err := h.dialect.ParsePermissionRequest(eventType, properties)
		if err != nil {
			h.logger.Warn("Failed to parse permission event", "error", err, "workspaceID", workspaceID)
			return
		}

		if h.shouldAutoApprovePermissions(workspaceID) {
			go h.autoApprovePermission(workspaceID, req.ID)
			return
		}

		req.RootSessionID = h.resolveRootSessionID(workspaceID, req.SessionID)
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type: "agent.permission",
			Data: req,
		})
	} else if h.dialect.IsPermissionResolved(eventType) {
		var resolution struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
			Reply     string `json:"reply"`
		}
		_ = json.Unmarshal(properties, &resolution)
		h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
			Type: "agent.permission.resolved",
			Data: map[string]string{
				"request_id": resolution.ID,
				"session_id": resolution.SessionID,
				"reply":      resolution.Reply,
			},
		})
	}
}

func (h *ProxyHandler) resolveRootSessionID(workspaceID, sessionID string) string {
	if h.sessionParents == nil || sessionID == "" {
		return sessionID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return h.sessionParents.resolveRoot(ctx, workspaceID, sessionID)
}

func (h *ProxyHandler) persistTitleFromEvent(workspaceID, rawData string) {
	var evt struct {
		Properties struct {
			SessionID string `json:"sessionID"`
			Info      struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				ParentID string `json:"parentID"`
			} `json:"info"`
		} `json:"properties"`
	}
	if json.Unmarshal([]byte(rawData), &evt) != nil {
		return
	}
	id := evt.Properties.Info.ID
	if id == "" {
		return
	}
	if h.isSessionDeleted(workspaceID, id) {
		return
	}
	if evt.Properties.Info.Title != "" {
		_ = h.sessionIndex.UpsertTitle(context.Background(), workspaceID, id, evt.Properties.Info.Title)
	}
	if evt.Properties.Info.ParentID != "" {
		_ = h.sessionIndex.UpsertParent(context.Background(), workspaceID, id, evt.Properties.Info.ParentID)
	}
}

func (h *ProxyHandler) persistContextFromEvent(workspaceID, rawData string) {
	if h.sessionIndex == nil {
		return
	}
	var evt struct {
		Properties struct {
			SessionID string `json:"sessionID"`
			Tokens    *struct {
				Input int64 `json:"input"`
				Cache struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"properties"`
	}
	if json.Unmarshal([]byte(rawData), &evt) != nil {
		return
	}
	if evt.Properties.SessionID == "" || evt.Properties.Tokens == nil {
		return
	}
	if h.isSessionDeleted(workspaceID, evt.Properties.SessionID) {
		return
	}
	promptTokens := evt.Properties.Tokens.Input +
		evt.Properties.Tokens.Cache.Read +
		evt.Properties.Tokens.Cache.Write
	_ = h.sessionIndex.UpsertContextUsed(context.Background(), workspaceID, evt.Properties.SessionID, promptTokens)
}

func (h *ProxyHandler) getPodIPForSSE(workspaceID string) string {
	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		return ""
	}
	workspace, err := v1Client.Workspaces(h.namespace).Get(context.Background(), workspaceID, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if workspace.Status.Phase != phaseActive {
		return ""
	}
	return workspace.Status.PodIP
}

// publishDismissedForWorkspace publishes a queue.update dismissed SSE event for
// every message currently in the queue for the given workspace. It is called
// before clearing the queue so that connected UIs can remove pending pills.
// Errors are logged and silently swallowed — the clear proceeds regardless.
func (h *ProxyHandler) publishDismissedForWorkspace(ctx context.Context, workspaceID string) {
	if h.queueSvc == nil || h.broker == nil {
		return
	}
	msgs, err := h.queueSvc.PeekAllWorkspace(ctx, workspaceID)
	if err != nil {
		h.logger.Error("Failed to peek workspace queue before dismiss publish", err, "workspaceID", workspaceID)
		return
	}
	for _, msg := range msgs {
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
			Type:      "queue.update",
			SessionID: msg.SessionID,
			Data: queueUpdateData{
				Event:     "dismissed",
				MessageID: msg.ID,
			},
		})
	}
}

const maxQueueRetries = 5

type queueUpdateData struct {
	Event     string `json:"event"`
	MessageID string `json:"messageID"`
	Error     string `json:"error,omitempty"`
}

func (h *ProxyHandler) drainQueuedMessage(workspaceID, sessionID string) {
	if h.queueSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for {
		msg, err := h.queueSvc.Dequeue(ctx, workspaceID, sessionID)
		if err != nil {
			h.logger.Error("Failed to dequeue message", err, "workspaceID", workspaceID, "sessionID", sessionID)
			return
		}
		if msg == nil {
			return
		}

		if err := h.sendQueuedToOpencode(ctx, workspaceID, sessionID, msg); err != nil {
			h.logger.Error("Failed to send queued message to opencode", err,
				"workspaceID", workspaceID, "sessionID", sessionID, "messageID", msg.ID)
			msg.RetryCount++
			if msg.RetryCount > maxQueueRetries {
				h.publishQueueEvent(workspaceID, sessionID, "error", msg.ID, "max retries exceeded")
				continue
			}
			if requeueErr := h.queueSvc.Requeue(ctx, workspaceID, sessionID, *msg); requeueErr != nil {
				h.logger.Error("Failed to requeue message", requeueErr, "workspaceID", workspaceID, "sessionID", sessionID)
			}
			select {
			case <-time.After(time.Duration(msg.RetryCount) * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		h.publishQueueEvent(workspaceID, sessionID, "sent", msg.ID, "")
	}
}

type promptRequestBody struct {
	Parts     []promptPart `json:"parts"`
	MessageID string       `json:"messageID"`
}

type promptPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (h *ProxyHandler) sendQueuedToOpencode(ctx context.Context, workspaceID, sessionID string, msg *msgqueue.QueuedMessage) error {
	podIP, password, err := h.getPodIPAndPassword(ctx, workspaceID)
	if err != nil {
		return err
	}

	body := promptRequestBody{
		Parts:     []promptPart{{Type: "text", Text: msg.Text}},
		MessageID: msg.ID,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling body: %w", err)
	}

	targetURL := fmt.Sprintf("http://%s:%d/session/%s/prompt_async", podIP, opencodePort, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth("opencode", password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("session busy")
	}
	return fmt.Errorf("unexpected status: %d", resp.StatusCode)
}

func (h *ProxyHandler) publishQueueEvent(workspaceID, sessionID, event, messageID, errMsg string) {
	if h.broker == nil {
		return
	}
	data := queueUpdateData{
		Event:     event,
		MessageID: messageID,
	}
	if errMsg != "" {
		data.Error = errMsg
	}
	h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
		Type:      "queue.update",
		SessionID: sessionID,
		Data:      data,
	})
}

// reconcileSessionState is called by the SSE tracker's onReconnect callback
// each time the tracker establishes a new connection to the workspace pod.
// It queries /v1/statusz on the agentd admin port to get the current session
// states and reconciles two classes of state drift:
//
//  1. Stranded queues: a session went idle while the SSE connection was down,
//     so the session.status=idle event was never received, leaving messages
//     stuck in the Redis queue. We trigger drainQueuedMessage for these.
//
//  2. Stale activeSess entries: a session is idle in opencode (per statusz)
//     but still marked active in our local activeSess map. This happens when
//     opencode dies (OOM/SIGTERM) mid-stream — the session.status=idle event
//     is never emitted, so onSessionIdle is never called, and our local map
//     keeps the session marked busy forever. Without this fix, POST to a
//     stuck session returns 409 Conflict indefinitely (until API restart).
//     See incident report 2026-06-16 (sessions ses_13076538bffeYtLrhoZ2ccRM1E
//     and ses_130c14344ffeVF52UQ6QGPmB0P stuck after pod OOMKill).
func (h *ProxyHandler) reconcileSessionState(workspaceID, podIP, password string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://%s:%d/v1/statusz", podIP, agentd.AgentdAdminPort) //nolint:gosec // G107: internal pod
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.logger.Debug("reconcileSessionState: failed to build statusz request", "workspaceID", workspaceID, "error", err)
		return
	}
	if password != "" {
		req.Header.Set("Authorization", "Bearer "+password)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Debug("reconcileSessionState: statusz unavailable", "workspaceID", workspaceID, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		h.logger.Debug("reconcileSessionState: unexpected statusz status", "workspaceID", workspaceID, "status", resp.StatusCode)
		return
	}

	var statusz agentd.StatuszResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&statusz); err != nil {
		h.logger.Debug("reconcileSessionState: failed to decode statusz", "workspaceID", workspaceID, "error", err)
		return
	}

	for _, sess := range statusz.Sessions {
		if sess.Status != "idle" {
			continue
		}

		// Reconcile stale activeSess entries: opencode says idle, but our
		// local map says active. This is the OOM/SIGTERM case — clean up
		// regardless of whether there are queued messages.
		if h.isSessionActive(workspaceID, sess.ID) {
			h.logger.Info("reconcileSessionState: clearing stale activeSess entry",
				"workspaceID", workspaceID, "sessionID", sess.ID,
				"reason", "session is idle in opencode but marked active locally")
			h.removeActiveSession(workspaceID, sess.ID)
			// Publish session.status=idle so connected clients update their UI.
			// Without this, browsers showing the session keep their busy
			// indicator until the next page reload.
			if h.broker != nil {
				h.publishWorkspaceEvent(workspaceID, apitypes.WorkspaceSSEEvent{
					Type:      "session.status",
					SessionID: sess.ID,
					Status:    "idle",
				})
			}
		}

		// Reconcile stranded queues: drain any queued messages for idle sessions.
		// Note: this runs regardless of whether activeSess was stale above —
		// queued messages should drain whenever a session is idle.
		if h.queueSvc != nil {
			n, err := h.queueSvc.Len(ctx, workspaceID, sess.ID)
			if err != nil || n == 0 {
				continue
			}
			h.logger.Info("reconcileSessionState: found stranded queue, triggering drain",
				"workspaceID", workspaceID, "sessionID", sess.ID, "queueLen", n)
			h.onSessionIdle(workspaceID, sess.ID)
		}
	}
}
