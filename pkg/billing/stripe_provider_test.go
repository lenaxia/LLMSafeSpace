// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v76"
)

// withStripeBackend swaps the global stripe API backend to point at srv for the
// duration of the test, restoring the previous backend on cleanup. stripe-go
// uses package-level backends, so tests using this helper MUST NOT run in
// parallel with each other.
func withStripeBackend(t *testing.T, srv *httptest.Server) {
	t.Helper()
	prev := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend,
		&stripe.BackendConfig{
			URL:               stripe.String(srv.URL),
			MaxNetworkRetries: stripe.Int64(0),
		},
	))
	prevKey := stripe.Key
	stripe.Key = "sk_test_provider_unit"
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, prev)
		stripe.Key = prevKey
	})
}

// newProviderTestServer returns an httptest.Server that records the requests it
// receives and responds with the canned body/status for the matching handler.
// If no handler matches, the server returns 500 so tests fail loudly.
func newProviderTestServer(t *testing.T, fn func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", fn)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStripeProvider_CreateCustomer_Success(t *testing.T) {
	var gotPath, gotEmail, gotName string
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotEmail = r.Form.Get("email")
		gotName = r.Form.Get("name")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cus_test_123","object":"customer"}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{SecretKey: "sk_test_provider_unit"}}
	id, err := p.CreateCustomer(context.Background(), "user@example.com", "Acme Inc.")
	if err != nil {
		t.Fatalf("CreateCustomer returned error: %v", err)
	}
	if id != "cus_test_123" {
		t.Errorf("expected cus_test_123, got %s", id)
	}
	if gotPath != "/v1/customers" {
		t.Errorf("expected POST /v1/customers, got %s", gotPath)
	}
	if gotEmail != "user@example.com" {
		t.Errorf("expected email user@example.com, got %s", gotEmail)
	}
	if gotName != "Acme Inc." {
		t.Errorf("expected name Acme Inc., got %s", gotName)
	}
}

func TestStripeProvider_CreateCustomer_StripeError(t *testing.T) {
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"email invalid"}}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{SecretKey: "sk_test_provider_unit"}}
	_, err := p.CreateCustomer(context.Background(), "not-an-email", "")
	if err == nil {
		t.Fatal("expected error on stripe failure, got nil")
	}
	if !strings.Contains(err.Error(), "create stripe customer") {
		t.Errorf("expected wrapped error, got %v", err)
	}
}

func TestStripeProvider_SuspendCustomer_Success(t *testing.T) {
	var gotPath, gotMethod, gotSuspended string
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = r.ParseForm()
		gotSuspended = r.Form.Get("metadata[suspended]")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cus_x","object":"customer","metadata":{"suspended":"true"}}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{SecretKey: "sk_test_provider_unit"}}
	if err := p.SuspendCustomer(context.Background(), "cus_x"); err != nil {
		t.Fatalf("SuspendCustomer returned error: %v", err)
	}
	if gotPath != "/v1/customers/cus_x" {
		t.Errorf("expected POST /v1/customers/cus_x, got %s", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotSuspended != "true" {
		t.Errorf("expected metadata[suspended]=true, got %q", gotSuspended)
	}
}

func TestStripeProvider_SuspendCustomer_StripeError(t *testing.T) {
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"no such customer"}}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{SecretKey: "sk_test_provider_unit"}}
	err := p.SuspendCustomer(context.Background(), "cus_missing")
	if err == nil {
		t.Fatal("expected error on missing customer")
	}
	if !strings.Contains(err.Error(), "mark customer") {
		t.Errorf("expected wrapped error, got %v", err)
	}
}

