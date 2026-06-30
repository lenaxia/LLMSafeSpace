// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package repolint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestWorkspaceCRD_DefaultsMatchGoAnnotations is the regression test for
// issue #281. The hand-maintained chart CRD (charts/llmsafespaces/crds/
// workspace.yaml) drifted from the Go kubebuilder annotations that are the
// source of truth. Without the admission webhook (kubectl-only creators, or
// if the webhook is down/misrouted), wrong defaults are applied at the API
// server layer. This test pins the known-drifted defaults to the Go values.
//
// Fields covered (Go annotation → CRD YAML default must match):
//   - autoSuspend.enabled          true   (workspace_types.go:65)
//   - autoSuspend.idleTimeoutSeconds 86400 (workspace_types.go:67)
//   - resources.memory              512Mi  (workspace_types.go:95)
//
// Also asserts that autoSuspend and resources carry default: {} so the API
// server materializes the parent object and sub-field defaults are reachable
// (without it, kubebuilder sub-defaults are dead at the apiserver layer).
func TestWorkspaceCRD_DefaultsMatchGoAnnotations(t *testing.T) {
	crdPath := filepath.Join("..", "..", "charts", "llmsafespaces", "crds", "workspace.yaml")
	src, err := os.ReadFile(crdPath)
	require.NoError(t, err, "read workspace CRD")

	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(src, &doc))

	specProps := navigate(t, &doc, "spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec", "properties")

	t.Run("autoSuspend default values", func(t *testing.T) {
		autoSuspend := mustStepInto(t, specProps, "autoSuspend")

		assertHasDefaultObject(t, autoSuspend, "autoSuspend")

		enabled := mustStepInto(t, autoSuspend, "properties", "enabled")
		assertDefaultValue(t, enabled, "autoSuspend.enabled", "true")

		idleTimeout := mustStepInto(t, autoSuspend, "properties", "idleTimeoutSeconds")
		assertDefaultValue(t, idleTimeout, "autoSuspend.idleTimeoutSeconds", "86400")
	})

	t.Run("resources.memory default value", func(t *testing.T) {
		resources := mustStepInto(t, specProps, "resources")

		assertHasDefaultObject(t, resources, "resources")

		memory := mustStepInto(t, resources, "properties", "memory")
		assertDefaultValue(t, memory, "resources.memory", "512Mi")
	})
}

// navigate walks a yaml.Node document along the given keys using stepInto.
func navigate(t *testing.T, doc *yaml.Node, keys ...string) *yaml.Node {
	t.Helper()
	cur := doc
	if cur.Kind == yaml.DocumentNode {
		require.Len(t, cur.Content, 1, "document node should have one child")
		cur = cur.Content[0]
	}
	for _, k := range keys {
		next, err := stepInto(cur, k)
		require.NoError(t, err, "navigate into %q", k)
		cur = next
	}
	return cur
}

func mustStepInto(t *testing.T, node *yaml.Node, keys ...string) *yaml.Node {
	t.Helper()
	cur := node
	for _, k := range keys {
		next, err := stepInto(cur, k)
		require.NoError(t, err, "step into %q", k)
		cur = next
	}
	return cur
}

// assertHasDefaultObject asserts that a schema node carries default: {} so
// the API server materializes the parent object and nested defaults apply.
func assertHasDefaultObject(t *testing.T, node *yaml.Node, label string) {
	t.Helper()
	def, err := stepInto(node, "default")
	require.NoError(t, err, "%s must have a default: {} so sub-field defaults are reachable", label)
	assert.Equal(t, yaml.MappingNode, def.Kind, "%s default should be an object (got kind %d)", label, def.Kind)
	assert.Empty(t, def.Content, "%s default should be an empty object {}", label)
}

// assertDefaultValue asserts that a schema property's default key holds the
// expected scalar value. yaml.v3 stores scalars as strings in .Value.
func assertDefaultValue(t *testing.T, node *yaml.Node, label, want string) {
	t.Helper()
	def, err := stepInto(node, "default")
	require.NoError(t, err, "%s must declare a default (drift from Go annotation)", label)
	assert.Equal(t, want, def.Value, "%s CRD default drifted from Go kubebuilder annotation", label)
}
