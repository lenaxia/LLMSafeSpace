// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
)

var (
	agentAddr  = fmt.Sprintf("http://localhost:%d", agentd.AgentPort)
	listenAddr = agentd.AgentdAddr
)

var log *zap.Logger

// buildVersion is the workspace-agentd build identifier surfaced via
// /v1/healthz. Default value is "dev" for development builds; production
// builds should override via -ldflags "-X main.buildVersion=$VERSION".
//
// This is the agentd build version, NOT opencode's version. See
// HealthzResponse.Version: pre-US-22.1, this field carried opencode's
// /global/health version (which conflated agentd liveness with opencode
// availability — see worklog 0096). Post-US-22.1, the field reports the
// agentd build identifier, which is meaningful for the kubelet probe's
// purpose: "is this agentd binary alive and serving HTTP?".
var buildVersion = "dev"

type OpenCodeClient struct {
	password string
	client   *http.Client
}

func (c *OpenCodeClient) doRequest(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", agentAddr+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(agentd.AuthUsername, c.password)
	return c.client.Do(req)
}

func (c *OpenCodeClient) IsHealthy(ctx context.Context) (bool, string, error) {
	resp, err := c.doRequest(ctx, "/global/health")
	if err != nil {
		return false, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, "", err
	}
	return result.Healthy, result.Version, nil
}

func (c *OpenCodeClient) ConnectedProviders(ctx context.Context) ([]string, error) {
	resp, err := c.doRequest(ctx, "/provider")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Connected []string `json:"connected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Connected, nil
}

func (c *OpenCodeClient) ConfiguredProviderCount(ctx context.Context) (int, error) {
	resp, err := c.doRequest(ctx, "/config/providers")
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Providers []struct{} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return len(result.Providers), nil
}

func (c *OpenCodeClient) ListSessions(ctx context.Context) ([]agentd.SessionInfo, error) {
	resp, err := c.doRequest(ctx, "/session")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var sessions []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, err
	}
	result := make([]agentd.SessionInfo, len(sessions))
	for i, s := range sessions {
		result[i] = agentd.SessionInfo{ID: s.ID, Status: "idle"}
		// Fetch title from individual session endpoint (GET /session list doesn't include it)
		if title := c.fetchSessionTitle(ctx, s.ID); title != "" {
			result[i].Title = title
		}
	}
	return result, nil
}

func (c *OpenCodeClient) fetchSessionTitle(ctx context.Context, sessionID string) string {
	resp, err := c.doRequest(ctx, "/session/"+sessionID)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var s struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&s)
	return s.Title
}

type providerCache struct {
	mu            sync.Mutex
	connected     []string
	configured    int
	sessions      []agentd.SessionInfo
	lastFetchedAt time.Time
}

// sessionStatusTracker subscribes to opencode's SSE stream and tracks busy/idle per session.
type sessionStatusTracker struct {
	mu       sync.RWMutex
	statuses map[string]string // session ID → "busy" | "idle"
}

func newSessionStatusTracker() *sessionStatusTracker {
	return &sessionStatusTracker{statuses: make(map[string]string)}
}

func (t *sessionStatusTracker) set(sessionID, status string) {
	t.mu.Lock()
	t.statuses[sessionID] = status
	t.mu.Unlock()
}

func (t *sessionStatusTracker) get(sessionID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.statuses[sessionID]; ok {
		return s
	}
	return "idle"
}

// prune removes entries for sessions that no longer exist.
func (t *sessionStatusTracker) prune(activeIDs []string) {
	active := make(map[string]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = struct{}{}
	}
	t.mu.Lock()
	for id := range t.statuses {
		if _, exists := active[id]; !exists {
			delete(t.statuses, id)
		}
	}
	t.mu.Unlock()
}

func (t *sessionStatusTracker) subscribe(ctx context.Context, client *OpenCodeClient) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := t.connectAndRead(ctx, client); err != nil {
			log.Debug("SSE stream ended", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff*2 > maxBackoff {
			backoff = maxBackoff
		} else {
			backoff = backoff * 2
		}
	}
}

func (t *sessionStatusTracker) connectAndRead(ctx context.Context, client *OpenCodeClient) error {
	req, err := http.NewRequestWithContext(ctx, "GET", agentAddr+"/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(agentd.AuthUsername, client.password)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	httpClient := &http.Client{Timeout: 0} // no timeout for SSE
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE returned status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	var eventData strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			eventData.WriteString(strings.TrimPrefix(line, "data: "))
			eventData.WriteString("\n")
		} else if line == "" && eventData.Len() > 0 {
			t.processEvent(eventData.String())
			eventData.Reset()
		}
	}
	return scanner.Err()
}

func (t *sessionStatusTracker) processEvent(data string) {
	// Flat format: {"type":"session.status","properties":{"sessionID":"ses_...","status":{"type":"idle"}}}
	var evt struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal([]byte(data), &evt) != nil || evt.Type != "session.status" {
		// Try nested format
		var nested struct {
			Payload struct {
				Type       string          `json:"type"`
				Properties json.RawMessage `json:"properties"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(data), &nested) != nil || nested.Payload.Type != "session.status" {
			return
		}
		evt.Properties = nested.Payload.Properties
	}

	var props struct {
		SessionID string `json:"sessionID"`
		Status    struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if json.Unmarshal(evt.Properties, &props) != nil || props.SessionID == "" {
		return
	}

	if props.Status.Type == "busy" || props.Status.Type == "idle" {
		t.set(props.SessionID, props.Status.Type)
	}
}

