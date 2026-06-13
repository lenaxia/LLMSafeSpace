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
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/agent"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const (
	defaultMaxActiveSessions   = 5
	maxConnectionsPerWorkspace = 10
	opencodePort               = agentd.AgentPort
	retryAfterSec              = 10

	// maxNonStreamingResponseBytes caps the buffered read in the shouldFilter
	// path to prevent OOM on a misbehaving or compromised upstream. (Epic 25 G1)
	// 32 MB matches the limit in models.go for provider /models responses.
	maxNonStreamingResponseBytes int64 = 32 << 20

	phaseActive      = v1.WorkspacePhaseActive
	phaseSuspending  = "Suspending"
	phaseSuspended   = "Suspended"
	phaseTerminating = "Terminating"
	phaseTerminated  = "Terminated"
)

type workspaceConfig struct {
	workspaceID            string
	maxActiveSessions      int
	autoApprovePermissions bool
}

type ProxyHandler struct {
	k8sClient         pkginterfaces.KubernetesClient
	httpClient        *http.Client
	logger            pkginterfaces.LoggerInterface
	namespace         string
	dialect           agent.Dialect
	agentStateChecker AgentStateChecker // for chat error enrichment (Epic 27b US-27b.5)

	pwCache   map[string]string
	pwCacheMu sync.RWMutex

	wsConfig   map[string]workspaceConfig
	wsConfigMu sync.RWMutex

	// US-23.4: Track prior phase per workspace to detect Active-from-non-Active
	// transitions that require password cache invalidation.
	priorPhase   map[string]string
	priorPhaseMu sync.Mutex

	activeSess map[string]map[string]bool
	activeMu   sync.Mutex

	connCount map[string]int
	connMu    sync.Mutex

	activityTracker *ActivityTracker
	watcher         *WorkspaceWatcher
	sseTracker      *SSETracker
	sessionIndex    interfaces.SessionIndexService
	broker          *WorkspaceEventBroker
	userBroker      *UserEventBroker
	sessionParents  *sessionParentCache

	// parentBackfilled tracks workspaces whose session_index has been
	// reconciled with opencode's /session list at least once this process
	// lifetime. Set membership is cleared by invalidateCaches so a workspace
	// suspend/restart cycle re-runs the backfill against the new pod.
	parentBackfilled   map[string]struct{}
	parentBackfilledMu sync.Mutex

	startOnce sync.Once
	stopOnce  sync.Once
}

func NewProxyHandler(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
	httpClient *http.Client,
	dialect agent.Dialect,
) (*ProxyHandler, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client cannot be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if namespace == "" {
		namespace = "default"
	}
	if httpClient == nil {
		// G12 (Epic 17): the default client now uses a 60s response-
		// header timeout instead of the prior 300s. 300s was an SSE-
		// holdover that bled into ALL request paths including
		// short-lived JSON message round-trips. 60s is a generous
		// upper bound for opencode message processing. Streaming
		// endpoints (StreamEvents, SSE) bypass this client via
		// http.Hijacker / direct conn handling and are not bounded
		// by ResponseHeaderTimeout.
		httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
			},
		}
	}
	return &ProxyHandler{
		k8sClient:        k8sClient,
		httpClient:       httpClient,
		logger:           logger,
		namespace:        namespace,
		dialect:          dialect,
		pwCache:          make(map[string]string),
		wsConfig:         make(map[string]workspaceConfig),
		priorPhase:       make(map[string]string),
		activeSess:       make(map[string]map[string]bool),
		connCount:        make(map[string]int),
		parentBackfilled: make(map[string]struct{}),
	}, nil
}

// EnableSessionParentResolution wires up the session-parent cache used to
// resolve subagent sessions back to their root user-visible session. When
// disabled (the default), the agent.permission/agent.question events emit
// with RootSessionID == SessionID, which means subtask prompts will not
// bubble up to the parent session view in the chat UI.
//
// Production callers (cmd/api) MUST enable this. Unit tests that exercise
// the synchronous publish path can leave it disabled to avoid having to
// mock the workspace-pod HTTP round-trip.
func (h *ProxyHandler) EnableSessionParentResolution() {
	if h.sessionParents != nil {
		return
	}
	h.sessionParents = newSessionParentCache(h.fetchSessionParent)
}

