package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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

	pw, err := os.ReadFile("/sandbox-cfg/password")
	if err != nil {
		log.Warn("failed to read password file", zap.String("path", "/sandbox-cfg/password"), zap.Error(err))
	}
	password := strings.TrimSpace(string(pw))

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
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := materializeSecrets(secrets); err != nil {
			log.Error("reload-secrets failed", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("secrets reloaded", zap.Int("count", len(secrets)))
		w.WriteHeader(http.StatusNoContent)
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

	// Clean previous secret files (full replace semantics)
	os.RemoveAll(secretsBaseDir)
	os.MkdirAll(secretsBaseDir, 0700)
	os.RemoveAll(sshDir)
	os.MkdirAll(sshDir, 0700)
	os.Remove(home + "/.git-credentials")
	os.Remove("/tmp/agent-config.json")
	os.Remove("/tmp/secrets-env")

	var envLines []string

	for _, s := range secrets {
		var meta map[string]string
		json.Unmarshal(s.Metadata, &meta)

		switch s.Type {
		case "llm-provider":
			os.WriteFile("/tmp/agent-config.json", []byte(s.Plaintext), 0600)

		case "ssh-key":
			keyType := meta["key_type"]
			if keyType == "" {
				keyType = "ed25519"
			}
			keyPath := sshDir + "/id_" + keyType + "_" + s.Name
			os.WriteFile(keyPath, []byte(s.Plaintext), 0600)
			host := meta["host"]
			if host == "" {
				host = "github.com"
			}
			configEntry := "Host " + host + "\n    IdentityFile " + keyPath + "\n    StrictHostKeyChecking accept-new\n"
			f, _ := os.OpenFile(sshDir+"/config", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			f.WriteString(configEntry)
			f.Close()

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
			f, _ := os.OpenFile(home+"/.git-credentials", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			f.WriteString(line)
			f.Close()

		case "secret-file":
			mountPath := meta["mount_path"]
			if mountPath == "" {
				continue
			}
			// Force all secret files under the safe base dir
			if !strings.HasPrefix(mountPath, secretsBaseDir) {
				mountPath = secretsBaseDir + "/" + strings.TrimPrefix(mountPath, "/")
			}
			os.MkdirAll(mountPath[:strings.LastIndex(mountPath, "/")], 0700)
			os.WriteFile(mountPath, []byte(s.Plaintext), 0600)

		case "env-secret":
			varName := meta["var_name"]
			if varName != "" {
				envLines = append(envLines, "export "+varName+"='"+s.Plaintext+"'")
				os.Setenv(varName, s.Plaintext)
			}
		}
	}

	// Write env file
	if len(envLines) > 0 {
		os.WriteFile("/tmp/secrets-env", []byte(strings.Join(envLines, "\n")+"\n"), 0600)
	} else {
		os.Remove("/tmp/secrets-env")
	}

	return nil
}
