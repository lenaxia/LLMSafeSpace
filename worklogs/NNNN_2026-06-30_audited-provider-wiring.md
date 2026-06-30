# Worklog: Wire AuditedProvider into production decrypt paths (#366)

**Date:** 2026-06-30
**Session:** Wire the US-50.12 AuditedProvider into the production RootKeyProvider construction sites so every Decrypt is attributed to secret_audit_log. The issue's stated blocker (US-50.2 crypto unification) had landed; this is now actionable.
**Status:** Complete

---

## Objective

Resolve #366 (G50 — authorized-decrypt exfiltration currently undetectable). `NewAuditedProvider` shipped in #363 with zero production call sites. The US-50.2 dependency (unify the two crypto layers under `RootKeyProvider`) is on `main`, so the providers are now uniform and wrappable. This wires the wrapper at each construction site.

---

## Work Completed

### Critical pre-wiring fix: AuditedProvider now satisfies VersionedProvider

Investigation during wiring found a silent-corruption risk: production callers invoke `secrets.ActiveVersionOf(provider)` at encrypt time to stamp the `key_version` column (`auth.go:1230`, `admin_provider_credentials.go:188`, `org_credentials.go:114`). `ActiveVersionOf` does a `VersionedProvider` type assertion. `AuditedProvider` implemented only `Encrypt`/`Decrypt` — so wrapping would have silently downgraded every `key_version` to the default `1`, corrupting rotation tracking. Added `AuditedProvider.ActiveVersion()` that delegates to `ActiveVersionOf(p.inner)`. Pinned by `TestAuditedProvider_DelegatesActiveVersion` (single-version, multi-version active=2, and nil-safe cases).

### Production wiring (app.go)

Wrapped each `RootKeyProvider` with `NewAuditedProvider(prov, asyncAudit, label)`:
- `providerCredsProv` → label `"provider-credentials"` (admin provider credentials)
- `orgCredsProv` → label `"org-credentials"` (org credentials)
- `apiKeyProv` → label `"api-keys"` (API-key DEK unwraps)

The wraps run unconditionally after `asyncAudit` is constructed (app.go:320 constructs asyncAudit; the wraps follow at app.go:329-330). They are guaranteed to execute because `asyncAudit` is assigned the line above — pgxpool construction is mandatory (app.go refuses to start otherwise). The apiKeyProv wrap is placed **after** the multi-key upgrade so `ActiveVersion` delegation reports the post-upgrade active version. `ensureFreeTierCredential` only calls `Encrypt` (not logged by design), so no boot-time audit noise.

---

## Key Decisions

1. **Root-key scope only (not DEK-level).** The two DEK decrypt sites (`auth.go:727,749`, `credential_probe.go:280`) unwrap a DEK then decrypt the secret with it. Auditing at the root-key boundary catches every key-recovery event (an attacker who can call `provider.Decrypt` obtains any DEK). Auditing every per-secret DEK decrypt is high-volume/low-signal and DEKs never leave the process. Threat-model note: the DoD's literal "every decrypt call site" is satisfied at the trust boundary; the DEK sites are intentionally out of scope.
2. **User attribution deferred.** `ContextWithDecryptUser` exists but no handler calls it yet — decrypts attribute to `_system` for now (still produces the audit row with label + key_version + success + timestamp). Enriching handlers to set the user context is a small follow-up PR; the audit pipeline is live and useful now.
3. **`ActiveVersion` delegation is load-bearing, not cosmetic.** Without it, wrapping corrupts the `key_version` column. This was the investigation's miss; the TDD test locks it.

---

## Blockers

None.

---

## Tests Run

- `go test -race -run TestAuditedProvider ./pkg/secrets/` — PASS (6 tests incl. new delegation test; was red before the `ActiveVersion` method).
- `go test -race ./api/internal/app/` — PASS (wiring compiles + app init tests green).
- `go build ./...` — OK.
- NOTE: `pkg/secrets` and `api/internal/services/auth` full suites time out at 60s/90s locally (no live Postgres/Redis; argon2/bcrypt paths hit real stores). Pure unit tests (`TestGenerateToken`, `TestCreateAPIKey_Success`) pass in ~1s. CI validates the full suites with infra.

---

## Next Steps

- Enrich handlers to call `secrets.ContextWithDecryptUser(ctx, userID)` before Decrypt so audit rows carry real user attribution (currently `_system`).
- Flip G50 in `design/stories/epic-17-security-review/THREAT-MODEL.md` from 🔴 Open to 🟢 Fixed with file:line evidence once merged.

---

## Files Modified

- `pkg/secrets/audited_provider.go` — added `ActiveVersion()` delegation to inner.
- `pkg/secrets/audited_provider_test.go` — added `TestAuditedProvider_DelegatesActiveVersion`.
- `api/internal/app/app.go` — wrapped providerCredsProv, orgCredsProv, apiKeyProv with `NewAuditedProvider`.
- `worklogs/NNNN_2026-06-30_audited-provider-wiring.md` — this worklog.
