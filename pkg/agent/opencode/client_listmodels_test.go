// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

// TestListModels_LargeProviderCatalog_NotTruncated guards the regression
// fixed in worklog 0372 (C2): the /provider response from opencode contains
// all 139+ providers from models.dev (~5 MB). The read limit must accommodate
// the full catalog; a 1 MiB limit silently truncates the JSON and breaks the
// model selector. See commit 7213e32a for the original 32 MiB rationale.
func TestListModels_LargeProviderCatalog_NotTruncated(t *testing.T) {
	const fourMiB = 4 * 1024 * 1024
	body := []byte("[" + strings.Repeat(`{"id":"x"},`, 1) + strings.Repeat(" ", fourMiB) + "]")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pw", nil)
	got, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("response truncated: got %d bytes, want %d (read limit too small)", len(got), len(body))
	}
}

// TestListModels_SetsBasicAuth confirms the credential path is exercised.
func TestListModels_SetsBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != agentd.AuthUsername || pass != "pw" {
			t.Errorf("missing/invalid basic auth: user=%q pass=%q ok=%v", user, pass, ok)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pw", nil)
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
}

// TestListModels_ServerError_ReturnsError confirms a 4xx/5xx is surfaced.
func TestListModels_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("busy"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "pw", nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatalf("expected error for 503 response, got nil")
	}
}
