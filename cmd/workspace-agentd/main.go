// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

var (
	// agentAddrAtomic holds the current opencode agent base URL.
	// Tests mutate it via setAgentAddr; production sets it once at
	// startup. atomic.Value gives data-race-free read/write so the
	// race detector doesn't flag concurrent test access.
	agentAddrAtomic atomic.Value
	listenAddr      = agentd.AgentdAddr
)

func init() {
	agentAddrAtomic.Store(fmt.Sprintf("http://localhost:%d", agentd.AgentPort))
}

// getAgentAddr returns the current opencode agent base URL.
func getAgentAddr() string {
	return agentAddrAtomic.Load().(string)
}

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
	req, err := http.NewRequestWithContext(ctx, "GET", getAgentAddr()+path, nil)
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

// ModelContextLimit queries /config/providers for the context window limit of a given model.
// Returns 0 if the model or limit cannot be found.
func (c *OpenCodeClient) ModelContextLimit(ctx context.Context, modelID, providerID string) int64 {
	resp, err := c.doRequest(ctx, "/config/providers")
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Providers []struct {
			ID     string `json:"id"`
			Models map[string]struct {
				ID    string `json:"id"`
				Limit struct {
					Context int64 `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}
	for _, p := range result.Providers {
		if providerID != "" && p.ID != providerID {
			continue
		}
		if m, ok := p.Models[modelID]; ok && m.Limit.Context > 0 {
			return m.Limit.Context
		}
		// Fallback: search all models in this provider
		for _, m := range p.Models {
			if m.ID == modelID && m.Limit.Context > 0 {
				return m.Limit.Context
			}
		}
	}
	return 0
}

func (c *OpenCodeClient) ListSessions(ctx context.Context) ([]agentd.SessionInfo, error) {
	resp, err := c.doRequest(ctx, "/session")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var sessions []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Tokens *struct {
			Input     int64 `json:"input"`
			Output    int64 `json:"output"`
			Reasoning int64 `json:"reasoning"`
			Cache     struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
		Model *struct {
			ID string `json:"id"`
		} `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, err
	}
	result := make([]agentd.SessionInfo, len(sessions))
	for i, s := range sessions {
		info := agentd.SessionInfo{ID: s.ID, Title: s.Title, Status: "idle"}
		if s.Tokens != nil {
			info.Tokens = &agentd.SessionTokens{
				Input:      s.Tokens.Input,
				Output:     s.Tokens.Output,
				Reasoning:  s.Tokens.Reasoning,
				CacheRead:  s.Tokens.Cache.Read,
				CacheWrite: s.Tokens.Cache.Write,
			}
		}
		if s.Model != nil {
			info.Model = s.Model.ID
		}
		// If title wasn't in list, fetch it individually
		if info.Title == "" {
			if title := c.fetchSessionTitle(ctx, s.ID); title != "" {
				info.Title = title
			}
		}
		result[i] = info
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

// sessionStatusTracker subscribes to opencode's SSE stream and tracks busy/idle per session
// and per-session prompt tokens from step-finish events.
type sessionStatusTracker struct {
	mu           sync.RWMutex
	statuses     map[string]string // session ID → "busy" | "idle"
	promptTokens map[string]int64  // session ID → current context size (input + cache.read + cache.write)
}

func newSessionStatusTracker() *sessionStatusTracker {
	return &sessionStatusTracker{
		statuses:     make(map[string]string),
		promptTokens: make(map[string]int64),
	}
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

// hasAnyBusy returns true if any tracked session is currently "busy".
// Used by the session-aware restart mechanism (US-44.2) to decide
// whether to defer an opencode restart until sessions are idle.
func (t *sessionStatusTracker) hasAnyBusy() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, s := range t.statuses {
		if s == "busy" {
			return true
		}
	}
	return false
}

// listBusy returns the IDs of all sessions currently marked "busy".
// Used for logging which sessions are blocking a deferred restart.
func (t *sessionStatusTracker) listBusy() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var busy []string
	for id, s := range t.statuses {
		if s == "busy" {
			busy = append(busy, id)
		}
	}
	return busy
}

// hasAnyData returns true if the tracker has tracked at least one
// session. Used by the session-aware restart logic to detect the
// SSE-disconnect case: an empty tracker means no session.status events
// have been received, so we cannot safely defer a restart.
func (t *sessionStatusTracker) hasAnyData() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.statuses) > 0
}

// snapshot returns the current busy session count and total prompt
// tokens across all sessions. Used by the ops metrics loop to update
// Prometheus gauges without holding the lock for multiple calls.
func (t *sessionStatusTracker) snapshot() (busyCount int, totalTokens int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, s := range t.statuses {
		if s == "busy" {
			busyCount++
		}
	}
	for _, tok := range t.promptTokens {
		totalTokens += tok
	}
	return busyCount, totalTokens
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
			delete(t.promptTokens, id)
		}
	}
	t.mu.Unlock()
}

