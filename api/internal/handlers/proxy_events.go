// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/services/msgqueue"
	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
	phase := workspace.Status.Phase

	h.priorPhaseMu.Lock()
	prior := h.priorPhase[workspace.Name]
	h.priorPhase[workspace.Name] = string(phase)
	h.priorPhaseMu.Unlock()

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
		if h.queueSvc != nil && (phase == phaseTerminated || phase == phaseTerminating) {
			if err := h.queueSvc.ClearWorkspace(context.Background(), workspace.Name); err != nil {
				h.logger.Error("Failed to clear message queue on terminate", err, "workspaceID", workspace.Name)
			}
		}
		if phase == phaseTerminated || phase == phaseTerminating {
			h.priorPhaseMu.Lock()
			delete(h.priorPhase, workspace.Name)
			h.priorPhaseMu.Unlock()

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
		// prior == "" means this is the first invocation for this workspace in the handler —
		// either a seed call (workspace was already Active on API restart) or a real transition
		// from a phase not yet seen by the handler (e.g. Creating→Active on a new workspace
		// whose Creating event arrived before the handler was aware of it).
		// prior != phaseActive means a real transition into Active (e.g. Creating → Active,
		// Resuming → Active). Both prior=="" and prior!=Active require starting the SSE subscription.
		// prior == phaseActive means a watch event with no phase change — only clear cached config.
		if prior == "" || prior != string(phaseActive) {
			h.invalidateCaches(workspace.Name)
			if h.sseTracker != nil {
				h.sseTracker.StopWatching(workspace.Name)
				h.sseTracker.EnsureWatching(workspace.Name)
			}
		} else {
			h.wsConfigMu.Lock()
			delete(h.wsConfig, workspace.Name)
			h.wsConfigMu.Unlock()
		}
	}
}

func (h *ProxyHandler) onSessionIdle(workspaceID, sessionID string) {
	h.removeActiveSession(workspaceID, sessionID)

	if h.broker != nil {
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
	if h.sessionIndex != nil {
		h.sessionIndex.RecordMessage(workspaceID, sessionID, "", time.Now())
		go h.fetchAndPersistTitle(workspaceID, sessionID)
	}
	if h.queueSvc != nil {
		go h.drainQueuedMessage(workspaceID, sessionID)
	}
}

func (h *ProxyHandler) onSessionActive(workspaceID, sessionID string) {
	h.wsConfigMu.RLock()
	cfg, ok := h.wsConfig[workspaceID]
	h.wsConfigMu.RUnlock()
	maxSessions := defaultMaxActiveSessions
	if ok && cfg.maxActiveSessions > 0 {
		maxSessions = cfg.maxActiveSessions
	}
	h.checkAndAddActiveSession(workspaceID, sessionID, maxSessions)

	if h.broker != nil {
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
			Type: "agent.question",
			Data: req,
		})
	} else if h.dialect.IsQuestionResolved(eventType) {
		var resolution struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(properties, &resolution)
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
		h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
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
	v1Client, v1Err := h.k8sClient.LlmsafespaceV1()
	if v1Err != nil {
		return fmt.Errorf("getting v1 client: %w", v1Err)
	}
	workspace, err := v1Client.Workspaces(h.namespace).Get(ctx, workspaceID, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting workspace: %w", err)
	}
	if workspace.Status.Phase != phaseActive || workspace.Status.PodIP == "" {
		return fmt.Errorf("workspace not active")
	}
	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("getting password: %w", err)
	}

	body := promptRequestBody{
		Parts:     []promptPart{{Type: "text", Text: msg.Text}},
		MessageID: msg.ID,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling body: %w", err)
	}

	targetURL := fmt.Sprintf("http://%s:%d/session/%s/prompt_async", workspace.Status.PodIP, opencodePort, sessionID)
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
	h.broker.Publish(workspaceID, apitypes.WorkspaceSSEEvent{
		Type:      "queue.update",
		SessionID: sessionID,
		Data:      data,
	})
}