func (h *ProxyHandler) Start() error {
	var startErr error
	h.startOnce.Do(func() {
		h.broker = NewWorkspaceEventBroker()
		h.userBroker = NewUserEventBroker()

		h.activityTracker = NewActivityTracker(h.k8sClient, h.logger, h.namespace)
		if err := h.activityTracker.Start(); err != nil {
			startErr = fmt.Errorf("starting activity tracker: %w", err)
			return
		}

		h.sseTracker = NewSSETracker(h.httpClient, h.logger, h.onSessionIdle)
		h.sseTracker.SetPasswordGetter(h.getPassword)
		h.sseTracker.SetPodIPResolver(h.getPodIPForSSE)
		h.sseTracker.SetOnSessionActive(h.onSessionActive)
		h.sseTracker.SetOnRawEvent(h.onRawEvent)

		watcher, err := NewWorkspaceWatcher(h.k8sClient, h.logger, h.namespace, h.onPhaseChange)
		if err != nil {
			_ = h.activityTracker.Stop()
			startErr = fmt.Errorf("creating CRD watcher: %w", err)
			return
		}
		watcher.SetUserBroker(h.userBroker)
		if err := watcher.Start(); err != nil {
			_ = h.activityTracker.Stop()
			startErr = fmt.Errorf("starting CRD watcher: %w", err)
			return
		}
		h.watcher = watcher

		// Subscribe to SSE for all currently-Active workspaces so that
		// session.next.step.ended events are captured immediately on API
		// replica startup — not only after the first browser connection.
		// Without this, a replica restart leaves all workspaces unwatched
		// until a user navigates to them, causing missed step-ended events
		// and stale context_used values in session_index.
		if h.sseTracker != nil {
			for wsName, phase := range watcher.GetAllKnownPhases() {
				if phase == string(phaseActive) {
					h.sseTracker.EnsureWatching(wsName)
				}
			}
		}
	})
	return startErr
}

func (h *ProxyHandler) Stop() error {
	h.stopOnce.Do(func() {
		if h.sseTracker != nil {
			h.sseTracker.Stop()
		}
		if h.watcher != nil {
			h.watcher.Stop()
		}
		if h.activityTracker != nil {
			_ = h.activityTracker.Stop()
		}
	})
	return nil
}

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
	// After the message round-trip completes, persist the opencode-generated
	// title to the session index so the sidebar reflects it immediately.
	if status < 300 && h.sessionIndex != nil {
		go h.fetchAndPersistTitle(wid, sid)
	}

	// Epic 27b US-27b.5: When the upstream returns an error and the workspace
	// has staged credentials (agentNeedsRefresh=true), the response body should
	// include a hint. The correct implementation requires buffering the response
	// body inside doProxy before flushing it, so that we can rewrite it here.
	// That buffering refactor is tracked separately. For now: log the hint so
	// it's observable in server traces, and expose it via the status endpoint
	// (GET /workspaces/:id/status returns agentNeedsRefresh:true independently).
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
			h.broker.Publish(workspaceID, WorkspaceSSEEvent{
				Type:      "session.status",
				SessionID: sid,
				Status:    "deleted",
			})
		}
	}()
}