func (t *sessionStatusTracker) setPromptTokens(sessionID string, tokens int64) {
	t.mu.Lock()
	t.promptTokens[sessionID] = tokens
	t.mu.Unlock()
}

func (t *sessionStatusTracker) getPromptTokens(sessionID string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.promptTokens[sessionID]
}

func (t *sessionStatusTracker) hasPromptTokens(sessionID string) bool {
	t.mu.RLock()
	_, ok := t.promptTokens[sessionID]
	t.mu.RUnlock()
	return ok
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
		err := t.connectAndRead(ctx, client)
		if err != nil && ctx.Err() == nil {
			log.Debug("SSE stream ended", zap.Error(err))
		}
		// If the parent context is done, exit
		if ctx.Err() != nil {
			return
		}
		// Reset backoff on successful read (timeout is expected, not an error)
		if err == nil || isTimeoutError(err) {
			backoff = 2 * time.Second
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

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// sseConnectionTimeout is the maximum lifetime of a single SSE connection.
// After this duration, the connection is closed and reconnected to prevent
// goroutine leaks from half-open sockets.
var sseConnectionTimeout = 5 * time.Minute

func (t *sessionStatusTracker) connectAndRead(ctx context.Context, client *OpenCodeClient) error {
	connCtx, cancel := context.WithTimeout(ctx, sseConnectionTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(connCtx, "GET", getAgentAddr()+"/event", nil)
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
	// Parse flat envelope first (cheap). Only try nested if flat fails.
	var evt struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal([]byte(data), &evt) != nil {
		return
	}
	switch evt.Type {
	case "session.status":
		t.handleSessionStatus(evt.Properties)
	case "session.next.step.ended":
		t.handleStepEnded(evt.Properties)
	default:
		// Try nested format for session.status (backward compat with global SSE endpoint).
		var nested struct {
			Payload struct {
				Type       string          `json:"type"`
				Properties json.RawMessage `json:"properties"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(data), &nested) != nil {
			return
		}
		switch nested.Payload.Type {
		case "session.status":
			t.handleSessionStatus(nested.Payload.Properties)
		case "session.next.step.ended":
			t.handleStepEnded(nested.Payload.Properties)
		}
	}
}

func (t *sessionStatusTracker) handleSessionStatus(props json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionID"`
		Status    struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if json.Unmarshal(props, &p) != nil || p.SessionID == "" {
		return
	}
	if p.Status.Type == "busy" || p.Status.Type == "idle" {
		t.set(p.SessionID, p.Status.Type)
	}
}

func (t *sessionStatusTracker) handleStepEnded(props json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionID"`
		Tokens    *struct {
			Input     int64 `json:"input"`
			Output    int64 `json:"output"`
			Reasoning int64 `json:"reasoning"`
			Cache     struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	}
	if json.Unmarshal(props, &p) != nil || p.SessionID == "" || p.Tokens == nil {
		return
	}
	promptTokens := p.Tokens.Input + p.Tokens.Cache.Read + p.Tokens.Cache.Write
	t.setPromptTokens(p.SessionID, promptTokens)
}

// fillGapsState prevents concurrent fillGaps iterations.
type fillGapsState struct {
	mu      sync.Mutex
	running bool
}

func (c *OpenCodeClient) fetchSessionPromptTokens(ctx context.Context, sessionID string) int64 {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.doRequest(fetchCtx, "/session/"+sessionID+"/message?limit=20")
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()

	var messages []struct {
		Info struct {
			Role   string `json:"role"`
			Tokens *struct {
				Input int64 `json:"input"`
				Cache struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	if json.NewDecoder(resp.Body).Decode(&messages) != nil {
		return 0
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == "assistant" && messages[i].Info.Tokens != nil {
			return messages[i].Info.Tokens.Input + messages[i].Info.Tokens.Cache.Read + messages[i].Info.Tokens.Cache.Write
		}
	}
	return 0
}

func runFill(ctx context.Context, client *OpenCodeClient, tracker *sessionStatusTracker, sessions func() []agentd.SessionInfo, state *fillGapsState) {
	state.mu.Lock()
	if state.running {
		state.mu.Unlock()
		return
	}
	state.running = true
	state.mu.Unlock()
	defer func() { state.running = false }()

	iterCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for _, s := range sessions() {
		if tracker.hasPromptTokens(s.ID) {
			continue
		}
		select {
		case <-iterCtx.Done():
			return
		default:
		}
		if tokens := client.fetchSessionPromptTokens(iterCtx, s.ID); tokens > 0 {
			tracker.setPromptTokens(s.ID, tokens)
		}
	}
}

func fillGaps(ctx context.Context, client *OpenCodeClient, tracker *sessionStatusTracker, sessions func() []agentd.SessionInfo, state *fillGapsState) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runFill(ctx, client, tracker, sessions, state)
		}
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

func getMemoryUsage() *agentd.MemoryUsage {
	// Read container memory from cgroup v2 (fallback to /proc/meminfo)
	memTotal := int64(0)
	memUsed := int64(0)

	// Try cgroup v2 first
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.current"); err == nil {
		_, _ = fmt.Sscanf(string(data), "%d", &memUsed)
	}
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		max := strings.TrimSpace(string(data))
		if max != "max" {
			_, _ = fmt.Sscanf(max, "%d", &memTotal)
		}
	}
	if memTotal == 0 {
		// Fallback to /proc/meminfo
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			var totalKB, availKB int64
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
				} else if strings.HasPrefix(line, "MemAvailable:") {
					_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &availKB)
				}
			}
			if totalKB > 0 {
				memTotal = totalKB * 1024
				if availKB > 0 {
					memUsed = (totalKB - availKB) * 1024
				}
			}
		}
	}

	if memTotal <= 0 {
		return nil
	}
	return &agentd.MemoryUsage{
		UsedBytes:  memUsed,
		TotalBytes: memTotal,
	}
}

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

// getCPUUsage reads cumulative CPU from cgroup v2 cpu.stat.
// Covers entire pod cgroup (all processes). UsageMicros is monotonically
// increasing; callers compute delta for rate.
func getCPUUsage() *agentd.CPUUsage {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return nil
	}
	var usageMicros int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "usage_usec ") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "usage_usec "), "%d", &usageMicros)
			break
		}
	}
	if usageMicros == 0 {
		return nil
	}
	var limitMicrosPerSec int64
	if maxData, merr := os.ReadFile("/sys/fs/cgroup/cpu.max"); merr == nil {
		fields := strings.Fields(strings.TrimSpace(string(maxData)))
		if len(fields) == 2 && fields[0] != "max" {
			var quota, period int64
			if _, serr := fmt.Sscanf(fields[0], "%d", &quota); serr == nil {
				if _, serr = fmt.Sscanf(fields[1], "%d", &period); serr == nil && period > 0 {
					limitMicrosPerSec = quota * 1_000_000 / period
				}
			}
		}
	}
	return &agentd.CPUUsage{
		UsageMicros:       usageMicros,
		LimitMicrosPerSec: limitMicrosPerSec,
	}
}

