package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	k8sClient  pkginterfaces.KubernetesClient
	httpClient *http.Client
	logger     pkginterfaces.LoggerInterface
	namespace  string
	dialect    agent.Dialect

	pwCache   map[string]string
	pwCacheMu sync.RWMutex

	wsConfig   map[string]workspaceConfig
	wsConfigMu sync.RWMutex

	activeSess map[string]map[string]bool
	activeMu   sync.Mutex

	connCount map[string]int
	connMu    sync.Mutex

	activityTracker *ActivityTracker
	watcher         *WorkspaceWatcher
	sseTracker      *SSETracker
	sessionIndex    interfaces.SessionIndexService
	broker          *WorkspaceEventBroker

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
				ResponseHeaderTimeout: 300 * time.Second,
			},
		}
	}
	return &ProxyHandler{
		k8sClient:  k8sClient,
		httpClient: httpClient,
		logger:     logger,
		namespace:  namespace,
		dialect:    dialect,
		pwCache:    make(map[string]string),
		wsConfig:   make(map[string]workspaceConfig),
		activeSess: make(map[string]map[string]bool),
		connCount:  make(map[string]int),
	}, nil
}

func (h *ProxyHandler) Start() error {
	var startErr error
	h.startOnce.Do(func() {
		h.broker = NewWorkspaceEventBroker()

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
			h.activityTracker.Stop()
			startErr = fmt.Errorf("creating CRD watcher: %w", err)
			return
		}
		if err := watcher.Start(); err != nil {
			h.activityTracker.Stop()
			startErr = fmt.Errorf("starting CRD watcher: %w", err)
			return
		}
		h.watcher = watcher
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
			h.activityTracker.Stop()
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
	wid := c.Param("id")
	h.proxyToWorkspace(c, "/session/"+sid+"/message", true, sid)
	// After the message round-trip completes, persist the opencode-generated
	// title to the session index so the sidebar reflects it immediately.
	if c.Writer.Status() < 300 && h.sessionIndex != nil {
		go h.fetchAndPersistTitle(wid, sid)
	}
}

func (h *ProxyHandler) SendPromptAsync(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToWorkspace(c, "/session/"+sid+"/prompt_async", true, sid)
}

func (h *ProxyHandler) GetHistory(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToWorkspace(c, "/session/"+sid+"/message", false, sid)
}

func (h *ProxyHandler) GetSession(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToWorkspace(c, "/session/"+sid, false, sid)
}

func (h *ProxyHandler) AbortSession(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToWorkspace(c, "/session/"+sid+"/abort", false, sid)
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
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering when behind a proxy
	c.Writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := h.broker.Subscribe(workspaceID)
	defer h.broker.Unsubscribe(workspaceID, ch)

	// Start watching the workspace pod as soon as a browser subscribes so that
	// events are available immediately when the user sends a message, rather than
	// waiting for the first write operation to trigger EnsureWatching.
	if h.sseTracker != nil {
		h.sseTracker.EnsureWatching(workspaceID)
	}

	// Emit any pending input requests so reconnecting browsers see them immediately.
	if h.dialect != nil {
		go h.emitPendingInputRequests(workspaceID)
	}

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
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
		bodyBytes, err = io.ReadAll(c.Request.Body)
		c.Request.Body.Close()
		if err != nil {
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

	if proxyErr != nil && isConnectionError(proxyErr) {
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
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "workspace connection failed",
			"retryAfter": retryAfterSec,
		})
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
	defer resp.Body.Close()

	// Determine whether to filter the response. Filtering only applies when
	// the caller asked, the response is JSON, and the upstream succeeded.
	contentType := resp.Header.Get("Content-Type")
	isJSON := strings.Contains(contentType, "application/json")
	shouldFilter := stripPatch && isJSON && resp.StatusCode >= 200 && resp.StatusCode < 300

	if shouldFilter {
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("reading workspace response: %w", readErr)
		}
		filtered, filterErr := stripPatchParts(raw)
		if filterErr != nil {
			h.logger.Warn("Failed to filter response, returning original", "error", filterErr.Error())
			filtered = raw
		}
		// Copy headers, then overwrite Content-Length to match filtered body.
		for k, vs := range resp.Header {
			for _, v := range vs {
				c.Writer.Header().Add(k, v)
			}
		}
		c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(filtered)))
		c.Writer.WriteHeader(resp.StatusCode)
		c.Writer.Write(filtered)
		return nil
	}

	for k, vs := range resp.Header {
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(resp.StatusCode)

	flusher, canFlush := c.Writer.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			c.Writer.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}

	return nil
}

// stripVerboseQuery removes the "verbose" query parameter from a raw query
// string. The verbose flag is consumed by the API proxy and must not be
// forwarded to opencode (which would reject unknown query params on some
// endpoints). Returns the remaining query string with "verbose" entries
// removed; preserves the order of remaining parameters.
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