const connectedCacheTTL = 15 * time.Second

func cachedState(ctx context.Context, client *OpenCodeClient, cache *providerCache, tracker *sessionStatusTracker) ([]string, int, []agentd.SessionInfo) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Since(cache.lastFetchedAt) < connectedCacheTTL && cache.connected != nil {
		// Even on cache hit, refresh session statuses from SSE tracker
		for i := range cache.sessions {
			cache.sessions[i].Status = tracker.get(cache.sessions[i].ID)
		}
		return cache.connected, cache.configured, cache.sessions
	}
	connected, connErr := client.ConnectedProviders(ctx)
	configured, cfgErr := client.ConfiguredProviderCount(ctx)
	sessions, sessErr := client.ListSessions(ctx)
	if connErr != nil {
		log.Warn("failed to fetch connected providers", zap.Error(connErr))
	}
	if cfgErr != nil {
		log.Warn("failed to fetch configured provider count", zap.Error(cfgErr))
	}
	if sessErr != nil {
		log.Debug("failed to fetch sessions", zap.Error(sessErr))
	}
	// Merge SSE-tracked statuses into session list
	for i := range sessions {
		sessions[i].Status = tracker.get(sessions[i].ID)
	}
	// Prune tracker entries for sessions that no longer exist
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	tracker.prune(ids)
	cache.connected = connected
	cache.configured = configured
	cache.sessions = sessions
	cache.lastFetchedAt = time.Now()
	return connected, configured, sessions
}

var workspacePath = agentd.WorkspacePath

func getDiskUsage() *agentd.DiskUsage {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(workspacePath, &stat); err != nil {
		return nil
	}
	// Statfs returns uint64 block counts; a disk large enough to overflow
	// int64 (>9 EiB) is implausible. Cast is safe in practice.
	total := int64(stat.Blocks) * int64(stat.Bsize) //nolint:gosec // G115: bounded by physical disk size
	free := int64(stat.Bfree) * int64(stat.Bsize)   //nolint:gosec // G115: same as above
	return &agentd.DiskUsage{
		UsedBytes:  total - free,
		TotalBytes: total,
	}
}

