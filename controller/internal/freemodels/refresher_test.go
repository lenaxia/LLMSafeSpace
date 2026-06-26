// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package freemodels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	return s
}

func TestSyncConfigMap_CreatesWhenAbsent(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	catalog := Catalog{
		Models:    []Model{{ID: "m1", Name: "M1", ContextLimit: 1000}},
		FetchedAt: time.Now().UTC(),
		Source:    "https://models.dev/api.json",
	}

	err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	require.Contains(t, cm.Data, ConfigMapKey)

	// Wire format: data["models.json"] is models-only; FetchedAt and
	// Source live in annotations to keep data byte-stable across
	// refreshes.
	var got struct {
		Models []Model `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	assert.Equal(t, catalog.Models, got.Models)
	assert.Equal(t, catalog.Source, cm.Annotations[annotationSource],
		"Source must be set as an annotation, not in the data payload")
	assert.NotEmpty(t, cm.Annotations[annotationFetchedAt],
		"FetchedAt annotation must be set on Create")
}

func TestSyncConfigMap_NoOwnerReference(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	catalog := Catalog{Models: []Model{{ID: "m1"}}}
	err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Empty(t, cm.OwnerReferences,
		"free-models CM must NOT carry an ownerReference — controller manages it directly, "+
			"matching the relay-router-peers CM lifecycle pattern")
}

func TestSyncConfigMap_StripsExistingOwnerReference(t *testing.T) {
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "test-ns",
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "v1", Kind: "Pod", Name: "stale-owner", UID: "stale-uid"},
			},
		},
		Data: map[string]string{ConfigMapKey: `{"models":[]}`},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	catalog := Catalog{Models: []Model{{ID: "m1"}}}
	err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Empty(t, cm.OwnerReferences,
		"any pre-existing ownerReference (e.g. from a previous controller version) must be stripped")
}

func TestSyncConfigMap_NoOpWhenIdentical(t *testing.T) {
	// Pre-seed a CM with the exact data["models.json"] bytes
	// SyncConfigMap will produce. Models-only payload — FetchedAt and
	// Source live in annotations, NOT in data, precisely so the no-op
	// fast path triggers when only the timestamp changes.
	catalog := Catalog{
		Models:    []Model{{ID: "m1", Name: "M1"}},
		FetchedAt: time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
		Source:    "https://models.dev/api.json",
	}
	dataBytes, _ := json.MarshalIndent(struct {
		Models []Model `json:"models"`
	}{Models: catalog.Models}, "", "  ")

	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            ConfigMapName,
			Namespace:       "test-ns",
			ResourceVersion: "1000",
			Annotations: map[string]string{
				annotationFetchedAt: "stale-but-the-data-is-identical",
				annotationSource:    "https://models.dev/api.json",
			},
		},
		Data: map[string]string{ConfigMapKey: string(dataBytes)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Equal(t, "1000", cm.ResourceVersion,
		"identical model payload must skip Update — RV must NOT bump even though FetchedAt "+
			"in the supplied catalog is fresh. This is what keeps Phase B pod kubelet volume "+
			"refreshes idle when the catalog hasn't changed.")
	// Stale annotation MUST still be present — we don't bump RV just to refresh it.
	assert.Equal(t, "stale-but-the-data-is-identical", cm.Annotations[annotationFetchedAt],
		"no-op fast path must not refresh the FetchedAt annotation either; "+
			"otherwise the Update is just hidden behind a different surface")
}

func TestSyncConfigMap_UpdatesWhenChanged(t *testing.T) {
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "test-ns",
		},
		Data: map[string]string{ConfigMapKey: `{"models":[],"fetched_at":"old"}`},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	newCatalog := Catalog{Models: []Model{{ID: "newer"}}}
	err := SyncConfigMap(context.Background(), c, "test-ns", newCatalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	var got struct {
		Models []Model `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	require.Len(t, got.Models, 1)
	assert.Equal(t, "newer", got.Models[0].ID)
}

func TestRefresher_Start_PublishesOnFirstTick(t *testing.T) {
	srv := fakeModelsDevServer(t)
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	r := &Refresher{
		Client:    c,
		Namespace: "test-ns",
		Interval:  10 * time.Hour, // long: we verify the immediate-first-fire path
		Fetcher:   &Fetcher{URL: srv.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Start(ctx)
	}()

	// Poll until the CM appears.
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cm := &corev1.ConfigMap{}
		lastErr = c.Get(context.Background(),
			types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm)
		if lastErr == nil {
			cancel()
			<-done
			// Verify content (data is models-only post-correctness fix).
			var got struct {
				Models []Model `json:"models"`
			}
			require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
			assert.NotEmpty(t, got.Models, "first refresh must publish non-empty catalog")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("Refresher did not publish ConfigMap within deadline: %v", lastErr)
}

func TestRefresher_FetchFailure_DoesNotDeleteExistingConfigMap(t *testing.T) {
	// Seed a known-good CM. Wire format: data["models.json"] is models
	// only; FetchedAt and Source live in annotations.
	existingModels := []Model{{ID: "stale-but-valid", Name: "Stale"}}
	dataBytes, _ := json.MarshalIndent(struct {
		Models []Model `json:"models"`
	}{Models: existingModels}, "", "  ")
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "test-ns",
			Annotations: map[string]string{
				annotationFetchedAt: "2026-01-01T00:00:00Z",
				annotationSource:    "https://models.dev/api.json",
			},
		},
		Data: map[string]string{ConfigMapKey: string(dataBytes)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	// Server that always 500s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	r := &Refresher{
		Client:    c,
		Namespace: "test-ns",
		Interval:  10 * time.Hour,
		Fetcher:   &Fetcher{URL: srv.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Start(ctx)
	}()

	// Give the goroutine time to attempt and fail the first fetch.
	// Poll for stable CM presence (more robust under CI load than a
	// fixed sleep). The CM was seeded at construction time, so any
	// failed fetch should leave it intact.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cm := &corev1.ConfigMap{}
		if err := c.Get(context.Background(),
			types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm),
		"failed fetch must not delete the existing CM")

	var got struct {
		Models []Model `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	require.Len(t, got.Models, 1)
	assert.Equal(t, "stale-but-valid", got.Models[0].ID,
		"stale CM content must be preserved on fetch failure — better than absent")
}

func TestRefresher_NeedLeaderElection(t *testing.T) {
	r := &Refresher{}
	assert.True(t, r.NeedLeaderElection(),
		"Refresher must require leader election so multi-replica controllers don't all fetch independently")
}

func TestRefresher_ZeroFreeModels_PreservesExistingConfigMap(t *testing.T) {
	// This guards the "no_free_models" outcome in the legacy in-pod
	// relay injector. If the upstream catalog returns an empty list
	// (transient outage or schema change), we must not overwrite a
	// previously-good CM with empty data — the workspace pods would
	// then start with no relay.
	existingModels := []Model{{ID: "real-model"}}
	dataBytes, _ := json.MarshalIndent(struct {
		Models []Model `json:"models"`
	}{Models: existingModels}, "", "  ")
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Namespace: "test-ns"},
		Data:       map[string]string{ConfigMapKey: string(dataBytes)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	// Server returns valid JSON with no free models.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"opencode": {"models": {"paid": {"id": "paid", "cost": {"input": 1.0}}}}}`))
	}))
	t.Cleanup(srv.Close)

	r := &Refresher{
		Client:    c,
		Namespace: "test-ns",
		Interval:  10 * time.Hour,
		Fetcher:   &Fetcher{URL: srv.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Start(ctx)
	}()
	// Poll for stable CM presence (more robust under CI load than a
	// fixed sleep). The CM was seeded; an empty fetch must NOT
	// overwrite it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cm := &corev1.ConfigMap{}
		if err := c.Get(context.Background(),
			types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	var got struct {
		Models []Model `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	require.Len(t, got.Models, 1)
	assert.Equal(t, "real-model", got.Models[0].ID,
		"empty fetch must preserve the existing non-empty CM, not overwrite it with []")
}

// TestRefresher_PeriodicTickFiresMoreThanOnce exercises the
// `case <-tick.C:` branch in Start() — a regression that breaks the
// for/select loop (e.g. someone moves the first fire INTO the select
// instead of before it) would not be caught by the existing
// "Interval: 10h" tests. PR #400 review finding.
//
// Strategy: 50ms interval + a counting HTTP server; assert ≥2 fetches
// before canceling.
func TestRefresher_PeriodicTickFiresMoreThanOnce(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"opencode": {"models": {"m1": {"id": "m1", "cost": {"input": 0}, "limit": {"context": 1, "output": 1}}}}}`))
	}))
	t.Cleanup(srv.Close)

	r := &Refresher{
		Client:    c,
		Namespace: "test-ns",
		Interval:  50 * time.Millisecond,
		Fetcher:   &Fetcher{URL: srv.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Start(ctx)
	}()

	// Wait until we observe ≥2 fetches OR timeout.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	got := atomic.LoadInt32(&hits)
	assert.GreaterOrEqual(t, got, int32(2),
		"the periodic ticker branch (case <-tick.C:) MUST fire at least once after the immediate "+
			"first fetch — observed %d total fetches in 3s with 50ms interval", got)
}

