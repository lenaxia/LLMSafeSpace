// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package relay defines the protocol types for client-proxied inference (Epic 26).
// The relay protocol allows an in-pod HTTP proxy to delegate outgoing HTTP
// requests to a remote client (browser/SDK) via a WebSocket channel.
package relay

import "time"

// Message types sent over the relay WebSocket channel.
const (
	TypeProxyRequest       = "proxy_request"
	TypeProxyResponseStart = "proxy_response_start"
	TypeProxyResponseChunk = "proxy_response_chunk"
	TypeProxyResponseEnd   = "proxy_response_end"
	TypeProxyError         = "proxy_error"
	TypePing               = "ping"
	TypePong               = "pong"
)

// ProxyRequest is sent from the agentd (via API server) to the client.
// The client must execute this HTTP request and stream the response back.
type ProxyRequest struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

// ProxyResponseStart is sent from the client to the server when the
// HTTP response headers have been received.
type ProxyResponseStart struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ProxyResponseChunk is sent from the client for each chunk of response body.
type ProxyResponseChunk struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Data string `json:"data"`
}

// ProxyResponseEnd signals the response is complete.
type ProxyResponseEnd struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ProxyError is sent from the client when the HTTP request fails.
type ProxyError struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Error  string `json:"error"`
	Status int    `json:"status"`
}

// Envelope is a generic message envelope for decoding the type field first.
type Envelope struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// Configuration defaults for the relay system.
const (
	// PingInterval is the interval between keepalive pings on the WebSocket.
	PingInterval = 30 * time.Second

	// RequestTimeout is how long the proxy waits for the client to start responding.
	RequestTimeout = 5 * time.Second

	// MaxPendingRequests is the maximum concurrent proxy requests per workspace.
	MaxPendingRequests = 60

	// RateLimitPerMinute is the max proxy requests per workspace per minute.
	RateLimitPerMinute = 60

	// FallbackRateLimitPerMinute is the server-side fallback rate limit for
	// CORS failures (US-26.6).
	FallbackRateLimitPerMinute = 10
)

// AllowedProxyHosts defines the hosts that the relay proxy is permitted to
// target. Requests to any other host are rejected to prevent the client from
// being used as an open proxy.
var AllowedProxyHosts = []string{
	"opencode.ai",
	"api.opencode.ai",
}

// IsAllowedHost checks if a given host (without port) is in the allowed list.
func IsAllowedHost(host string) bool {
	for _, h := range AllowedProxyHosts {
		if host == h {
			return true
		}
	}
	return false
}
