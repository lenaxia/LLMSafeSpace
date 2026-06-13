// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/services/eventbroker"
)

func (h *ProxyHandler) StreamEvents(c *gin.Context) {
	workspaceID := c.Param("id")
	if workspaceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace ID required"})
		return
	}

	v1Client, err := h.k8sClient.LlmsafespaceV1()
	if err != nil {
		h.logger.Error("Failed to get LLMSafespaceV1 client for SSE", err, "workspaceID", workspaceID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	_, err = v1Client.Workspaces(h.namespace).Get(c.Request.Context(), workspaceID, metav1.GetOptions{})
	if err != nil {
		h.logger.Error("Failed to get workspace CRD for SSE", err, "workspaceID", workspaceID)
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	if h.broker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "event broker not initialized"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := h.broker.Subscribe(workspaceID)
	defer h.broker.Unsubscribe(workspaceID, sub)

	if h.sseTracker != nil {
		h.sseTracker.EnsureWatching(workspaceID)
	}

	streamCtx, streamCancel := context.WithCancel(c.Request.Context())
	defer streamCancel()

	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))

	if h.dialect != nil {
		go h.emitPendingInputRequests(workspaceID)
	}

	go heartbeatLoop(streamCtx, sub)

	for {
		select {
		case <-streamCtx.Done():
			return
		case evt, open := <-sub.Ch:
			if !open {
				return
			}
			if evt.Type == eventbroker.HeartbeatSentinelType {
				if _, writeErr := fmt.Fprint(c.Writer, ":\n\n"); writeErr != nil {
					streamCancel()
					return
				}
				flusher.Flush()
				_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))
				continue
			}
			if evt.Type == "resync" {
				resyncEvt := apitypes.WorkspaceSSEEvent{Type: "resync", WorkspaceID: workspaceID}
				data, marshalErr := json.Marshal(resyncEvt)
				if marshalErr != nil {
					h.logger.Warn("SSE resync marshal failed", "error", marshalErr, "workspaceID", workspaceID)
					continue
				}
				if _, writeErr := fmt.Fprintf(c.Writer, "data: %s\n\n", data); writeErr != nil {
					streamCancel()
					return
				}
				flusher.Flush()
				_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))
				continue
			}
			if evt.WorkspaceID == "" {
				evt.WorkspaceID = workspaceID
			}
			data, marshalErr := json.Marshal(evt)
			if marshalErr != nil {
				h.logger.Warn("SSE event marshal failed, dropping",
					"error", marshalErr,
					"workspaceID", workspaceID,
					"eventType", evt.Type,
				)
				continue
			}
			if _, writeErr := fmt.Fprintf(c.Writer, "data: %s\n\n", data); writeErr != nil {
				streamCancel()
				return
			}
			flusher.Flush()
			_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))
		}
	}
}
