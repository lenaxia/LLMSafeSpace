// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBootstrapToken(t *testing.T, dir string) string {
	t.Helper()
	tokenPath := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("fake-token"), 0600))
	return tokenPath
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func runBootstrap(args []string) int {
	return runBootstrapCommand(args, io.Discard, io.Discard)
}

func TestRunBootstrapCommand_Success(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	secretsPayload := []map[string]any{
		{"type": "llm-provider", "name": "test", "plaintext": "sk-test"},
	}
	wsCfg := map[string]any{"defaultModel": "glm-5.2"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/pod-bootstrap", r.URL.Path)
		require.Equal(t, "Bearer fake-token", r.Header.Get("Authorization"))

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "ws-123", body["workspaceID"])

		resp := bootstrapResponse{
			Secrets:         rawJSON(t, secretsPayload),
			WorkspaceConfig: rawJSON(t, wsCfg),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	code := runBootstrap([]string{
		"--workspace-id", "ws-123",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got, 1)
	assert.Equal(t, "sk-test", got[0]["plaintext"])

	// workspace-config.json must be written as a sibling.
	cfgPath := filepath.Join(filepath.Dir(outPath), "workspace-config.json")
	cfgData, err := os.ReadFile(cfgPath)
	require.NoError(t, err, "workspace-config.json must be written on success")
	var gotCfg map[string]any
	require.NoError(t, json.Unmarshal(cfgData, &gotCfg))
	assert.Equal(t, "glm-5.2", gotCfg["defaultModel"])
}

func TestRunBootstrapCommand_404_WritesEmpty(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	code := runBootstrap([]string{
		"--workspace-id", "ws-404",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(data), "404 must produce empty secrets array")
}

func TestRunBootstrapCommand_500_Degrades(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	code := runBootstrap([]string{
		"--workspace-id", "ws-500",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	data, _ := os.ReadFile(outPath)
	assert.Equal(t, "[]", string(data), "5xx must degrade to empty secrets")
}

func TestRunBootstrapCommand_NetworkError_Degrades(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close() // close immediately → network error

	code := runBootstrap([]string{
		"--workspace-id", "ws-net",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	data, _ := os.ReadFile(outPath)
	assert.Equal(t, "[]", string(data), "network error must degrade to empty secrets")
}

func TestRunBootstrapCommand_TokenFileMissing_Degrades(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")

	code := runBootstrap([]string{
		"--workspace-id", "ws-notoken",
		"--api-url", "http://localhost:1",
		"--token-file", filepath.Join(dir, "nonexistent"),
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	data, _ := os.ReadFile(outPath)
	assert.Equal(t, "[]", string(data), "missing token must degrade to empty secrets")
}

func TestRunBootstrapCommand_FileMode(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(bootstrapResponse{
			Secrets:         rawJSON(t, []map[string]any{{"name": "x"}}),
			WorkspaceConfig: rawJSON(t, map[string]any{}),
		})
	}))
	defer srv.Close()

	code := runBootstrap([]string{
		"--workspace-id", "ws-mode",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "secrets.json must be mode 0600")
}

func TestRunBootstrapCommand_EnvFallback(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(bootstrapResponse{
			Secrets:         rawJSON(t, []map[string]any{}),
			WorkspaceConfig: rawJSON(t, map[string]any{}),
		})
	}))
	defer srv.Close()

	t.Setenv("LLMSAFESPACE_API_URL", srv.URL)

	code := runBootstrap([]string{
		"--workspace-id", "ws-env",
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code, "--api-url absent must fall back to LLMSAFESPACE_API_URL env var")
}

func TestRunBootstrapCommand_MissingWorkspaceID_Errors(t *testing.T) {
	dir := t.TempDir()
	code := runBootstrap([]string{
		"--token-file", filepath.Join(dir, "token"),
		"--out", filepath.Join(dir, "out.json"),
	})
	assert.NotEqual(t, 0, code, "missing --workspace-id must error")
}

func TestRunBootstrapCommand_SuccessNoWorkspaceConfig(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "secrets.json")
	tokenPath := writeBootstrapToken(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(bootstrapResponse{
			Secrets:         rawJSON(t, []map[string]any{{"name": "x"}}),
			WorkspaceConfig: nil,
		})
	}))
	defer srv.Close()

	code := runBootstrap([]string{
		"--workspace-id", "ws-nocfg",
		"--api-url", srv.URL,
		"--token-file", tokenPath,
		"--out", outPath,
	})
	require.Equal(t, 0, code)

	// secrets.json must still be written.
	data, _ := os.ReadFile(outPath)
	assert.Contains(t, string(data), "\"name\":\"x\"")

	// workspace-config.json must NOT be written when WorkspaceConfig is nil.
	cfgPath := filepath.Join(filepath.Dir(outPath), "workspace-config.json")
	_, err := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(err), "workspace-config.json must not be written when config is nil/empty")
}
