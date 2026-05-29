# Worklog: Auto-push Secrets to Agentd on Bind

**Date:** 2026-05-29
**Session:** Fix SSH key not materialized when bound to running workspace
**Status:** Complete

---

## Objective

User attached an SSH key to a running workspace but the key was never written to `~/.ssh/`. Investigate and fix the secrets injection flow for runtime secret binding.

---

## Work Completed

- Investigated SSH key injection flow: init container handles boot-time secrets via `entrypoint-common.sh`, but secrets attached after boot must go through agentd's `/v1/reload-secrets` endpoint
- Found that `SetBindings` handler (`secrets.go:188`) saved bindings to DB but never called reload-secrets on the running pod's agentd
- The `ReloadSecrets` handler already had the full push logic (prepare → decrypt → POST to agentd) but it was only callable via explicit `POST /workspaces/:id/reload-secrets` which the frontend never calls after binding
- Extracted `doReload()` shared helper from `ReloadSecrets` to avoid duplication
- Added `pushSecretsToAgent()` method called from `SetBindings` after successful bind update
- Fixed a bug in `pushSecretsToAgent` where `extractAuth` return values were destructured in wrong order (`sessionID, _ := extractAuth(c)` — sessionID got userID instead of sessionID, causing DEK lookup to fail)
- Errors from `pushSecretsToAgent` are silently ignored (workspace may not be Active, pod may not exist)
- Wrote 2 new e2e tests:
  - `TestHandler_E2E_BindTriggersReloadSecrets`: creates SSH key, binds to workspace, verifies mock agentd received the reload-secrets POST with decrypted secret payload
  - `TestHandler_E2E_BindNoReloadWhenNoPod`: verifies bind succeeds even when workspace has no running pod (graceful degradation)

---

## Key Decisions

- **Silent failure on push**: `pushSecretsToAgent` logs nothing and returns silently on failure. The bind itself succeeds (DB state is correct); the pod will get secrets on next activation if push fails now. This avoids breaking the bind UX when the workspace is suspended.
- **Reuse `doReload` from `ReloadSecrets`**: The explicit reload-secrets endpoint still works for manual triggers. Both paths share the same agentd push logic.

---

## Blockers

None.

---

## Tests Run

- `go test -race -run 'TestHandler_E2E' ./internal/handlers/` — 5 tests PASS
- `go vet ./internal/handlers/` — clean

---

## Next Steps

1. Deploy and verify SSH key injection works on a running workspace
2. Consider also calling `pushSecretsToAgent` from `UpdateSecret` and `DeleteSecret` handlers (secrets changed while workspace is running should be reflected immediately)

---

## Files Modified

- `api/internal/handlers/secrets.go` — extracted `doReload()`, added `pushSecretsToAgent()`, called from `SetBindings`
- `api/internal/handlers/secrets_integration_test.go` — 2 new e2e tests with mock agentd server
