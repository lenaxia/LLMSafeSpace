// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// resolverClient builds a fake client preloaded with the given
// RuntimeEnvironment objects, suitable for resolveRuntimeImage (which takes a
// client.Reader). Mirrors the testScheme/reconcilerFor pattern but yields a
// bare reader since the resolver is a package-level function, not a method.
func resolverClient(t *testing.T, rtes ...*v1.RuntimeEnvironment) (context.Context, *fake.ClientBuilder) {
	t.Helper()
	scheme := testScheme(t)
	objs := make([]runtime.Object, 0, len(rtes))
	for _, r := range rtes {
		objs = append(objs, r)
	}
	return context.Background(), fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...)
}

// makeRTE builds a cluster-scoped RuntimeEnvironment fixture.
func makeRTE(name, image, language, version string) *v1.RuntimeEnvironment {
	return &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{Kind: "RuntimeEnvironment", APIVersion: "llmsafespaces.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1.RuntimeEnvironmentSpec{
			Image:    image,
			Language: language,
			Version:  version,
		},
	}
}

// TestResolveRuntimeImage_ExplicitReferenceUsedAsIs — when spec.runtime
// contains "/", it is an explicit image reference and no CRD lookup happens.
// Value: the explicit-image path (used by every pod_builder/security test) is
// the fast path; a regression that ignored the "/" check would force every
// explicit-image workspace through a failing CRD lookup → workspace Failed.
// Failure mode: spurious "no RuntimeEnvironment found" for image-style
// runtimes. Expected: input returned unchanged, matchedName empty, no error.
func TestResolveRuntimeImage_ExplicitReferenceUsedAsIs(t *testing.T) {
	ctx, b := resolverClient(t) // no RTEs loaded
	c := b.Build()

	img, matched, err := resolveRuntimeImage(ctx, c, "ghcr.io/acme/runtimes/base:ts-1")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/runtimes/base:ts-1", img)
	assert.Empty(t, matched, "explicit references have no matched RTE name")
}

// TestResolveRuntimeImage_EmptyRuntimeErrors — an empty spec.runtime must fail
// loudly, not resolve to an empty image that K8s later rejects opaquely.
// Value: the API/webhook should never permit this, but the resolver is a
// defensive boundary. Failure mode: silent empty image → opaque pod-scheduling
// error. Expected: typed error mentioning spec.runtime is empty.
func TestResolveRuntimeImage_EmptyRuntimeErrors(t *testing.T) {
	ctx, b := resolverClient(t)
	c := b.Build()

	_, _, err := resolveRuntimeImage(ctx, c, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty",
		"empty runtime must surface a clear error, not an opaque lookup miss")
}

// TestResolveRuntimeImage_ExactNameMatch — the primary lookup path: a
// RuntimeEnvironment whose metadata.name equals spec.runtime.
// Value: this is how named runtimes resolve in production. Failure mode: wrong
// image selected → workspace runs against the wrong toolchain. Expected: the
// RTE's image + its name returned.
func TestResolveRuntimeImage_ExactNameMatch(t *testing.T) {
	rte := makeRTE("python", "ghcr.io/acme/python:3.11", "python", "3.11")
	ctx, b := resolverClient(t, rte)
	c := b.Build()

	img, matched, err := resolveRuntimeImage(ctx, c, "python")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/python:3.11", img)
	assert.Equal(t, "python", matched)
}

// TestResolveRuntimeImage_ExactMatchEmptyImageErrors — a RTE that exists but
// has an empty spec.image is misconfigured; resolving to "" would produce a
// pod with no container image. Value: catches operator misconfiguration.
// Failure mode: pod created with empty image → kubelet reject loop. Expected:
// typed error naming the offending RTE.
func TestResolveRuntimeImage_ExactMatchEmptyImageErrors(t *testing.T) {
	rte := makeRTE("broken", "", "go", "1.22")
	ctx, b := resolverClient(t, rte)
	c := b.Build()

	_, matched, err := resolveRuntimeImage(ctx, c, "broken")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken", "error must name the misconfigured RTE")
	assert.Contains(t, err.Error(), "empty spec.image",
		"error must explain the misconfiguration")
	assert.Equal(t, "broken", matched, "matched name is returned even on the image-empty error")
}

