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
