// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	opencode "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// respondWithAPIError maps API errors to HTTP responses.
// Local helper for package handlers (same logic as server.respondWithError).
func respondWithAPIError(c *gin.Context, err error) {
	type apiError interface {
		StatusCode() int
		Error() string
	}
	if ae, ok := err.(apiError); ok {
		c.JSON(ae.StatusCode(), gin.H{"error": ae.Error()})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// AgentStateStore is the DB surface needed by the reload handler.
type AgentStateStore interface {
	GetLastCredentialChangedAt(ctx context.Context, workspaceID string) (time.Time, error)
	MarkAgentReloaded(ctx context.Context, tx *sql.Tx, workspaceID string, priorChangedAt time.Time) (time.Time, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// WorkspaceServicer is the minimal workspace service surface for reload.
type WorkspaceServicer interface {
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*types.Workspace, error)
}

// AgentReloadHandler handles POST /api/v1/workspaces/:id/agent/reload.
type AgentReloadHandler struct {
	workspaceSvc WorkspaceServicer
	db           AgentStateStore
	podResolver  PodIPResolver
	httpClient   *http.Client
	logger       pkginterfaces.LoggerInterface
	sseTracker   *SSETracker
	getPassword  func(ctx context.Context, workspaceID string) (string, error)
}

// NewAgentReloadHandler constructs the handler with all dependencies.
func NewAgentReloadHandler(
	wsSvc WorkspaceServicer,
	db AgentStateStore,
	podResolver PodIPResolver,
	httpClient *http.Client,
	logger pkginterfaces.LoggerInterface,
) *AgentReloadHandler {
	return &AgentReloadHandler{
		workspaceSvc: wsSvc,
		db:           db,
		podResolver:  podResolver,
		httpClient:   httpClient,
		logger:       logger,
	}
}

// SetSSETracker injects the tracker for drain mode support.
func (h *AgentReloadHandler) SetSSETracker(t *SSETracker) { h.sseTracker = t }

// SetPasswordGetter injects the password getter for drain mode (needs opencode client).
func (h *AgentReloadHandler) SetPasswordGetter(getter func(ctx context.Context, workspaceID string) (string, error)) {
	h.getPassword = getter
}

// Reload handles POST /api/v1/workspaces/:id/agent/reload.
func (h *AgentReloadHandler) Reload(c *gin.Context) {
	workspaceID := c.Param("id")
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	ws, err := h.workspaceSvc.GetWorkspace(c.Request.Context(), userID, workspaceID)
	if err != nil {
		respondWithAPIError(c, err)
		return
	}

	if ws.Phase != "Active" {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("cannot reload agent: workspace is in phase %q (must be Active)", ws.Phase),
		})
		return
	}

	podIP, err := h.podResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
	if err != nil || podIP == "" {
		c.JSON(http.StatusConflict, gin.H{
			"error": "cannot reload agent: workspace pod is not reachable",
		})
		return
	}

	// Drain mode: wait for all sessions to become idle before disposing.
	drain := c.Query("drain") == "true"
	drainTimeout := 5 * time.Minute
	if v := c.Query("drainTimeoutSeconds"); v != "" {
		if t, _ := fmt.Sscanf(v, "%d", new(int)); t == 1 {
			var secs int
			fmt.Sscanf(v, "%d", &secs)
			if secs > 0 && secs <= 600 {
				drainTimeout = time.Duration(secs) * time.Second
			}
		}
	}

	if drain && h.sseTracker != nil && h.getPassword != nil {
		pw, err := h.getPassword(c.Request.Context(), workspaceID)
		if err != nil {
			respondWithAPIError(c, apierrors.NewInternalError("get_opencode_password_failed", err))
			return
		}
		opencodeCl := opencode.NewClient(
			fmt.Sprintf("http://%s:%d", podIP, agentd.AgentPort),
			pw,
		)
		if err := WaitUntilIdle(c.Request.Context(), workspaceID, h.sseTracker, opencodeCl, drainTimeout); err != nil {
			var drainErr *ErrDrainTimeout
			if errors.As(err, &drainErr) {
				c.JSON(http.StatusRequestTimeout, gin.H{
					"error": gin.H{
						"code":           "drain_timeout",
						"message":        fmt.Sprintf("workspace did not become idle within %s", drainTimeout),
						"busySessionIDs": drainErr.BusySessions,
					},
				})
				return
			}
			respondWithAPIError(c, apierrors.NewInternalError("drain_failed", err))
			return
		}
	}

	priorChangedAt, err := h.db.GetLastCredentialChangedAt(c.Request.Context(), workspaceID)
	if err != nil {
		respondWithAPIError(c, apierrors.NewInternalError("agent_state_read_failed", err))
		return
	}

	// Dispatch to agentd (which calls opencode dispose locally).
	agentdURL := fmt.Sprintf("http://%s:%d/v1/agent/reload", podIP, agentd.AgentdPort)
	req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, agentdURL, nil)
	resp, err := h.httpClient.Do(req)
	if err != nil {
		if h.logger != nil { h.logger.Error("agent reload: agentd unreachable", err) }
		respondWithAPIError(c, apierrors.NewInternalError("agent_unreachable", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		respondWithAPIError(c, apierrors.NewInternalError("dispose_failed",
			fmt.Errorf("agentd returned %d: %s", resp.StatusCode, string(body)),
		))
		return
	}

	// Dispose succeeded. Update agent state.
	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		if h.logger != nil { h.logger.Warn("agent reload: tx begin failed; dispose done, banner may persist", "error", err.Error()) }
		c.JSON(http.StatusOK, gin.H{
			"disposed":       true,
			"lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
			"warning":        "Agent was reloaded but state could not be updated. The banner may reappear; clicking Reload again is safe.",
		})
		return
	}
	defer func() {
		if tx != nil {
			tx.Rollback() //nolint:errcheck
		}
	}()

	disposedAt, err := h.db.MarkAgentReloaded(c.Request.Context(), tx, workspaceID, priorChangedAt)
	if err != nil {
		if errors.Is(err, apierrors.ErrNoAgentStateRow) {
			c.JSON(http.StatusConflict, gin.H{"error": "workspace has no pending credentials to reload"})
			return
		}
		if h.logger != nil { h.logger.Warn("agent reload: MarkAgentReloaded failed", "error", err.Error()) }
		c.JSON(http.StatusOK, gin.H{
			"disposed":       true,
			"lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
			"warning":        "Agent was reloaded but state could not be updated. The banner may reappear; clicking Reload again is safe.",
		})
		return
	}
	if err := tx.Commit(); err != nil {
		if h.logger != nil { h.logger.Warn("agent reload: tx commit failed", "error", err.Error()) }
		c.JSON(http.StatusOK, gin.H{
			"disposed":       true,
			"lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
			"warning":        "Agent was reloaded but state could not be updated. The banner may reappear; clicking Reload again is safe.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"disposed":       true,
		"lastDisposedAt": disposedAt.Format(time.RFC3339),
	})
}