// TestResolveRuntimeImage_ColonToDashFallback — "python:3.11" is the
// documented convenience form; the resolver retries with ":" → "-" ("python-3.11")
// when the literal name is absent. Value: preserves the user-facing runtime
// shorthand. Failure mode: shorthand runtimes silently fail to resolve.
// Expected: the "-"-named RTE's image returned.
func TestResolveRuntimeImage_ColonToDashFallback(t *testing.T) {
	rte := makeRTE("python-3.11", "ghcr.io/acme/python:3.11", "python", "3.11")
	ctx, b := resolverClient(t, rte)
	c := b.Build()

	img, matched, err := resolveRuntimeImage(ctx, c, "python:3.11")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/python:3.11", img)
	assert.Equal(t, "python-3.11", matched)
}

// TestResolveRuntimeImage_LanguageVersionScan — when neither an exact name nor
// a "-" form matches, the resolver lists all RTEs and picks one matching
// Spec.Language + Spec.Version. Value: lets operators name RTEs freely while
// users reference them by language:version. Failure mode: list-scan miss →
// resolvable runtime treated as missing. Expected: the matching RTE's image.
func TestResolveRuntimeImage_LanguageVersionScan(t *testing.T) {
	rte := makeRTE("custom-py", "ghcr.io/acme/python:3.12-custom", "python", "3.12")
	other := makeRTE("node-20", "ghcr.io/acme/node:20", "nodejs", "20")
	ctx, b := resolverClient(t, rte, other)
	c := b.Build()

	img, matched, err := resolveRuntimeImage(ctx, c, "python:3.12")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/python:3.12-custom", img)
	assert.Equal(t, "custom-py", matched)
}

// TestResolveRuntimeImage_LanguageVersionScanDeterministic — when multiple
// RTEs match the same language:version, the resolver must pick deterministically
// (lowest metadata.name) so two controller replicas never schedule different
// images for the same workspace across re-reconciles.
// Value: determinism under choice. Failure mode: non-deterministic selection
// causes image flapping across reconciles. Expected: lowest-named RTE wins,
// and two calls return identical results.
func TestResolveRuntimeImage_LanguageVersionScanDeterministic(t *testing.T) {
	beta := makeRTE("z-beta", "ghcr.io/acme/python:beta", "python", "3.13")
	alpha := makeRTE("a-alpha", "ghcr.io/acme/python:alpha", "python", "3.13")
	gamma := makeRTE("m-gamma", "ghcr.io/acme/python:gamma", "python", "3.13")
	ctx, b := resolverClient(t, beta, alpha, gamma)
	c := b.Build()

	img1, matched1, err1 := resolveRuntimeImage(ctx, c, "python:3.13")
	require.NoError(t, err1)
	assert.Equal(t, "a-alpha", matched1, "lowest-named RTE must win")
	assert.Equal(t, "ghcr.io/acme/python:alpha", img1)

	// Second call must be identical despite map/iteration nondeterminism in
	// any underlying list ordering.
	img2, matched2, err2 := resolveRuntimeImage(ctx, c, "python:3.13")
	require.NoError(t, err2)
	assert.Equal(t, matched1, matched2)
	assert.Equal(t, img1, img2)
}

// TestResolveRuntimeImage_LanguageVersionScanIgnoresEmptyImage — a RTE that
// matches language:version but has an empty image must be skipped, not
// selected (the list-scan guard is `e.Spec.Image != ""`).
// Value: a single misconfigured RTE can't poison the whole language:version
// resolution. Failure mode: empty image selected → pod creation fails.
// Expected: a non-empty-image sibling is selected; if only empty-image RTEs
// match, resolution fails with "not found" (not with an empty image).
func TestResolveRuntimeImage_LanguageVersionScanIgnoresEmptyImage(t *testing.T) {
	emptyImg := makeRTE("py-empty", "", "python", "3.11")
	good := makeRTE("py-good", "ghcr.io/acme/python:3.11", "python", "3.11")
	ctx, b := resolverClient(t, emptyImg, good)
	c := b.Build()

	img, matched, err := resolveRuntimeImage(ctx, c, "python:3.11")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/python:3.11", img)
	assert.Equal(t, "py-good", matched)
}

// TestResolveRuntimeImage_NoMatchErrors — a runtime that matches nothing
// (no exact name, no "-" form, no language:version) must surface a clear
// error so the phase handler marks the workspace Failed rather than hanging.
// Value: the "stuck Pending" failure mode. Failure mode: nil error + empty
// image → workspace hangs in Pending. Expected: typed error naming the input.
func TestResolveRuntimeImage_NoMatchErrors(t *testing.T) {
	ctx, b := resolverClient(t, makeRTE("unrelated", "img", "ruby", "3.2"))
	c := b.Build()

	_, _, err := resolveRuntimeImage(ctx, c, "rust:1.75")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rust:1.75",
		"error must echo the unresolved runtime so operators can trace it")
}
