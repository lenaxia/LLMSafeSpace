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
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const (
	defaultMaxActiveSessions = 5
	maxConnectionsPerSandbox = 10
	opencodePort             = 4096
	retryAfterSec            = 10

	phaseRunning     = "Running"
	phaseSuspending  = "Suspending"
	phaseSuspended   = "Suspended"
	phaseTerminating = "Terminating"
	phaseTerminated  = "Terminated"
)

type workspaceConfig struct {
	workspaceID       string
	maxActiveSessions int
}

type ProxyHandler struct {
	k8sClient  pkginterfaces.KubernetesClient
	httpClient *http.Client
	logger     pkginterfaces.LoggerInterface
	namespace  string

	pwCache   map[string]string
	pwCacheMu sync.RWMutex

	wsConfig   map[string]workspaceConfig
	wsConfigMu sync.RWMutex

	activeSess map[string]map[string]bool
	activeMu   sync.Mutex

	connCount map[string]int
	connMu    sync.Mutex

	activityTracker *ActivityTracker
	watcher         *SandboxWatcher
	sseTracker      *SSETracker
	sessionIndex    interfaces.SessionIndexService

	startOnce sync.Once
	stopOnce  sync.Once
}

func NewProxyHandler(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
	httpClient *http.Client,
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
				ResponseHeaderTimeout: 30 * time.Second,
			},
		}
	}
	return &ProxyHandler{
		k8sClient:  k8sClient,
		httpClient: httpClient,
		logger:     logger,
		namespace:  namespace,
		pwCache:    make(map[string]string),
		wsConfig:   make(map[string]workspaceConfig),
		activeSess: make(map[string]map[string]bool),
		connCount:  make(map[string]int),
	}, nil
}

