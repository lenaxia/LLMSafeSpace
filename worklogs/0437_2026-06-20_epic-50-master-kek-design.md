# Worklog: Epic 50 — Master KEK Hardening (Design)

**Date:** 2026-06-20
**Session:** Security assessment of the master KEK delivery + at-rest crypto layering, leading to the Epic 50 design document
**Status:** Complete

---

## Objective

Verify a user-stated belief that the server KEK is "never stored in env vars and
is only securely injected into the pod." When that belief proved false, scope and
design an epic that brings the master-KEK security posture in line with the
blast radius: the KEK is the root of trust for every credential stored at rest
(admin/org LLM API keys, org SSO secrets, API-key DEKs, and via Redis every
user DEK).

---

## Work Completed

### Phase 1 — Verification of the user's claim

The user's belief was not documented anywhere in `README-LLM.md` (verified by
reading all 1441 lines). Tracing the actual mechanism:

- `charts/llmsafespaces/templates/api-deployment.yaml:63-67` delivers the KEK
  via `secretKeyRef → env` (env var, not file mount)
- `api/internal/app/secrets_adapters.go:471` reads it via `os.Getenv`
- No KMS/Vault/HSM provider exists (only the warning string at line 440
  mentions them)

### Phase 2 — Security assessment

Identified 8 findings, 3 HIGH:

| ID | Severity | Finding |
|---|---|---|
| H1 | HIGH | KEK delivered as env var (`/proc/1/environ` exposure) |
| H2 | HIGH | No rotation support — destructive in practice |
| H3 | HIGH | No KMS/Vault/HSM provider exists |
| M1 | MED | `static` warning doesn't fire on empty Helm default |
| M2 | MED | `RootKeyProvider` shares purpose string with Redis DEK cache |
| M3 | MED | Sealed provider's in-memory exposure not documented |
| L1 | LOW | `seal-key` prints root key to stderr |
| L2 | LOW | Sealed provider KEK derivation uses no HKDF info |

### Phase 3 — Epic design (4 iterations, each adversarially reviewed)

**Iteration 1 (rejected):** 11 stories including hand-rolled AWS+GCP+Vault KMS
providers and a magic-prefix ciphertext format. ~10 days.

**Iteration 2 (rejected by user scope decision):** Same as Iteration 1 minus
the KMS providers. ~5 days. User said "limit scope to file on disk; external
providers can come later."

**Iteration 3 (rejected by adversarial review):** 10 stories, ~5 days. Review
discovered the codebase has **two parallel crypto layers**:
- Layer 1: `RootKeyProvider` interface (`Encrypt/Decrypt`) — protects `api_keys`
  and `org_sso_configs`
- Layer 2: `AdminKeyDeriver func(label) []byte` callback returning raw key —
  protects `provider_credentials` (all admin/org LLM API keys)

The Iteration 3 design only hardened Layer 1. Rotation, audit, and versioning
mechanisms would have left every LLM API key unrotatable and unaudited.

**Iteration 4 (final):** Added US-50.2 (unify both layers under `RootKeyProvider`)
as the foundational story. 12 stories, ~6.75 days. User confirmed: keep
unification, keep multi-key zero-downtime rotation, ensure unification is
thoroughly tested.

### Phase 4 — Final adversarial review (Rule 11)

Found three additional issues in the Iteration 4 draft, all fixed:

1. **Boot-order claim wrong:** epic cited line 317 (admin handler) as earliest
   consumer of `dekMasterKey()`. Actual earliest is line 238 (Redis DEK cache
   construction), 70+ lines earlier. Fixed: A19 added, D5/US-50.2 boot sequence
   updated, `TestAppBoot_ProvidersConstructedBeforeRedisCache` test added.

2. **Missed second `else` branch:** epic documented `app.go:425-429` but missed
   `app.go:381` (`authSvc.SetMasterKey(dekMasterKey())`). Both call `dekMasterKey()`
   when `rkp == nil`. Fixed: A20 added, US-50.2 consumer table updated, US-50.7
   file-list updated, `TestAppBoot_BothElseBranchesMigrated` test added.

3. **OIDC state key undocumented:** `app.go:394` uses
   `deriveServerKey("oidc-state-cookie")`. Verified at `sso.go:542,559` it's an
   HMAC signing key (not AES), so correctly excluded from rotation — but the
   epic was silent on why. Fixed: A21 added with HMAC-not-AES verification,
   D8 explicitly documents intentional omission.

---

## Key Decisions

1. **Scope to file-on-disk delivery.** External providers (KMS/Vault) deferred
   per user decision. Rationale: the dominant threat is RCE in the API pod,
   which KMS does not prevent (attacker calls `provider.Decrypt` as legitimate
   code does). KMS's real value is exfil-limitation + audit logs — meaningful
   but not the dominant threat. The `RootKeyProvider` interface is kept clean
   so a future `TransitProvider` (wrapping Vault/OpenBao Transit) drops in
   without touching callers.

2. **Unify both crypto layers under `RootKeyProvider` (US-50.2).** This is the
   foundational story. Without it, rotation/audit/versioning only apply to
   Layer 1 (api_keys + org_sso) and miss Layer 2 (provider_credentials — the
   largest credential store). The unification is high-blast-radius (21 call
   sites + boot-order change) and tested via 6 failure-mode categories
   encompassing 65 test cases (D6).

3. **Keep multi-key zero-downtime rotation (D4).** Explicit product requirement:
   the API must stay live during rotation. Offline rotation was considered and
   rejected as violating the requirement. The multi-key provider holds old+new
   keys during the transition window and tries newest-to-oldest on decrypt;
   relies on GCM auth-tag mismatch returning clean errors (verified at
   `crypto.go:155`).

4. **Audit logging lands before rotation CLI (D7).** Without audit, rotation is
   calendar theater. Detection before recovery.

5. **OIDC state key intentionally not rotated (D8).** Verified it's an HMAC
   signing key for transient PKCE cookies (`sso.go:542,559`), not an encryption
   key wrapping persisted secrets.

---

## Blockers

None.

---

## Tests Run

No code changes — design only. Verification performed during design:

- `grep` for `deriveServerKey\|AdminKeyDeriver\|DecryptSecret` across `.go` files
  to map every crypto call site (informed the two-layer discovery)
- Read of `api/internal/app/app.go` lines 230-450 to verify boot order
- Read of `pkg/secrets/root_key.go`, `crypto.go`, `key_service.go`, `injection.go`,
  `secret_service.go` to verify interface signatures and call patterns
- Read of `api/migrations/000015, 000019, 000028, 000038, 000040` to verify
  schema state (key_version columns, audit_log table, latest migration number)
- Read of `charts/llmsafespaces/templates/api-deployment.yaml, secret.yaml` to
  verify KEK delivery mechanism

---

## Next Steps

1. Reviewer feedback on this design PR
2. If approved: begin Phase 1 (quick wins — US-50.8, 50.9, 50.10, 50.11) in a
   follow-up branch
3. Phase 2 (US-50.1 file mount + US-50.2 unification) is the critical path;
   US-50.2 should be its own PR with the full test matrix in place
4. Phases 3-4 follow after US-50.2 lands

---

## Files Modified

- `design/stories/epic-50-master-kek-hardening/README.md` (new, 905 lines) — the epic design
- `worklogs/0437_2026-06-20_epic-50-master-kek-design.md` (new) — this worklog