// buildStatuszHandler returns the /v1/statusz HTTP handler, parameterised on
// all runtime dependencies. Extracted from main() so tests can exercise the
// real handler wiring without reimplementing it.
func buildStatuszHandler(
	client *OpenCodeClient,
	cache *providerCache,
	tracker *sessionStatusTracker,
	pressureMon *memoryPressureMonitor,
	startedAt time.Time,
) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		healthy, version, _ := client.IsHealthy(r.Context())
		connected, configured, sessions := cachedState(r.Context(), client, cache, tracker)
		ready := healthy && len(connected) > 0

		activeCnt := 0
		for _, s := range sessions {
			if s.Status == "busy" {
				activeCnt++
			}
		}

		// Context usage: per-session ContextUsed from SSE prompt tokens.
		// Top-level TotalTokens = model context limit (same for all sessions).
		// UsedTokens is not meaningful as an aggregate; set to 0.
		var contextUsage *agentd.ContextUsage
		{
			var modelID string
			for i, s := range sessions {
				sessions[i].ContextUsed = tracker.getPromptTokens(s.ID)
				if modelID == "" && s.Model != "" {
					modelID = s.Model
				}
			}
			contextLimit := client.ModelContextLimit(r.Context(), modelID, "")
			contextUsage = &agentd.ContextUsage{
				UsedTokens:  0,
				TotalTokens: contextLimit,
			}
		}

		// US-44.6: enrich sessions with estimated memory from context tokens.
		for i := range sessions {
			tokens := int64(0)
			if sessions[i].Tokens != nil {
				tokens = sessions[i].Tokens.Input + sessions[i].Tokens.CacheRead + sessions[i].Tokens.CacheWrite
			}
			if tokens == 0 {
				tokens = sessions[i].ContextUsed
			}
			sessions[i].EstimatedMemoryMB = estimateSessionMemoryMB(tokens)
		}

		// US-44.5: surface memory pressure state.
		pressure, _, _ := pressureMon.snapshot()

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
			Memory:              getMemoryUsage(),
			CPU:                 getCPUUsage(),
			Context:             contextUsage,
			MemoryPressure:      pressure,
		})
	})
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

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	bgCtx, bgCancel := context.WithCancel(rootCtx)
	var bgWg sync.WaitGroup

	pw, err := os.ReadFile(agentd.PasswordPath)
	if err != nil {
		log.Warn("failed to read password file", zap.String("path", agentd.PasswordPath), zap.Error(err))
	}
	password := strings.TrimSpace(string(pw))

	// Construct the HTTP client before the managed process so the onStart
	// callback can close over it and be set in the struct literal — before
	// start() spawns the supervisor goroutine. Assigning onStart after
	// start() (the original wiring) races with the supervisor's mutex-
	// protected read in supervise(): on the initial boot the supervisor
	// could observe onStart == nil and silently skip the stale-session
	// cleanup, defeating the entire fix.
	client := &OpenCodeClient{
		password: password,
		client:   &http.Client{Timeout: 5 * time.Second},
	}

	// US-44.7: surface the reason for the previous opencode restart
	// (if any) and consume the one-shot marker before starting the
	// supervisor. No-op when no marker is present (clean boot).
	logRestartReason(RestartReasonMarkerPath, log.Core())

	var proc *managedProcess
	if supervise {
		proc = &managedProcess{
			// Wire stale-session cleanup: after every opencode (re)start,
			// once opencode is healthy, abort any sessions still marked
			// busy from the previous run. Runs in a probeWg-tracked
			// goroutine; bounded by a 30s deadline inside
			// abortStaleSessions.
			onStart: func() {
				abortStaleSessionsAfterStart(rootCtx, client, log)
			},
		}
		proc.start()
	}

	startedAt := time.Now()
	cache := &providerCache{}
	sseTracker := newSessionStatusTracker()
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		sseTracker.subscribe(bgCtx, client)
	}()

	// US-44.8: periodic metrics collection for ops dashboards. Updates
	// memory usage, active sessions, and context token gauges every 60s.
	// US-44.5: memory pressure monitor checks cgroup usage against the
	// 85% threshold and surfaces the state via statusz.
	pressureMonitor := newMemoryPressureMonitor()
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		pressureMonitor.run(bgCtx, log)
	}()

	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		wsID := os.Getenv("WORKSPACE_ID")
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-bgCtx.Done():
				return
			case <-ticker.C:
				if memBytes, err := readCgroupMemoryCurrent(); err == nil {
					pkgOpsMetrics.SetMemoryUsage(wsID, memBytes)
				}
				pkgOpsMetrics.UpdateFromTracker(wsID, sseTracker)
			}
		}
	}()

	fillState := &fillGapsState{}
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		fillGaps(bgCtx, client, sseTracker, func() []agentd.SessionInfo {
			cache.mu.Lock()
			sessions := cache.sessions
			cache.mu.Unlock()
			return sessions
		}, fillState)
	}()

	// S18.10: Gate recorder measures time-to-each-startup-milestone from boot.
	// Gates: opencode_up, providers_connected, readyz_first_200.
	gr := newGateRecorder(startedAt, agentdGateDurationSeconds, log)

	// US-22.2: Eager-refresh readiness cache. Background goroutine refreshes
	// opencode's IsHealthy every 5s; /v1/readyz reads from this cache without
	// making inline opencode calls.
	healthCache := newHealthzCache()
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		refreshIsHealthyLoop(bgCtx, client, healthCache, log, gr)
	}()

	// Epic 26: Phase-2 relay injection.
	// After opencode is healthy, fetch the live free model list and rewrite
	// the config to use the CF Worker relay. Runs at most once per pod lifetime.
	// Skipped if the user has a personal opencode API key (paying Zen subscriber).
	if relayURL := os.Getenv("INFERENCE_RELAY_BASEURL"); relayURL != "" && proc != nil {
		xdgData := os.Getenv("XDG_DATA_HOME")
		homeDir, _ := os.UserHomeDir()
		authJSONPath := filepath.Join(homeDir, ".local", "opencode", "auth.json")
		if xdgData != "" {
			authJSONPath = filepath.Join(xdgData, "opencode", "auth.json")
		}
		startRelayInjector(rootCtx, relayInjectorConfig{
			RelayURL:         relayURL,
			OpenCodeBaseURL:  getAgentAddr(),
			OpenCodePassword: password,
			AgentConfigPath:  envOrDefault("LLMSAFESPACES_AGENT_CONFIG_PATH", agentd.AgentConfigPath),
			AuthJSONPath:     authJSONPath,
			HealthCheck:      func() bool { snap := healthCache.Snapshot(); return snap.Initialized && snap.Healthy },
			KillOpenCode:     func() { proc.restart() },
		})
	}

	// US-22.8: Two separate http.Server instances eliminate listener-layer
	// head-of-line blocking. Admin port serves health probes (kubelet,
	// controller) on a dedicated goroutine pool; user port serves
	// reload-secrets and future proxy endpoints independently.
	adminMux := http.NewServeMux()
	userMux := http.NewServeMux()

	// F1.4.2 (Epic 17): the admin endpoints used to be unauthenticated.
	// /v1/statusz and /v1/readyz can leak session metadata and
	// provider-config to any pod that can route to the workspace's
	// admin port (4098). When AGENTD_ADMIN_TOKEN is set in the env
	// (controller-supplied via the workspace's password Secret), those
	// two endpoints require `Authorization: Bearer <token>`.
	// /v1/healthz remains open: it only emits `{ok, started_at}` and
	// the kubelet liveness probe targets it without configured headers.
	adminToken := os.Getenv("AGENTD_ADMIN_TOKEN")

	// Admin endpoints (healthz, readyz, statusz) — admin port.
	adminMux.HandleFunc("/v1/healthz", healthzHandler(startedAt))

	readyzHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := healthCache.Snapshot()

		// Ready requires: cache initialized + opencode healthy.
		// Provider connectivity is no longer a readiness gate (S18.11):
		// it is surfaced separately via WorkspaceConditionProviderReady on
		// the Workspace CRD. Provider info is still included in the response
		// body for observability.
		connected, configured, _ := cachedState(r.Context(), client, cache, sseTracker)
		ready := snap.Initialized && snap.Healthy

		// S18.10: Record providers_connected gate on first non-empty connected list.
		if len(connected) > 0 {
			gr.MaybeRecord(gateProvidersConnected)
		}

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
			// RelayInjected: true once the relay injector successfully completed.
			// Included in readyz (not statusz) because readyz is cache-based and
			// lightweight, making it safe to call on every ListModels cache miss.
			RelayInjected: getActiveRelayModels() != nil,
		})

		// S18.10: Record readyz_first_200 gate on first 200 response.
		if ready {
			gr.MaybeRecord(gateReadyzFirst200)
		}
	})
	adminMux.Handle("/v1/readyz", requireBearerToken(adminToken, readyzHandler))

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
	statuszHandler := buildStatuszHandler(client, cache, sseTracker, pressureMonitor, startedAt)
	adminMux.Handle("/v1/statusz", requireBearerToken(adminToken, statuszHandler))

	// S18.10: Expose Prometheus metrics on admin port so the cluster-level
	// Prometheus scraper can collect per-pod agentd gate timings.
	adminMux.Handle("/metrics", promhttp.Handler())

	// User endpoints — user port.
	// The session lister probes opencode's /session endpoint to (a) prune
	// stale busy entries from the tracker when opencode dies mid-busy and
	// is respawned (C2a), and (b) decide cold-start behavior when the
	// tracker is empty after an agentd restart (C2b). It closes over the
	// production OpenCodeClient; tests inject a stub.
	liveSessions := func(ctx context.Context) []string {
		sessions, err := client.ListSessions(ctx)
		if err != nil {
			return nil
		}
		ids := make([]string, len(sessions))
		for i, s := range sessions {
			ids[i] = s.ID
		}
		return ids
	}

	userMux.HandleFunc("/v1/reload-secrets", reloadSecretsHandler(loadMaterializeConfig(), reloadSecretsDeps{
		Proc:             proc,
		OpencodePassword: password,
		Tracker:          sseTracker,
		BgCtx:            bgCtx,
		BgWg:             &bgWg,
		Lister:           liveSessions,
	}))
	userMux.HandleFunc("/v1/agent/reload", agentReloadHandler(password, log))

	// Start admin server (health probes) on dedicated port.
	adminSrv := &http.Server{
		Addr:              agentd.AgentdAdminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srvErr := make(chan error, 2)
	go func() {
		log.Info("workspace-agentd admin server starting", zap.String("addr", agentd.AgentdAdminAddr))
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- fmt.Errorf("admin server: %w", err)
		}
	}()

	// Start user server on the original port.
	log.Info("workspace-agentd user server starting", zap.String("addr", listenAddr))
	userSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           userMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := userSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- fmt.Errorf("user server: %w", err)
		}
	}()

	select {
	case <-rootCtx.Done():
		log.Info("workspace-agentd received shutdown signal")
	case err := <-srvErr:
		log.Error("workspace-agentd server error", zap.Error(err))
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer shutdownCancel()

	var srvWg sync.WaitGroup
	srvWg.Add(2)
	go func() {
		defer srvWg.Done()
		if err := adminSrv.Shutdown(shutdownCtx); err != nil {
			log.Warn("workspace-agentd admin server shutdown error", zap.Error(err))
		}
	}()
	go func() {
		defer srvWg.Done()
		if err := userSrv.Shutdown(shutdownCtx); err != nil {
			log.Warn("workspace-agentd user server shutdown error", zap.Error(err))
		}
	}()
	srvWg.Wait()

	bgCancel()

	bgWaitDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(bgWaitDone)
	}()
	select {
	case <-bgWaitDone:
	case <-time.After(5 * time.Second):
		log.Warn("workspace-agentd background goroutines did not exit within 5s")
	}

	if proc != nil {
		proc.stop()
	}

	log.Info("workspace-agentd shutdown complete")
}

