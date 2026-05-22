package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

func newRE(name, lang, version, image string) *resources.RuntimeEnvironment {
	return &resources.RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resources.RuntimeEnvironmentSpec{
			Language: lang,
			Version:  version,
			Image:    image,
		},
	}
}

// Case 1: a runtime string containing '/' is treated as a fully-qualified
// container image and bypasses RuntimeEnvironment lookup. This is the
// development escape hatch.
func TestResolveRuntimeImage_FullyQualifiedImage_BypassesLookup(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	img, name, err := resolveRuntimeImage(context.Background(), c,
		"llmsafespace/runtime-base:dev")
	require.NoError(t, err)
	assert.Equal(t, "llmsafespace/runtime-base:dev", img)
	assert.Empty(t, name, "no RuntimeEnvironment matched (fully-qualified bypass)")

	// Registry-prefixed images are also bypassed.
	img, _, err = resolveRuntimeImage(context.Background(), c,
		"ghcr.io/example/runtime:1.0")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/example/runtime:1.0", img)
}

// Case 2: an exact-name match wins.
func TestResolveRuntimeImage_ExactName(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newRE("python311", "python", "3.11", "registry.local/py:311")).
		Build()

	img, name, err := resolveRuntimeImage(context.Background(), c, "python311")
	require.NoError(t, err)
	assert.Equal(t, "registry.local/py:311", img)
	assert.Equal(t, "python311", name)
}

// Case 3: spec.runtime contains ':' (e.g. "python:3.11") and a
// RuntimeEnvironment named "python-3.11" exists. Exact-name fails (':' is
// not allowed in K8s names), so the resolver falls back to the
// ':'-replaced name.
func TestResolveRuntimeImage_ColonReplacedName(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newRE("python-3.11", "python", "3.11", "registry.local/py:311")).
		Build()

	img, name, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
	require.NoError(t, err)
	assert.Equal(t, "registry.local/py:311", img)
	assert.Equal(t, "python-3.11", name)
}

// Case 4: language+version match. Used when names don't follow either of
// the conventions above. Demonstrates picking by spec.language + ":" +
// spec.version even when name is unrelated.
func TestResolveRuntimeImage_LanguageVersionMatch(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newRE("ml-stack", "python", "3.11", "registry.local/ml:1.0")).
		Build()

	img, name, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
	require.NoError(t, err)
	assert.Equal(t, "registry.local/ml:1.0", img)
	assert.Equal(t, "ml-stack", name)
}

// When multiple RuntimeEnvironments share the same language+version, the
// resolver picks the alphabetically-first by name. Important: if Go map
// iteration randomized this, controllers in different leader generations
// could pick different images for the same Sandbox.
func TestResolveRuntimeImage_LanguageVersion_DeterministicOnTie(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			newRE("z-second", "python", "3.11", "registry.local/z:2"),
			newRE("a-first", "python", "3.11", "registry.local/a:1"),
			newRE("m-middle", "python", "3.11", "registry.local/m:1"),
		).Build()

	for i := 0; i < 5; i++ {
		img, name, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
		require.NoError(t, err)
		assert.Equal(t, "a-first", name, "iteration %d", i)
		assert.Equal(t, "registry.local/a:1", img)
	}
}

// No matching RuntimeEnvironment → clear error referencing the runtime
// string and the strategies tried. Prevents a confusing "image pull
// backoff on python:3.11" when the real cause is missing config.
func TestResolveRuntimeImage_NotFound_ReturnsHelpfulError(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	_, _, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "python:3.11")
	assert.Contains(t, err.Error(), "no RuntimeEnvironment found")
}

// Empty runtime is rejected up front rather than returning a confusing
// "lookup ” failed" error.
func TestResolveRuntimeImage_EmptyRuntime_Rejected(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	_, _, err := resolveRuntimeImage(context.Background(), c, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// A RuntimeEnvironment exists but has empty spec.image — this is a config
// bug that should fail loudly, not silently fall back to the runtime
// string as image.
func TestResolveRuntimeImage_EnvWithEmptyImage_Errors(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newRE("python-3.11", "python", "3.11", "")).
		Build()

	_, _, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty spec.image")
}

// Exact-name match takes precedence over language+version match: if a user
// names a RuntimeEnvironment after the runtime spec exactly, that's the
// most specific intent.
func TestResolveRuntimeImage_ExactNameWinsOverLangVersion(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			newRE("python-3.11", "python", "3.11", "registry.local/named:exact"),
			newRE("ml-stack", "python", "3.11", "registry.local/lang:match"),
		).Build()

	img, name, err := resolveRuntimeImage(context.Background(), c, "python:3.11")
	require.NoError(t, err)
	assert.Equal(t, "python-3.11", name)
	assert.Equal(t, "registry.local/named:exact", img)
}
