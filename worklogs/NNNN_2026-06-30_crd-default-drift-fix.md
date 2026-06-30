# Worklog: Fix CRD default-value drift + missing default:{} (#281)

**Date:** 2026-06-30
**Session:** Resolve the two remaining CRD defects from #281 (the path bug was fixed in PR #283; the drift and missing `default:{}` were left open).
**Status:** Complete

---

## Objective

Issue #281's primary root cause (wrong CRDDirectoryPaths) was fixed in PR #283, but two defects remained on `main`: (1) three CRD default values had drifted from the Go kubebuilder annotations that are the source of truth, and (2) the `autoSuspend` and `resources` object schemas lacked `default: {}`, making their sub-field defaults unreachable at the API-server layer. These are a real production hazard for `kubectl apply` paths that bypass the admission webhook.

---

## Work Completed

- **Fixed three default-value drifts** in `charts/llmsafespaces/crds/workspace.yaml`:
  - `autoSuspend.enabled`: `false` → `true` (matches `workspace_types.go:65` `+kubebuilder:default=true`)
  - `autoSuspend.idleTimeoutSeconds`: `3600` → `86400` (matches `workspace_types.go:67`)
  - `resources.memory`: `"1Gi"` → `"512Mi"` (matches `workspace_types.go:95`)
- **Added `default: {}`** to both `autoSuspend` and `resources` object schemas so the API server materialises the parent object and the nested sub-field defaults become reachable without the webhook.
- **Added regression test** (`pkg/repolint/crd_default_drift_test.go`): reads the real CRD YAML, navigates the OpenAPI schema tree, and asserts the three defaults + both `default:{}` are correct. Written first (TDD red), confirmed it catches the drift, then made green by the fix. This locks the known-drifted fields; a fuller extension of `crd_drift.go` to diff ALL defaults (not just these) is a separate follow-up.

---

## Key Decisions

1. **Scope: fix the values + add the test, not extend the full drift detector.** Fully extending `crd_drift.go` to diff every `default:` value (not just field-name presence) is 2-3 hours and a larger change. The focused regression test covers the known-drifted fields now; the systemic fix is tracked as recommendation #3 in the issue findings.
2. **Root-only scope for default:{}.** Added `default: {}` only to the two nested object types that have sub-defaults (`autoSuspend`, `resources`). Other object-typed properties without sub-defaults (`credentials`, `podSecurityContext`) don't need it — they have no nested defaults to reach.

---

## Blockers

None.

---

## Tests Run

- `go test -run TestWorkspaceCRD_DefaultsMatchGoAnnotations -v ./pkg/repolint/` — PASS (was red before the YAML fix).
- `go build ./pkg/repolint/ ./pkg/apis/...` — OK.
- `go vet ./pkg/repolint/` — OK.
- NOTE: the full `pkg/repolint/` suite has a pre-existing failure in `sequence_test.go` (main's own `NNNN_2026-06-30_chatpage-optimistic-survival.md` hasn't been numbered by the renumber bot yet). This is a transient main-level condition unrelated to this change.

---

## Next Steps

- After merge: the envtest workflow (`.github/workflows/envtest.yml`) can have its AutoSuspend assertions made unconditional (the `TODO` at `envtest_defaults_test.go:102-114`), since sub-field defaults are now reachable. That's a separate small PR.
- Systemic follow-up: extend `crd_drift.go` to diff `default:` values across all fields, not just field-name presence.

---

## Files Modified

- `charts/llmsafespaces/crds/workspace.yaml` — fixed 3 default values + added 2 `default: {}`.
- `pkg/repolint/crd_default_drift_test.go` — new regression test.
- `worklogs/NNNN_2026-06-30_crd-default-drift-fix.md` — this worklog.
