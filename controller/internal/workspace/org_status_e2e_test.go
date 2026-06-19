// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// These tests prove the controller's CachedOrgStatusClient agrees with the
// production API contract for the internal org-status endpoint: the client
// sends X-Internal-Token (org_status_client.go:138) and the API checks
// X-Internal-Token (internal_org_status.go:63); both use {status:...} JSON.
//
// The server side is a CONTRACT MIRROR — it reproduces the production handler's
// fail-closed + token-gate + JSON-shape logic but is NOT the real handler. The
// real handler (api/internal/handlers.InternalOrgStatusHandler) cannot be
// imported here because Go's `internal` package rule prevents
// controller/internal/workspace from importing api/internal/handlers (they are
// under different internal/ roots). The handler's own behavior is tested
// independently in internal_org_status_test.go; this test proves the CLIENT
// side of the contract: if the client renamed its header or changed its
// response parsing, these tests would fail.

// contractMirrorServer reproduces the production InternalOrgStatusHandler's
// wire contract: fail-closed when no token is configured, X-Internal-Token
// header check, and {status:...} JSON response shape. It mirrors
// internal_org_status.go:52-77.
func contractMirrorServer(expectedToken string, store func(ctx context.Context, orgID string) (*types.Organization, error)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectedToken == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Internal-Token") != expectedToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Path shape: /api/v1/internal/orgs/<orgID>/status
		segments := splitPath(r.URL.Path)
		orgID := ""
		if len(segments) >= 2 && segments[len(segments)-1] == "status" {
			orgID = segments[len(segments)-2]
		}
		org, err := store(r.Context(), orgID)
		if err != nil || org == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"active"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"` + string(org.Status) + `"}`))
	}))
}

func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	return out
}

// TestE2E_OrgStatus_ClientAgainstProductionContract proves the controller's
// CachedOrgStatusClient successfully fetches from a server that honors the
// production contract (X-Internal-Token header, {status:...} JSON). The client
// sends X-Internal-Token (org_status_client.go:138); the mirror checks it. A
// matching token → 200 + parsed status. This is the wiring that was previously
// only tested with a stub that did not validate the header.
func TestE2E_OrgStatus_ClientAgainstProductionContract(t *testing.T) {
	const token = "shared-secret-e2e"
	store := func(_ context.Context, _ string) (*types.Organization, error) {
		return &types.Organization{ID: "org-1", Status: types.OrgStatusSuspended}, nil
	}

	srv := contractMirrorServer(token, store)
	defer srv.Close()

	c := newTestClient(t, srv.URL, token, time.Minute)
	got, ok := c.GetOrgStatus(context.Background(), "org-1")
	if !ok {
		t.Fatal("expected a successful fetch (token agreement), got !ok")
	}
	if got != string(types.OrgStatusSuspended) {
		t.Fatalf("expected status=%q, got %q", types.OrgStatusSuspended, got)
	}
}

// TestE2E_OrgStatus_TokenMismatch_RejectedByContract proves the negative half:
// a mismatched token yields a fetch failure (ok == false), confirming the
// token gate on the server side is real and the client's mismatched token does
// not silently pass.
func TestE2E_OrgStatus_TokenMismatch_RejectedByContract(t *testing.T) {
	store := func(_ context.Context, _ string) (*types.Organization, error) {
		return &types.Organization{ID: "org-1", Status: types.OrgStatusActive}, nil
	}

	srv := contractMirrorServer("correct-token", store)
	defer srv.Close()

	c := newTestClient(t, srv.URL, "wrong-token", time.Minute)
	_, ok := c.GetOrgStatus(context.Background(), "org-1")
	if ok {
		t.Fatal("a mismatched token must NOT yield a successful fetch (ok must be false)")
	}
}