// StreamEvents opens a persistent SSE connection for the given workspace.
// Events are sourced from the WorkspaceEventBroker: workspace phase changes
// (from WorkspaceWatcher) and session status events (from SSETracker) are
// both multiplexed onto this single stream.
//
// The connection terminates at the API server; the pod does not need to be
// reachable for the browser to stay connected.
func (h *ProxyHandler) StreamEvents(c *gin.Context) {
	workspaceID := c.Param("id")
	if workspaceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace ID required"})
		return
	}

	// Verify the workspace exists (but do not require it to be Active — the
	// client may legitimately connect while the workspace is resuming).
	_, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
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

	// Start watching the workspace pod as soon as a browser subscribes so that
	// events are available immediately when the user sends a message, rather than
	// waiting for the first write operation to trigger EnsureWatching.
	if h.sseTracker != nil {
		h.sseTracker.EnsureWatching(workspaceID)
	}

	streamCtx, streamCancel := context.WithCancel(c.Request.Context())
	defer streamCancel()

	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))

	// Emit any pending input requests so reconnecting browsers see them immediately.
	if h.dialect != nil {
		go h.emitPendingInputRequests(workspaceID)
	}

	// Heartbeat goroutine — sends comment lines to keep connection alive (FM1)
	go heartbeatLoop(streamCtx, sub)

	for {
		select {
		case <-streamCtx.Done():
			return
		case evt, open := <-sub.ch:
			if !open {
				return
			}
			if evt.Type == heartbeatSentinelType {
				if _, writeErr := fmt.Fprint(c.Writer, ":\n\n"); writeErr != nil {
					streamCancel()
					return
				}
				flusher.Flush()
				_ = rc.SetWriteDeadline(time.Now().Add(writeDeadlineWindow))
				continue
			}
			if evt.Type == "resync" {
				resyncEvt := WorkspaceSSEEvent{Type: "resync", WorkspaceID: workspaceID}
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
			// Ensure workspace_id is set on all events
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

// proxyToWorkspace forwards the incoming request to the workspace pod's opencode
// server. Patch-part filtering is not applied at this level; it is handled
// directly by callers that invoke doProxy with stripPatch=true.
func (h *ProxyHandler) proxyToWorkspace(c *gin.Context, targetPath string, isWriteOp bool, sessionID string) {
	workspaceID := c.Param("id")
	if workspaceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace ID required"})
		return
	}

	var workspace *v1.Workspace
	// Reuse workspace CRD if already fetched by ownership middleware (avoids double read)
	if cached, exists := c.Get("workspace"); exists {
		if sb, ok := cached.(*v1.Workspace); ok {
			workspace = sb
		}
	}
	if workspace == nil {
		var err error
		workspace, err = h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
		if err != nil {
			h.logger.Error("Failed to get workspace CRD", err, "workspaceID", workspaceID)
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
	}

	if workspace.Status.Phase != phaseActive || workspace.Status.PodIP == "" {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "workspace not ready",
			"phase":      workspace.Status.Phase,
			"retryAfter": retryAfterSec,
		})
		return
	}

	password, err := h.getPassword(c.Request.Context(), workspaceID)
	if err != nil {
		h.logger.Error("Failed to get workspace password", err, "workspaceID", workspaceID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workspace credentials"})
		return
	}

	maxSessions := int(workspace.Spec.MaxActiveSessions)
	if maxSessions <= 0 {
		maxSessions = defaultMaxActiveSessions
	}

	if !h.acquireConnection(workspaceID) {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":      "connection limit reached",
			"retryAfter": retryAfterSec,
		})
		return
	}
	defer h.releaseConnection(workspaceID)

	if isWriteOp && sessionID != "" {
		if !h.checkAndAddActiveSession(workspaceID, sessionID, maxSessions) {
			h.releaseConnection(workspaceID)
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":             "active session limit reached",
				"maxActiveSessions": maxSessions,
				"retryAfter":        retryAfterSec,
			})
			return
		}
	}

	if isWriteOp && sessionID != "" && h.sseTracker != nil {
		h.sseTracker.EnsureWatching(workspaceID)
	}

	var bodyBytes []byte
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		// Limit request body to prevent OOM on oversized payloads (Epic 25 G1).
		// 10 MB is generous for any legitimate opencode prompt or tool result.
		// Pass http.NoBody (discard) as the ResponseWriter so MaxBytesReader cannot
		// write its own 413 response — we write our own JSON error below to avoid
		// a double-write where net/http and c.JSON both append to the response.
		limited := http.MaxBytesReader(nil, c.Request.Body, 10*1024*1024)
		bodyBytes, err = io.ReadAll(limited)
		_ = c.Request.Body.Close()
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body exceeds 10 MB limit"})
				return
			}
			h.logger.Error("Failed to read request body", err, "workspaceID", workspaceID)
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
	}

	podIP := workspace.Status.PodIP
	// NOTE: stripPatch is intentionally false (streaming mode). If re-enabled,
	// you MUST strip Accept-Encoding from the upstream request because opencode
	// v1.15+ compresses JSON responses >1KB via gzip/deflate, which would break
	// json.Unmarshal in stripPatchParts(). See worklog 0070 for full analysis.
	stripPatch := false
	proxyErr := h.doProxy(c, podIP, targetPath, password, bodyBytes, stripPatch)

	// Only attempt a pod-IP retry when the response headers have NOT yet been
	// committed. If doProxy flushed headers (streaming path), the response is
	// already partially written and a retry would produce a double-write.
	// c.Writer.Written() returns true once WriteHeader has been called.
	if proxyErr != nil && isConnectionError(proxyErr) && !c.Writer.Written() {
		freshWS, getErr := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
		if getErr == nil && freshWS.Status.PodIP != "" && freshWS.Status.PodIP != podIP && freshWS.Status.Phase == phaseActive {
			h.logger.Info("Retrying proxy with fresh pod IP", "workspaceID", workspaceID, "oldIP", podIP, "newIP", freshWS.Status.PodIP)
			proxyErr = h.doProxy(c, freshWS.Status.PodIP, targetPath, password, bodyBytes, stripPatch)
		}
	}

	if proxyErr != nil {
		h.logger.Error("Proxy request failed", proxyErr, "workspaceID", workspaceID)
		if isWriteOp && sessionID != "" {
			h.removeActiveSession(workspaceID, sessionID)
		}
		// Only write an error response if headers have not yet been committed.
		// For streaming responses (SSE, prompt_async) the status code was already
		// flushed — calling c.JSON here would append JSON to the partial stream,
		// producing an invalid response. Log and return; the client's stream is
		// already truncated and will need to reconnect. (Epic 25 B2)
		if !c.Writer.Written() {
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":      "workspace connection failed",
				"retryAfter": retryAfterSec,
			})
		}
		return
	}

	if h.activityTracker != nil {
		h.activityTracker.Record(workspaceID)
	}

	if h.sessionIndex != nil && sessionID != "" && isWriteOp {
		h.sessionIndex.RecordMessage(workspaceID, sessionID, "", time.Now())
	}
}

