// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package repolint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// stubFetcher returns a pre-built CRD or a synthetic error.
type stubFetcher struct {
	crd *apiextv1.CustomResourceDefinition
	err error
}

func (s stubFetcher) GetCRD(_ context.Context, _ string) (*apiextv1.CustomResourceDefinition, error) {
	return s.crd, s.err
}

// crdWithSpec builds a CRD with a Spec.Versions[0] schema whose
// .properties.spec.properties has the given keys present.
func crdWithSpec(name string, specPropKeys ...string) *apiextv1.CustomResourceDefinition {
	specProps := make(map[string]apiextv1.JSONSchemaProps, len(specPropKeys))
	for _, k := range specPropKeys {
		specProps[k] = apiextv1.JSONSchemaProps{Type: "string"}
	}
	return &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Versions: []apiextv1.CustomResourceDefinitionVersion{
				{
					Name: "v1",
					Schema: &apiextv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextv1.JSONSchemaProps{
								"spec": {
									Type:       "object",
									Properties: specProps,
								},
							},
						},
					},
				},
			},
		},
	}
}

// chartFixture returns the absolute path to a tmp chart YAML file
// with the given spec property keys declared.
func chartFixture(t *testing.T, specPropKeys ...string) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	rel = "chart.yaml"
	body := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workspaces.llmsafespaces.dev
