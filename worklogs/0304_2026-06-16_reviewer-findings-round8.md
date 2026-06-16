# Worklog: Reviewer Findings Resolution (Round 8)

**Date:** 2026-06-16
**Session:** Address reviewer's 7th-pass blocking findings on PR #188, re-push, monitor CI toward approval
**Status:** In Progress — all fixes landed locally, ready to push

---

## Objective

Resolve the 3 blocking findings from the automated reviewer's 7th pass on PR #188:
- **C-1:** `CreateOrgRequest.Password` is `binding:"required"` but silently discarded
- **C-2:** `AcceptOrgKeyRequest` is orphaned dead code
- **T-1:** No tests for the server-KEK org credential path (the keystone behaviour of the entire Story 1)

---

## Work Completed

### Unblocked from worklog 303 (2026-06-16)
- GitHub auth restored; pushed commits `6210d0a4` (merge main + 0300→0302 renumber) and `dfa7338c` (prior doc update)
- CI re-triggered — Lint failed on new collision: main advanced with PR #190 landing `0302_2026-06-16_relay-router-image-build.md`
- Renamed worklog `0302→0303`, merged origin/main, pushed
- Lint PASS on that re-run; migration safety, gitleaks, trivy, govulncheck, pkg/secrets integration all PASS

### Finding C-1 fix: Remove Password from CreateOrgRequest

Validated: `Create` handler (`orgs.go:93-136`) never reads `req.Password`. The field was collected via `binding:"required"` binding and thrown away.

**`pkg/types/types.go`** — removed `Password` field from `CreateOrgRequest`:
```go
// Before:
Password string `json:"password" binding:"required" log:"-"`

// After: field removed entirely
```

**`api/internal/handlers/orgs_test.go`** — removed `"password":"*"` from 3 test payloads; deleted `TestOrgsHandler_Create_MissingPassword` entirely (tested a dead validation rule)

**`api/internal/handlers/org_create_billing_test.go`** — removed `"password":"secretpass"` from 5 test payloads (lines 92, 123, 150, 180, 247)

**`frontend/src/api/orgs.ts`** — removed `password: string` from `CreateOrgRequest` interface

**`frontend/src/components/settings/OrgSettingsTab.tsx`** — removed `useRef` import, `password`/`passwordRef` state, password validation, password input DOM, and the misleading helper text "Your password is used to create an encryption key"

### Finding C-2 fix: Remove AcceptOrgKeyRequest dead code

Validated via grep: zero consumers of `AcceptOrgKeyRequest` outside the type definition itself.

**`pkg/types/types.go`** — removed `AcceptOrgKeyRequest` type entirely (lines 726-729, ~6 lines)

### Finding T-1 fix: Add server-KEK org credential tests

#### Injection tests (`pkg/secrets/credential_precedence_test.go`)

Three new tests appended:

1. **`TestCredentialPrecedence_OrgCredentialViaServerKEK`** — `OwnerType="org"` binding encrypted with orgKEK; deriver returns the key for `"org-credentials"` label; verifies credential decrypts and produces correct provider data. The keystone test for the story.

2. **`TestCredentialPrecedence_DomainSeparation_AdminAndOrgDistinctKeys`** — workspace with both `admin` (anthropic) and `org` (openai) bindings. Deriver returns CRYPTOGRAPHICALLY DISTINCT keys for the two HKDF labels. Both credentials must decrypt successfully. A regression that reuses the wrong label would cause exactly one decryption to fail — a stronger signal than a label-string assertion.

3. **`TestCredentialPrecedence_OrgCredential_WrongKEK_FailsAndFallsBack`** — org credential encrypted with key A, deriver returns key B (different). Verifies fail-soft contract: `PrepareSecretsForInjection` returns nil error but the undecryptable credential is skipped (no provider data injected). Anchors the robustness praised by the reviewer.

#### Handler tests (new file `api/internal/handlers/org_credentials_test.go`)