// doProxy sends the request to the sandbox and writes the response back to
// the client. When stripPatch is true, JSON responses with status 2xx are
// buffered in memory so parts of type=="patch" can be removed before being
// sent to the client. Streaming endpoints (events, prompt_async) must always
// be invoked with stripPatch=false.
func (h *ProxyHandler) doProxy(c *gin.Context, podIP, targetPath, password string, body []byte, stripPatch bool) error {
	targetURL := fmt.Sprintf("http://%s:%d%s", podIP, opencodePort, targetPath)
	if forwardedQuery := stripVerboseQuery(c.Request.URL.RawQuery); forwardedQuery != "" {
		targetURL += "?" + forwardedQuery
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, bodyReader)
	if err != nil {
		return fmt.Errorf("creating proxy request: %w", err)
	}

	for k, vs := range c.Request.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.SetBasicAuth("opencode", password)
	req.Header.Set("X-Forwarded-For", c.ClientIP())

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("proxy request to workspace: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// US-23.4: If upstream returns 401, the password is stale (e.g., rotated
	// after Failed→recovery). Convert to 502 without forwarding
	// WWW-Authenticate (which would trigger a browser basic-auth dialog).
	// Proactively invalidate the password cache so the next request fetches fresh.
	if resp.StatusCode == http.StatusUnauthorized {
		wsID := c.Param("id")
		h.invalidateCaches(wsID)
		h.logger.Warn("Upstream auth failed; password cache invalidated",
			"workspaceID", wsID, "path", targetPath)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":       "upstream authentication failed; please retry",
			"workspaceID": wsID,
		})
		return nil
	}

	// Determine whether to filter the response. Filtering only applies when
	// the caller asked, the response is JSON, and the upstream succeeded.
	contentType := resp.Header.Get("Content-Type")
	isJSON := strings.Contains(contentType, "application/json")
	shouldFilter := stripPatch && isJSON && resp.StatusCode >= 200 && resp.StatusCode < 300

	if shouldFilter {
		// Bound the read to prevent OOM on a misbehaving upstream. (Epic 25 G1)
		// 32 MB matches the limit already established in models.go for provider responses.
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxNonStreamingResponseBytes))
		if readErr != nil {
			return fmt.Errorf("reading workspace response: %w", readErr)
		}
		if int64(len(raw)) >= maxNonStreamingResponseBytes {
			return fmt.Errorf("workspace response exceeds %d-byte limit", maxNonStreamingResponseBytes)
		}
		filtered, filterErr := stripPatchParts(raw)
		if filterErr != nil {
			h.logger.Warn("Failed to filter response, returning original", "error", filterErr.Error())
			filtered = raw
		}
		// Copy safe headers, then overwrite Content-Length to match filtered body.
		copyResponseHeaders(resp.Header, c.Writer.Header())
		c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(filtered)))
		c.Writer.WriteHeader(resp.StatusCode)
		_, _ = c.Writer.Write(filtered)
		return nil
	}

	copyResponseHeaders(resp.Header, c.Writer.Header())
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(resp.StatusCode)

	flusher, canFlush := c.Writer.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = c.Writer.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			// io.EOF and io.ErrUnexpectedEOF are both normal stream termination
			// signals. HTTP/1.1 responses without Content-Length use connection
			// close to signal end-of-body, which produces ErrUnexpectedEOF in
			// Go's HTTP client. SSE streams also terminate this way when the pod
			// restarts cleanly. Treat both as normal completion. (Epic 25 B2)
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
			// Any other error is a true mid-stream failure. Headers are already
			// flushed (status 200), so we cannot change the status code.
			// Write an SSE error event so the client can distinguish "pod died"
			// from "clean stream end" and trigger reconnect logic. (Epic 25 B2)
			const sseErrEvent = "event: error\ndata: {\"error\":\"upstream connection lost\"}\n\n"
			_, _ = c.Writer.Write([]byte(sseErrEvent))
			if canFlush {
				flusher.Flush()
			}
			return fmt.Errorf("upstream stream cut short: %w", readErr)
		}
	}

	return nil
}

// blockedResponseHeaders are headers from upstream that must never be
// forwarded to the browser. WWW-Authenticate triggers a basic-auth dialog;
// Set-Cookie and Proxy-Authenticate are security-sensitive.
var blockedResponseHeaders = map[string]bool{
	"Www-Authenticate":   true,
	"Proxy-Authenticate": true,
	"Set-Cookie":         true,
}

// copyResponseHeaders copies upstream response headers to the client writer,
// stripping any headers in blockedResponseHeaders. This prevents the browser
// from receiving WWW-Authenticate (which triggers a basic-auth dialog) when
// the proxy's cached password is stale (US-23.4).
func copyResponseHeaders(src http.Header, dst http.Header) {
	for k, vs := range src {
		if blockedResponseHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// stripVerboseQuery removes the "verbose" query parameter from the raw query
// string. The verbose flag is consumed by the API proxy and must not be
// forwarded to opencode. Returns the remaining query string with "verbose"
// entries removed; preserves the order of remaining parameters.
func stripVerboseQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		// On parse failure, return the original — we'd rather forward an
		// unparseable query and let opencode reject it than swallow it.
		return rawQuery
	}
	values.Del("verbose")
	// Strip workspace routing params — LLMSafeSpace manages workspace routing
	// at the pod level (OPENCODE_WORKSPACE_ID env var), not via query params.
	// opencode v1.15+ validates these with Effect Schema and returns 400 for
	// invalid values, so stripping prevents accidental client-originated errors.
	values.Del("workspace")
	values.Del("directory")
	return values.Encode()
}

