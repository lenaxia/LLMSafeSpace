// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package repolint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Synthetic fixture tests — drift mechanics
// ---------------------------------------------------------------------------

// writeFixture is the shared helper that drops a Go file and a CRD
// YAML into a fresh temp dir so each test owns its environment.
// Returns the temp root suitable to pass as `root` to CRDDriftCheck.
func writeFixture(t *testing.T, goSrc, crdYAML string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "go-pkg"), 0o755); err != nil {
		t.Fatalf("mkdir go-pkg: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "crds"), 0o755); err != nil {
		t.Fatalf("mkdir crds: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go-pkg", "types.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "crds", "thing.yaml"), []byte(crdYAML), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return root
}

const minimalGoNoDrift = `package types

type Thing struct {
	Name  string ` + "`json:\"name\"`" + `
	Count int    ` + "`json:\"count,omitempty\"`" + `
}
`

const minimalCRDNoDrift = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          properties:
            spec:
              properties:
                name:
                  type: string
                count:
                  type: integer
`

func TestCRDDrift_NoDrift_ReturnsOK(t *testing.T) {
	root := writeFixture(t, minimalGoNoDrift, minimalCRDNoDrift)

	rep, err := CRDDriftCheck(root, CRDBinding{
		GoFile:   "go-pkg/types.go",
		GoStruct: "Thing",
		CRDFile:  "crds/thing.yaml",
		CRDPath:  []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Errorf("expected drift-free report, got: %s", rep.String())
	}
}

func TestCRDDrift_GoFieldNotInCRD_Reports(t *testing.T) {
	// Mirrors the Epic 22 incident: Go added DiskUsedBytes / DiskTotalBytes
	// but the chart CRD didn't get updated. apiserver silently drops them.
	goSrc := `package types

type Thing struct {
	Name           string ` + "`json:\"name\"`" + `
	DiskUsedBytes  int64  ` + "`json:\"diskUsedBytes,omitempty\"`" + `
	DiskTotalBytes int64  ` + "`json:\"diskTotalBytes,omitempty\"`" + `
}
`
	root := writeFixture(t, goSrc, minimalCRDNoDrift) // CRD only knows name/count

	rep, err := CRDDriftCheck(root, CRDBinding{
		GoFile:   "go-pkg/types.go",
		GoStruct: "Thing",
		CRDFile:  "crds/thing.yaml",
		CRDPath:  []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatal("expected drift detection, got OK")
	}
	want := []string{"diskTotalBytes", "diskUsedBytes"}
	got := append([]string(nil), rep.GoMissingInCRD...)
	sort.Strings(got)
	if !equalSlices(got, want) {
		t.Errorf("GoMissingInCRD = %v, want %v", got, want)
	}
	// CRD has `count` which Go doesn't — that's a separate diff,
	// also expected.
	if len(rep.CRDMissingInGo) != 1 || rep.CRDMissingInGo[0] != "count" {
		t.Errorf("CRDMissingInGo = %v, want [count]", rep.CRDMissingInGo)
	}
}

func TestCRDDrift_CRDPropertyNotInGo_Reports(t *testing.T) {
	// Mirrors the AgentSessionStatus incident: CRD declared
	// `lastActivityAt: date-time` but Go renamed it to `Status`.
	// The CRD's stale property is the failure to detect.
	goSrc := `package types

type Thing struct {
	Name   string ` + "`json:\"name\"`" + `
	Status string ` + "`json:\"status\"`" + `
}
`
	crd := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          properties:
            spec:
              properties:
                name:
                  type: string
                lastActivityAt:
                  type: string
                  format: date-time
`
	root := writeFixture(t, goSrc, crd)

	rep, err := CRDDriftCheck(root, CRDBinding{
		GoFile:   "go-pkg/types.go",
		GoStruct: "Thing",
		CRDFile:  "crds/thing.yaml",
		CRDPath:  []string{"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatal("expected drift detection, got OK")
	}
	if !equalSlices(rep.GoMissingInCRD, []string{"status"}) {
		t.Errorf("GoMissingInCRD = %v, want [status]", rep.GoMissingInCRD)
	}
	if !equalSlices(rep.CRDMissingInGo, []string{"lastActivityAt"}) {
		t.Errorf("CRDMissingInGo = %v, want [lastActivityAt]", rep.CRDMissingInGo)
	}
}

func TestCRDDrift_NestedArrayItems_Resolves(t *testing.T) {
	// CRD path can drill into `items` of a sequence-typed property.
	// This is the AgentSessionStatus-style binding (a slice-of-struct
	// nested under WorkspaceStatus).
	goSrc := `package types

type Item struct {
	ID    string ` + "`json:\"id\"`" + `
	Title string ` + "`json:\"title,omitempty\"`" + `
}
`
	crd := `
spec:
  schema:
    items:
      properties:
        id:
          type: string
        title:
          type: string
`
	root := writeFixture(t, goSrc, crd)

	rep, err := CRDDriftCheck(root, CRDBinding{
		GoFile:   "go-pkg/types.go",
		GoStruct: "Item",
		CRDFile:  "crds/thing.yaml",
		CRDPath:  []string{"spec", "schema", "items"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Errorf("expected OK, got: %s", rep.String())
	}
}

// ---------------------------------------------------------------------------
// JSON tag extraction edge cases
// ---------------------------------------------------------------------------

func TestExtractJSONTags_OmitemptyStrippedFromName(t *testing.T) {
	src := `package x
type Y struct {
	A string ` + "`json:\"a,omitempty\"`" + `
	B string ` + "`json:\"b\"`" + `
}
`
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "y.go"), src)

	got, err := extractJSONTags(filepath.Join(root, "y.go"), "Y")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"a", "b"}
	if !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractJSONTags_DashTagSkipped(t *testing.T) {
	// `json:"-"` means the field is intentionally never serialized.
	// It must NOT appear in the extracted tag list, regardless of
	// what the CRD declares.
	src := `package x
type Y struct {
	Public string ` + "`json:\"public\"`" + `
	Hidden string ` + "`json:\"-\"`" + `
}
`
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "y.go"), src)

	got, err := extractJSONTags(filepath.Join(root, "y.go"), "Y")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !equalSlices(got, []string{"public"}) {
		t.Errorf("got %v, want [public] only — `json:\"-\"` must be excluded", got)
	}
}

func TestExtractJSONTags_NoJSONTagSkipped(t *testing.T) {
	// A field with no `json:` tag is not a project convention; treat
	// as Go-private. Better to under-report than to invent a name
	// (the lowerCamelCase guess is wrong often enough to be unsafe).
	src := `package x
type Y struct {
	WithTag    string ` + "`json:\"with_tag\"`" + `
	WithoutTag string
}
`
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "y.go"), src)

	got, err := extractJSONTags(filepath.Join(root, "y.go"), "Y")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !equalSlices(got, []string{"with_tag"}) {
		t.Errorf("got %v, want [with_tag]", got)
	}
}