func (h *ProxyHandler) Start() error {
	var startErr error
	h.startOnce.Do(func() {
		h.activityTracker = NewActivityTracker(h.k8sClient, h.logger, h.namespace)
		if err := h.activityTracker.Start(); err != nil {
			startErr = fmt.Errorf("starting activity tracker: %w", err)
			return
		}

		h.sseTracker = NewSSETracker(h.httpClient, h.logger, h.onSessionIdle)
		h.sseTracker.SetPasswordGetter(h.getPassword)
		h.sseTracker.SetPodIPResolver(h.getPodIPForSSE)
		h.sseTracker.SetOnSessionActive(h.onSessionActive)

		watcher, err := NewSandboxWatcher(h.k8sClient, h.logger, h.namespace, h.onPhaseChange)
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
	h.proxyToSandbox(c, "/session", false, "", false)
}

func (h *ProxyHandler) ListSessions(c *gin.Context) {
	h.proxyToSandbox(c, "/session", false, "", false)
}

func (h *ProxyHandler) SendMessage(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/message", true, sid, true)
}

func (h *ProxyHandler) SendPromptAsync(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/prompt_async", true, sid, false)
}

func (h *ProxyHandler) GetHistory(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/message", false, sid, true)
}

func (h *ProxyHandler) AbortSession(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/abort", false, sid, false)
}

func (h *ProxyHandler) StreamEvents(c *gin.Context) {
	h.proxyToSandbox(c, "/event", false, "", false)
}

// proxyToSandbox forwards the incoming request to the sandbox pod's opencode
// server. When filterParts is true and ?verbose=true is NOT set, response
// parts of type=="patch" are stripped before sending to the client.
func (h *ProxyHandler) proxyToSandbox(c *gin.Context, targetPath string, isWriteOp bool, sessionID string, filterParts bool) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sandbox ID required"})
		return
	}

	var sandbox *v1.Sandbox
	// Reuse sandbox CRD if already fetched by ownership middleware (avoids double read)
	if cached, exists := c.Get("sandbox"); exists {
		if sb, ok := cached.(*v1.Sandbox); ok {
			sandbox = sb
		}
	}
	if sandbox == nil {
		var err error
		sandbox, err = h.k8sClient.LlmsafespaceV1().Sandboxes(h.namespace).Get(sandboxID, metav1.GetOptions{})
		if err != nil {
			h.logger.Error("Failed to get sandbox CRD", err, "sandboxID", sandboxID)
			c.JSON(http.StatusNotFound, gin.H{"error": "sandbox not found"})
			return
		}
	}

	if sandbox.Status.Phase != phaseRunning || sandbox.Status.PodIP == "" {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "sandbox not ready",
			"phase":      sandbox.Status.Phase,
			"retryAfter": retryAfterSec,
		})
		return
	}

	password, err := h.getPassword(c.Request.Context(), sandboxID)
	if err != nil {
		h.logger.Error("Failed to get sandbox password", err, "sandboxID", sandboxID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve sandbox credentials"})
		return
	}

	wsCfg, err := h.getWorkspaceConfig(c.Request.Context(), sandboxID, sandbox.Spec.WorkspaceRef)
	if err != nil {
		h.logger.Error("Failed to get workspace config", err, "sandboxID", sandboxID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workspace configuration"})
		return
	}

	if !h.acquireConnection(sandboxID) {
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":      "connection limit reached",
			"retryAfter": retryAfterSec,
		})
		return
	}
	defer h.releaseConnection(sandboxID)

	if isWriteOp && sessionID != "" {
		if !h.checkAndAddActiveSession(sandboxID, sessionID, wsCfg.maxActiveSessions) {
			h.releaseConnection(sandboxID)
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":             "active session limit reached",
				"maxActiveSessions": wsCfg.maxActiveSessions,
				"retryAfter":        retryAfterSec,
			})
			return
		}
	}

	if isWriteOp && sessionID != "" && h.sseTracker != nil {
		h.sseTracker.EnsureWatching(sandboxID)
	}

	var bodyBytes []byte
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		bodyBytes, err = io.ReadAll(c.Request.Body)
		c.Request.Body.Close()
		if err != nil {
			h.logger.Error("Failed to read request body", err, "sandboxID", sandboxID)
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
	}

	podIP := sandbox.Status.PodIP
	verbose := c.Query("verbose") == "true"
	stripPatch := filterParts && !verbose
	proxyErr := h.doProxy(c, podIP, targetPath, password, bodyBytes, stripPatch)

	if proxyErr != nil && isConnectionError(proxyErr) {
		freshSandbox, getErr := h.k8sClient.LlmsafespaceV1().Sandboxes(h.namespace).Get(sandboxID, metav1.GetOptions{})
		if getErr == nil && freshSandbox.Status.PodIP != "" && freshSandbox.Status.PodIP != podIP && freshSandbox.Status.Phase == phaseRunning {
			h.logger.Info("Retrying proxy with fresh pod IP", "sandboxID", sandboxID, "oldIP", podIP, "newIP", freshSandbox.Status.PodIP)
			proxyErr = h.doProxy(c, freshSandbox.Status.PodIP, targetPath, password, bodyBytes, stripPatch)
		}
	}

	if proxyErr != nil {
		h.logger.Error("Proxy request failed", proxyErr, "sandboxID", sandboxID)
		if isWriteOp && sessionID != "" {
			h.removeActiveSession(sandboxID, sessionID)
		}
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfterSec))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "sandbox connection failed",
			"retryAfter": retryAfterSec,
		})
		return
	}

	if h.activityTracker != nil && wsCfg.workspaceID != "" {
		h.activityTracker.Record(wsCfg.workspaceID)
	} else if h.activityTracker != nil && wsCfg.workspaceID == "" {
		h.logger.Debug("Skipping activity tracking: sandbox has no workspaceRef", "sandboxID", sandboxID)
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
		return fmt.Errorf("proxy request to sandbox: %w", err)
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
			return fmt.Errorf("reading sandbox response: %w", readErr)
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

func (h *ProxyHandler) getPassword(ctx context.Context, sandboxID string) (string, error) {
	h.pwCacheMu.RLock()
	if pw, ok := h.pwCache[sandboxID]; ok {
		h.pwCacheMu.RUnlock()
		return pw, nil
	}
	h.pwCacheMu.RUnlock()

	secretName := fmt.Sprintf("sandbox-pw-%s", sandboxID)
	secret, err := h.k8sClient.Clientset().CoreV1().Secrets(h.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading password secret %s: %w", secretName, err)
	}

	pw := string(secret.Data["password"])
	if pw == "" {
		return "", fmt.Errorf("password secret %s has empty password key", secretName)
	}

	h.pwCacheMu.Lock()
	h.pwCache[sandboxID] = pw
	h.pwCacheMu.Unlock()

	return pw, nil
}

func (h *ProxyHandler) getWorkspaceConfig(ctx context.Context, sandboxID, workspaceRef string) (workspaceConfig, error) {
	h.wsConfigMu.RLock()
	if cfg, ok := h.wsConfig[sandboxID]; ok {
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
	h.wsConfig[sandboxID] = cfg
	h.wsConfigMu.Unlock()

	return cfg, nil
}

func (h *ProxyHandler) checkAndAddActiveSession(sandboxID, sessionID string, maxSessions int) bool {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()

	if h.activeSess[sandboxID] == nil {
		h.activeSess[sandboxID] = make(map[string]bool)
	}

	if h.activeSess[sandboxID][sessionID] {
		return true
	}

	if len(h.activeSess[sandboxID]) >= maxSessions {
		return false
	}

	h.activeSess[sandboxID][sessionID] = true
	return true
}

func (h *ProxyHandler) removeActiveSession(sandboxID, sessionID string) {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	if sessions, ok := h.activeSess[sandboxID]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(h.activeSess, sandboxID)
		}
	}
}