// stripPatchParts removes any element where "type" == "patch" from a "parts"
// array. It handles both shapes opencode returns:
//   - {"info": ..., "parts": [...]}  (single message)
//   - [{"info":..., "parts":[...]}, ...]  (history)
//
// Returns the original bytes unchanged if the body is neither shape.
func stripPatchParts(body []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body, nil
	}

	switch trimmed[0] {
	case '{':
		var msg messageEnvelope
		if err := json.Unmarshal(body, &msg); err != nil {
			return nil, err
		}
		if msg.Parts == nil {
			// No parts field — pass through as-is to avoid mangling unrelated
			// JSON objects (e.g. error responses).
			return body, nil
		}
		msg.Parts = filterOutPatch(msg.Parts)
		return json.Marshal(msg)
	case '[':
		var msgs []messageEnvelope
		if err := json.Unmarshal(body, &msgs); err != nil {
			return nil, err
		}
		filteredAny := false
		for i, m := range msgs {
			if m.Parts != nil {
				msgs[i].Parts = filterOutPatch(m.Parts)
				filteredAny = true
			}
		}
		if !filteredAny {
			return body, nil
		}
		return json.Marshal(msgs)
	default:
		return body, nil
	}
}

// messageEnvelope is the minimal shape used to filter parts. Other fields
// are preserved via json.RawMessage.
type messageEnvelope struct {
	Info  json.RawMessage   `json:"info,omitempty"`
	Parts []json.RawMessage `json:"parts"`
}

// filterOutPatch returns a slice with patch parts removed. Each element is
// inspected for a "type" field; if it equals "patch", it is dropped.
func filterOutPatch(parts []json.RawMessage) []json.RawMessage {
	if len(parts) == 0 {
		return parts
	}
	out := make([]json.RawMessage, 0, len(parts))
	for _, p := range parts {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(p, &probe); err != nil {
			// Couldn't parse this entry — keep it (don't silently drop unknown shapes).
			out = append(out, p)
			continue
		}
		if probe.Type == "patch" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (h *ProxyHandler) getPassword(ctx context.Context, workspaceID string) (string, error) {
	h.pwCacheMu.RLock()
	if pw, ok := h.pwCache[workspaceID]; ok {
		h.pwCacheMu.RUnlock()
		return pw, nil
	}
	h.pwCacheMu.RUnlock()

	secretName := fmt.Sprintf("workspace-pw-%s", workspaceID)
	secret, err := h.k8sClient.Clientset().CoreV1().Secrets(h.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading password secret %s: %w", secretName, err)
	}

	pw := string(secret.Data["password"])
	if pw == "" {
		return "", fmt.Errorf("password secret %s has empty password key", secretName)
	}

	h.pwCacheMu.Lock()
	h.pwCache[workspaceID] = pw
	h.pwCacheMu.Unlock()

	return pw, nil
}

func (h *ProxyHandler) checkAndAddActiveSession(workspaceID, sessionID string, maxSessions int) bool {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()

	if h.activeSess[workspaceID] == nil {
		h.activeSess[workspaceID] = make(map[string]bool)
	}

	if h.activeSess[workspaceID][sessionID] {
		return true
	}

	if len(h.activeSess[workspaceID]) >= maxSessions {
		return false
	}

	h.activeSess[workspaceID][sessionID] = true
	return true
}

func (h *ProxyHandler) removeActiveSession(workspaceID, sessionID string) {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	if sessions, ok := h.activeSess[workspaceID]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(h.activeSess, workspaceID)
		}
	}
}

func (h *ProxyHandler) activeSessionCount(workspaceID string) int {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	return len(h.activeSess[workspaceID])
}

func (h *ProxyHandler) acquireConnection(workspaceID string) bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[workspaceID] >= maxConnectionsPerWorkspace {
		return false
	}
	h.connCount[workspaceID]++
	return true
}

func (h *ProxyHandler) releaseConnection(workspaceID string) {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[workspaceID] > 0 {
		h.connCount[workspaceID]--
	}
	if h.connCount[workspaceID] == 0 {
		delete(h.connCount, workspaceID)
	}
}

func (h *ProxyHandler) connectionCount(workspaceID string) int {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	return h.connCount[workspaceID]
}

func (h *ProxyHandler) invalidateCaches(workspaceID string) {
	h.pwCacheMu.Lock()
	delete(h.pwCache, workspaceID)
	h.pwCacheMu.Unlock()

	h.wsConfigMu.Lock()
	delete(h.wsConfig, workspaceID)
	h.wsConfigMu.Unlock()

	h.activeMu.Lock()
	delete(h.activeSess, workspaceID)
	h.activeMu.Unlock()

	if h.sessionParents != nil {
		h.sessionParents.invalidate(workspaceID)
	}

	h.parentBackfilledMu.Lock()
	delete(h.parentBackfilled, workspaceID)
	h.parentBackfilledMu.Unlock()
}

