# 0148 — Fix models.go regressions from eb1a7c4 squash

**Date:** 2026-06-04
**Status:** Complete

---

## What

`eb1a7c4` dropped several Epic 26 fields and the `ModelStore` interface from
`api/internal/handlers/models.go` when squash-merging model-selection code on
top of `98d157b`. Found during E2E validation when `GET /models` returned empty
arrays with no `proxyRequired` field and `PUT /model` no longer pushed the
relay baseURL to opencode.

---

## Changes

**Restored from `98d157b`:**
- `annotatedModel.ProxyRequired bool` field (and population logic `tier == "free"`)
- `annotatedModel.Tier string` field
- `PUT /workspaces/:id/model` relay baseURL push logic (sets opencode baseURL to
  relay endpoint when free-tier model selected)

**Added from `eb1a7c4` (legitimate additions, re-applied on top of restore):**
- `ModelStore` interface (superset of `WorkspaceMetadataUpdater`, used by
  `secrets.go` for `wsUpdater` field type)
- `evictModelCache()` helper
- `SetWorkspaceMetadataUpdater` now accepts `ModelStore` parameter type

---

## Verification

`go build ./api/...` passes.
