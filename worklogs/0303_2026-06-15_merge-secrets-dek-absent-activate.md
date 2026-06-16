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

Five new tests covering the merge surface, plus one updated test for the new
empty-incoming semantics:

| Test | What it proves |
|---|---|
| `TestSeedEphemeralSecrets_PreservesUserCredentials` (new) | **Primary regression:** calls `seedEphemeralSecrets` end-to-end with a `fakeSecretInjector` returning admin-only payload after a prior full inject; user credentials must survive |
| `TestSeedEphemeralSecrets_IncomingWinsForExistingName` (new) | Incoming replaces existing for same name (admin rotation) |
| `TestSeedEphemeralSecrets_NoExistingSecret` (new) | No existing secret + non-empty incoming → create |
| `TestSeedEphemeralSecrets_EmptyIncomingPreservesExisting` (new) | Empty incoming → existing unchanged |
| `TestMergeSecretsByName_MalformedExisting_FallsBackToIncoming` (new) | Malformed JSON in stored secret falls back to writing incoming as-is |
| `TestSeedEphemeralSecrets_EmptyResult_NoWrite` (updated) | Empty incoming + no existing → still no write (guard preserved under new semantics) |
| `TestHandler_RevealSecret_CiphertextDecryptFailed_Returns409` (new) | Ciphertext mismatch → 409 + actionable message + audit log entry |
| `TestHandler_RevealSecret_DEKUnavailable_Returns403` (new) | DEK absent → 403 + "re-authenticate" message + distinct audit reason |

---

## Improved error handling and logging

In addition to the merge fix, this PR addresses a separate gap revealed by the
production incident: the reveal/inject path returned a generic `500 internal
error` and emitted no audit log when the stored ciphertext could not be
decrypted with the current DEK (the production failure mode). Operators had
no signal; users had no actionable message.

Changes:

- **New sentinel `ErrCiphertextDecryptFailed`** — distinguishes "DEK is fine,
  ciphertext doesn't match it" (data-state problem, e.g. DEK rotated without
  re-encrypt) from `ErrDEKUnavailable` (session expired, fixable by
  re-authenticating).

- **`DecryptSecretValue` now writes structured audit log entries** for both
  failure modes, with `reason=ciphertext_aead_failure` or `reason=dek_unavailable`,
  plus `name`, `type`, and `key_version`. Operators can correlate user reports
  with audit log entries by `secretID`.

- **`handleSecretError` maps `ErrCiphertextDecryptFailed` to 409 Conflict** with
  an actionable user-facing message that explains what happened and what to do
  (re-create the secret; contact admin if password unchanged). 409 is the
  correct status: "the resource exists but is in an inconsistent state with
  the current key material" — re-authenticating won't help.

- **`RevealSecret` handler emits structured Warn logs** with `userID`, `secretID`,
  and the underlying error, so operators can see decrypt failures in the
  application log without grepping audit tables.

---

## Files Modified

- `api/internal/services/workspace/workspace_service.go` — add `MergeSecretsManifest`, `mergeSecretsByName`; rewire `seedEphemeralSecrets` to use merge path
- `api/internal/services/workspace/workspace_service_test.go` — 5 new tests + 1 updated
- `pkg/secrets/errors.go` — add `ErrCiphertextDecryptFailed` sentinel
- `pkg/secrets/secret_service.go` — `DecryptSecretValue` distinguishes DEK-absent vs ciphertext-mismatch; emits structured audit entries
- `api/internal/handlers/secrets.go` — `handleSecretError` maps new sentinel to 409 + actionable message; `RevealSecret` adds structured Warn logging
- `api/internal/handlers/secrets_integration_test.go` — 2 new reveal tests
- `worklogs/0303_2026-06-15_merge-secrets-dek-absent-activate.md` — this worklog