func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
	phase := workspace.Status.Phase

	// Track prior phase for Active-from-non-Active detection (US-23.4).
	h.priorPhaseMu.Lock()
	prior := h.priorPhase[workspace.Name]
	h.priorPhase[workspace.Name] = string(phase)
	h.priorPhaseMu.Unlock()

	// S28.4: Publish workspace.phase to user-scoped stream only (hard cutover).
	// The workspace session stream no longer carries phase events.
	if h.userBroker != nil && workspace.Spec.Owner.UserID != "" {
		h.userBroker.RecordWorkspaceOwner(workspace.Name, workspace.Spec.Owner.UserID)
		h.userBroker.PublishToUser(workspace.Spec.Owner.UserID, WorkspaceSSEEvent{
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
		// Clean up priorPhase for terminal phases.
		if phase == phaseTerminated || phase == phaseTerminating {
			h.priorPhaseMu.Lock()
			delete(h.priorPhase, workspace.Name)
			h.priorPhaseMu.Unlock()

			// Remove activity tracker entry so deleted workspaces do not
			// accumulate unboundedly in the tracker map. (Epic 25 B5)
			if h.activityTracker != nil {
				h.activityTracker.Delete(workspace.Name)
			}
		}
		return
	}

	// US-23.4: Invalidate pwCache on Failed (password secret is deleted by
	// cleanupFailedWorkspaceSecrets; ensurePasswordSecret regenerates a new
	// one on recovery).
	if phase == v1.WorkspacePhaseFailed {
		h.invalidateCaches(workspace.Name)
		return
	}

	if phase == phaseActive {
		// US-23.4: Password may have rotated during a non-Active interval
		// (e.g., ensurePasswordSecret regenerated it after Failed→recovery).
		// Invalidate pwCache when transitioning Active-from-non-Active.
		if prior != "" && prior != string(phaseActive) {
			h.invalidateCaches(workspace.Name)
			// The SSETracker may have been started while the workspace was
			// still Creating/Resuming and hit "no pod IP" repeatedly, backing
			// off up to 30s. Now that the pod is Active and has an IP, reset
			// the subscription so it reconnects immediately instead of waiting
			// out the current backoff interval. Without this, the first user
			// message after workspace startup can have its idle event dropped
			// because the tracker hasn't successfully connected to the pod yet.
			if h.sseTracker != nil {
				h.sseTracker.StopWatching(workspace.Name)
				h.sseTracker.EnsureWatching(workspace.Name)
			}
		} else {
			// Active→Active reconcile (no transition). Only wsConfig may be stale.
			h.wsConfigMu.Lock()
			delete(h.wsConfig, workspace.Name)
			h.wsConfigMu.Unlock()
		}
	}
}

func (h *ProxyHandler) onSessionIdle(workspaceID, sessionID string) {
	h.removeActiveSession(workspaceID, sessionID)

	if h.broker != nil {
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "idle",
		})
	}

	if h.userBroker != nil {
		if userID := h.userBroker.WorkspaceOwner(workspaceID); userID != "" {
			h.userBroker.PublishToUser(userID, WorkspaceSSEEvent{
				Type:        "session.status",
				WorkspaceID: workspaceID,
				SessionID:   sessionID,
				Status:      "idle",
			})
		}
	}

	if h.activityTracker != nil {
		h.wsConfigMu.RLock()
		cfg, ok := h.wsConfig[workspaceID]
		h.wsConfigMu.RUnlock()
		if ok && cfg.workspaceID != "" {
			h.activityTracker.Record(cfg.workspaceID)
			// Record message in session index
			if h.sessionIndex != nil {
				h.sessionIndex.RecordMessage(cfg.workspaceID, sessionID, "", time.Now())
				go h.fetchAndPersistTitle(cfg.workspaceID, sessionID)
			}
		}
	}
}

// SetSessionIndex injects the session index service for recording message activity.
func (h *ProxyHandler) SetSessionIndex(si interfaces.SessionIndexService) {
	h.sessionIndex = si
}

