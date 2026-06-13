// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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

	if phase == phaseSuspending || phase == phaseSuspended || phase == phaseTerminating || phase == phaseTerminated {
		h.invalidateCaches(workspace.Name)
		if h.sseTracker != nil {
			h.sseTracker.StopWatching(workspace.Name)
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
		if prior != "" && prior != string(phaseActive) {
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
		if h.sessionIndex != nil {
			h.sessionIndex.RecordMessage(workspaceID, sessionID, "", time.Now())
			go h.fetchAndPersistTitle(workspaceID, sessionID)
		}
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
