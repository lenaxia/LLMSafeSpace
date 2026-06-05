// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package relaycftest also contains unit tests for the auth content
// JSON structure that feeds into opencode's provider system.
package relaycftest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthContentJSON_Structure validates that the OPENCODE_AUTH_CONTENT
// JSON we generate matches the schema opencode expects:
//
//	{"opencode": {"type":"api", "key":"public", "metadata":{"baseURL":"..."}}}
//
// This is the contract between our controller and opencode's AccountPlugin
// (account.ts:36 — Object.assign(provider.options.aisdk.provider, metadata)).
func TestAuthContentJSON_Structure(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKey  string
		wantURL  string
		wantMeta bool
	}{
		{
			name:     "without relay (default)",
			content:  `{"opencode":{"type":"api","key":"public"}}`,
			wantKey:  "public",
			wantURL:  "",
			wantMeta: false,
		},
		{
			name:     "with relay URL",
			content:  `{"opencode":{"type":"api","key":"public","metadata":{"baseURL":"https://relay.safespaces.dev"}}}`,
			wantKey:  "public",
			wantURL:  "https://relay.safespaces.dev",
			wantMeta: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var parsed map[string]struct {
				Type     string            `json:"type"`
				Key      string            `json:"key"`
				Metadata map[string]string `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal([]byte(tt.content), &parsed))

			oc, ok := parsed["opencode"]
			require.True(t, ok, "must have 'opencode' key")
			assert.Equal(t, "api", oc.Type)
			assert.Equal(t, tt.wantKey, oc.Key)

			if tt.wantMeta {
				require.NotNil(t, oc.Metadata)
				assert.Equal(t, tt.wantURL, oc.Metadata["baseURL"])
			} else {
				assert.Nil(t, oc.Metadata)
			}
		})
	}
}

// TestAuthContentJSON_MetadataOverridesBaseURL documents the critical
// assumption: metadata fields are spread into provider.options.aisdk.provider
// by opencode's AccountPlugin. If this contract changes, the relay breaks.
//
// This test validates the JSON shape matches what opencode expects, NOT
// that opencode actually processes it (that requires the integration tests
// in relay_contract_test.go or a live cluster).
func TestAuthContentJSON_MetadataOverridesBaseURL(t *testing.T) {
	content := `{"opencode":{"type":"api","key":"public","metadata":{"baseURL":"https://relay.safespaces.dev"}}}`

	// Simulate what opencode's AccountPlugin does:
	// Object.assign(provider.options.aisdk.provider, credential.metadata)
	var cred struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	var wrapper map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(content), &wrapper))
	require.NoError(t, json.Unmarshal(wrapper["opencode"], &cred))

	// Simulate: provider.options.aisdk.provider starts with {apiKey: "public"}
	providerOptions := map[string]string{"apiKey": cred.Key}

	// AccountPlugin: Object.assign(providerOptions, metadata)
	for k, v := range cred.Metadata {
		providerOptions[k] = v
	}

	// After assign: apiKey is preserved, baseURL is added
	assert.Equal(t, "public", providerOptions["apiKey"],
		"apiKey must survive metadata merge (not overwritten)")
	assert.Equal(t, "https://relay.safespaces.dev", providerOptions["baseURL"],
		"baseURL must be present after metadata merge")
}

// TestAuthContentJSON_WithSecret verifies that when a relay secret is
// provided, the baseURL in OPENCODE_AUTH_CONTENT includes the secret as
// the first path segment: https://relay.example.com/<secret>
//
// The Worker strips this segment before forwarding to upstream, so opencode
// never sends the secret to opencode.ai — it's consumed at the CF edge.
func TestAuthContentJSON_WithSecret(t *testing.T) {
	const relayURL = "https://relay.safespaces.dev"
	const secret = "supersecret123"

	content := buildAuthContent(relayURL, secret)

	var parsed map[string]struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"]
	require.NotNil(t, oc.Metadata)
	assert.Equal(t, relayURL+"/"+secret, oc.Metadata["baseURL"],
		"baseURL must embed secret as first path segment")
	// Secret must not appear in the key field
	assert.NotContains(t, oc.Key, secret)
}

// TestAuthContentJSON_NoSecret verifies that without a secret the baseURL
// is the plain relay URL (no path appended).
func TestAuthContentJSON_NoSecret(t *testing.T) {
	const relayURL = "https://relay.safespaces.dev"

	content := buildAuthContent(relayURL, "")

	var parsed map[string]struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"]
	require.NotNil(t, oc.Metadata)
	assert.Equal(t, relayURL, oc.Metadata["baseURL"],
		"baseURL must be plain relay URL when no secret")
}

// TestAuthContentJSON_NoRelay verifies that without a relay URL, no metadata
// is included (no baseURL override).
func TestAuthContentJSON_NoRelay(t *testing.T) {
	content := buildAuthContent("", "")

	var parsed map[string]struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"]
	assert.Nil(t, oc.Metadata, "no metadata when relay URL is empty")
}

// buildAuthContent mirrors the logic in
// controller/internal/workspace/pod_builder.go:buildOpenCodeAuthContent.
// Kept here as a pure function for test isolation — changes to the
// controller implementation must be reflected here.
func buildAuthContent(relayURL, secret string) string {
	if relayURL == "" {
		return `{"opencode":{"type":"api","key":"public"}}`
	}
	baseURL := relayURL
	if secret != "" {
		baseURL = relayURL + "/" + secret
	}
	type meta struct {
		BaseURL string `json:"baseURL"`
	}
	type entry struct {
		Type     string `json:"type"`
		Key      string `json:"key"`
		Metadata meta   `json:"metadata"`
	}
	content := map[string]entry{
		"opencode": {Type: "api", Key: "public", Metadata: meta{BaseURL: baseURL}},
	}
	out, _ := json.Marshal(content)
	return string(out)
}
