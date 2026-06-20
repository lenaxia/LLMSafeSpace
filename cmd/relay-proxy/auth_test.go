// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireToken_AcceptsMatchingToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := requireToken("secret-abc", inner)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(TokenHeader, "secret-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "matching token must reach the inner handler")
	assert.Equal(t, "ok", rec.Body.String())
}

func TestRequireToken_RejectsMissingHeader(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := requireToken("secret-abc", inner)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.False(t, called, "inner handler must NOT be called when token header is absent")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "token", "401 body should hint at the missing auth")
}

func TestRequireToken_RejectsWrongToken(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := requireToken("secret-abc", inner)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(TokenHeader, "wrong-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.False(t, called, "inner handler must NOT be called when token is wrong")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_RejectsEmptyToken(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := requireToken("secret-abc", inner)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	req.Header.Set(TokenHeader, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_NoTokenConfigured_AllowsAll(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := requireToken("", inner)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, called, "with no token configured the gate is disabled (backwards-compat / healthz reuse)")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestRequireToken_DifferentHeaderNames doc-decides the header name. We pin it
// so the router and proxy agree without further coordination. X-Relay-Token is
// a custom header (no caching/standard-semantics to worry about) and is
// unambiguous about its purpose.
func TestRequireToken_HeaderName(t *testing.T) {
	require.Equal(t, "X-Relay-Token", TokenHeader,
		"TokenHeader must be stable — the relay-router sends this exact header name")
}

// TestRequireToken_HealthzAndMetricsExempt verifies that health and metrics
// endpoints are NOT token-gated — the router's health-checker needs to probe
// /healthz without knowing a relay-specific token (the check is "is the proxy
// up", not "am I authorized"). buildMux centralises this exemption.
func TestRequireToken_HealthzAndMetricsExempt(t *testing.T) {
	mux := buildMux("secret-abc", dummyProxy{}, newRelayMetrics())

	for _, path := range []string{"/healthz", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
			"%s must NOT require a token (router health probe + scrape path)", path)
	}
}

// dummyProxy satisfies http.Handler for the mux test without needing a real upstream.
type dummyProxy struct{}

func (dummyProxy) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
