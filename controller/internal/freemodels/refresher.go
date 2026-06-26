// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package freemodels

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Compile-time assertions that *Refresher satisfies the controller-
// runtime Runnable + LeaderElectionRunnable interfaces, mirroring the
// pattern in controller/internal/relay/orphan_detector.go:214-215. A
// signature drift in Start or NeedLeaderElection will fail the build
// instead of silently breaking mgr.Add at runtime.
var (
	_ manager.Runnable               = (*Refresher)(nil)
	_ manager.LeaderElectionRunnable = (*Refresher)(nil)
)

// Annotation keys for the diagnostic fields that were previously inside
// the JSON envelope alongside Models. They are split out so the
// ConfigMap's `data` payload is byte-stable across refreshes when the
// model list itself is unchanged — see SyncConfigMap doc comment for
// why this matters.
//
// Operators can `kubectl get configmap llmsafespaces-free-models -o
// jsonpath='{.metadata.annotations}'` to inspect freshness.
const (
	annotationFetchedAt = "freemodels.llmsafespaces.dev/fetched-at"
	annotationSource    = "freemodels.llmsafespaces.dev/source"
)

// SyncConfigMap creates or updates the free-models ConfigMap with the
// supplied catalog. The CM has no ownerReference because it is
// controller-managed (same lifecycle pattern as the relay-router peers
// CM in controller/internal/relay/router_configmap.go).
//
// Wire format: the `data["models.json"]` payload contains ONLY the
// model array (`{"models":[...]}`). The fetch timestamp and source URL
// — which would otherwise change on every refresh and defeat the no-op
// fast path — live in annotations
// (freemodels.llmsafespaces.dev/{fetched-at, source}).
//
// No-op fast path: when the existing CM already contains an identical
// `data["models.json"]` payload AND no ownerReferences need stripping,
// returns nil without calling Update. The annotations are still
// refreshed via Update only when something else needs updating; we
// don't bump RV just to record a new timestamp. This is the common
// case during periodic refreshes when models.dev hasn't changed.
//
// Phase B note: the no-op fast path matters for downstream pods that
// mount this CM as a projected volume — every Update can trigger a
// kubelet volume refresh and spurious agent-config rebuilds. Keeping
// the CM byte-stable when the catalog is unchanged is the contract
// the no-op fast path delivers on.
func SyncConfigMap(ctx context.Context, c client.Client, namespace string, catalog Catalog) error {
	data, err := json.MarshalIndent(struct {
		Models []Model `json:"models"`
	}{Models: catalog.Models}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal free-models catalog: %w", err)
	}

	annotations := map[string]string{
		annotationFetchedAt: catalog.FetchedAt.UTC().Format(time.RFC3339Nano),
		annotationSource:    catalog.Source,
	}

	cmName := types.NamespacedName{Name: ConfigMapName, Namespace: namespace}
	existing := &corev1.ConfigMap{}

	if getErr := c.Get(ctx, cmName, existing); getErr != nil {
		if !apierrors.IsNotFound(getErr) {
			return fmt.Errorf("get free-models ConfigMap: %w", getErr)
		}

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        ConfigMapName,
				Namespace:   namespace,
				Annotations: annotations,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "llmsafespaces-controller",
					"app.kubernetes.io/component":  "freemodels",
				},
			},
			Data: map[string]string{
				ConfigMapKey: string(data),
			},
		}
		return c.Create(ctx, cm)
	}

	// No-op when the model payload is byte-identical AND no
	// ownerReferences need stripping. We deliberately do NOT include
	// annotation comparison here — that would bump RV for a pure
	// timestamp refresh and defeat the whole optimization. Operators
	// who want fresh annotations can wait for the next genuine catalog
	// change or force-bump via `kubectl annotate`.
	currentData, ok := existing.Data[ConfigMapKey]
	if ok && currentData == string(data) && len(existing.OwnerReferences) == 0 {
		return nil
	}

	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Data[ConfigMapKey] = string(data)
	existing.Annotations[annotationFetchedAt] = annotations[annotationFetchedAt]
	existing.Annotations[annotationSource] = annotations[annotationSource]
	// Match the relay-router-peers ConfigMap pattern: never let an
	// ownerReference attach to a controller-managed CM, so a future
	// resource deletion can't GC the CM and race with kubelet's volume
	// sync. Strip any pre-existing ownerRefs defensively.
	existing.OwnerReferences = nil
	return c.Update(ctx, existing)
}