// fetchAndPersistTitle fetches the session title from the opencode agent and
// upserts it into the session index. Intended to be called in a goroutine
// after SendMessage completes, so the sidebar shows the auto-generated title
// without requiring the frontend to make a separate request.
func (h *ProxyHandler) fetchAndPersistTitle(workspaceID, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil || workspace.Status.PodIP == "" {
		return
	}
	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		return
	}

	url := fmt.Sprintf("http://%s:%d/session/%s", workspace.Status.PodIP, opencodePort, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth("opencode", password)

	resp, err := h.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var session struct {
		Title    string `json:"title"`
		ParentID string `json:"parentID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return
	}

	if session.Title != "" {
		if err := h.sessionIndex.UpsertTitle(context.Background(), workspaceID, sessionID, session.Title); err != nil {
			h.logger.Error("Failed to persist session title", err, "workspaceID", workspaceID, "sessionID", sessionID)
		}
	}
	// Mirror parentID to the session index so the sidebar can render the
	// hierarchy. opencode subagent (subtask) sessions have parentID set;
	// top-level sessions don't, and we leave the column NULL for those.
	if session.ParentID != "" {
		if err := h.sessionIndex.UpsertParent(context.Background(), workspaceID, sessionID, session.ParentID); err != nil {
			h.logger.Error("Failed to persist session parent", err, "workspaceID", workspaceID, "sessionID", sessionID)
		}
	}
}

// BackfillSessionParents reconciles session_index.parent_session_id with the
// authoritative parentID values from opencode's GET /session list. Used to
// catch sessions that pre-date the parent_session_id migration (or that
// missed the SSE session.updated stream during a restart). Idempotent and
// per-process: subsequent calls for the same workspace are no-ops until
// invalidateCaches drops the marker (e.g. on workspace suspend/restart).
//
// Non-blocking: the caller (typically the sidebar's session-list endpoint)
// gets a fast response from the DB and the backfill runs in the background.
// Failures are logged at debug, never surfaced — the worst outcome of a
// failed backfill is that subagent sessions render at the top level until
// the next opportunity refreshes them.
func (h *ProxyHandler) BackfillSessionParents(workspaceID string) {
	if h.sessionIndex == nil || h.dialect == nil {
		return
	}
	h.parentBackfilledMu.Lock()
	if _, done := h.parentBackfilled[workspaceID]; done {
		h.parentBackfilledMu.Unlock()
		return
	}
	// Mark BEFORE the goroutine fires so concurrent callers don't queue
	// duplicate backfills. If the goroutine fails, invalidateCaches will
	// reset this on the next workspace lifecycle event.
	h.parentBackfilled[workspaceID] = struct{}{}
	h.parentBackfilledMu.Unlock()

	go h.runParentBackfill(workspaceID)
}

func (h *ProxyHandler) runParentBackfill(workspaceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil || workspace.Status.Phase != phaseActive || workspace.Status.PodIP == "" {
		// Pod not reachable — drop the marker so the next call retries.
		h.parentBackfilledMu.Lock()
		delete(h.parentBackfilled, workspaceID)
		h.parentBackfilledMu.Unlock()
		return
	}

	password, err := h.getPassword(ctx, workspaceID)
	if err != nil {
		h.parentBackfilledMu.Lock()
		delete(h.parentBackfilled, workspaceID)
		h.parentBackfilledMu.Unlock()
		return
	}

	url := fmt.Sprintf("http://%s:%d%s", workspace.Status.PodIP, opencodePort, h.dialect.SessionListPath())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth("opencode", password)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Debug("Backfill: session list fetch failed", "workspaceID", workspaceID, "error", err)
		h.parentBackfilledMu.Lock()
		delete(h.parentBackfilled, workspaceID)
		h.parentBackfilledMu.Unlock()
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		h.parentBackfilledMu.Lock()
		delete(h.parentBackfilled, workspaceID)
		h.parentBackfilledMu.Unlock()
		return
	}

	var sessions []struct {
		ID       string `json:"id"`
		ParentID string `json:"parentID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return
	}

	written := 0
	for _, s := range sessions {
		if s.ID == "" || s.ParentID == "" {
			continue
		}
		if err := h.sessionIndex.UpsertParent(context.Background(), workspaceID, s.ID, s.ParentID); err != nil {
			h.logger.Debug("Backfill: upsert parent failed", "workspaceID", workspaceID, "sessionID", s.ID, "error", err)
			continue
		}
		written++
	}
	if written > 0 {
		h.logger.Info("Backfilled session parents", "workspaceID", workspaceID, "count", written)
	}
}

// GetActiveSessions returns the active session IDs for a workspace.
// This is a per-replica view (not globally consistent across API replicas).
func (h *ProxyHandler) GetActiveSessions(workspaceID string) []string {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	sessions := h.activeSess[workspaceID]
	if sessions == nil {
		return nil
	}
	result := make([]string, 0, len(sessions))
	for sid := range sessions {
		result = append(result, sid)
	}
	return result
}

func (h *ProxyHandler) SetActiveSessionsForTest(workspaceID string, sessionIDs []string) {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	m := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		m[id] = true
	}
	h.activeSess[workspaceID] = m
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
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "busy",
		})
	}

	if h.userBroker != nil {
		if userID := h.userBroker.WorkspaceOwner(workspaceID); userID != "" {
			h.userBroker.PublishToUser(userID, WorkspaceSSEEvent{
				Type:        "session.status",
				WorkspaceID: workspaceID,
				SessionID:   sessionID,
				Status:      "busy",
			})
		}
	}
}

func (h *ProxyHandler) onRawEvent(workspaceID, eventType, rawData string) {
	// Forward event to browser SSE subscribers
	if h.broker != nil {
		var parsed interface{}
		_ = json.Unmarshal([]byte(rawData), &parsed)
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type:      "opencode.event",
			EventType: eventType,
			Data:      parsed,
		})
	}

	// Persist session title to DB when opencode emits session.updated with a title
	if eventType == "session.updated" && h.sessionIndex != nil {
		h.persistTitleFromEvent(workspaceID, rawData)
	}

	// Persist prompt token count to DB for durable context_used tracking
	if eventType == "session.next.step.ended" {
		h.persistContextFromEvent(workspaceID, rawData)
	}

	// Emit normalized input request events for questions/permissions
	if h.dialect != nil {
		h.emitNormalizedInputEvent(workspaceID, eventType, rawData)
	}
}

// emitNormalizedInputEvent detects question/permission events from the agent
// and publishes stable, agent-agnostic events for the frontend.
//
// rawData is the full opencode SSE envelope as captured from the wire:
//
//	{"type":"permission.asked","properties":{"id":"per_...","sessionID":"ses_...",...}}
//
// The dialect parsers expect the inner properties object, so we unwrap the
// envelope here before dispatching. Per US-16.3 design.
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
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type: "agent.question",
			Data: req,
		})
	} else if h.dialect.IsQuestionResolved(eventType) {
		var resolution struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(properties, &resolution)
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
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

		// Auto-approve if workspace has the setting enabled
		if h.shouldAutoApprovePermissions(workspaceID) {
			go h.autoApprovePermission(workspaceID, req.ID)
			return
		}

		req.RootSessionID = h.resolveRootSessionID(workspaceID, req.SessionID)
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
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
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type: "agent.permission.resolved",
			Data: map[string]string{
				"request_id": resolution.ID,
				"session_id": resolution.SessionID,
				"reply":      resolution.Reply,
			},
		})
	}
}