func main() {
	var err error
	log, err = zap.NewProduction()
	if err != nil {
		log = zap.NewNop()
	}
	defer func() { _ = log.Sync() }()

	// Subcommand dispatch. The materialize subcommand reads
	// /sandbox-cfg/secrets.json and applies it via pkg/agentd/secrets, then
	// exits. This replaces the legacy bash secret-loop in
	// runtimes/base/tools/entrypoints/entrypoint-common.sh and consolidates
	// secret materialization in a single, tested code path. See worklog
	// 0078 (Epic 17 G2/G20 remediation).
	if len(os.Args) > 1 && os.Args[1] == "materialize" {
		os.Exit(runMaterializeCommand(os.Args[2:], os.Stdout, os.Stderr))
	}

	supervise := len(os.Args) > 1 && os.Args[1] == "--supervise"

	pw, err := os.ReadFile(agentd.PasswordPath)
	if err != nil {
		log.Warn("failed to read password file", zap.String("path", agentd.PasswordPath), zap.Error(err))
	}
	password := strings.TrimSpace(string(pw))

	var proc *managedProcess
	if supervise {
		proc = &managedProcess{}
		proc.start()
	}

	client := &OpenCodeClient{
		password: password,
		client:   &http.Client{Timeout: 5 * time.Second},
	}

	startedAt := time.Now()
	cache := &providerCache{}
	sseTracker := newSessionStatusTracker()
	go sseTracker.subscribe(context.Background(), client)

	// US-22.2: Eager-refresh readiness cache. Background goroutine refreshes
	// opencode's IsHealthy every 5s; /v1/readyz reads from this cache without
	// making inline opencode calls.
	healthCache := newHealthzCache()
	go refreshIsHealthyLoop(context.Background(), client, healthCache, log)

	// US-22.8: Two separate http.Server instances eliminate listener-layer
	// head-of-line blocking. Admin port serves health probes (kubelet,
	// controller) on a dedicated goroutine pool; user port serves
	// reload-secrets and future proxy endpoints independently.
	adminMux := http.NewServeMux()
	userMux := http.NewServeMux()

	// Admin endpoints (healthz, readyz, statusz) — admin port.
	adminMux.HandleFunc("/v1/healthz", healthzHandler(startedAt))

	adminMux.HandleFunc("/v1/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := healthCache.Snapshot()

		// Ready requires: cache initialized + opencode healthy + at least one provider connected.
		// Provider info comes from the existing providerCache (cachedState) which is also
		// used by /v1/statusz. We read it here for the providers_connected field but the
		// ready decision is driven primarily by the healthzCache's IsHealthy observation.
		connected, configured, _ := cachedState(r.Context(), client, cache, sseTracker)
		ready := snap.Initialized && snap.Healthy && len(connected) > 0

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(agentd.ReadyzResponse{
			Ready:               ready,
			ProvidersConnected:  connected,
			ProvidersConfigured: configured,
			AgentVersion:        snap.Version,
			AgentType:           "opencode",
		})
	})

	// US-22.4: /v1/statusz is the EXPENSIVE deep-introspection endpoint.
	// It makes multiple synchronous HTTP calls to opencode (IsHealthy,
	// ConnectedProviders, ConfiguredProviderCount, ListSessions) under a
	// mutex. Under SSE load, these calls can take seconds to complete.
	//
	// Consumers:
	//   - Controller's deep-status poll (60s interval, drives session-list/disk-usage fields)
	//   - API service status enrichment (infrequent)
	//
	// Performance contract: NO upper bound. Callers must use a generous
	// timeout (controller uses 30s). Do NOT use this endpoint for liveness
	// or readiness probes — use /v1/healthz and /v1/readyz respectively.
	adminMux.HandleFunc("/v1/statusz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		healthy, version, _ := client.IsHealthy(r.Context())
		connected, configured, sessions := cachedState(r.Context(), client, cache, sseTracker)
		ready := healthy && len(connected) > 0

		activeCnt := 0
		for _, s := range sessions {
			if s.Status == "busy" {
				activeCnt++
			}
		}

		_ = json.NewEncoder(w).Encode(agentd.StatuszResponse{
			Healthy:             healthy,
			Ready:               ready,
			Connected:           connected,
			ProvidersConfigured: configured,
			Sessions:            sessions,
			SessionsActive:      activeCnt,
			SessionsError:       0,
			LastError:           "",
			AgentType:           "opencode",
			AgentVersion:        version,
			UptimeSeconds:       int(time.Since(startedAt).Seconds()),
			Disk:                getDiskUsage(),
		})
	})

	// User endpoints (reload-secrets) — user port.
	userMux.HandleFunc("/v1/reload-secrets", reloadSecretsHandler(loadMaterializeConfig(), proc))

	// Start admin server (health probes) on dedicated port.
	adminSrv := &http.Server{
		Addr:              agentd.AgentdAdminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("workspace-agentd admin server starting", zap.String("addr", agentd.AgentdAdminAddr))
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("workspace-agentd admin server failed", zap.Error(err))
		}
	}()

	// Start user server on the original port.
	log.Info("workspace-agentd user server starting", zap.String("addr", listenAddr))
	userSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           userMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := userSrv.ListenAndServe(); err != nil {
		log.Fatal("workspace-agentd user server failed", zap.Error(err))
	}
}