// managedProcess supervises the opencode serve process.
//
// Lifecycle model
// ---------------
// One **supervisor goroutine** owns the *exec.Cmd. The supervisor is
// the SOLE caller of cmd.Wait() — concurrent Wait() on the same Cmd
// is undefined, and was the proximate cause of Bug 2 in worklog 0125
// where restart() called Wait() while the previous start()'s monitor
// goroutine was already waiting. The first Wait to return won, but
// the kernel had not yet reaped the child, so a new opencode failed
// to bind port 4096.
//
// The supervisor loop is:
//
//  1. spawn child via p.cmdFactory()
//  2. announce "child is up" by closing p.upCh and re-creating it
//     (so the next iteration has a fresh signal)
//  3. p.cmd.Wait() — blocks until the child exits
//  4. inspect intent flags:
//     - stopRequested → exit the goroutine (no restart)
//     - restartRequested → loop with restartCount=0
//     - neither → loop with backoff
//
// Callers communicate intent via:
//
//   - start() — spawn the supervisor; idempotent under the mutex
//   - restart() — signal the current child, set restartRequested,
//     await the next "child is up"
//   - stop() — signal the current child, set stopRequested, await
//     supervisor goroutine exit
//
// All three are safe to call from any goroutine.
type managedProcess struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	restartCount  int
	lastRestartAt time.Time

	// cmdFactory builds a fresh *exec.Cmd for each (re)start.
	// Production wires `opencode serve …`; tests inject a fake.
	// Set lazily by start() if nil at call time, so production code
	// can construct managedProcess{} with no arguments and tests can
	// pre-populate the field before calling start().
	cmdFactory func() *exec.Cmd

	// healthCheckURL is the URL polled after restart() to verify the
	// new child is serving. Empty means skip the health check.
	healthCheckURL string

	// supervisor lifecycle channels.
	//
	//   upCh — closed by the supervisor every time a fresh child is
	//          running. restart() reads upCh under the mutex,
	//          releases the mutex, and waits on the captured channel
	//          for the supervisor to close it.
	//   doneCh — closed by the supervisor when it exits permanently
	//            (after stop()). stop() awaits this.
	//   stopRequested / restartRequested — flags read by the
	//          supervisor inside the loop body to decide what to do
	//          after the current child exits. Both protected by mu.
	upCh             chan struct{}
	doneCh           chan struct{}
	stopRequested    bool
	restartRequested bool

	// probeWg tracks any in-flight healthProbeAfterRestart
	// goroutines. stop() waits on it so the probe can no longer
	// touch the package-level log after stop() returns. Without this
	// a leaked probe and a test's t.Cleanup that swaps out the
	// logger race on `log` (caught by go test -race).
	probeWg sync.WaitGroup

	// onStart, if non-nil, is called in a probeWg-tracked goroutine
	// immediately after each child process starts. Production uses
	// this to abort stale busy sessions after every opencode restart.
	// Tests may inject a custom callback; nil means no-op.
	//
	// MUST be set before start(): supervise() reads it under p.mu on the
	// first iteration, and the supervisor goroutine is spawned by start().
	// Assigning it after start() races with that read and may cause the
	// initial-boot invocation to be skipped.
	onStart func()
}