func (h *ProxyHandler) activeSessionCount(sandboxID string) int {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	return len(h.activeSess[sandboxID])
}

func (h *ProxyHandler) acquireConnection(sandboxID string) bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[sandboxID] >= maxConnectionsPerSandbox {
		return false
	}
	h.connCount[sandboxID]++
	return true
}

func (h *ProxyHandler) releaseConnection(sandboxID string) {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.connCount[sandboxID] > 0 {
		h.connCount[sandboxID]--
	}
	if h.connCount[sandboxID] == 0 {
		delete(h.connCount, sandboxID)
	}
}

func (h *ProxyHandler) connectionCount(sandboxID string) int {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	return h.connCount[sandboxID]
}

func (h *ProxyHandler) invalidateCaches(sandboxID string) {
	h.pwCacheMu.Lock()
	delete(h.pwCache, sandboxID)
	h.pwCacheMu.Unlock()

	h.wsConfigMu.Lock()
	delete(h.wsConfig, sandboxID)
	h.wsConfigMu.Unlock()

	h.activeMu.Lock()
	delete(h.activeSess, sandboxID)
	h.activeMu.Unlock()
}

func (h *ProxyHandler) onPhaseChange(sandbox *v1.Sandbox) {
	phase := sandbox.Status.Phase
	if phase == phaseSuspending || phase == phaseSuspended || phase == phaseTerminating || phase == phaseTerminated {
		h.invalidateCaches(sandbox.Name)
		if h.sseTracker != nil {
			h.sseTracker.StopWatching(sandbox.Name)
		}
		return
	}
	if phase == phaseRunning {
		h.wsConfigMu.Lock()
		delete(h.wsConfig, sandbox.Name)
		h.wsConfigMu.Unlock()
	}
}

func (h *ProxyHandler) onSessionIdle(sandboxID, sessionID string) {
	h.removeActiveSession(sandboxID, sessionID)
	if h.activityTracker != nil {
		h.wsConfigMu.RLock()
		cfg, ok := h.wsConfig[sandboxID]
		h.wsConfigMu.RUnlock()
		if ok && cfg.workspaceID != "" {
			h.activityTracker.Record(cfg.workspaceID)
			// Record message in session index
			if h.sessionIndex != nil {
				h.sessionIndex.RecordMessage(cfg.workspaceID, sessionID, "", time.Now())
			}
		}
	}
}

// SetSessionIndex injects the session index service for recording message activity.
func (h *ProxyHandler) SetSessionIndex(si interfaces.SessionIndexService) {
	h.sessionIndex = si
}

// GetActiveSessions returns the active session IDs for a sandbox.
// This is a per-replica view (not globally consistent across API replicas).
func (h *ProxyHandler) GetActiveSessions(sandboxID string) []string {
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	sessions := h.activeSess[sandboxID]
	if sessions == nil {
		return nil
	}
	result := make([]string, 0, len(sessions))
	for sid := range sessions {
		result = append(result, sid)
	}
	return result
}

func (h *ProxyHandler) onSessionActive(sandboxID, sessionID string) {
	h.wsConfigMu.RLock()
	cfg, ok := h.wsConfig[sandboxID]
	h.wsConfigMu.RUnlock()
	maxSessions := defaultMaxActiveSessions
	if ok {
		maxSessions = cfg.maxActiveSessions
	}
	h.checkAndAddActiveSession(sandboxID, sessionID, maxSessions)
}

func (h *ProxyHandler) getPodIPForSSE(sandboxID string) string {
	sandbox, err := h.k8sClient.LlmsafespaceV1().Sandboxes(h.namespace).Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if sandbox.Status.Phase != phaseRunning {
		return ""
	}
	return sandbox.Status.PodIP
}

// GetSandboxCRD retrieves a Sandbox CRD by name. Used by the ownership
// middleware in the router to verify sandbox ownership before proxying.
func (h *ProxyHandler) GetSandboxCRD(sandboxID string) (*v1.Sandbox, error) {
	return h.k8sClient.LlmsafespaceV1().Sandboxes(h.namespace).Get(sandboxID, metav1.GetOptions{})
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