// Refresher is a controller-runtime Runnable that periodically fetches
// the free model catalog and publishes it as a ConfigMap. Wire it via
// mgr.Add(refresher) in main.go.
//
// First fetch runs at Start; subsequent fetches every Interval. A
// fetch failure does NOT delete the existing ConfigMap — stale-but-
// valid is strictly better than absent (workspace pods would fall back
// to the legacy in-pod relay injector path).
type Refresher struct {
	Client    client.Client
	Namespace string
	// Interval governs how often the catalog is re-fetched. Production
	// should use 6h; the catalog changes ~weekly. Tests inject a much
	// shorter interval and rely on a fake server URL.
	Interval time.Duration
	// Fetcher is the catalog source. Tests inject one with a fake URL.
	Fetcher *Fetcher
}

// Start implements manager.Runnable. Returns when ctx is canceled.
// Errors from individual fetches are logged but do not propagate —
// returning a non-nil error here would tear down the manager, which
// is the wrong response to a transient upstream outage.
func (r *Refresher) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("freemodels")

	interval := r.Interval
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	// Fire one fetch immediately at startup, then on the ticker.
	r.refreshOnce(ctx, logger)

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			r.refreshOnce(ctx, logger)
		}
	}
}

// NeedLeaderElection ensures only one controller replica refreshes the
// catalog. Without this, every replica would fetch and write
// independently — wasteful and would generate spurious ResourceVersion
// churn on the CM.
func (r *Refresher) NeedLeaderElection() bool {
	return true
}

// refreshOnce runs a single fetch + sync cycle. Errors are logged but
// not returned; the next tick will retry. The existing ConfigMap (if
// any) is preserved on failure.
func (r *Refresher) refreshOnce(ctx context.Context, logger interface {
	Info(msg string, keysAndValues ...interface{})
	Error(err error, msg string, keysAndValues ...interface{})
}) {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout+5*time.Second)
	defer cancel()

	// Defensive nil-check. main.go always sets Fetcher, but defending
	// here protects against a future caller forgetting and the
	// resulting nil-deref taking down the manager. The package is
	// `internal`, so this guard is not load-bearing — just cheap
	// insurance.
	if r.Fetcher == nil {
		logger.Error(nil, "free-models refresher: nil Fetcher; skipping refresh (controller misconfigured)")
		return
	}

	models, err := r.Fetcher.Fetch(fetchCtx)
	if err != nil {
		logger.Error(err, "free-models fetch failed; keeping existing ConfigMap")
		return
	}
	// LIMITATION: this guard cannot distinguish a *transient* empty
	// (upstream outage / schema drift) from a *permanent* empty
	// (opencode deprecates the free tier). In the permanent case the
	// CM serves a stale list indefinitely. Phase B (the eventual
	// pod-side consumer) inherits this ambiguity: pods would
	// pre-render a relay config against a deprecated catalog. If
	// opencode ever truly drops the free tier, the operator must
	// either delete the CM manually or set
	// `freeModelsRefresher.enabled=false`. Acceptable today (no
	// consumer in this PR; free tier is durable).
	if len(models) == 0 {
		logger.Info("free-models fetch returned 0 free models; keeping existing ConfigMap")
		return
	}

	catalog := Catalog{
		Models:    models,
		FetchedAt: time.Now().UTC(),
		Source:    r.fetcherURLForLog(),
	}
	if err := SyncConfigMap(ctx, r.Client, r.Namespace, catalog); err != nil {
		logger.Error(err, "free-models ConfigMap sync failed")
		return
	}
	logger.Info("free-models catalog refreshed", "count", len(models), "namespace", r.Namespace)
}

func (r *Refresher) fetcherURLForLog() string {
	if r.Fetcher != nil && r.Fetcher.URL != "" {
		return r.Fetcher.URL
	}
	return ModelsDevAPIURL
}
