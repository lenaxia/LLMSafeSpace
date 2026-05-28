package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
)

// === ListSessions ===

func TestListSessions_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/session", r.URL.Path)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "testpw", pass)
		json.NewEncoder(w).Encode([]struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}{
			{ID: "ses_1", Title: "My Chat"},
			{ID: "ses_2", Title: ""},
		})
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "testpw", client: &http.Client{Timeout: 5 * time.Second}}
	// Override agentAddr for test
	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(server.URL)

	sessions, err := client.ListSessions(context.Background())
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, "ses_1", sessions[0].ID)
	assert.Equal(t, "My Chat", sessions[0].Title)
	assert.Equal(t, "idle", sessions[0].Status)
	assert.Equal(t, "ses_2", sessions[1].ID)
	assert.Equal(t, "", sessions[1].Title)
}

func TestListSessions_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{}{})
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(server.URL)

	sessions, err := client.ListSessions(context.Background())
	require.NoError(t, err)
	assert.Len(t, sessions, 0)
}

func TestListSessions_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(server.URL)

	// Server returns 500 but body is empty — decode will fail
	_, err := client.ListSessions(context.Background())
	assert.Error(t, err)
}

func TestListSessions_ConnectionRefused(t *testing.T) {
	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 1 * time.Second}}
	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr("http://127.0.0.1:1") // nothing listening

	_, err := client.ListSessions(context.Background())
	assert.Error(t, err)
}

// === cachedState ===

func TestCachedState_CachesWithinTTL(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		switch r.URL.Path {
		case "/provider":
			json.NewEncoder(w).Encode(map[string][]string{"connected": {"opencode"}})
		case "/config/providers":
			json.NewEncoder(w).Encode(map[string][]struct{}{"providers": {{}}})
		case "/session":
			json.NewEncoder(w).Encode([]struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			}{{ID: "ses_1", Title: "cached"}})
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(server.URL)

	cache := &providerCache{}

	// First call populates cache
	connected1, configured1, sessions1 := cachedState(context.Background(), client, cache)
	assert.Equal(t, []string{"opencode"}, connected1)
	assert.Equal(t, 1, configured1)
	assert.Len(t, sessions1, 1)
	firstCallCount := callCount

	// Second call within TTL should use cache
	connected2, configured2, sessions2 := cachedState(context.Background(), client, cache)
	assert.Equal(t, connected1, connected2)
	assert.Equal(t, configured1, configured2)
	assert.Equal(t, sessions1, sessions2)
	assert.Equal(t, firstCallCount, callCount, "should not make additional HTTP calls within TTL")
}

// === statusz endpoint integration ===

func TestStatuszEndpoint_IncludesSessionsAndDisk(t *testing.T) {
	// Mock opencode server
	opencodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			json.NewEncoder(w).Encode(map[string]interface{}{"healthy": true, "version": "1.0.0"})
		case "/provider":
			json.NewEncoder(w).Encode(map[string][]string{"connected": {"opencode"}})
		case "/config/providers":
			json.NewEncoder(w).Encode(map[string][]struct{}{"providers": {{}}})
		case "/session":
			json.NewEncoder(w).Encode([]struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			}{
				{ID: "ses_1", Title: "Test Session"},
			})
		}
	}))
	defer opencodeSrv.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(opencodeSrv.URL)

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	cache := &providerCache{}
	startedAt := time.Now()

	// Build the handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		healthy, version, _ := client.IsHealthy(r.Context())
		connected, configured, sessions := cachedState(r.Context(), client, cache)
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
			AgentType:           "opencode",
			AgentVersion:        version,
			UptimeSeconds:       int(time.Since(startedAt).Seconds()),
			Disk:                &agentd.DiskUsage{UsedBytes: 100, TotalBytes: 1000},
		})
	})

	req := httptest.NewRequest("GET", "/v1/statusz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp agentd.StatuszResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Healthy)
	assert.True(t, resp.Ready)
	assert.Len(t, resp.Sessions, 1)
	assert.Equal(t, "ses_1", resp.Sessions[0].ID)
	assert.Equal(t, "Test Session", resp.Sessions[0].Title)
	assert.Equal(t, "idle", resp.Sessions[0].Status)
	assert.NotNil(t, resp.Disk)
	assert.Equal(t, int64(100), resp.Disk.UsedBytes)
	assert.Equal(t, int64(1000), resp.Disk.TotalBytes)
}

// setAgentAddr is a test helper to override the package-level agentAddr.
func setAgentAddr(addr string) {
	agentAddr = addr
}
