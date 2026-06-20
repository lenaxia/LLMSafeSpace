// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/subtle"
	"net/http"
)

// TokenHeader is the HTTP header carrying the relay shared-secret token. The
// relay-router sets it on every request it forwards to a relay VM; the
// relay-proxy validates it via requireToken. Pinned by test so both sides
// agree without runtime coordination.
const TokenHeader = "X-Relay-Token"

// requireToken returns middleware that rejects requests whose X-Relay-Token
// header does not constant-time-match the expected token. If expected is empty,
// the gate is disabled (the proxy runs without auth — for local dev or the
// backwards-compatible path before the router is upgraded to send tokens).
//
// Comparison uses crypto/subtle.ConstantTimeCompare to avoid timing oracles.
// On mismatch the handler returns 401 with a brief body and does NOT call the
// inner handler, so an unauthenticated caller cannot reach the upstream.
func requireToken(expected string, next http.Handler) http.Handler {
	if expected == "" {
		return next
	}
	expectedBytes := []byte(expected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(TokenHeader)
		if subtle.ConstantTimeCompare([]byte(got), expectedBytes) != 1 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized: invalid or missing relay token\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// buildMux wires the proxy's HTTP routes. /healthz and /metrics are NOT
// token-gated (router health probes and Prometheus scrapes need to reach them
// without a per-relay secret); the catch-all / handler is gated.
func buildMux(token string, proxy http.Handler, metrics *relayMetrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/metrics", metricsHandler(metrics))
	mux.Handle("/", requireToken(token, proxy))
	return mux
}