const (
	maxBackoffSec  = 30
	healthCheckURL = "http://localhost:4096/v1/readyz"
)

// start spawns the supervisor goroutine. Calling start() more than
// once is a no-op (it does NOT restart — use restart() for that).
//
// In production this is invoked exactly once at agentd boot. The
// supervisor goroutine outlives every individual *exec.Cmd; restarts
// are loop iterations inside the supervisor, not new goroutines.
func (p *managedProcess) start() {
	p.mu.Lock()
	if p.doneCh != nil {
		// Supervisor already running.
		p.mu.Unlock()
		return
	}
	if p.cmdFactory == nil {
		p.cmdFactory = defaultOpencodeCmdFactory
	}
	if p.healthCheckURL == "" {
		p.healthCheckURL = healthCheckURL
	}
	p.upCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.mu.Unlock()

	go p.supervise()
}

// supervise is the supervisor goroutine. Sole owner of cmd.Wait().
//
// Loop invariants:
//
//   - On entry, the previous iteration's child (if any) has been
//     reaped by Wait(). Port resources are free.
//   - p.cmd is overwritten each iteration; the previous value's
//     ProcessState is set, exposing whether the child exited cleanly.
//   - p.upCh is closed exactly once per spawned child, then replaced
//     with a fresh channel before the next iteration.
//
// The loop terminates only when stopRequested is observed after a
// child exit. doneCh is closed before return so stop() can join.
func (p *managedProcess) supervise() {
	defer func() {
		p.mu.Lock()
		close(p.doneCh)
		p.mu.Unlock()
	}()

	for {
		p.mu.Lock()
		// Build a fresh cmd. exec.Cmd is single-shot — one Start +
		// one Wait per instance.
		cmd := p.cmdFactory()
		if err := cmd.Start(); err != nil {
			log.Error("failed to start opencode", zap.Error(err))
			// Reset request flags so the next loop iteration can
			// decide based on backoff rather than a stale signal.
			p.restartRequested = false
			stopReq := p.stopRequested
			p.mu.Unlock()
			if stopReq {
				return
			}
			// Treat Start() failure the same as a crash: backoff.
			p.applyBackoff()
			continue
		}
		p.cmd = cmd
		p.lastRestartAt = time.Now()
		log.Info("opencode started",
			zap.Int("pid", cmd.Process.Pid),
			zap.Int("restartCount", p.restartCount))

		// Announce the new child is up. close() must be called
		// exactly once per channel; we replace upCh before the next
		// iteration so the next close() targets a fresh channel.
		upCh := p.upCh
		p.upCh = make(chan struct{})
		onStart := p.onStart
		p.mu.Unlock()
		close(upCh)

		// Fire the onStart callback (e.g. abort stale sessions) in a
		// tracked goroutine so stop() can join it before returning.
		if onStart != nil {
			p.probeWg.Add(1)
			go func() {
				defer p.probeWg.Done()
				onStart()
			}()
		}

		// Sole Wait() in the codebase. This is the contract that
		// Bug 2 broke.
		waitErr := cmd.Wait()

		p.mu.Lock()
		stopReq := p.stopRequested
		restartReq := p.restartRequested
		p.restartRequested = false
		p.mu.Unlock()

		if stopReq {
			log.Info("opencode supervisor exiting",
				zap.Int("pid", cmd.Process.Pid),
				zap.Error(waitErr))
			return
		}
		if restartReq {
			// Operator-initiated restart: reset counters and loop
			// immediately (no backoff).
			p.mu.Lock()
			p.restartCount = 0
			p.mu.Unlock()
			continue
		}

		// Crash path: classify exit, handle OOM, record metric, log, backoff, loop.
		exitKind := classifyExit(waitErr)
		if isOOMExit(exitKind) {
			handleOOMExit(workspaceIDFromEnv(), RestartReasonMarkerPath)
		} else {
			if err := writeRestartReasonMarker(RestartReasonMarkerPath, "crash", nil); err != nil {
				log.Error("failed to write restart-reason marker", zap.Error(err))
			} else {
				logRestartReasonAtWrite("crash", nil, log.Core())
			}
			pkgOpsMetrics.RecordRestart(workspaceIDFromEnv(), "crash")
		}
		log.Warn("opencode exited unexpectedly",
			zap.Error(waitErr),
			zap.Int("restartCount", p.restartCount))
		p.applyBackoff()
	}
}

