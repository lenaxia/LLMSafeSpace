// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/services/activity"
	"github.com/lenaxia/llmsafespace/api/internal/services/eventbroker"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/sse"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	"github.com/lenaxia/llmsafespace/pkg/agent"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

const (
	defaultMaxActiveSessions   = 5
	maxConnectionsPerWorkspace = 10
	opencodePort               = agentd.AgentPort
	retryAfterSec              = 10

	phaseActive      = v1.WorkspacePhaseActive
	phaseSuspending  = "Suspending"
	phaseSuspended   = "Suspended"
	phaseTerminating = "Terminating"
	phaseTerminated  = "Terminated"
)

type workspaceConfig struct {
	maxActiveSessions      int
	autoApprovePermissions bool
}

type ProxyHandler struct {
	k8sClient         pkginterfaces.KubernetesClient
	httpClient        *http.Client
	logger            pkginterfaces.LoggerInterface
	namespace         string
	dialect           agent.Dialect
	agentStateChecker AgentStateChecker

	pwCache   map[string]string
	pwCacheMu sync.RWMutex

	wsConfig   map[string]workspaceConfig
	wsConfigMu sync.RWMutex

	priorPhase   map[string]string
	priorPhaseMu sync.Mutex

	activeSess map[string]map[string]bool
	activeMu   sync.RWMutex

	connCount map[string]int
	connMu    sync.RWMutex

	activityTracker *activity.ActivityTracker
	watcher         *workspace.Watcher
	sseTracker      *sse.Tracker
	sessionIndex    interfaces.SessionIndexService
	broker          *eventbroker.WorkspaceEventBroker
	userBroker      *eventbroker.UserEventBroker
	sessionParents  *sessionParentCache

	parentBackfilled   map[string]struct{}
	parentBackfilledMu sync.Mutex

	// deletedSessions tracks sessions that were explicitly deleted via the API.
	// Late SSE events (session.updated, idle, step.ended) from opencode that
	// arrive after deletion are suppressed to prevent re-inserting the session
	// into session_index. Keyed by "workspaceID/sessionID".
	deletedSessions   map[string]struct{}
	deletedSessionsMu sync.RWMutex

	meteringSvc interfaces.MeteringService

	// versionSyncCb is the callback wired into the CRD watcher to persist
	// runtime version info (imageTag) to the DB whenever a workspace becomes
	// Active. Set via SetVersionSyncCallback before Start().
	versionSyncCb workspace.VersionSyncCallback

	queueSvc interfaces.MessageQueueService

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
		deletedSessions:  make(map[string]struct{}),
	}, nil
}

func (h *ProxyHandler) proxyToWorkspace(c *gin.Context, targetPath string, isWriteOp bool, sessionID string) {
	h.proxyToWorkspaceWithErrBody(c, targetPath, isWriteOp, sessionID, nil)
}

