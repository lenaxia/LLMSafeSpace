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
)

// SyncConfigMap creates or updates the free-models ConfigMap with the
// supplied catalog. The CM has no ownerReference because it is
// controller-managed (same lifecycle pattern as the relay-router peers
// CM in controller/internal/relay/router_configmap.go).
//
// No-op fast path: when the existing CM already contains an identical
// payload AND no ownerReferences need stripping, returns nil without
// calling Update. This is the common case during periodic refreshes
// when models.dev hasn't changed.
//
// Returns the bytes that were (or would have been) written so callers
// can hash them, log them, etc.
func SyncConfigMap(ctx context.Context, c client.Client, namespace string, catalog Catalog) ([]byte, error) {
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal free-models catalog: %w", err)
	}

	cmName := types.NamespacedName{Name: ConfigMapName, Namespace: namespace}
	existing := &corev1.ConfigMap{}

	if getErr := c.Get(ctx, cmName, existing); getErr != nil {
		if !apierrors.IsNotFound(getErr) {
			return nil, fmt.Errorf("get free-models ConfigMap: %w", getErr)
		}

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ConfigMapName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "llmsafespaces-controller",
					"app.kubernetes.io/component":  "freemodels",
				},
			},
			Data: map[string]string{
				ConfigMapKey: string(data),
			},
		}
		return data, c.Create(ctx, cm)
	}

	currentData, ok := existing.Data[ConfigMapKey]
	if ok && currentData == string(data) && len(existing.OwnerReferences) == 0 {
		return data, nil
	}

	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	existing.Data[ConfigMapKey] = string(data)
	// Match the relay-router-peers ConfigMap pattern: never let an
	// ownerReference attach to a controller-managed CM, so a future
	// resource deletion can't GC the CM and race with kubelet's volume
	// sync. Strip any pre-existing ownerRefs defensively.
	existing.OwnerReferences = nil
	return data, c.Update(ctx, existing)
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

	models, err := r.Fetcher.Fetch(fetchCtx)
	if err != nil {
		logger.Error(err, "free-models fetch failed; keeping existing ConfigMap")
		return
	}
	if len(models) == 0 {
		logger.Info("free-models fetch returned 0 free models; keeping existing ConfigMap")
		return
	}

	catalog := Catalog{
		Models:    models,
		FetchedAt: time.Now().UTC(),
		Source:    r.fetcherURLForLog(),
	}
	if _, err := SyncConfigMap(ctx, r.Client, r.Namespace, catalog); err != nil {
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