// applyBackoff advances the restart counter and sleeps. Called only
// from the supervisor goroutine after an unexpected child exit.
//
// Resets the counter when the previous child stayed up for >60s,
// which prevents legitimate long-running children from being
// penalized by historical crashes.
func (p *managedProcess) applyBackoff() {
	p.mu.Lock()
	p.restartCount++
	backoff := time.Duration(1<<min(p.restartCount, 5)) * time.Second
	if backoff > maxBackoffSec*time.Second {
		backoff = maxBackoffSec * time.Second
	}
	if time.Since(p.lastRestartAt) > 60*time.Second {
		p.restartCount = 0
		backoff = time.Second
	}
	p.mu.Unlock()
	log.Info("restarting opencode", zap.Duration("backoff", backoff))
	time.Sleep(backoff)
}

// restart signals the current child to exit and blocks until the
// supervisor has spawned and started a replacement. Safe to call from
// HTTP handlers; bounded by SIGKILL fallback (5s) + Start() time.
//
// If the supervisor isn't running (start() was never called), this is
// a no-op — callers in tests pass nil rather than building a partial
// supervisor.
func (p *managedProcess) restart() {
	p.mu.Lock()
	if p.doneCh == nil {
		// Supervisor not running.
		p.mu.Unlock()
		return
	}
	p.restartRequested = true
	cmd := p.cmd
	upCh := p.upCh
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		log.Info("stopping opencode for restart", zap.Int("pid", cmd.Process.Pid))
		_ = cmd.Process.Signal(syscall.SIGTERM)
		// Give the child up to 5s to exit on SIGTERM, then SIGKILL.
		// We can't Wait() here (supervisor owns Wait), so we rely on
		// the supervisor's loop iteration: when the child exits, the
		// supervisor will see restartRequested and loop. We poll
		// upCh to know when the new child is up.
		killTimer := time.AfterFunc(5*time.Second, func() {
			_ = cmd.Process.Kill()
		})
		defer killTimer.Stop()
	}

	// Block until the supervisor closes the upCh that was current
	// when restart() was called. The supervisor closes upCh only
	// after a successful Start(), guaranteeing the new child is up
	// AND the old one is reaped (Wait returned).
	<-upCh

	// Optional: post-restart health probe. Spawn a background
	// goroutine; restart() does not block on it. Pre-fix this used a
	// fresh context to outlive the triggering HTTP request; same
	// reason here. Tracked via probeWg so stop() can wait for it,
	// preventing log-pointer races during test teardown.
	if p.healthCheckURL != "" {
		p.probeWg.Add(1)
		go func() {
			defer p.probeWg.Done()
			p.healthProbeAfterRestart()
		}()
	}
}