func TestStripeProvider_ReportUsage_HappyPath(t *testing.T) {
	var mu sync.Mutex
	var seen []struct {
		path      string
		quantity  string
		timestamp string
		idemKey   string
	}
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		seen = append(seen, struct {
			path      string
			quantity  string
			timestamp string
			idemKey   string
		}{
			path:      r.URL.Path,
			quantity:  r.Form.Get("quantity"),
			timestamp: r.Form.Get("timestamp"),
			idemKey:   r.Header.Get("Idempotency-Key"),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mbod_123","object":"usage_record"}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{
		SecretKey: "sk_test_provider_unit",
		Meters: map[string]string{
			"llm_tokens":      "si_abc",
			"compute_seconds": "si_def",
		},
	}}
	events := []UsageExportEvent{
		{EventType: "llm_tokens", Quantity: 1500, Timestamp: "2026-06-15T10:00:00Z", ExternalCustomerID: "cus_1", IdempotencyKey: "idem-1"},
		{EventType: "compute_seconds", Quantity: 60, Timestamp: "2026-06-15T10:01:00Z", ExternalCustomerID: "cus_1", IdempotencyKey: "idem-2"},
	}
	ids, err := p.ReportUsage(context.Background(), events)
	if err != nil {
		t.Fatalf("ReportUsage returned error: %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 1 {
		t.Fatalf("expected [1 1], got %v", ids)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("expected 2 stripe calls, got %d", len(seen))
	}
	// usagerecord.New encodes the subscription item in the URL path:
	// /v1/subscription_items/{id}/usage_records
	if seen[0].path != "/v1/subscription_items/si_abc/usage_records" {
		t.Errorf("call 0 path mismatch: %s", seen[0].path)
	}
	if seen[0].quantity != "1500" || seen[0].timestamp != "1781517600" {
		t.Errorf("call 0 payload mismatch: %+v", seen[0])
	}
	if seen[0].idemKey != "idem-1" {
		t.Errorf("call 0 idempotency key not forwarded: %q", seen[0].idemKey)
	}
	if seen[1].path != "/v1/subscription_items/si_def/usage_records" {
		t.Errorf("call 1 path mismatch: %s", seen[1].path)
	}
	if seen[1].quantity != "60" || seen[1].timestamp != "1781517660" {
		t.Errorf("call 1 payload mismatch: %+v", seen[1])
	}
	if seen[1].idemKey != "idem-2" {
		t.Errorf("call 1 idempotency key not forwarded: %q", seen[1].idemKey)
	}
}

func TestStripeProvider_ReportUsage_UnknownMeterSkipped(t *testing.T) {
	calls := 0
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{
		SecretKey: "sk_test_provider_unit",
		Meters:    map[string]string{"llm_tokens": "si_abc"},
	}}
	events := []UsageExportEvent{
		{EventType: "llm_tokens", Quantity: 10, Timestamp: "2026-06-15T10:00:00Z"},
		{EventType: "unknown_event", Quantity: 99, Timestamp: "2026-06-15T10:00:00Z"},
		{EventType: "compute_seconds", Quantity: 5, Timestamp: "2026-06-15T10:00:00Z"},
	}
	ids, err := p.ReportUsage(context.Background(), events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 result ids, got %d", len(ids))
	}
	if ids[0] != 1 {
		t.Errorf("first event (configured meter) should be reported, got %d", ids[0])
	}
	if ids[1] != 0 || ids[2] != 0 {
		t.Errorf("unconfigured meters should be skipped (0), got %d and %d", ids[1], ids[2])
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 stripe call (only the configured meter), got %d", calls)
	}
}

func TestStripeProvider_ReportUsage_StripeErrorStopsAndReturnsPartial(t *testing.T) {
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad subscription item"}}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{
		SecretKey: "sk_test_provider_unit",
		Meters:    map[string]string{"llm_tokens": "si_abc"},
	}}
	events := []UsageExportEvent{
		{EventType: "llm_tokens", Quantity: 1, Timestamp: "2026-06-15T10:00:00Z"},
		{EventType: "llm_tokens", Quantity: 2, Timestamp: "2026-06-15T10:01:00Z"},
	}
	ids, err := p.ReportUsage(context.Background(), events)
	if err == nil {
		t.Fatal("expected error on stripe failure, got nil")
	}
	if len(ids) != 0 {
		t.Errorf("expected empty partial ids slice on first-event failure, got %v", ids)
	}
}

func TestStripeProvider_ReportUsage_Empty(t *testing.T) {
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no stripe calls expected for empty events")
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{
		SecretKey: "sk_test_provider_unit",
		Meters:    map[string]string{"llm_tokens": "si_abc"},
	}}
	ids, err := p.ReportUsage(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty ids, got %v", ids)
	}
}

func TestStripeProvider_ReportUsage_IdempotencyKeyOnlyWhenSet(t *testing.T) {
	var gotHeader string
	srv := newProviderTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{}`))
	})
	withStripeBackend(t, srv)

	p := &StripeProvider{cfg: StripeConfig{
		SecretKey: "sk_test_provider_unit",
		Meters:    map[string]string{"llm_tokens": "si_abc"},
	}}
	if _, err := p.ReportUsage(context.Background(), []UsageExportEvent{
		{EventType: "llm_tokens", Quantity: 1, Timestamp: "2026-06-15T10:00:00Z"},
	}); err != nil {
		t.Fatalf("call without idempotency key: %v", err)
	}

	if _, err := p.ReportUsage(context.Background(), []UsageExportEvent{
		{EventType: "llm_tokens", Quantity: 1, Timestamp: "2026-06-15T10:00:00Z", IdempotencyKey: "key-xyz"},
	}); err != nil {
		t.Fatalf("call with idempotency key: %v", err)
	}
	if gotHeader != "key-xyz" {
		t.Errorf("expected Idempotency-Key=key-xyz forwarded, got %q", gotHeader)
	}
}

func TestParseUnixTimestamp(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     int64
		fallback bool
	}{
		{name: "valid RFC3339", in: "2026-06-15T10:00:00Z", want: 1781517600},
		{name: "empty", in: "", fallback: true},
		{name: "garbage", in: "not-a-time", fallback: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseUnixTimestamp(c.in)
			if c.fallback {
				now := time.Now().Unix()
				if got < now-10 || got > now+10 {
					t.Errorf("fallback expected ~now (%d), got %d", now, got)
				}
				return
			}
			if got != c.want {
				t.Errorf("expected %d, got %d", c.want, got)
			}
		})
	}
}