func (h *ProxyHandler) getMaxSessions(ctx context.Context, workspaceID, workspaceRef string) (workspaceConfig, error) {
	h.wsConfigMu.RLock()
	if cfg, ok := h.wsConfig[workspaceID]; ok {
		h.wsConfigMu.RUnlock()
		return cfg, nil
	}
	h.wsConfigMu.RUnlock()

	if workspaceRef == "" {
		return workspaceConfig{
			workspaceID:       "",
			maxActiveSessions: defaultMaxActiveSessions,
		}, nil
	}

	ws, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceRef, metav1.GetOptions{})
	if err != nil {
		h.logger.Warn("Failed to get workspace CRD, using defaults", "workspaceRef", workspaceRef)
		return workspaceConfig{
			workspaceID:       workspaceRef,
			maxActiveSessions: defaultMaxActiveSessions,
		}, nil
	}

	maxSessions := defaultMaxActiveSessions
	if ws.Spec.MaxActiveSessions > 0 {
		maxSessions = int(ws.Spec.MaxActiveSessions)
	}

	cfg := workspaceConfig{
		workspaceID:       workspaceRef,
		maxActiveSessions: maxSessions,
	}

	h.wsConfigMu.Lock()
	h.wsConfig[workspaceID] = cfg
	h.wsConfigMu.Unlock()

	return cfg, nil
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
}

func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
	phase := workspace.Status.Phase

	// Publish the phase change to all browser SSE subscribers.
	if h.broker != nil {
		h.broker.Publish(workspace.Name, WorkspaceSSEEvent{
			Type:  "workspace.phase",
			Phase: string(phase),
		})
	}

	if phase == phaseSuspending || phase == phaseSuspended || phase == phaseTerminating || phase == phaseTerminated {
		h.invalidateCaches(workspace.Name)
		if h.sseTracker != nil {
			h.sseTracker.StopWatching(workspace.Name)
		}
		return
	}
	if phase == phaseActive {
		h.wsConfigMu.Lock()
		delete(h.wsConfig, workspace.Name)
		h.wsConfigMu.Unlock()
	}
}

func (h *ProxyHandler) onSessionIdle(workspaceID, sessionID string) {
	h.removeActiveSession(workspaceID, sessionID)

	// Publish session idle event to browser SSE subscribers.
	if h.broker != nil {
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "idle",
		})
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
	defer resp.Body.Close()

	var session struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil || session.Title == "" {
		return
	}

	if err := h.sessionIndex.UpsertTitle(context.Background(), workspaceID, sessionID, session.Title); err != nil {
		h.logger.Error("Failed to persist session title", err, "workspaceID", workspaceID, "sessionID", sessionID)
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

func (h *ProxyHandler) onSessionActive(workspaceID, sessionID string) {
	h.wsConfigMu.RLock()
	cfg, ok := h.wsConfig[workspaceID]
	h.wsConfigMu.RUnlock()
	maxSessions := defaultMaxActiveSessions
	if ok {
		maxSessions = cfg.maxActiveSessions
	}
	h.checkAndAddActiveSession(workspaceID, sessionID, maxSessions)

	// Publish session busy event to browser SSE subscribers.
	if h.broker != nil {
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type:      "session.status",
			SessionID: sessionID,
			Status:    "busy",
		})
	}
}

func (h *ProxyHandler) onRawEvent(workspaceID, eventType, rawData string) {
	if h.broker == nil {
		return
	}
	var parsed interface{}
	json.Unmarshal([]byte(rawData), &parsed)
	h.broker.Publish(workspaceID, WorkspaceSSEEvent{
		Type:      "opencode.event",
		EventType: eventType,
		Data:      parsed,
	})

	// Persist session title to DB when opencode emits session.updated with a title
	if eventType == "session.updated" && h.sessionIndex != nil {
		h.persistTitleFromEvent(workspaceID, rawData)
	}

	// Emit normalized input request events for questions/permissions
	if h.dialect != nil {
		h.emitNormalizedInputEvent(workspaceID, eventType, rawData)
	}
}

// emitNormalizedInputEvent detects question/permission events from the agent
// and publishes stable, agent-agnostic events for the frontend.
func (h *ProxyHandler) emitNormalizedInputEvent(workspaceID, eventType, rawData string) {
	properties := json.RawMessage(rawData)

	if h.dialect.IsQuestionAsked(eventType) {
		req, err := h.dialect.ParseQuestionRequest(eventType, properties)
		if err != nil {
			h.logger.Warn("Failed to parse question event", "error", err, "workspaceID", workspaceID)
			return
		}
		h.broker.Publish(workspaceID, WorkspaceSSEEvent{
			Type: "agent.question",
			Data: req,
		})
	} else if h.dialect.IsQuestionResolved(eventType) {
		var resolution struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		json.Unmarshal(properties, &resolution)
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
		json.Unmarshal(properties, &resolution)
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
	cfg.autoApprovePermissions = workspace.Spec.AutoApprovePermissions
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
	resp.Body.Close()

	h.logger.Info("Auto-approved permission",
		"workspaceID", workspaceID, "requestID", requestID)
}

// persistTitleFromEvent extracts the session title from a session.updated SSE
// event and writes it to the session index. This ensures PostgreSQL always has
// the latest title without requiring a separate fetch from opencode.
func (h *ProxyHandler) persistTitleFromEvent(workspaceID, rawData string) {
	// opencode session.updated event shape (both v1.2 and v1.15):
	//   {"type":"session.updated","properties":{"sessionID":"ses_...","info":{"id":"ses_...","title":"..."}}}
	// v1.15 also has a top-level "id" field (ignored by Go JSON decoder).
	var evt struct {
		Properties struct {
			SessionID string `json:"sessionID"`
			Info      struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"info"`
		} `json:"properties"`
	}
	if json.Unmarshal([]byte(rawData), &evt) == nil && evt.Properties.Info.ID != "" && evt.Properties.Info.Title != "" {
		h.sessionIndex.UpsertTitle(context.Background(), workspaceID, evt.Properties.Info.ID, evt.Properties.Info.Title)
	}
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
