// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// F1.4.2 (Epic 17 Phase 1): /v1/statusz and /v1/readyz on the agentd
// admin port were unauthenticated. requireBearerToken wraps them
// when AGENTD_ADMIN_TOKEN is set.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestG7_F142_RequireBearerToken_RejectsUnauthenticated(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := requireBearerToken("expected-token", inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/statusz", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, called, "inner handler must not be called for unauthenticated request")
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
}

func TestG7_F142_RequireBearerToken_RejectsWrongToken(t *testing.T) {
	wrapped := requireBearerToken("expected", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler must not be called")
	}))

	for _, header := range []string{
		"Bearer wrong-token",
		"Bearer ",
		"expected", // bare token without Bearer
		"Basic expected",
	} {
		t.Run(header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "header %q must be rejected", header)
		})
	}
}

func TestG7_F142_RequireBearerToken_AcceptsCorrectToken(t *testing.T) {
	called := false
	wrapped := requireBearerToken("the-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "inner handler must be called for authenticated request")
}

func TestG7_F142_RequireBearerToken_EmptyTokenIsBypass(t *testing.T) {
	// AGENTD_ADMIN_TOKEN unset (empty string) → no auth required.
	// This lets dev / kind clusters skip the wiring while production
	// gets defense-in-depth.
	called := false
	wrapped := requireBearerToken("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "empty token must let the request pass through")
}
