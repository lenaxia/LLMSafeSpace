# Worklog: Merge secrets on DEK-absent activate — preserve user credentials

**Date:** 2026-06-15
**Status:** Complete

---

## Problem

When a workspace was activated or restarted and the user's DEK was unavailable
(API-key auth, expired session, or background reconcile), `seedEphemeralSecrets`
called `createEphemeralSecretsSecret` which called `EnsureSecretsManifest` and
**replaced the entire `secrets.json`** with only admin credentials.

User-owned credentials (e.g. `thekao cloud`) that had been correctly written to
the K8s Secret when the DEK was last available were silently dropped. The pod
booted with only the relay provider.

**Observed in production:** user `4382f558` had `thekao cloud` credential
(`cc2035d9`) bound and previously injected, but every workspace activate after
DEK expiry overwrote the secret with admin-only, causing the pod to boot without
the personal LLM provider.

---

## Root Cause

`seedEphemeralSecrets` → `createEphemeralSecretsSecret` → `EnsureSecretsManifest`.

`EnsureSecretsManifest` is a correct full-replace write — it's the right tool for
`pushSecretsToAgent` (bind-time delivery when the DEK is available). It's the
wrong tool for `seedEphemeralSecrets` (admin-only refresh when DEK is absent),
because there it clobbers user credentials with a degraded subset.

---

## Fix

Added `MergeSecretsManifest` with a merge-by-name strategy, and wired
`seedEphemeralSecrets` to use it instead of `createEphemeralSecretsSecret`.

**Merge semantics:**
- Incoming entry wins for names it contains (admin key rotations propagate)
- Existing entry is preserved for names absent from incoming (user credentials
  survive a DEK-absent refresh)
- Empty incoming + no existing secret: no write (same guard as before — avoids
  creating a Secret with `secrets.json: []` that the init container would mount
  and treat as "no credentials")
- Empty incoming + existing secret: existing preserved unchanged

`mergeSecretsByName` is a pure JSON helper (no K8s dependency) that builds the
merged array: start with all incoming entries in order, then append any existing
entries whose name was not covered by incoming.

---

## Tests

Eight tests covering the merge surface:

| Test | What it proves |
|---|---|
| `TestSeedEphemeralSecrets_PreservesUserCredentials` | Primary regression: DEK expires → admin-only activate → user credential must survive |
| `TestSeedEphemeralSecrets_IncomingWinsForExistingName` | Incoming replaces existing for same name (admin rotation) |
| `TestSeedEphemeralSecrets_NoExistingSecret` | No existing secret + non-empty incoming → create |
| `TestSeedEphemeralSecrets_EmptyIncomingPreservesExisting` | Empty incoming → existing unchanged |
| `TestSeedEphemeralSecrets_EmptyResult_NoWrite` | Empty incoming + no existing → no write (guard preserved) |
| `TestEnsureSecretsManifest_PreservesWorkspaceConfig` | Credential bind does not clobber workspace-config.json |
| `TestEnsureSecretsManifest_PreservesWorkspaceConfig_BindFirst` | Reverse ordering also safe |
| `TestEnsureWorkspaceConfig_UpdatesExistingSecret` | EnsureWorkspaceConfig preserves secrets.json |

---

## Files Modified

- `api/internal/services/workspace/workspace_service.go` — add `MergeSecretsManifest`, `mergeSecretsByName`; rewire `seedEphemeralSecrets` to use merge path
- `api/internal/services/workspace/workspace_service_test.go` — 8 tests
- `worklogs/0299_2026-06-15_merge-secrets-dek-absent-activate.md` — this worklog
