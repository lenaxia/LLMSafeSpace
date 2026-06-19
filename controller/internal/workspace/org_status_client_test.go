// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, baseURL, token string, ttl time.Duration) *CachedOrgStatusClient {
	t.Helper()
	return &CachedOrgStatusClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: 2 * time.Second},
		ttl:        ttl,
	}
}

func orgStatusServer(t *testing.T, status string, fetchCount *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetchCount != nil {
			atomic.AddInt32(fetchCount, 1)
		}
		fmt.Fprintf(w, `{"status":%q}`, status)
	}))
}

// TestOrgStatus_CacheMiss_FetchesAndReturns: no prior entry → one live fetch,
// status returned.
func TestOrgStatus_CacheMiss_FetchesAndReturns(t *testing.T) {
	var hits int32
	srv := orgStatusServer(t, "suspended", &hits)
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", time.Minute)
	got, ok := c.GetOrgStatus(context.Background(), "org-1")
	if !ok {
		t.Fatal("expected ok=true on successful fetch")
	}
	if got != "suspended" {
		t.Errorf("expected suspended, got %q", got)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected 1 fetch, got %d", atomic.LoadInt32(&hits))
	}
}

// TestOrgStatus_CacheHit_NoRefetchWithinTTL: a second call within the TTL must
// be served from cache (no second HTTP fetch).
func TestOrgStatus_CacheHit_NoRefetchWithinTTL(t *testing.T) {
	var hits int32
	srv := orgStatusServer(t, "active", &hits)
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", time.Minute)

	for i := 0; i < 5; i++ {
		got, ok := c.GetOrgStatus(context.Background(), "org-1")
		if !ok || got != "active" {
			t.Fatalf("call %d: expected active/ok, got %q/%v", i, got, ok)
		}
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected exactly 1 fetch for 5 cached calls, got %d", atomic.LoadInt32(&hits))
	}
}

// TestOrgStatus_StaleServedOnRefreshFailure: after the TTL expires, a refresh
// is attempted; if it fails (server down), the last-known status is served
// (cache absorbs the transient failure) — NOT fail-open.
func TestOrgStatus_StaleServedOnRefreshFailure(t *testing.T) {
	var hits int32
	srv := orgStatusServer(t, "suspended", &hits)
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", 50*time.Millisecond)

	// Prime the cache.
	got, ok := c.GetOrgStatus(context.Background(), "org-1")
	if !ok || got != "suspended" {
		t.Fatalf("prime: expected suspended/ok, got %q/%v", got, ok)
	}

	// Shut down the upstream so the refresh fails.
	srv.Close()
	time.Sleep(80 * time.Millisecond) // exceed TTL

	got, ok = c.GetOrgStatus(context.Background(), "org-1")
	if !ok {
		t.Fatal("expected ok=true serving stale status, got false (would fail-open)")
	}
	if got != "suspended" {
		t.Errorf("expected stale 'suspended' served from cache, got %q", got)
	}
}

// TestOrgStatus_NoEntryAndFetchFailure_FailsOpen: with no cached entry and a
// fetch failure, the client returns ok=false so the reconciler leaves the
// workspace running (D20 fail-safe).
func TestOrgStatus_NoEntryAndFetchFailure_FailsOpen(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:0", "", time.Minute) // nothing listening
	got, ok := c.GetOrgStatus(context.Background(), "org-1")
	if ok {
		t.Fatalf("expected ok=false on fetch failure with no cache, got ok=true status=%q", got)
	}
}

// TestOrgStatus_DifferentOrgsFetchIndependently: org-1 and org-2 have separate
// cache entries.
func TestOrgStatus_DifferentOrgsFetchIndependently(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path == "/api/v1/internal/orgs/org-1/status" {
			fmt.Fprint(w, `{"status":"active"}`)
			return
		}
		fmt.Fprint(w, `{"status":"suspended"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", time.Minute)
	got1, _ := c.GetOrgStatus(context.Background(), "org-1")
	got2, _ := c.GetOrgStatus(context.Background(), "org-2")
	if got1 != "active" {
		t.Errorf("org-1: expected active, got %q", got1)
	}
	if got2 != "suspended" {
		t.Errorf("org-2: expected suspended, got %q", got2)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("expected 2 fetches (one per org), got %d", atomic.LoadInt32(&hits))
	}
}

// TestOrgStatus_DisabledClient_FailsOpen: empty baseURL → client disabled →
// ok=false (controller never org-suspends; feature off).
func TestOrgStatus_DisabledClient_FailsOpen(t *testing.T) {
	c := newTestClient(t, "", "", time.Minute)
	_, ok := c.GetOrgStatus(context.Background(), "org-1")
	if ok {
		t.Fatal("disabled client must return ok=false")
	}
}

// TestOrgStatus_NilClient_FailsOpen: a nil receiver must also fail open so the
// reconciler can call GetOrgStatus unconditionally.
func TestOrgStatus_NilClient_FailsOpen(t *testing.T) {
	var c *CachedOrgStatusClient
	_, ok := c.GetOrgStatus(context.Background(), "org-1")
	if ok {
		t.Fatal("nil client must return ok=false")
	}
}

// TestOrgStatus_TokenSentInHeader: when a token is configured it is sent as
// X-Internal-Token.
func TestOrgStatus_TokenSentInHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "sekret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"status":"active"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "sekret", time.Minute)
	got, ok := c.GetOrgStatus(context.Background(), "org-1")
	if !ok || got != "active" {
		t.Fatalf("expected active/ok with token, got %q/%v", got, ok)
	}
}

// TestOrgStatus_Non200FailsOpen: a non-200 response (e.g. 401 from wrong
// token) is a fetch failure → fail open on cache miss.
func TestOrgStatus_Non200FailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", time.Minute)
	_, ok := c.GetOrgStatus(context.Background(), "org-1")
	if ok {
		t.Fatal("non-200 response must fail open (ok=false)")
	}
}
