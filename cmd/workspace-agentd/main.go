package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
)

const (
	agentAddr  = "http://localhost:4096"
	listenAddr = "0.0.0.0:4097"
)

var log *zap.Logger

type AgentClient interface {
	IsHealthy(ctx context.Context) (healthy bool, version string, err error)
	ConnectedProviders(ctx context.Context) ([]string, error)
	ConfiguredProviderCount(ctx context.Context) (int, error)
}

type OpenCodeClient struct {
	password string
	client   *http.Client
}

func (c *OpenCodeClient) doRequest(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", agentAddr+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", c.password)
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

type providerCache struct {
	mu            sync.Mutex
	connected     []string
	configured    int
	lastFetchedAt time.Time
}

const connectedCacheTTL = 30 * time.Second

func cachedConnected(ctx context.Context, client AgentClient, cache *providerCache) ([]string, int) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Since(cache.lastFetchedAt) < connectedCacheTTL && cache.connected != nil {
		return cache.connected, cache.configured
	}
	connected, connErr := client.ConnectedProviders(ctx)
	configured, cfgErr := client.ConfiguredProviderCount(ctx)
	if connErr != nil {
		log.Warn("failed to fetch connected providers", zap.Error(connErr))
	}
	if cfgErr != nil {
		log.Warn("failed to fetch configured provider count", zap.Error(cfgErr))
	}
	cache.connected = connected
	cache.configured = configured
	cache.lastFetchedAt = time.Now()
	return connected, configured
}

func main() {
	var err error
	log, err = zap.NewProduction()
	if err != nil {
		log = zap.NewNop()
	}
	defer log.Sync()

	supervise := len(os.Args) > 1 && os.Args[1] == "--supervise"

	pw, err := os.ReadFile("/sandbox-cfg/password")
	if err != nil {
		log.Warn("failed to read password file", zap.String("path", "/sandbox-cfg/password"), zap.Error(err))
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
		connected, configured := cachedConnected(r.Context(), client, cache)
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
		connected, configured := cachedConnected(r.Context(), client, cache)
		ready := healthy && len(connected) > 0
		json.NewEncoder(w).Encode(agentd.StatuszResponse{
			Healthy:             healthy,
			Ready:               ready,
			Connected:           connected,
			ProvidersConfigured: configured,
			SessionsActive:      0,
			SessionsError:       0,
			LastError:           "",
			AgentType:           "opencode",
			AgentVersion:        version,
			UptimeSeconds:       int(time.Since(startedAt).Seconds()),
		})
	})

	log.Info("workspace-agentd starting", zap.String("addr", listenAddr))
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal("workspace-agentd server failed", zap.Error(err))
	}
}

// managedProcess supervises the opencode serve process.
type managedProcess struct {
	mu             sync.Mutex
	cmd            *exec.Cmd
	restartCount   int
	lastRestartAt  time.Time
	stopping       bool
}

const (
	maxBackoffSec  = 30
	healthCheckURL = "http://localhost:4096/v1/readyz"
)

func (p *managedProcess) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = false
	p.cmd = exec.Command("opencode", "serve", "--hostname", "0.0.0.0", "--port", "4096")
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
	if a < b { return a }
	return b
}

func buildEnv() []string {
	env := os.Environ()
	// Source secrets-env file if it exists
	data, err := os.ReadFile("/tmp/secrets-env")
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

const secretsBaseDir = "/home/sandbox/.secrets"

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
	os.Remove("/tmp/agent-config.json")
	os.Remove("/tmp/secrets-env")

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
			err = os.WriteFile("/tmp/agent-config.json", []byte(s.Plaintext), 0600)

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
			f, err = os.OpenFile("/tmp/secrets-env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
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
