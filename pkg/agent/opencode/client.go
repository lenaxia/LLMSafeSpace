// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// Client communicates with a running opencode instance's HTTP API.
// It implements credential injection via PUT /auth/:providerID (Control API)
// and instance disposal via POST /instance/dispose.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client targeting the given opencode base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
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
func (c *Client) PushCredentials(providers []secrets.LLMProviderData) error {
	if len(providers) == 0 {
		return nil
	}

	for _, p := range providers {
		if err := c.setAuth(p); err != nil {
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
func (c *Client) DisposeInstance() error {
	url := c.baseURL + "/instance/dispose"
	resp, err := c.httpClient.Post(url, "application/json", http.NoBody)
	if err != nil {
		return fmt.Errorf("POST /instance/dispose: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST /instance/dispose returned %d", resp.StatusCode)
	}
	return nil
}

// RefreshCredentials is the combined operation for Step 2:
// 1. Push all provider credentials to auth.json (PUT /auth/:providerID)
// 2. Dispose the instance so it picks up the new keys on next access
//
// If push fails, dispose is NOT called (credentials unchanged, no reason to
// disrupt the running instance).
//
// Returns nil if providers is empty (no-op, no dispose).
func (c *Client) RefreshCredentials(providers []secrets.LLMProviderData) error {
	if len(providers) == 0 {
		return nil
	}

	if err := c.PushCredentials(providers); err != nil {
		return fmt.Errorf("push credentials: %w", err)
	}

	if err := c.DisposeInstance(); err != nil {
		return fmt.Errorf("dispose instance: %w", err)
	}
	return nil
}

// setAuth sends PUT /auth/:providerID with the credential payload.
func (c *Client) setAuth(p secrets.LLMProviderData) error {
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
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT request for %s: %w", p.Provider, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT /auth/%s: %w", p.Provider, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

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
