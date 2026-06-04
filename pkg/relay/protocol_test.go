// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAllowedHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"opencode.ai allowed", "opencode.ai", true},
		{"api.opencode.ai allowed", "api.opencode.ai", true},
		{"random host blocked", "evil.com", false},
		{"empty string blocked", "", false},
		{"subdomain not allowed", "sub.opencode.ai", false},
		{"prefix not allowed", "notopencode.ai", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsAllowedHost(tt.host))
		})
	}
}

func TestProxyRequestJSON(t *testing.T) {
	req := ProxyRequest{
		Type:    TypeProxyRequest,
		ID:      "req_abc123",
		Method:  "POST",
		URL:     "https://opencode.ai/v1/chat/completions",
		Headers: map[string]string{"content-type": "application/json", "authorization": "Bearer public"},
		Body:    `{"model":"test","messages":[]}`,
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded ProxyRequest
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, req, decoded)
}

func TestProxyResponseStartJSON(t *testing.T) {
	resp := ProxyResponseStart{
		Type:    TypeProxyResponseStart,
		ID:      "req_abc123",
		Status:  200,
		Headers: map[string]string{"content-type": "text/event-stream"},
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ProxyResponseStart
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, resp, decoded)
}

func TestProxyResponseChunkJSON(t *testing.T) {
	chunk := ProxyResponseChunk{
		Type: TypeProxyResponseChunk,
		ID:   "req_abc123",
		Data: `data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n",
	}
	data, err := json.Marshal(chunk)
	require.NoError(t, err)

	var decoded ProxyResponseChunk
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, chunk, decoded)
}

func TestProxyErrorJSON(t *testing.T) {
	pe := ProxyError{
		Type:   TypeProxyError,
		ID:     "req_abc123",
		Error:  "CORS blocked",
		Status: 0,
	}
	data, err := json.Marshal(pe)
	require.NoError(t, err)

	var decoded ProxyError
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, pe, decoded)
}

func TestEnvelopeDecode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType string
		wantID   string
	}{
		{"proxy request", `{"type":"proxy_request","id":"r1"}`, TypeProxyRequest, "r1"},
		{"proxy response start", `{"type":"proxy_response_start","id":"r2"}`, TypeProxyResponseStart, "r2"},
		{"proxy chunk", `{"type":"proxy_response_chunk","id":"r3"}`, TypeProxyResponseChunk, "r3"},
		{"proxy end", `{"type":"proxy_response_end","id":"r4"}`, TypeProxyResponseEnd, "r4"},
		{"proxy error", `{"type":"proxy_error","id":"r5"}`, TypeProxyError, "r5"},
		{"ping", `{"type":"ping"}`, TypePing, ""},
		{"pong", `{"type":"pong"}`, TypePong, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env Envelope
			require.NoError(t, json.Unmarshal([]byte(tt.input), &env))
			assert.Equal(t, tt.wantType, env.Type)
			assert.Equal(t, tt.wantID, env.ID)
		})
	}
}

func TestConstants(t *testing.T) {
	// Validate the protocol constants haven't drifted
	assert.Equal(t, "proxy_request", TypeProxyRequest)
	assert.Equal(t, "proxy_response_start", TypeProxyResponseStart)
	assert.Equal(t, "proxy_response_chunk", TypeProxyResponseChunk)
	assert.Equal(t, "proxy_response_end", TypeProxyResponseEnd)
	assert.Equal(t, "proxy_error", TypeProxyError)
	assert.Equal(t, "ping", TypePing)
	assert.Equal(t, "pong", TypePong)
}