// managedProcess supervises the opencode serve process.
type managedProcess struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	restartCount  int
	lastRestartAt time.Time
	stopping      bool
}

const (
	maxBackoffSec  = 30
	healthCheckURL = "http://localhost:4096/v1/readyz"
)

func (p *managedProcess) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = false
	// G204: argument list is fixed at compile time; agentd.AgentPort is
	// a typed int constant. The only "variable" here is fmt.Sprintf
	// converting that constant to a string. noctx: opencode is a
	// long-running daemon, no per-call deadline applies.
	//nolint:gosec,noctx // G204/noctx: fixed argv, daemon process
	p.cmd = exec.Command("opencode", "serve", "--hostname", "0.0.0.0", "--port", fmt.Sprintf("%d", agentd.AgentPort))
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr
	p.cmd.Env = buildEnvFrom(agentd.SecretsEnvPath)
	if err := p.cmd.Start(); err != nil {
		log.Error("failed to start opencode", zap.Error(err))
		return
	}
	p.lastRestartAt = time.Now()
	log.Info("opencode started", zap.Int("pid", p.cmd.Process.Pid), zap.Int("restartCount", p.restartCount))

	// Monitor in background
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		stopping := p.stopping
		p.mu.Unlock()
		if stopping {
			return // intentional stop, restart() will handle it
		}
		log.Warn("opencode exited unexpectedly", zap.Error(err), zap.Int("restartCount", p.restartCount))
		p.restartCount++
		// Exponential backoff: 1s, 2s, 4s, 8s, ... max 30s
		backoff := time.Duration(1<<min(p.restartCount, 5)) * time.Second
		if backoff > maxBackoffSec*time.Second {
			backoff = maxBackoffSec * time.Second
		}
		// Reset counter if last restart was >60s ago (stable period)
		if time.Since(p.lastRestartAt) > 60*time.Second {
			p.restartCount = 0
			backoff = time.Second
		}
		log.Info("restarting opencode", zap.Duration("backoff", backoff))
		time.Sleep(backoff)
		p.start()
	}()
}

func (p *managedProcess) restart() {
	p.mu.Lock()
	p.stopping = true
	cmd := p.cmd
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		log.Info("stopping opencode for restart", zap.Int("pid", cmd.Process.Pid))
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	p.restartCount = 0
	p.start()

	// Verify opencode came back up (health check with timeout). The
	// goroutine deliberately uses a fresh context: restart() is invoked
	// from a Gin handler whose ctx may already be canceled before the
	// child process is up, and we want the health probe to outlive the
	// triggering request.
	//nolint:contextcheck // intentional fresh context; see comment above
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		client := &http.Client{Timeout: 2 * time.Second}
		for i := 0; i < 10; i++ {
			time.Sleep(time.Second)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthCheckURL, nil)
			if err != nil {
				log.Warn("health check request build failed", zap.Error(err))
				return
			}
			resp, err := client.Do(req)
			if err == nil && resp.StatusCode == 200 {
				_ = resp.Body.Close()
				log.Info("opencode healthy after restart")
				return
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
		log.Warn("opencode did not become healthy within 10s after restart")
	}()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