// stop signals the current child and blocks until the supervisor
// goroutine exits. Safe to call from any goroutine. Idempotent: a
// second stop() returns immediately because doneCh is already closed.
func (p *managedProcess) stop() {
	p.mu.Lock()
	if p.doneCh == nil {
		p.mu.Unlock()
		return
	}
	p.stopRequested = true
	cmd := p.cmd
	doneCh := p.doneCh
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		killTimer := time.AfterFunc(5*time.Second, func() {
			_ = cmd.Process.Kill()
		})
		defer killTimer.Stop()
	}

	<-doneCh

	// Drain any in-flight health-probe goroutines so they cannot
	// touch the package-level log after stop() returns. Bounded by
	// healthProbeAfterRestart's own 15s timeout AND the early-abort
	// on doneCh close — in practice this returns within tens of ms.
	p.probeWg.Wait()
}

// healthProbeAfterRestart polls the configured health URL up to 10
// times at 1-second intervals. Logs success or failure but does not
// block restart() — the probe is purely diagnostic.
//
// Aborts early if doneCh is closed by stop(): without this, the probe
// goroutine outlives the test that started it and races on the
// package-level log when withTestLogger restores the previous logger.
//
// Uses a fresh context: restart() may be invoked from a Gin handler
// whose ctx is canceled before the new child becomes ready, but we
// want the probe to outlive the triggering request.
func (p *managedProcess) healthProbeAfterRestart() {
	p.mu.Lock()
	doneCh := p.doneCh
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 10; i++ {
		select {
		case <-doneCh:
			return // supervisor shut down; abandon probe
		case <-time.After(time.Second):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.healthCheckURL, nil)
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
}

// defaultOpencodeCmdFactory builds the production *exec.Cmd that runs
// `opencode serve` on the well-known port. Pulled out so tests can
// substitute a fake without touching this function.
func defaultOpencodeCmdFactory() *exec.Cmd {
	// G204: argument list is fixed at compile time; agentd.AgentPort
	// is a typed int constant. The only "variable" here is
	// fmt.Sprintf converting that constant to a string. noctx:
	// opencode is a long-running daemon, no per-call deadline.
	//nolint:gosec,noctx // G204/noctx: fixed argv, daemon process
	cmd := exec.Command("opencode", "serve", "--hostname", "0.0.0.0", "--port", fmt.Sprintf("%d", agentd.AgentPort))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = buildEnvFrom(agentd.SecretsEnvPath)
	return cmd
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// requireBearerToken wraps an http.Handler so that requests must carry
// `Authorization: Bearer <token>` matching the configured token. When
// the token is empty (env unset), the handler runs unprotected — this
// lets development / kind clusters skip the wiring while production
// gets defense-in-depth.
//
// Closes F1.4.2 (Epic 17 Phase 1): pre-fix /v1/statusz, /v1/readyz,
// and /v1/healthz on the agentd admin port were reachable from any
// pod in the workspace namespace that could route to the workspace
// pod IP. The chart's NetPol (G16) blocks workspace-to-workspace
// ingress, but a misconfigured cluster (NetPol disabled, CNI bug,
// operator opted out) would let any tenant probe another's session
// list. Token auth is the application-layer defense.
func requireBearerToken(token string, h http.Handler) http.Handler {
	if token == "" {
		return h
	}
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agentd"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}