// proxyToWorkspaceWithErrBody behaves like proxyToWorkspace but optionally
// rewrites the response body on 4xx/5xx. When onErrorBody is non-nil and the
// upstream returns status >= 400, the response body is buffered (up to
// chatErrorBufferCap bytes), passed through onErrorBody, and the transformed
// bytes are written to the client. Used by SendMessage (US-27b.5) to inject
// the agentNeedsRefresh / hint fields when the agent fails with staged
// credentials pending. 2xx responses stream as before (no buffering).
func (h *ProxyHandler) proxyToWorkspaceWithErrBody(
	c *gin.Context,
	targetPath string,
	isWriteOp bool,
	sessionID string,
	onErrorBody func(statusCode int, body []byte) []byte,
) {
	workspaceID := c.Param("id")
	if workspaceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace ID required"})
		return
	}

	var workspace *v1.Workspace
	if cached, exists := c.Get("workspace"); exists {
		if sb, ok := cached.(*v1.Workspace); ok {
			workspace = sb
		}
	}
	if workspace == nil {
		v1Client, v1Err := h.k8sClient.LlmsafespaceV1()
		if v1Err != nil {
			h.logger.Error("Failed to get LLMSafespaceV1 client", v1Err, "workspaceID", workspaceID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		var err error
		workspace, err = v1Client.Workspaces(h.namespace).Get(c.Request.Context(), workspaceID, metav1.GetOptions{})
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

	if h.meteringSvc != nil && workspaceID != "" {
		userID, _ := extractAuth(c)
		if userID != "" && workspace.Labels["llmsafespace.dev/canary"] != "true" {
			owner := types.BillingOwner{ID: userID, Type: types.OwnerTypeUser}
			allowed, _, qerr := h.meteringSvc.CheckQuota(c.Request.Context(), owner, "llm_request")
			if qerr != nil {
				h.logger.Warn("Quota check failed, allowing request", "error", qerr, "user_id", userID)
			} else if !allowed {
				metrics.RecordQuotaExceeded("llm_request")
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "quota exceeded", "event_type": "llm_request"})
				return
			}
		}
	}

	proxyErr := h.doProxy(c, podIP, targetPath, password, bodyBytes, onErrorBody)

	if proxyErr != nil && isConnectionError(proxyErr) && !c.Writer.Written() {
		freshWS, getErr := func() (*v1.Workspace, error) {
			v1Client, v1Err := h.k8sClient.LlmsafespaceV1()
			if v1Err != nil {
				return nil, v1Err
			}
			return v1Client.Workspaces(h.namespace).Get(c.Request.Context(), workspaceID, metav1.GetOptions{})
		}()
		if getErr == nil && freshWS.Status.PodIP != "" && freshWS.Status.PodIP != podIP && freshWS.Status.Phase == phaseActive {
			h.logger.Info("Retrying proxy with fresh pod IP", "workspaceID", workspaceID, "oldIP", podIP, "newIP", freshWS.Status.PodIP)
			proxyErr = h.doProxy(c, freshWS.Status.PodIP, targetPath, password, bodyBytes, onErrorBody)
		}
	}

	if proxyErr != nil {
		h.logger.Error("Proxy request failed", proxyErr, "workspaceID", workspaceID)
		if isWriteOp && sessionID != "" {
			h.removeActiveSession(workspaceID, sessionID)
		}
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

	if h.meteringSvc != nil && workspaceID != "" {
		userID, _ := extractAuth(c)
		if userID != "" && workspace.Labels["llmsafespace.dev/canary"] != "true" {
			h.meteringSvc.Record(types.UsageEvent{
				IdempotencyKey: fmt.Sprintf("llmreq:%s:%d", workspaceID, time.Now().UnixNano()),
				Owner:          types.BillingOwner{ID: userID, Type: types.OwnerTypeUser},
				ActorID:        userID,
				WorkspaceID:    workspaceID,
				EventType:      "llm_request",
				EventSubtype:   "message",
				Quantity:       1,
				Source:         "api",
				EventTime:      time.Now(),
				RequestContext: map[string]any{
					"ip":         c.ClientIP(),
					"request_id": c.GetString("request_id"),
					"session_id": sessionID,
				},
			})
		}
	}
}

// chatErrorBufferCap bounds the amount of upstream body buffered when an
// onErrorBody transform is supplied. Chat error responses are small JSON
// payloads (~1 KB); a runaway upstream must not consume unbounded memory.
// Truncation is handled by EnrichChatErrorBody (non-JSON wraps to a 1024-byte
// "message" field), so anything above this cap is dropped on the floor.
const chatErrorBufferCap = 64 * 1024

// doProxy sends the request to the sandbox and writes the response back to
// the client. Streaming endpoints (events, prompt_async) are streamed
// directly to the client with flushed writes.
//
// When onErrorBody is non-nil and the upstream returns status >= 400, the
// response body is buffered (up to chatErrorBufferCap), passed through
// onErrorBody, and the transformed bytes are written. This is the US-27b.5
// path that lets SendMessage enrich chat errors with agentNeedsRefresh / hint
// fields. 2xx responses always stream chunk-by-chunk.
func (h *ProxyHandler) doProxy(c *gin.Context, podIP, targetPath, password string, body []byte, onErrorBody func(int, []byte) []byte) error {
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

	copyResponseHeaders(resp.Header, c.Writer.Header())
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// US-27b.5: when an error-body transform is supplied AND the upstream
	// returned an error status, buffer the body (bounded), transform, write.
	// 2xx / 3xx always stream chunk-by-chunk regardless of onErrorBody.
	if onErrorBody != nil && resp.StatusCode >= 400 {
		buf := make([]byte, 0, 4*1024)
		tmp := make([]byte, 32*1024)
		for {
			n, readErr := resp.Body.Read(tmp)
			if n > 0 {
				if len(buf)+n > chatErrorBufferCap {
					buf = append(buf, tmp[:chatErrorBufferCap-len(buf)]...)
				} else {
					buf = append(buf, tmp[:n]...)
				}
			}
			if readErr != nil {
				break
			}
			if len(buf) >= chatErrorBufferCap {
				break
			}
		}
		transformed := onErrorBody(resp.StatusCode, buf)
		// Content-Length is now potentially wrong; drop it and let the writer
		// send chunked encoding or fixate on the new length.
		c.Writer.Header().Del("Content-Length")
		c.Writer.WriteHeader(resp.StatusCode)
		_, _ = c.Writer.Write(transformed)
		return nil
	}

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
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
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