// resolveRootSessionID returns the top-level (root) session for a sessionID
// by walking the parent chain. Falls back to sessionID itself if resolution
// fails (e.g. workspace pod unreachable, password unavailable, or sessionParents
// cache is not configured). The fallback ensures top-level sessions and any
// transient lookup failure still produce a usable event for the frontend.
//
// Resolution uses a short context timeout so a stalled pod cannot block the
// SSE event loop.
func (h *ProxyHandler) resolveRootSessionID(workspaceID, sessionID string) string {
	if h.sessionParents == nil || sessionID == "" {
		return sessionID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return h.sessionParents.resolveRoot(ctx, workspaceID, sessionID)
}

// shouldAutoApprovePermissions checks the workspace CRD for the auto-approve setting.
// Uses the wsConfig cache; falls back to a K8s read on cache miss.
func (h *ProxyHandler) shouldAutoApprovePermissions(workspaceID string) bool {
	h.wsConfigMu.RLock()
	if cfg, ok := h.wsConfig[workspaceID]; ok {
		h.wsConfigMu.RUnlock()
		return cfg.autoApprovePermissions
	}
	h.wsConfigMu.RUnlock()

	workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		return false // fail closed
	}

	h.wsConfigMu.Lock()
	cfg := h.wsConfig[workspaceID]
	cfg.workspaceID = workspaceID
	cfg.autoApprovePermissions = workspace.Spec.AutoApprovePermissions
	cfg.maxActiveSessions = int(workspace.Spec.MaxActiveSessions)
	h.wsConfig[workspaceID] = cfg
	h.wsConfigMu.Unlock()

	return workspace.Spec.AutoApprovePermissions
}

// autoApprovePermission sends a POST to the pod to approve a permission request.
func (h *ProxyHandler) autoApprovePermission(workspaceID, requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
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

// persistTitleFromEvent extracts the session title and parentID from a
// session.updated SSE event and writes them to the session index. This
// ensures PostgreSQL always has the latest title and the
// (sessionID → parentID) mapping needed for the sidebar hierarchy without
// requiring a separate fetch from opencode.
func (h *ProxyHandler) persistTitleFromEvent(workspaceID, rawData string) {
	// opencode session.updated event shape (both v1.2 and v1.15):
	//   {"type":"session.updated","properties":{"sessionID":"ses_...","info":{"id":"ses_...","title":"...","parentID":"ses_..."}}}
	// v1.15 also has a top-level "id" field (ignored by Go JSON decoder).
	// parentID is omitted on top-level sessions; present on subagent (subtask) sessions.
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

// persistContextFromEvent extracts the prompt token count from a
// session.next.step.ended SSE event and persists it to session_index.
// This makes context_used durable across pod restarts and available for
// any session regardless of whether opencode currently has it in memory.
//
// promptTokens = tokens.input + tokens.cache.read + tokens.cache.write
// (the raw prompt size for the last LLM step — not cumulative).
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
	workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if workspace.Status.Phase != phaseActive {
		return ""
	}
	return workspace.Status.PodIP
}

// GetWorkspaceCRD retrieves a Workspace CRD by name. Used by the ownership
// middleware in the router to verify sandbox ownership before proxying.
func (h *ProxyHandler) GetWorkspaceCRD(workspaceID string) (*v1.Workspace, error) {
	return h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "network is unreachable")
}

// sessionIDPattern is the strict allow-list for the `:sessionId` URL
// parameter. Closes F1.1.2 (Epic 17 Phase 1) and RT-2.16 (Phase 2):
// pre-fix the value was concatenated into the upstream URL path without
// validation, so a user with
//
//	sessionId = "../../../v1/admin"
//
// could address an arbitrary upstream endpoint. The pattern accepts
// only alphanumerics, dot, dash, underscore — covers UUIDs, opencode
// session IDs (sess_*), and any other shape a legitimate client would
// produce.
//
// Length cap 128 is generous: a UUID is 36 chars; opencode session IDs
// are around 40-60. 128 leaves headroom for a longer scheme without
// being unbounded.
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

// GetSSETracker returns the proxy's SSE tracker (for drain mode wiring).
// Returns nil if the tracker hasn't been initialized yet.
func (h *ProxyHandler) GetSSETracker() *SSETracker {
	return h.sseTracker
}

// GetPasswordGetter returns the proxy's password getter function (for drain mode).
func (h *ProxyHandler) GetPasswordGetter() func(ctx context.Context, workspaceID string) (string, error) {
	return h.getPassword
}

// SetAgentStateChecker installs the checker used to enrich error responses
// with agentNeedsRefresh hints (Epic 27b US-27b.5).
func (h *ProxyHandler) SetAgentStateChecker(c AgentStateChecker) {
	h.agentStateChecker = c
}