func TestExtractJSONTags_InlineSkipped(t *testing.T) {
	// `json:",inline"` flattens the field's struct's tags into the
	// parent. The drift check should NOT claim "inline" as a CRD
	// property name; the embedded type gets its own binding.
	src := `package x
type Inner struct{ N string ` + "`json:\"n\"`" + ` }
type Y struct {
	Inner ` + "`json:\",inline\"`" + `
	Own   string ` + "`json:\"own\"`" + `
}
`
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "y.go"), src)

	got, err := extractJSONTags(filepath.Join(root, "y.go"), "Y")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Only "own" should be reported. "Inner" is anonymous-embedded
	// (no field name), and even with the `,inline` tag we treat it
	// as the caller's responsibility to bind separately.
	if !equalSlices(got, []string{"own"}) {
		t.Errorf("got %v, want [own]; embedded inline must not surface", got)
	}
}

func TestExtractJSONTags_StructNotFound(t *testing.T) {
	src := `package x
type Y struct{ A string ` + "`json:\"a\"`" + ` }
`
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "y.go"), src)

	_, err := extractJSONTags(filepath.Join(root, "y.go"), "Z") // wrong name
	if err == nil {
		t.Fatal("expected error for missing type, got nil")
	}
	if !strings.Contains(err.Error(), "Z") {
		t.Errorf("error should name the missing type, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// CRD path traversal edge cases
// ---------------------------------------------------------------------------

func TestExtractCRDProperties_PathNotFound_ErrorIncludesPath(t *testing.T) {
	yaml := `
spec:
  versions:
    - name: v1
`
	root := t.TempDir()
	p := filepath.Join(root, "x.yaml")
	mustWriteFile(t, p, yaml)

	_, err := extractCRDProperties(p, []string{"spec", "versions", "0", "wrong"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "wrong") {
		t.Errorf("error should cite the missing key, got %q", err.Error())
	}
}

func TestExtractCRDProperties_SequenceIndexOutOfRange(t *testing.T) {
	yaml := `
versions:
  - name: v1
`
	root := t.TempDir()
	p := filepath.Join(root, "x.yaml")
	mustWriteFile(t, p, yaml)

	_, err := extractCRDProperties(p, []string{"versions", "5"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error should mention out-of-range, got %q", err.Error())
	}
}

func TestExtractCRDProperties_SequenceNonIntegerKey(t *testing.T) {
	yaml := `
versions:
  - name: v1
`
	root := t.TempDir()
	p := filepath.Join(root, "x.yaml")
	mustWriteFile(t, p, yaml)

	_, err := extractCRDProperties(p, []string{"versions", "first"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "integer") {
		t.Errorf("error should mention integer index, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Live-tree contract — runs against the real Go and CRD files.
// This is the test that fails in CI on real drift.
// ---------------------------------------------------------------------------

// TestLive_CRDDrift_NoDrift is the test that fails CI on a real
// drift incident. It runs every binding in LiveBindings() against
// the actual repo files. A failure here means a developer landed
// a Go field without updating the chart CRD (or vice versa), and
// the controller would silently lose data on the next deploy.
//
// To diagnose a failure: read the binding line in the failure
// message, compare GoMissingInCRD / CRDMissingInGo, and either:
//
//  1. add the field to the CRD's openAPIV3Schema.properties block
//     (the common case — Go change landed first); or
//  2. remove it from the Go struct (the rare case — CRD was the
//     intended source of truth and Go shouldn't have it).
func TestLive_CRDDrift_NoDrift(t *testing.T) {
	root := repoRoot(t)
	bindings := LiveBindings()

	failed := 0
	for _, b := range bindings {
		rep, err := CRDDriftCheck(root, b)
		if err != nil {
			t.Errorf("CRDDriftCheck(%s :: %s) error: %v", b.GoFile, b.GoStruct, err)
			failed++
			continue
		}
		if !rep.OK() {
			t.Errorf("drift detected:\n%s", rep.String())
			failed++
		}
	}
	if failed > 0 {
		t.Logf("%d of %d bindings failed; see messages above", failed, len(bindings))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
