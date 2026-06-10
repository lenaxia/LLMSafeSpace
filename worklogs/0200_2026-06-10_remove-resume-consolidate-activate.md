# Worklog 0199 — Remove POST /resume; consolidate all resume paths through ActivateWorkspace

**Date:** 2026-06-10

## Problem

Four workspaces lost provider credentials after a batch resume on 2026-06-10. Only one
workspace (`9b7c0e76`) retained credentials — investigation revealed it was the only one
resumed via `POST /activate` (the correct API path). The other three hit `POST /resume`
logic (via `kubectl patch status`) and booted without credentials.

Root cause: `POST /resume` → `ResumeWorkspace()` transitioned the workspace to `Resuming`
without calling `refreshEphemeralSecrets`. The `workspace-secrets-<id>` K8s Secret was
never created, the credential-setup init container found no `secrets.json`, and the pod
booted with only the relay provider.

A second gap existed: `POST /sessions/new` → `EnsureSession()` also called `ResumeWorkspace`
directly on the Suspended path — same credential bypass, hit every time a user navigated
directly to a workspace URL while it was suspended.

`ActivateWorkspace` was the only correct path: it calls `refreshEphemeralSecrets` then
transitions to Resuming. This was confirmed by the `9b7c0e76` timestamp evidence in the
API logs — it had `credentialsPendingSince` set and was already in `Resuming` phase before
the batch patch ran.

## Decision

Remove `POST /resume` and `ResumeWorkspace` entirely. There is no valid use case for
resuming a workspace without credential injection — the method's existence was a footgun.
All resume paths go through `ActivateWorkspace`, which guarantees credentials are in the
K8s Secret before the controller creates the pod.

## Changes

**API (`api/`):**
- `POST /:id/resume` route deleted from `router.go`
- `ResumeWorkspace` deleted from `workspace_service.go`; `ActivateWorkspace` now owns the
  full Suspended→Resuming transition inline (CRD fetch, `isActivePhase` idempotency check,
  phase guard, `LastActivityAt` reset, `UpdateStatus`)
- `EnsureSession` calls `ActivateWorkspace` instead of `ResumeWorkspace` on the Suspended
  path — ensures credentials are injected on direct workspace URL navigation
- `ResumeWorkspace` removed from `interfaces.go` and `mocks/workspace.go`
- Tests: `TestResumeWorkspace_*` replaced with `TestActivateWorkspace_*` covering happy
  path, ownership failure, K8s Get failure, K8s UpdateStatus failure, and phase guards

**SDKs:**
- `resume()` removed from Python, Go, and TypeScript clients
- Go SDK: `Restart()` and `Rename()` restored (accidentally deleted alongside `Resume()`)
- Go canary scenarios: `c.Workspaces.Resume()` → `c.Workspaces.Activate()`
  (`d-ws-lifecycle`, `d-sse-events`, `d-suspend-resume-session`)
- Python canary: `c.workspaces.resume()` → `c.workspaces.activate()`
- TypeScript canary: same
- VSCode extension: `resumeWorkspace()` → `activateWorkspace()`; duplicate removed
- OpenAPI spec: `/workspaces/{id}/resume` endpoint removed
- Contract tests (Hurl): `POST /resume` → `POST /activate`
- Spec completeness test updated
- `sdks/tests/README.md` table updated

## PR

PR #82 — approved after one round of review fixes:
- Restored accidentally-deleted Go SDK `Restart()` and `Rename()` methods
- Updated Go canary scenarios (missed in first pass)
- Removed duplicate `activateWorkspace` in VSCode extension
- Fixed Python indentation error in live integration test
- Added `TestActivateWorkspace_K8sGetFails` and `TestActivateWorkspace_K8sUpdateFails`
