package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	stopCh chan struct{}
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
		httpClient = &http.Client{Timeout: 30 * time.Second}
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
		stopCh:     make(chan struct{}),
	}, nil
}

func (h *ProxyHandler) Start() error {
	h.activityTracker = NewActivityTracker(h.k8sClient, h.logger, h.namespace)
	if err := h.activityTracker.Start(); err != nil {
		return fmt.Errorf("starting activity tracker: %w", err)
	}

	h.sseTracker = NewSSETracker(h.httpClient, h.logger, h.onSessionIdle)
	h.sseTracker.SetPasswordGetter(h.getPassword)
	h.sseTracker.SetPodIPResolver(h.getPodIPForSSE)
	h.sseTracker.SetOnSessionActive(h.onSessionActive)

	var err error
	h.watcher, err = NewSandboxWatcher(h.k8sClient, h.logger, h.namespace, h.onPhaseChange)
	if err != nil {
		h.activityTracker.Stop()
		return fmt.Errorf("creating CRD watcher: %w", err)
	}
	if err := h.watcher.Start(); err != nil {
		h.activityTracker.Stop()
		return fmt.Errorf("starting CRD watcher: %w", err)
	}
	return nil
}

func (h *ProxyHandler) Stop() error {
	close(h.stopCh)
	if h.sseTracker != nil {
		h.sseTracker.Stop()
	}
	if h.watcher != nil {
		h.watcher.Stop()
	}
	if h.activityTracker != nil {
		h.activityTracker.Stop()
	}
	return nil
}

func (h *ProxyHandler) CreateSession(c *gin.Context) {
	h.proxyToSandbox(c, "/session", false, "")
}

func (h *ProxyHandler) ListSessions(c *gin.Context) {
	h.proxyToSandbox(c, "/session", false, "")
}

func (h *ProxyHandler) SendMessage(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/message", true, sid)
}

func (h *ProxyHandler) SendPromptAsync(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/prompt_async", true, sid)
}

func (h *ProxyHandler) GetHistory(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/message", false, sid)
}

func (h *ProxyHandler) AbortSession(c *gin.Context) {
	sid := c.Param("sessionId")
	h.proxyToSandbox(c, "/session/"+sid+"/abort", false, sid)
}

func (h *ProxyHandler) StreamEvents(c *gin.Context) {
	h.proxyToSandbox(c, "/event", false, "")
}

func (h *ProxyHandler) proxyToSandbox(c *gin.Context, targetPath string, isWriteOp bool, sessionID string) {
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
	proxyErr := h.doProxy(c, podIP, targetPath, password, bodyBytes)

	if proxyErr != nil && isConnectionError(proxyErr) {
		freshSandbox, getErr := h.k8sClient.LlmsafespaceV1().Sandboxes(h.namespace).Get(sandboxID, metav1.GetOptions{})
		if getErr == nil && freshSandbox.Status.PodIP != "" && freshSandbox.Status.PodIP != podIP && freshSandbox.Status.Phase == phaseRunning {
			h.logger.Info("Retrying proxy with fresh pod IP", "sandboxID", sandboxID, "oldIP", podIP, "newIP", freshSandbox.Status.PodIP)
			proxyErr = h.doProxy(c, freshSandbox.Status.PodIP, targetPath, password, bodyBytes)
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

func (h *ProxyHandler) doProxy(c *gin.Context, podIP, targetPath, password string, body []byte) error {
	targetURL := fmt.Sprintf("http://%s:%d%s", podIP, opencodePort, targetPath)
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = io.NopCloser(strings.NewReader(string(body)))
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
		}
	}
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

func makeSandboxCRD(name, podIP, phase, workspaceRef string) *v1.Sandbox {
	return &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"user-id":                    "test-user",
				"llmsafespace.dev/workspace": workspaceRef,
			},
		},
		Spec: v1.SandboxSpec{
			Runtime:      "python",
			WorkspaceRef: workspaceRef,
		},
		Status: v1.SandboxStatus{
			Phase: phase,
			PodIP: podIP,
		},
	}
}

func makePasswordSecret(sandboxID, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sandbox-pw-%s", sandboxID),
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}
}

func makeWorkspaceCRD(name string, maxActiveSessions int) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			MaxActiveSessions: int32(maxActiveSessions),
		},
	}
}