8 tests, modeled on `admin_provider_credentials_test.go`:
- `TestOrgCredentials_Create_Success` — happy path, verifies stored ciphertext decrypts back to original
- `TestOrgCredentials_Create_NilKEK_503` — nil deriver → 503, no store call
- `TestOrgCredentials_Create_MissingAPIKey_400`
- `TestOrgCredentials_Create_BindFails_Returns201WithWarning` — bind failure doesn't fail create (bindWarning in response)
- `TestOrgCredentials_Update_APIKeyRotation_Success` — decrypt old with orgKEK, replace apiKey, re-encrypt, key version increments
- `TestOrgCredentials_Update_NilKEK_503` — deriver returns nil on rotation → 503, stored ciphertext preserved
- `TestOrgCredentials_Update_NotFound_404`
- `TestOrgCredentials_Update_NameOnly_NoReEncrypt` — metadata-only update doesn't call deriver or re-encrypt
- `TestOrgCredentials_Update_CorruptCiphertext_500` — unreadable ciphertext → 500 (mirrors admin C-4 fix)

---

## Key Decisions

1. **Domain separation test uses distinct keys, not just label assertions.** The reviewer noted that the existing mock deriver `func(label string) []byte { return adminKEK }` ignores the label, so it can't catch a regression where the wrong label is used. The new domain separation test creates two different KEKs and returns each for its correct label — a regression would cause a decrypt failure. This is stronger than asserting the label string.

2. **Wrong-KEK fail-soft test anchors robustness.** The reviewer's robustness section said "✓ No robustness concerns" — but this test proves the fail-soft contract is real, not just assumed. A `defer recover()` or a future change to `decryptBinding` that panics on wrong keys would be caught here.

3. **Name-only update test protects against unnecessary re-encryption.** The reviewer didn't flag this, but adding `TestOrgCredentials_Update_NameOnly_NoReEncrypt` verifies the conditional on line 155 of `org_credentials.go` (`if req.APIKey != nil`). A future change that always re-encrypts would call the deriver and fail this test.

---

## Blockers

**Auth e2e suite timing.** `TestE2E_RealAuth_Recover` and related bcrypt-cost-12 tests take ~181s under `-race` when run as a full suite. The 120s budget is tight for a combined `go test -timeout 120s -race ./...` run. This is pre-existing (my changes don't touch auth) but may cause CI flakiness. Not blocking the PR — CI uses separate jobs per package with >120s timeouts.

**Current:** Pending push + CI re-run + reviewer re-review.

---

## Tests Run

- `go build ./pkg/types/... ./pkg/secrets/... ./api/internal/handlers/...` — **PASS**
- `go test -timeout 60s -race -run "TestCredentialPrecedence" ./pkg/secrets/...` — **PASS** (1.067s)
- `go test -timeout 60s -race -run "TestOrgCredentials|TestOrgsHandler|TestCreateOrg" ./api/internal/handlers/...` — **PASS** (1.149s)
- `go test -timeout 300s -race ./api/internal/services/auth/...` — **PASS** (181s — bcrypt cost 12, pre-existing slowness, not a regression)
- `go vet ./pkg/types/... ./pkg/secrets/... ./api/internal/handlers/...` — **PASS**
- `gofmt` — clean (after formatting org_credentials_test.go)

---

## Next Steps

1. Push this branch (renumber + reviewer fixes)
2. Monitor CI — should be the first fully-green run across all checks
3. Automated reviewer should re-evaluate on the new HEAD — C-1, C-2, and T-1 should all be resolved
4. If reviewer returns zero findings → merge PR #188
5. Start Story 2 (restrict org creation + email resolution) or Story 3 (single-org enforcement)

---

## Files Modified

### Modified (7 files)
- `pkg/types/types.go` — removed `Password` from `CreateOrgRequest`, removed `AcceptOrgKeyRequest` type
- `api/internal/handlers/orgs_test.go` — removed Password payloads, deleted `TestOrgsHandler_Create_MissingPassword`
- `api/internal/handlers/org_create_billing_test.go` — removed Password from 5 test payloads
- `frontend/src/api/orgs.ts` — removed `password: string` from `CreateOrgRequest`
- `frontend/src/components/settings/OrgSettingsTab.tsx` — removed password input + validation + helper text
- `pkg/secrets/credential_precedence_test.go` — added 3 tests (org injection, domain separation, wrong-KEK fail-soft)

### New (1 file)
- `api/internal/handlers/org_credentials_test.go` — 9 handler tests (Create/Update happy + unhappy paths)

### Branch
`feat/org-access-control-story1-eliminate-org-dek`
