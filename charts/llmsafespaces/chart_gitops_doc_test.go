// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package chart_test

// Regression tests for the FluxCD / Argo CD chart-freshness documentation (#456).
//
// Chart.yaml's `version` is intentionally pinned at 0.1.0 and never bumped
// (the chart is consumed from a Git source, not published to a registry).
// FluxCD's source-controller packages a GitRepository-sourced chart ONCE and
// re-uses that packaged artifact as long as the Chart.yaml version is
// unchanged. With the DEFAULT `reconcileStrategy: ChartVersion`, that means
// the chart is packaged exactly once on first reconcile and never again —
// so every subsequent `helm upgrade` renders against a stale snapshot, no
// matter how many commits land on main. New templates, new ConfigMap keys
// (e.g. new migrations), new RBAC — all invisible to the cluster.
//
// This trap is silent and already caused a production incident (2026-06-29):
// the migrations ConfigMap still held only migration 000001 after PR #451
// added 000002–000004, because the chart was never re-packaged. The
// migration Job (#455) never actually ran its new args either.
//
// The fix (#456, option 1) is documentation: the chart MUST tell consumers
// to set `reconcileStrategy: Revision` (Flux) so source-controller
// re-packages on every git revision. These tests pin the documentation so a
// future cleanup cannot silently delete the warning.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chartFile reads a file relative to the chart root.
func chartFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chartDir(t), rel))
	require.NoError(t, err, "chart file %s must exist", rel)
	return string(b)
}

// TestChart_ReadmeDocumentsFluxReconcileStrategy asserts the chart README
// prominently documents the GitOps reconcileStrategy trap and its fix. This
// is the regression guard for the #456 documentation fix: if the section is
// removed, the trap re-emerges for every new consumer.
func TestChart_ReadmeDocumentsFluxReconcileStrategy(t *testing.T) {
	readme := chartFile(t, "README.md")

	// The fix keyword consumers must apply.
	assert.Contains(t, readme, "reconcileStrategy",
		"README must document reconcileStrategy (the Flux setting that avoids stale chart packaging; #456)")
	assert.Contains(t, readme, "Revision",
		"README must name reconcileStrategy: Revision as the remediation for Git-sourced deploys")

	// It must explain WHY (otherwise the fix looks arbitrary). The version
	// pin is the root cause and must be called out.
	assert.Contains(t, readme, "0.1.0",
		"README must explain the Chart.yaml version is pinned (the reason ChartVersion re-packages only once)")

	// It must name the affected tooling so operators searching the README can
	// find the section. FluxCD is the primary consumer; Argo CD has the same
	// class of issue.
	for _, term := range []string{"Flux", "GitRepository"} {
		assert.Contains(t, readme, term,
			"README must reference Flux/GitRepository so the trap is discoverable")
	}

	// It must give a copy-pasteable spec so operators don't paraphrase it wrong.
	assert.Contains(t, readme, "chart:",
		"README must include a HelmRelease chart.spec example for reconcileStrategy")
}

// TestChart_ChartYamlDescriptionReferencesGitOps asserts Chart.yaml's
// description field flags the GitOps freshness concern. `helm show chart`
// surfaces the description before the README, so it is the first line of
// defense for a consumer who never opens the repo.
func TestChart_ChartYamlDescriptionReferencesGitOps(t *testing.T) {
	chart := chartFile(t, "Chart.yaml")

	// The description is a block scalar; collapse newlines for substring checks.
	desc := collapseChartDescription(chart)

	// It must mention the version pin + the GitOps reconciliation caveat.
	assert.True(t,
		strings.Contains(desc, "0.1.0") || strings.Contains(strings.ToLower(desc), "pinned") ||
			strings.Contains(strings.ToLower(desc), "unreleased"),
		"Chart.yaml description must flag that the chart version is pinned / not registry-published")
	assert.True(t,
		strings.Contains(desc, "reconcileStrategy") || strings.Contains(desc, "GitOps") ||
			strings.Contains(desc, "Flux"),
		"Chart.yaml description must reference GitOps/reconcileStrategy so `helm show chart` warns consumers (#456)")
}

// collapseChartDescription extracts and flattens the `description:` block
// scalar from Chart.yaml into a single space-separated string. If parsing
// fails it returns the whole file so assertions still operate on real bytes.
func collapseChartDescription(chart string) string {
	lines := strings.Split(chart, "\n")
	var (
		out        []string
		inDesc     bool
		descIndent = -1
	)
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if !inDesc {
			if strings.HasPrefix(strings.TrimSpace(trimmed), "description:") {
				inDesc = true
				// record indentation of the description key for block-scalar folding
				descIndent = leadingSpaces(trimmed)
				rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "description:"))
				rest = strings.Trim(rest, "|>")
				if rest != "" {
					out = append(out, strings.TrimSpace(rest))
				}
			}
			continue
		}
		// We are inside the description block scalar: continue while indented
		// deeper than the key (or blank).
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if leadingSpaces(trimmed) <= descIndent {
			break // block ended
		}
		out = append(out, strings.TrimSpace(trimmed))
	}
	if len(out) == 0 {
		return chart
	}
	return strings.Join(out, " ")
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
			continue
		}
		break
	}
	return n
}
