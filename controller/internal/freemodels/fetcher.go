// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package freemodels publishes the cluster-wide opencode free-tier model
// catalog as a ConfigMap, so workspace pods can render their relay
// agent-config.json before opencode boots — eliminating the in-pod
// opencode-restart cycle that the legacy relay-injector goroutine
// imposed.
//
// Why this is cluster-wide and not per-workspace:
//
// The free model list comes from opencode's static catalog (proxied
// through models.dev). The filter applied is:
//
//   - providerID == "opencode"
//   - cost.input == 0
//
// None of these vary per workspace — every pod that uses the free tier
// gets the same list. The pre-2026-06-23 implementation re-fetched it
// once per pod by spinning up opencode with a placeholder config,
// querying its /provider endpoint, then killing and restarting opencode
// with the real config. That ~6-8s cost was paid on every cold start
// and every resume. With this package, the controller fetches the list
// once per refreshInterval and publishes it as a ConfigMap; pods read
// the file directly during their bootstrap init container.
//
// Failure semantics:
//
//   - Initial fetch failure at controller startup: log, retry on the
//     normal refresh interval. Workspaces created before the first
//     successful fetch fall back to the legacy in-pod relay injector
//     (it observes the missing/empty ConfigMap and runs unchanged).
//   - Periodic refresh failure: keep the existing ConfigMap; new pods
//     read the stale-but-valid catalog. The catalog changes rarely
//     (new model added every few weeks), so a few hours of staleness
//     is harmless.
//   - models.dev outage: same as above. The fetched list is durable.
//
// Concurrency: the refresher Runnable owns all writes to the ConfigMap.
// Reconcilers do not touch it; they only mount it into pod specs.
package freemodels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// ConfigMapName is the name of the ConfigMap published into the
// controller's namespace. Workspace pods reference it by name when the
// chart renders their pod-spec ConfigMap mount.
const ConfigMapName = "llmsafespaces-free-models"

// ConfigMapKey is the key inside the ConfigMap whose value is the
// JSON-encoded free model list.
const ConfigMapKey = "models.json"

// ModelsDevAPIURL is the public catalog opencode itself proxies to.
// Pulling from this URL avoids a chicken-and-egg dependency on having
// a workspace pod running just to discover models.
const ModelsDevAPIURL = "https://models.dev/api.json"

// httpFetchTimeout bounds a single GET to models.dev. The endpoint is
// usually <100ms; the timeout is the worst-case before we give up on
// this refresh cycle and try again at the next tick.
const httpFetchTimeout = 30 * time.Second

// Model is the minimal model info needed to render the relay provider
// entry in agent-config.json. Field names match the JSON consumed by
// agentd's materialize subcommand (see cmd/workspace-agentd/secrets.go
// applyRelayConfig). Stable wire format — changes here require a
// matching change in the agentd reader.
type Model struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ContextLimit int    `json:"context_limit"`
	OutputLimit  int    `json:"output_limit"`
}

// Catalog is the wire-format envelope written into the ConfigMap.
// FetchedAt is included so operators can observe how stale the catalog
// is. agentd does not need this field; it is purely diagnostic.
type Catalog struct {
	Models    []Model   `json:"models"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
}

// modelsDevResponse is the subset of the models.dev /api.json response
// we care about. The full response is large (every provider on the
// internet); we only need the opencode entry's models map.
type modelsDevResponse struct {
	OpenCode *struct {
		Models map[string]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Cost struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
			} `json:"cost"`
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	} `json:"opencode"`
}

// Fetcher fetches the free model list from a configured upstream URL.
// Defaults to models.dev. Tests inject a fake server URL.
type Fetcher struct {
	// URL is the upstream catalog endpoint. Defaults to
	// ModelsDevAPIURL when empty.
	URL string
	// HTTPClient overrides the default client. Tests inject one with
	// a tighter timeout; production uses a default 30s client.
	HTTPClient *http.Client
}

// Fetch returns the free-tier opencode model list. A model is "free"
// iff its cost.input is exactly zero. The result is sorted by model ID
// for stable ConfigMap diffs (so Update() is a no-op when the catalog
// is unchanged).
//
// Returns an empty slice (not an error) when the upstream response
// contains no opencode entry or no free models. Callers can decide
// whether to treat that as a hard failure; the controller-side
// Runnable treats it as "skip this refresh, keep existing CM".
func (f *Fetcher) Fetch(ctx context.Context) ([]Model, error) {
	url := f.URL
	if url == "" {
		url = ModelsDevAPIURL
	}
	client := f.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpFetchTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "llmsafespaces-controller/freemodels")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s: %d: %s", url, resp.StatusCode, string(body))
	}

	// 32 MiB ceiling on the response — models.dev today is ~1 MiB. A
	// pathologically large response should not OOM the controller.
	var parsed modelsDevResponse
	body := io.LimitReader(resp.Body, 32*1024*1024)
	if err := json.NewDecoder(body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode %s: %w", url, err)
	}

	if parsed.OpenCode == nil {
		// Catalog had no opencode entry — upstream schema change or
		// outage. Treat as transient: empty list, no error.
		return []Model{}, nil
	}

	out := make([]Model, 0, len(parsed.OpenCode.Models))
	for key, m := range parsed.OpenCode.Models {
		if m.Cost.Input != 0 {
			continue
		}
		id := m.ID
		if id == "" {
			id = key
		}
		out = append(out, Model{
			ID:           id,
			Name:         m.Name,
			ContextLimit: m.Limit.Context,
			OutputLimit:  m.Limit.Output,
		})
	}

	// Stable order so a no-change refresh produces an identical
	// ConfigMap payload (Update path returns early in syncConfigMap).
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}
