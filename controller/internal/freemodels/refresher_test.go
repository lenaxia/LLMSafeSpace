// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package freemodels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	_, err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	require.Contains(t, cm.Data, ConfigMapKey)

	var got Catalog
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	assert.Equal(t, catalog.Models, got.Models)
	assert.Equal(t, catalog.Source, got.Source)
}

func TestSyncConfigMap_NoOwnerReference(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	catalog := Catalog{Models: []Model{{ID: "m1"}}}
	_, err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
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
	_, err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Empty(t, cm.OwnerReferences,
		"any pre-existing ownerReference (e.g. from a previous controller version) must be stripped")
}

func TestSyncConfigMap_NoOpWhenIdentical(t *testing.T) {
	// Pre-seed a CM with the exact bytes SyncConfigMap will produce.
	catalog := Catalog{
		Models:    []Model{{ID: "m1", Name: "M1"}},
		FetchedAt: time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
		Source:    "https://models.dev/api.json",
	}
	bytes, _ := json.MarshalIndent(catalog, "", "  ")

	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            ConfigMapName,
			Namespace:       "test-ns",
			ResourceVersion: "1000",
		},
		Data: map[string]string{ConfigMapKey: string(bytes)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(preSeeded).Build()

	_, err := SyncConfigMap(context.Background(), c, "test-ns", catalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))
	assert.Equal(t, "1000", cm.ResourceVersion,
		"identical payload must skip the Update call entirely (no RV bump) — "+
			"this is what makes the 6h refresh loop cheap when models.dev hasn't changed")
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
	_, err := SyncConfigMap(context.Background(), c, "test-ns", newCatalog)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	var got Catalog
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
			// Verify content.
			var catalog Catalog
			require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &catalog))
			assert.NotEmpty(t, catalog.Models, "first refresh must publish non-empty catalog")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("Refresher did not publish ConfigMap within deadline: %v", lastErr)
}

func TestRefresher_FetchFailure_DoesNotDeleteExistingConfigMap(t *testing.T) {
	// Seed a known-good CM.
	existing := Catalog{
		Models:    []Model{{ID: "stale-but-valid", Name: "Stale"}},
		FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	bytes, _ := json.MarshalIndent(existing, "", "  ")
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Namespace: "test-ns"},
		Data:       map[string]string{ConfigMapKey: string(bytes)},
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
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm),
		"failed fetch must not delete the existing CM")

	var got Catalog
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
	existing := Catalog{Models: []Model{{ID: "real-model"}}}
	bytes, _ := json.MarshalIndent(existing, "", "  ")
	preSeeded := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Namespace: "test-ns"},
		Data:       map[string]string{ConfigMapKey: string(bytes)},
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
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: ConfigMapName, Namespace: "test-ns"}, cm))

	var got Catalog
	require.NoError(t, json.Unmarshal([]byte(cm.Data[ConfigMapKey]), &got))
	require.Len(t, got.Models, 1)
	assert.Equal(t, "real-model", got.Models[0].ID,
		"empty fetch must preserve the existing non-empty CM, not overwrite it with []")
}
