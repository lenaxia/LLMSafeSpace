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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	var s struct {
		Title string `json:"title"`
	}
	json.NewDecoder(resp.Body).Decode(&s)
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
	defer resp.Body.Close()

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
	total := int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bfree) * int64(stat.Bsize)
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
	defer log.Sync()

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

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		healthy, version, err := client.IsHealthy(r.Context())
		if err != nil {
			log.Warn("healthz: agent health check failed", zap.Error(err))
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(agentd.HealthzResponse{
				Healthy: false, Version: "", UptimeSeconds: 0,
			})
			return
		}
		json.NewEncoder(w).Encode(agentd.HealthzResponse{
			Healthy:       healthy,
			Version:       version,
			UptimeSeconds: int(time.Since(startedAt).Seconds()),
		})
	})

	mux.HandleFunc("/v1/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		connected, configured, _ := cachedState(r.Context(), client, cache, sseTracker)
		healthy, version, _ := client.IsHealthy(r.Context())
		ready := healthy && len(connected) > 0
		json.NewEncoder(w).Encode(agentd.ReadyzResponse{
			Ready:               ready,
			ProvidersConnected:  connected,
			ProvidersConfigured: configured,
			AgentVersion:        version,
			AgentType:           "opencode",
		})
	})

	mux.HandleFunc("/v1/reload-secrets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var secrets []struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			Metadata  json.RawMessage `json:"metadata"`
			Plaintext string          `json:"plaintext"`
		}
		if err := json.NewDecoder(r.Body).Decode(&secrets); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid json: " + err.Error()})
			return
		}
		if err := materializeSecrets(secrets); err != nil {
			log.Error("reload-secrets: materialize failed", zap.Error(err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		log.Info("secrets reloaded", zap.Int("count", len(secrets)))

		// If env vars or LLM config present, restart opencode to pick them up
		hasEnvOrLLM := false
		for _, s := range secrets {
			if s.Type == "env-secret" || s.Type == "llm-provider" {
				hasEnvOrLLM = true
				break
			}
		}
		restarted := false
		if hasEnvOrLLM && proc != nil {
			log.Info("env/llm secrets changed, restarting opencode")
			proc.restart()
			restarted = true
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"reloaded":  len(secrets),
			"restarted": restarted,
		})
	})

	mux.HandleFunc("/v1/statusz", func(w http.ResponseWriter, r *http.Request) {
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

		json.NewEncoder(w).Encode(agentd.StatuszResponse{
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

	log.Info("workspace-agentd starting", zap.String("addr", listenAddr))
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal("workspace-agentd server failed", zap.Error(err))
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
	p.cmd = exec.Command("opencode", "serve", "--hostname", "0.0.0.0", "--port", fmt.Sprintf("%d", agentd.AgentPort))
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr
	p.cmd.Env = buildEnv()
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
		cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}

	p.restartCount = 0
	p.start()

	// Verify opencode came back up (health check with timeout)
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(time.Second)
			resp, err := http.Get(healthCheckURL)
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				log.Info("opencode healthy after restart")
				return
			}
			if resp != nil {
				resp.Body.Close()
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

func buildEnv() []string {
	env := os.Environ()
	// Source secrets-env file if it exists
	data, err := os.ReadFile(agentd.SecretsEnvPath)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "export ") {
				kv := strings.TrimPrefix(line, "export ")
				// Remove surrounding quotes from value
				kv = strings.Replace(kv, "='", "=", 1)
				kv = strings.TrimSuffix(kv, "'")
				env = append(env, kv)
			}
		}
	}
	return env
}

var secretsBaseDir = agentd.SecretsBasePath

func materializeSecrets(secrets []struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Metadata  json.RawMessage `json:"metadata"`
	Plaintext string          `json:"plaintext"`
}) error {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/sandbox"
	}
	sshDir := home + "/.ssh"

	// Full replace: clean everything first
	os.RemoveAll(secretsBaseDir)
	os.MkdirAll(secretsBaseDir, 0700)
	os.RemoveAll(sshDir)
	os.MkdirAll(sshDir, 0700)
	os.Remove(home + "/.git-credentials")
	os.Remove(agentd.AgentConfigPath)
	os.Remove(agentd.SecretsEnvPath)

	var errors []string

	for _, s := range secrets {
		var meta map[string]string
		json.Unmarshal(s.Metadata, &meta)

		if s.Name == "" {
			errors = append(errors, fmt.Sprintf("%s: empty name", s.Type))
			continue
		}

		var err error
		switch s.Type {
		case "llm-provider":
			err = os.WriteFile(agentd.AgentConfigPath, []byte(s.Plaintext), 0600)

		case "ssh-key":
			keyType := meta["key_type"]
			if keyType == "" {
				keyType = "ed25519"
			}
			keyPath := sshDir + "/id_" + keyType + "_" + s.Name
			if err = os.WriteFile(keyPath, []byte(s.Plaintext), 0600); err != nil {
				break
			}
			host := meta["host"]
			if host == "" {
				host = "github.com"
			}
			configEntry := "Host " + host + "\n    IdentityFile " + keyPath + "\n    StrictHostKeyChecking accept-new\n"
			var f *os.File
			f, err = os.OpenFile(sshDir+"/config", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				f.WriteString(configEntry)
				f.Close()
			}

		case "git-credential":
			host := meta["host"]
			if host == "" {
				host = "github.com"
			}
			protocol := meta["protocol"]
			if protocol == "" {
				protocol = "https"
			}
			line := protocol + "://oauth2:" + s.Plaintext + "@" + host + "\n"
			var f *os.File
			f, err = os.OpenFile(home+"/.git-credentials", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				f.WriteString(line)
				f.Close()
			}

		case "secret-file":
			mountPath := meta["mount_path"]
			if mountPath == "" {
				errors = append(errors, fmt.Sprintf("secret-file '%s': missing mount_path", s.Name))
				continue
			}
			// Force all secret files under the safe base dir
			if !strings.HasPrefix(mountPath, secretsBaseDir) {
				mountPath = secretsBaseDir + "/" + strings.TrimPrefix(mountPath, "/")
			}
			// Prevent path traversal
			if strings.Contains(mountPath, "..") {
				errors = append(errors, fmt.Sprintf("secret-file '%s': path traversal not allowed", s.Name))
				continue
			}
			dir := mountPath[:strings.LastIndex(mountPath, "/")]
			if err = os.MkdirAll(dir, 0700); err == nil {
				err = os.WriteFile(mountPath, []byte(s.Plaintext), 0600)
			}

		case "env-secret":
			varName := meta["var_name"]
			if varName == "" {
				errors = append(errors, fmt.Sprintf("env-secret '%s': missing var_name", s.Name))
				continue
			}
			var f *os.File
			f, err = os.OpenFile(agentd.SecretsEnvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				fmt.Fprintf(f, "export %s='%s'\n", varName, s.Plaintext)
				f.Close()
			}
			os.Setenv(varName, s.Plaintext)

		default:
			errors = append(errors, fmt.Sprintf("unknown type '%s' for secret '%s'", s.Type, s.Name))
			continue
		}

		if err != nil {
			errors = append(errors, fmt.Sprintf("%s '%s': %v", s.Type, s.Name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("partial failure: %s", strings.Join(errors, "; "))
	}
	return nil
}