spec:
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
`
	for _, k := range specPropKeys {
		body += "              " + k + ":\n                type: string\n"
	}
	require.NoError(t, writeFile(filepath.Join(root, rel), []byte(body)))
	return root, rel
}

// writeFile is a tiny wrapper so the fixture builder reads cleanly.
func writeFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}

// ---------------------------------------------------------------------------
// ClusterDriftCheck — happy path
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_NoDrift(t *testing.T) {
	root, rel := chartFixture(t, "alpha", "beta", "gamma")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	deployed := crdWithSpec("workspaces.llmsafespaces.dev", "alpha", "beta", "gamma")

	rep, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: deployed})
	require.NoError(t, err)
	assert.True(t, rep.OK(), "no drift expected, got %s", rep.String())
}

// ---------------------------------------------------------------------------
// Reproduces the 2026-06-19 incident exactly: chart has spec.suspend,
// cluster does not. Must surface as ChartMissingInCluster.
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_ChartHasSuspendButClusterMissingIt(t *testing.T) {
	root, rel := chartFixture(t, "alpha", "suspend", "beta")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	deployed := crdWithSpec("workspaces.llmsafespaces.dev", "alpha", "beta")

	rep, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: deployed})
	require.NoError(t, err)
	require.False(t, rep.OK(), "drift must be reported")
	assert.Equal(t, []string{"suspend"}, rep.ChartMissingInCluster,
		"the pruned field must be named in ChartMissingInCluster so operators can correlate")
	assert.Empty(t, rep.ClusterMissingInChart)
	out := rep.String()
	assert.Contains(t, out, "suspend")
	assert.Contains(t, out, "kubectl apply -f", "remediation step must be included in the message")
}

// ---------------------------------------------------------------------------
// Cluster has fields the chart removed (e.g. ephemeralStorage in our case).
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_ClusterHasStaleFieldsRemovedFromChart(t *testing.T) {
	root, rel := chartFixture(t, "alpha", "beta")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	deployed := crdWithSpec("workspaces.llmsafespaces.dev", "alpha", "beta", "ephemeralStorage")

	rep, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: deployed})
	require.NoError(t, err)
	require.False(t, rep.OK())
	assert.Empty(t, rep.ChartMissingInCluster)
	assert.Equal(t, []string{"ephemeralStorage"}, rep.ClusterMissingInChart)
}

// ---------------------------------------------------------------------------
// Both directions of drift surface together.
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_BothDirectionsOfDrift(t *testing.T) {
	root, rel := chartFixture(t, "alpha", "newField")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	deployed := crdWithSpec("workspaces.llmsafespaces.dev", "alpha", "oldField")

	rep, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: deployed})
	require.NoError(t, err)
	require.False(t, rep.OK())
	assert.Equal(t, []string{"newField"}, rep.ChartMissingInCluster)
	assert.Equal(t, []string{"oldField"}, rep.ClusterMissingInChart)
}

// ---------------------------------------------------------------------------
// Ignore lists honored.
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_RespectsIgnoreLists(t *testing.T) {
	root, rel := chartFixture(t, "alpha", "newField")
	binding := ClusterDriftBinding{
		CRDName:                 "workspaces.llmsafespaces.dev",
		CRDFile:                 rel,
		CRDPath:                 []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
		IgnoreChartProperties:   []string{"newField"},
		IgnoreClusterProperties: []string{"oldField"},
	}
	deployed := crdWithSpec("workspaces.llmsafespaces.dev", "alpha", "oldField")

	rep, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: deployed})
	require.NoError(t, err)
	assert.True(t, rep.OK(), "ignore lists must suppress diffs")
}

// ---------------------------------------------------------------------------
// Failure paths
// ---------------------------------------------------------------------------

func TestClusterDriftCheck_FetchError(t *testing.T) {
	root, rel := chartFixture(t, "alpha")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	_, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{err: errors.New("forbidden")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch cluster CRD")
	assert.Contains(t, err.Error(), "forbidden")
}

func TestClusterDriftCheck_ChartFileMissing(t *testing.T) {
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: "does/not/exist.yaml",
		CRDPath: []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	_, err := ClusterDriftCheck(context.Background(), "/tmp/nope", binding, stubFetcher{crd: crdWithSpec("x", "a")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse chart CRD")
}

func TestClusterDriftCheck_InvalidVersionIndex_ChartFailsFirst(t *testing.T) {
	root, rel := chartFixture(t, "alpha")
	binding := ClusterDriftBinding{
		CRDName: "workspaces.llmsafespaces.dev",
		CRDFile: rel,
		// version 9 doesn't exist in either chart or cluster — chart parse fails first
		CRDPath: []string{"spec", "versions", "9", "schema", "openAPIV3Schema", "properties", "spec"},
	}
	_, err := ClusterDriftCheck(context.Background(), root, binding, stubFetcher{crd: crdWithSpec("workspaces.llmsafespaces.dev", "alpha")})
	require.Error(t, err)
	// Chart parser fails first because the YAML walker sees the bad index
	// before the cluster fetch is consulted.
	assert.Contains(t, err.Error(), "parse chart CRD")
}

// ---------------------------------------------------------------------------
// extractDeployedCRDProperties unit tests — items[] traversal matters
// because LiveBindings includes a `sessions.items` path.
// ---------------------------------------------------------------------------

func TestExtractDeployedCRDProperties_TraversesItems(t *testing.T) {
	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "workspaces.llmsafespaces.dev"},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Versions: []apiextv1.CustomResourceDefinitionVersion{
				{
					Name: "v1",
					Schema: &apiextv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextv1.JSONSchemaProps{
								"status": {
									Type: "object",
									Properties: map[string]apiextv1.JSONSchemaProps{
										"sessions": {
											Type: "array",
											Items: &apiextv1.JSONSchemaPropsOrArray{
												Schema: &apiextv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextv1.JSONSchemaProps{
														"id":     {Type: "string"},
														"title":  {Type: "string"},
														"status": {Type: "string"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	out, err := extractDeployedCRDProperties(crd, []string{
		"spec", "versions", "0", "schema", "openAPIV3Schema", "properties",
		"status", "properties", "sessions", "items",
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"id", "title", "status"}, mapKeys(out))
}

// extractDeployedCRDProperties error paths — these reach the deployed-CRD
// walker directly, unlike TestClusterDriftCheck_InvalidVersionIndex_ChartFailsFirst
// which exercises the chart parser first.

func TestExtractDeployedCRDProperties_VersionIndexOutOfRange(t *testing.T) {
	crd := crdWithSpec("workspaces.llmsafespaces.dev", "alpha")
	_, err := extractDeployedCRDProperties(crd, []string{
		"spec", "versions", "9", "schema", "openAPIV3Schema", "properties", "spec",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestExtractDeployedCRDProperties_MissingOpenAPIV3Schema(t *testing.T) {
	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "workspaces.llmsafespaces.dev"},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Versions: []apiextv1.CustomResourceDefinitionVersion{
				{Name: "v1", Schema: nil},
			},
		},
	}
	_, err := extractDeployedCRDProperties(crd, []string{
		"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no openAPIV3Schema")
}

func TestExtractDeployedCRDProperties_UnsupportedPathPrefix(t *testing.T) {
	crd := crdWithSpec("workspaces.llmsafespaces.dev", "alpha")
	_, err := extractDeployedCRDProperties(crd, []string{
		"status", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported path prefix")
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// LiveClusterBindings — sanity
// ---------------------------------------------------------------------------

func TestLiveClusterBindings_NonEmpty(t *testing.T) {
	bs := LiveClusterBindings()
	require.NotEmpty(t, bs)
	for _, b := range bs {
		assert.NotEmpty(t, b.CRDName, "binding %+v missing CRDName", b)
		assert.NotEmpty(t, b.CRDFile, "binding %+v missing CRDFile", b)
		assert.NotEmpty(t, b.CRDPath, "binding %+v missing CRDPath", b)
	}
}
