// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// Client communicates with a running opencode instance's HTTP API.
// It implements credential injection via PUT /auth/:providerID (Control API)
// and instance disposal via POST /instance/dispose.
//
// Every opencode endpoint — including /auth/* and /instance/* — is gated
// by HTTP Basic auth with username `agentd.AuthUsername` and the
// per-pod password mounted at /sandbox-cfg/password
// (= OPENCODE_SERVER_PASSWORD env var). Calling these endpoints without
// auth produces 401 + WWW-Authenticate: Basic realm="Secure Area",
// which is what broke the live credential flow in worklog 0125.
type Client struct {
	baseURL    string
	password   string
	httpClient *http.Client
}

// NewClient creates a Client targeting the given opencode base URL.
//
// password is the value mounted at /sandbox-cfg/password inside the
// sandbox pod and exported to opencode as OPENCODE_SERVER_PASSWORD. It
// is the SAME secret used by every other agentd → opencode call (see
// cmd/workspace-agentd/main.go OpenCodeClient). Passing the empty
// string is allowed (so unit tests that don't need auth-gated paths
// still work) but will fail against a real opencode server with 401.
func NewClient(baseURL, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		password: password,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// PushCredentials writes each provider's API key to opencode's auth store
// via PUT /auth/:providerID. This writes to auth.json but does NOT trigger
// provider state refresh — call DisposeInstance or (future) RefreshProviders
// afterward to pick up the new credentials.
//
// Returns nil if providers is empty (no-op).
// Returns the first error encountered; subsequent providers are not attempted.
func (c *Client) PushCredentials(ctx context.Context, providers []secrets.LLMProviderData) error {
	if len(providers) == 0 {
		return nil
	}

	for _, p := range providers {
		if err := c.setAuth(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// DisposeInstance triggers POST /instance/dispose, which invalidates all
// InstanceState caches for the current instance. The opencode process stays
// alive; the next request triggers a fresh instance load with updated auth.
//
// In-flight LLM calls are aborted. Sessions persist in SQLite.
func (c *Client) DisposeInstance(ctx context.Context) error {
	url := c.baseURL + "/instance/dispose"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("POST /instance/dispose: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(agentd.AuthUsername, c.password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /instance/dispose: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort drain
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST /instance/dispose returned %d", resp.StatusCode)
	}
	return nil
}

// StageCredentials writes provider credentials to opencode's auth.json
// (via PUT /auth/:providerID) but does NOT trigger provider-state refresh.
// The credentials are "staged" — they exist on disk but opencode's
// in-memory provider state is unchanged until DisposeInstance is called
// separately by the caller (typically via POST /api/v1/workspaces/:id/agent/reload).
//
// Returns nil if providers is empty (no-op).
func (c *Client) StageCredentials(ctx context.Context, providers []secrets.LLMProviderData) error {
	return c.PushCredentials(ctx, providers)
}

// setAuth sends PUT /auth/:providerID with the credential payload.
func (c *Client) setAuth(ctx context.Context, p secrets.LLMProviderData) error {
	payload := authPayload{
		Type: "api",
		Key:  p.APIKey,
	}
	if p.BaseURL != "" {
		payload.Metadata = map[string]string{"baseURL": p.BaseURL}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal auth payload for %s: %w", p.Provider, err)
	}

	url := c.baseURL + "/auth/" + p.Provider
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT request for %s: %w", p.Provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(agentd.AuthUsername, c.password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT /auth/%s: %w", p.Provider, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort drain
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT /auth/%s returned %d", p.Provider, resp.StatusCode)
	}
	return nil
}

// authPayload matches opencode's Auth.Info schema for type:"api".
type authPayload struct {
	Type     string            `json:"type"`
	Key      string            `json:"key"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