// TestRefresher_IntegrationNoOpWhenModelsUnchanged covers the
// integration-level no-op path that the previous version of this PR
// silently broke: refreshOnce produces a Catalog with a
// always-different FetchedAt, but SyncConfigMap should still no-op
// (skip Update + leave RV unchanged) when the underlying model list
// is identical between refreshes. PR #400 review primary blocker.
func TestRefresher_IntegrationNoOpWhenModelsUnchanged(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	// Server returns the same models every time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"opencode": {"models": {"m1": {"id": "m1", "cost": {"input": 0}, "limit": {"context": 100000, "output": 8000}}}}}`))
	}))
	t.Cleanup(srv.Close)

	r := &Refresher{
		Client:    c,
		Namespace: "test-ns",
		Interval:  50 * time.Millisecond,
		Fetcher:   &Fetcher{URL: srv.URL},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Start(ctx)
	}()

	// Wait for the first publish (CM appears).
	var firstRV string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cm := &corev1.ConfigMap{}
		if err := c.Get(context.Background(),
			types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm); err == nil {
			firstRV = cm.ResourceVersion
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEmpty(t, firstRV, "first publish never landed")

	// Wait long enough for ≥3 more refresh ticks. With models
	// unchanged, the CM's ResourceVersion MUST remain == firstRV
	// (no-op path triggers).
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Equal(t, firstRV, cm.ResourceVersion,
		"INTEGRATION-LEVEL no-op contract: refreshOnce produces a Catalog with "+
			"freshly-stamped FetchedAt every tick, but SyncConfigMap must NOT "+
			"bump the CM's ResourceVersion when the underlying model list is "+
			"byte-identical between refreshes. This is what keeps Phase B pod "+
			"kubelet volume refreshes idle when the catalog is unchanged.")
}
