// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// orgStatusSuspended is the status string the internal API endpoint returns
// for a suspended org. Kept as a local constant (rather than importing
// pkg/types) so the controller does not take a dependency on the API DTO
// package — the controller only needs the wire string.
const orgStatusSuspended = "suspended"

// OrgStatusClient reports an org's operational status. ok is false when the
// status could not be determined (API unreachable and no cached value); callers
// MUST fail open in that case (D20). ok is true whenever a value — fresh or
// stale — is available.
//
// The interface lets tests inject a fake without an HTTP server.
type OrgStatusClient interface {
	GetOrgStatus(ctx context.Context, orgID string) (status string, ok bool)
}

// OrgStatusLogger is the minimal logging surface CachedOrgStatusClient uses.
// controller-runtime's logr.Logger satisfies it; nil disables logging.
type OrgStatusLogger interface {
	Info(msg string, keysAndValues ...any)
	Error(err error, msg string, keysAndValues ...any)
}

// CachedOrgStatusClient is an OrgStatusClient backed by the API service's
// internal org-status endpoint with a per-org TTL cache.
//
// Cache semantics (D20):
//   - On a fresh cache hit (< TTL): return the cached status, no fetch.
//   - On a stale entry (>= TTL) or cache miss: attempt a fetch.
//   - Fetch success: update the cache and return the new status.
//   - Fetch failure + prior entry: serve the STALE cached status so a
//     transient API outage does not flip the controller into fail-open and
//     leave suspended orgs running. This is the "30s cache absorbs transient
//     failures" guarantee.
//   - Fetch failure + no entry: return ok=false (fail open — D20: an
//     unwarranted suspension is more disruptive than leaving a pod running).
//
// Concurrency: each orgID has its own entry guarded by a mutex, so concurrent
// refreshes for the SAME org are deduplicated (only one in-flight fetch per
// org) while different orgs refresh in parallel. The mutex is held for the
// duration of the fetch, bounding concurrent same-org fetches to one.
type CachedOrgStatusClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	ttl        time.Duration
	logger     OrgStatusLogger
	entries    sync.Map // orgID -> *orgStatusEntry
}

type orgStatusEntry struct {
	mu        sync.Mutex
	status    string
	fetchedAt time.Time
}

// NewCachedOrgStatusClient constructs the client. baseURL is the API service
// root (e.g. http://llmsafespaces-api.llmsafespaces.svc:8080); leave empty to
// disable org-suspension (the reconciler then never org-suspends). token, when
// non-empty, is sent as X-Internal-Token. ttl is the cache freshness window
// (production: 30s). logger may be nil.
func NewCachedOrgStatusClient(baseURL, token string, ttl time.Duration, logger OrgStatusLogger) *CachedOrgStatusClient {
	return &CachedOrgStatusClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		ttl:        ttl,
		logger:     logger,
	}
}

// GetOrgStatus implements OrgStatusClient. See CachedOrgStatusClient docs for
// the cache + fail-open contract.
func (c *CachedOrgStatusClient) GetOrgStatus(ctx context.Context, orgID string) (string, bool) {
	if c == nil || c.baseURL == "" {
		return "", false
	}

	v, _ := c.entries.LoadOrStore(orgID, &orgStatusEntry{})
	e := v.(*orgStatusEntry)
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status != "" && time.Since(e.fetchedAt) < c.ttl {
		return e.status, true
	}

	status, err := c.fetch(ctx, orgID)
	if err != nil {
		if e.status != "" {
			// Absorb the transient failure: serve the last-known status.
			if c.logger != nil {
				c.logger.Info("org status refresh failed; serving stale cached status",
					"orgID", orgID, "error", err.Error(), "staleStatus", e.status)
			}
			return e.status, true
		}
		if c.logger != nil {
			c.logger.Info("org status lookup failed; failing open (no cached value)",
				"orgID", orgID, "error", err.Error())
		}
		return "", false
	}

	e.status = status
	e.fetchedAt = time.Now()
	return status, true
}

func (c *CachedOrgStatusClient) fetch(ctx context.Context, orgID string) (string, error) {
	// Use a detached context so a single reconcile's cancellation cannot abort
	// a fetch shared across concurrent reconciles of the same org. Bounded by
	// the httpClient Timeout.
	fetchCtx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()

	url := c.baseURL + "/api/v1/internal/orgs/" + orgID + "/status"
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-Internal-Token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if body.Status == "" {
		return "", fmt.Errorf("empty status in response")
	}
	return body.Status, nil
}
