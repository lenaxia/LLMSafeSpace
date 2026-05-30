# Worklog: Secrets Management — Remediation of All 12 Bugs from Worklog 0085

**Date:** 2026-05-30
**Session:** Fix every bug surfaced in the live-cluster sweep recorded in worklog 0085 — Critical, High, Medium, and Low — leaving nothing failing. User explicitly stated "fix ALL bugs, including lows; do not leave anything failing." Architectural decisions for Bug 3, 6, 9, and 10 made up front before coding (see Key Decisions).
**Status:** Complete. All 12 bugs fixed with TDD coverage; full test suite passes; gofmt clean; one pre-existing formatting issue in `auth_sessionid_test.go` corrected per Rule 5.

---

## Objective

Convert the 12 root-caused bugs from worklog 0085 into a remediation patch set. Each bug is paired with a TDD regression test added in advance of the fix and verified to fail, then to pass after the fix. All fixes ship together so the secrets management subsystem reaches a known-good state in one go.

---

## Key Decisions

Decisions made up front so the implementation could proceed without re-prompting:

1. **Bug 3 (delivery model):** SetBindings auto-pushes via reload-secrets server-side. Two side-effects on every bind: (a) write the per-workspace K8s Secret `workspace-secrets-<id>` (durable, picked up by init container on next pod start), and (b) HTTP-push the same payload to live agentd (in-place update for already-running pods). This matches the user's mental model — bind a secret, it appears — without requiring pod restart or an explicit "Apply" gesture in the UI.
2. **Bug 9 (rotation strategy):** Eager re-encryption inside the rotate transaction. Walk every `user_secrets` row owned by the user, decrypt with old DEK, re-encrypt with new DEK, bump `key_version` atomically. Lazy migration (US-10.8) was the original epic-10 plan but was never implemented and would have required a dual-DEK-lookup code path on every secret read; eager is simpler, correct on completion, and OK at typical user secret counts (<1000).
3. **Bug 6 (type rename):** `llm-provider` → `api-key` everywhere with a DB migration. The `user_secrets.type` column is plain `VARCHAR(50)` with no CHECK constraint, so the migration is a single UPDATE. No dual-name compatibility shim — every caller is in this repo.
4. **Bug 10 (recovery-key UX):** Return `recoveryKey` in the register response (one-time display). No new endpoint; a `GET /account/recovery-key` would itself need to invalidate the old key, which adds operational complexity without clear value over the at-register flow.

---

## Work Completed

### Bug 5 — Register doesn't UnlockDEK (High → fixed)

`auth.go::Register` now calls `UnlockDEK` after `InitializeUserKeys`, using the freshly-issued JWT's `jti` as the session ID. Key-init failures are now fatal (return registration error) instead of the previous "non-fatal: keys will be initialized on first secret creation" comment, which was wrong — there was no lazy-init path. Empty `jti` is also now fatal.

- `api/internal/services/auth/auth.go:446-487` — Register rewritten for fail-closed key init + UnlockDEK
- `api/internal/services/auth/auth_test.go:607-697` — three new tests: `TestRegister_UnlocksDEKAndReturnsRecoveryKey`, `TestRegister_KeyInitFailureFailsClosed`, `TestLogin_OmitsRecoveryKey` (all red→green)

### Bug 10 — Recovery key never delivered (High → fixed)

`AuthResponse` now has a `RecoveryKey string` field with `omitempty` so login responses don't carry an empty value. `Register` populates it from `InitializeUserKeys`'s previously-discarded return value.

- `pkg/types/types.go:328-337` — added `RecoveryKey string \`json:"recoveryKey,omitempty"\`` to `AuthResponse`
- Bug 10 covered by the same test (`TestRegister_UnlocksDEKAndReturnsRecoveryKey`) that fixes Bug 5.

### Bug 1 — reload-secrets unwired (High → fixed)

Added `secretsPodIPResolver` adapter (in `app/secrets_adapters.go`) that combines a workspace CRD getter with a database ownership lookup. `app.New` now calls `secretsHandler.SetPodIPResolver(...)` so `POST /api/v1/workspaces/:id/reload-secrets` is no longer 503. Ownership is checked via DB (Postgres is authoritative for API-layer ownership); non-owner returns empty IP (treated as "no running pod" / 409 by the handler), so we don't leak workspace existence cross-user.

- `api/internal/handlers/secrets.go:31-54` — added `HasPodIPResolver()` accessor for wiring tests
- `api/internal/app/secrets_adapters.go:282-348` — `secretsPodIPResolver`, `workspaceCRDGetter`, `dbOwnerLookup`
- `api/internal/app/app.go:127-138` — wires resolver in production
- `api/internal/app/secrets_podip_resolver_test.go` (new) — 7 unit tests covering owner-active, non-owner, missing workspace, suspended, CRD error, DB error, empty inputs
- `api/internal/app/secrets_wiring_test.go:147-192` — wiring smoke test

### Bug 2 — Bind-time auto-push silently swallows errors (High → fixed)

`pushSecretsToAgent` now logs at Warn for any failure of `PrepareSecretsForInjection`, the manifest write, or the HTTP push. The expected case (no running pod, `errNoRunningPod`) is downgraded to Info so it doesn't pollute Warn dashboards. SecretsHandler gained `SetLogger(pkginterfaces.LoggerInterface)`.

- `api/internal/handlers/secrets.go:60-73` — `SetLogger`
- `api/internal/handlers/secrets.go:282-345` — rewritten `pushSecretsToAgent` with explicit logging; `warn`/`info` helpers
- `api/internal/handlers/secrets_integration_test.go:601-680` — `TestHandler_BindLogsReloadFailure` regression test using a recordingLogger fake

### Bug 3 + 14 — Bound secrets only reach pod via Activate; frontend has no path (Critical → fixed)

The chosen design (decision #1) is implemented end-to-end: `SetBindings` now writes the K8s `workspace-secrets-<id>` Secret AND HTTP-pushes to agentd in the same handler invocation. The K8s Secret survives pod restarts; the HTTP push covers live pods. No changes to `WorkspaceSettingsDrawer.tsx` are needed — the UI already calls `PUT /workspaces/:id/bindings` and the server-side delivery is now durable + immediate.

- `api/internal/handlers/secrets.go:31-54` — `SecretsManifestWriter` interface, `SetSecretsManifestWriter`
- `api/internal/services/workspace/workspace_service.go:903-957` — public `EnsureSecretsManifest` method (writer impl); old `createEphemeralSecretsSecret` now delegates to it
- `api/internal/app/app.go:140-145` — wires workspace.Service as the manifest writer
- `api/internal/handlers/secrets_integration_test.go:519-599` — `TestHandler_BindWritesManifestForDurability`

### Bug 9 — KEK rotation orphans existing secrets / data loss (Critical → fixed)

`KeyService.RotateKeyWithPassword` now requires a `SecretStore` (set via `SetSecretStore`, called automatically by `NewSecretService`). Rotation flow:

1. Verify password (unwrap old DEK with derived KEK).
2. Generate new DEK.
3. **Re-encrypt every `user_secrets` row** under the new DEK in a single SERIALIZABLE transaction (`ReEncryptUserSecrets`). The transform closure decrypts with old DEK and encrypts with new DEK; intermediate plaintexts are zeroed.
4. Wrap new DEK with KEK and bump `key_version`.
5. Refresh session DEK cache.

If step 3 fails, neither user_keys nor any secret row changes — old DEK still in place, rotation aborted. The transactional contract is encoded in the SecretStore interface.

- `pkg/secrets/key_service.go:39-65, 282-380` — restructured RotateKeyWithPassword + new SetSecretStore
- `pkg/secrets/store.go:6-31` — added `ReEncryptUserSecrets(ctx, userID, newKeyVersion, transform)` to interface
- `pkg/secrets/pg_secret_store.go:90-149` — Postgres impl with `BeginTx(IsoLevel: Serializable)` and `SELECT ... FOR UPDATE`
- `pkg/secrets/secret_service_test.go::mockSecretStore`, `api/internal/handlers/secrets_test_helpers_test.go::testSecretStore`, `api/internal/app/secrets_adapters.go::dbSecretStoreAdapter`, `api/internal/services/auth/auth_e2e_secrets_test.go::memSecretStore` — all four in-tree SecretStore implementations got the new method (two-pass: compute new ciphertexts, then commit)
- `pkg/secrets/secret_service.go:18-24` — `NewSecretService` now wires the store on the KeyService
- `pkg/secrets/key_service_test.go:469-552` — `TestKeyService_RotateKey_EagerlyReEncryptsSecrets` regression test
- `pkg/secrets/key_rotation_test.go` — existing tests refactored via `newRotationTestService` helper to provide a SecretStore (rotation now refuses to run without one)
- `pkg/secrets/e2e_test.go:165-176` — Phase 8 comment corrected; now asserts every pre-rotation secret is decryptable post-rotate (was previously documented as the failure mode)

### Bug 6 — `llm-provider` → `api-key` rename (Medium → fixed)

Full rename across code, tests, frontend, SDK, and database. New migration `000010_rename_llm_provider_to_api_key.up.sql` runs `UPDATE user_secrets SET type = 'api-key' WHERE type = 'llm-provider'`. Down-migration reverses it.

- `pkg/secrets/types.go:11-58` — `SecretTypeAPIKey` is canonical; `SecretTypeLLMProvider` removed; new `ValidSecretTypesList()` and `MetadataRequirementsBySecretType` for self-documenting errors
- `cmd/workspace-agentd/secrets.go:218`, `pkg/agentd/secrets/secrets.go:400-466` — agent-side dispatch + `applyAPIKey`
- `frontend/src/api/secrets.ts`, `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx`, `frontend/src/components/settings/SecretsTab.tsx`, `sdks/typescript/src/types.ts` — string literal updates
- `api/migrations/000010_rename_llm_provider_to_api_key.{up,down}.sql` — new migration
- All test files: `SecretTypeLLMProvider` → `SecretTypeAPIKey`, `"llm-provider"` → `"api-key"` (8 Go files)

### Bug 7 — Metadata field names undocumented; better error messages (Low → fixed)

The `invalid secret type` error now lists the valid set:
> `invalid secret type: bogus (valid: api-key, ssh-key, git-credential, secret-file, env-secret)`

Missing-metadata errors already named the field (verified by `TestSecretService_CreateSecret_InvalidMetadata_NamesField`). Added the documentation map `MetadataRequirementsBySecretType` to `types.go` so OpenAPI generators or SDK builders can pick up the requirements programmatically.

- `pkg/secrets/types.go:42-79` — `ValidSecretTypesList()`, `MetadataRequirementsBySecretType`, `formatSecretTypes`
- `pkg/secrets/secret_service.go:31-36` — error message includes the valid list
- `pkg/secrets/secret_service_test.go:331-380` — `TestSecretService_CreateSecret_InvalidType_ListsValidTypes`, `TestSecretService_CreateSecret_InvalidMetadata_NamesField`

### Bug 11 — `user_secret_bindings` rows survive workspace soft-delete (Medium → fixed)

`MarkWorkspaceDeleted` now deletes any `user_secret_bindings` rows pointing at the deleted workspace, in addition to the soft-delete `UPDATE`. The bindings table has no FK on `workspace_id` (column types differ historically: `workspaces.id UUID` vs `user_secret_bindings.workspace_id VARCHAR(36)`); rather than a schema migration, we clean up at the application layer.

- `api/internal/services/database/database.go:419-446` — extended `MarkWorkspaceDeleted` with binding purge
- `api/internal/services/database/database_test.go:809-862` — `TestMarkWorkspaceDeleted_PurgesBindings` and `TestMarkWorkspaceDeleted_BindingDeleteFailureDoesNotBreakSoftDelete`

### Bug 12 — Workspaces stuck in Failed leak K8s Secrets (Medium → fixed)

Two coordinated changes:

1. **Health-check loop** now triggers a pod recreate when `ConsecutiveHealthFailures` reaches `healthCheckFailureThreshold` *via the connection-refused branch* (previously only the "agent says unhealthy" branch did this; the connection-refused path could climb to 36+ as observed). On threshold hit: delete the pod, transition workspace to Creating, clear PodIP, increment RestartCount.

2. **Failed-phase reconciler** now calls `cleanupFailedWorkspaceSecrets`, which deletes `workspace-secrets-*`, `workspace-creds-*`, and `workspace-pw-*` Secrets for the workspace. Idempotent (missing Secret = success).

- `controller/internal/workspace/controller.go:64-74` — Failed phase calls cleanup
- `controller/internal/workspace/controller.go:484-516` — `cleanupFailedWorkspaceSecrets`
- `controller/internal/workspace/controller.go:973-1003` — connection-refused branch triggers restart at threshold
- `controller/internal/workspace/health_test.go:241-310` — new `TestCheckAgentHealth_ConnectionRefused_RestartsAfterThreshold`
- `controller/internal/workspace/controller_test.go` — new `TestReconcile_Failed_CleansUpSecrets`

### Bug 13 — API-layer mount_path validator (Low → fixed)

`validateMountPath` runs the same path-traversal logic as the in-pod materializer's `resolveMountPath` at the API layer, so adversarial input is rejected before it reaches the database. Rejects: empty / whitespace-only paths, absolute paths, and any relative path that resolves outside the (notional) base after `Clean`. Trade-off: the API now requires relative paths, while the materializer would accept absolute paths under the concrete base; existing tests using absolute paths were updated to relative paths. This is a conscious tightening.

- `pkg/secrets/secret_service.go:347-405` — `validateMountPath`; called from `validateMetadata` for `secret-file`
- `pkg/secrets/secret_service_test.go:382-425` — `TestSecretService_CreateSecret_RejectsAdversarialMountPath`
- Test files updated to relative paths: `pkg/secrets/injection_test.go:135`, `pkg/secrets/secret_service_extended_test.go:65`, `pkg/secrets/integration_test.go:308`, `pkg/secrets/e2e_test.go:86`, `api/internal/handlers/secrets_integration_test.go:217`

### Permanent shell test (worklog 0085 follow-up)

Promoted the `/tmp/secretstest/` ad-hoc scripts into a permanent suite. Covers all bug-class regressions (Bug 5, 6, 7, 9, 10, 13 plus W7 isolation and W10 password-change preservation).

- `local/test-secrets.sh` — sibling to `local/test-auth.sh`; usage: `local/test-secrets.sh [BASE_URL]`

### Pre-existing tech debt cleared

- `api/internal/services/auth/auth_sessionid_test.go` — gofmt-clean (Rule 5: "no pre-existing errors are acceptable")

---

## Key Decisions (in-flight)

- **API-layer mount_path is stricter than materializer:** the materializer accepts absolute paths under the concrete base (`/home/sandbox/.secrets`); the API validator rejects all absolute paths and requires relative inputs. The API does not know the concrete base in production (it's defined in `pkg/agentd/types.go`), and importing the agentd package from the API package would invert the dependency direction. The strictness is tighter than necessary but consistent and easy to explain to callers; absolute paths under the base never gave the user anything they couldn't get with a relative path.
- **`workspace.Service.EnsureSecretsManifest` is a public wrapper of the previously-private `createEphemeralSecretsSecret`:** kept the old name as a thin call into the new function to minimise the diff in `ActivateWorkspace`. The "create or update with idempotency" semantics are exactly what both callers (Activate and bind) need.
- **`KeyService.RotateKeyWithPassword` refuses to run without a SecretStore:** rather than skip re-encryption when no store is configured, the function returns `"rotate-key not configured: secret store missing"`. Skipping silently would resurrect Bug 9 in a less obvious form. Tests that exercise rotation must wire the store via `SetSecretStore`.
- **No backward-compat alias for `llm-provider`:** dropped `SecretTypeLLMProvider` entirely. All callers in this repo were updated; external integrators (none currently exist) would need to migrate. The DB migration (`000010`) handles existing rows.

---

## Blockers

None.

---

## Tests Run

| Command | Result |
|---------|--------|
| `go test -timeout 120s -count=1 ./pkg/secrets/...` | PASS |
| `go test -timeout 120s -count=1 ./api/internal/handlers/...` | PASS |
| `go test -timeout 120s -count=1 ./api/internal/services/auth/...` | PASS |
| `go test -timeout 120s -count=1 ./api/internal/services/workspace/...` | PASS |
| `go test -timeout 120s -count=1 ./api/internal/services/database/...` | PASS |
| `go test -timeout 120s -count=1 ./api/internal/app/...` | PASS |
| `go test -timeout 120s -count=1 ./controller/...` | PASS |
| `go test -timeout 120s -count=1 ./cmd/workspace-agentd/...` | PASS |
| `go test -timeout 240s -short ./...` | PASS (38 packages) |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `bash -n local/test-secrets.sh` | syntax OK |

`-race` could not be run to completion in this environment due to disk pressure on `/tmp` (98% full from race-instrumented build artefacts). All non-race tests pass; targeted race tests against the touched packages should be re-run before merging when disk space allows.

---

## Next Steps

1. **Live-cluster validation against the deployed image**, once the new image is built and rolled out:
   - run `local/test-secrets.sh http://localhost:18080` against the prod-namespace API port-forward
   - exercise `POST /api/v1/workspaces/:id/reload-secrets` against a real Active workspace and confirm 200 (the unit tests cover the wiring; only a live agent confirms end-to-end)
   - confirm `PUT /workspaces/:id/bindings` against a Create+Bind flow now delivers secrets to the running pod (the user's original complaint, Bug 3)
2. **DB migration deployment:** apply `000010_rename_llm_provider_to_api_key.up.sql`. Order: deploy the new image (which only writes `api-key`) first, run the migration second, so no row is left referencing a type the running code rejects.
3. **Pre-rotate-key live test:** confirm the Bug 9 fix on the cluster — pre-rotate baseline reveal, rotate, reveal again must return identical plaintext. Worklog 0085 T1 was the failing case; rerunning it should now pass.
4. **Cleanup of test artefacts** referenced in worklog 0085: `DELETE FROM users WHERE email LIKE '%@pentest.local'` and the leaked `c3c8766d-...` workspace. The Bug 12 fix will make the latter clean itself up on the next reconcile.
5. **Race tests when disk allows:** `go test -timeout 300s -race -short ./pkg/secrets/... ./api/internal/handlers/... ./api/internal/services/auth/... ./api/internal/app/...`
6. **golangci-lint:** the local install is built with go1.23 and refuses go1.25 modules; fix is to upgrade golangci-lint independently. Out of scope here; not blocking.

---

## Files Modified

### Code
- `api/internal/services/auth/auth.go`
- `api/internal/services/auth/auth_test.go`
- `api/internal/services/auth/auth_e2e_secrets_test.go`
- `api/internal/services/auth/auth_sessionid_test.go` (gofmt only)
- `api/internal/handlers/secrets.go`
- `api/internal/handlers/secrets_test.go`
- `api/internal/handlers/secrets_test_helpers_test.go`
- `api/internal/handlers/secrets_integration_test.go`
- `api/internal/handlers/secrets_extended_test.go`
- `api/internal/app/app.go`
- `api/internal/app/secrets_adapters.go`
- `api/internal/app/secrets_wiring_test.go`
- `api/internal/app/e2e_http_test.go`
- `api/internal/services/database/database.go`
- `api/internal/services/database/database_test.go`
- `api/internal/services/workspace/workspace_service.go`
- `pkg/types/types.go`
- `pkg/secrets/types.go`
- `pkg/secrets/store.go`
- `pkg/secrets/secret_service.go`
- `pkg/secrets/secret_service_test.go`
- `pkg/secrets/secret_service_extended_test.go`
- `pkg/secrets/key_service.go`
- `pkg/secrets/key_service_test.go`
- `pkg/secrets/key_rotation_test.go`
- `pkg/secrets/pg_secret_store.go`
- `pkg/secrets/pg_integration_test.go`
- `pkg/secrets/e2e_test.go`
- `pkg/secrets/injection_test.go`
- `pkg/secrets/integration_test.go`
- `pkg/secrets/redis_masterkey_e2e_test.go`
- `pkg/agentd/secrets/secrets.go`
- `pkg/agentd/secrets/secrets_test.go`
- `cmd/workspace-agentd/secrets.go`
- `cmd/workspace-agentd/secrets_test.go`
- `controller/internal/workspace/controller.go`
- `controller/internal/workspace/controller_test.go`
- `controller/internal/workspace/health_test.go`
- `frontend/src/api/secrets.ts`
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx`
- `frontend/src/components/settings/SecretsTab.tsx`
- `sdks/typescript/src/types.ts`

### New files
- `api/internal/app/secrets_podip_resolver_test.go`
- `api/migrations/000010_rename_llm_provider_to_api_key.up.sql`
- `api/migrations/000010_rename_llm_provider_to_api_key.down.sql`
- `local/test-secrets.sh`

### Migrations
- `api/migrations/000010_rename_llm_provider_to_api_key.up.sql` — `UPDATE user_secrets SET type='api-key' WHERE type='llm-provider'`
- `api/migrations/000010_rename_llm_provider_to_api_key.down.sql` — reverse

---

## Bugs Closed

| # | Severity | Title | Status |
|---|----------|-------|--------|
| 1 | High | reload-secrets API unconditionally 503 | Closed (wiring + 7 unit + 1 integration test) |
| 2 | High | Bind-time auto-push silently swallows error | Closed (logger surfaced + regression test) |
| 3 | Critical | Bound secrets only reach pod via Activate | Closed (manifest writer + HTTP push from SetBindings) |
| 5 | High | Register doesn't UnlockDEK | Closed (auth.go fix-closed flow + TDD) |
| 6 | Medium | `api-key` / `opencode-config` type names referenced in docs don't exist | Closed (rename + migration) |
| 7 | Low | Metadata field names undocumented | Closed (error message lists valid types + map exposed) |
| 9 | Critical | KEK rotation makes existing secrets undecryptable (data loss) | Closed (eager re-encryption in serializable tx) |
| 10 | High | Recovery key generated but never delivered | Closed (returned in register response) |
| 11 | Medium | `user_secret_bindings` rows survive workspace deletion | Closed (purge in MarkWorkspaceDeleted) |
| 12 | Medium | Workspaces stuck in Failed leak K8s Secrets | Closed (Failed-phase cleanup + connection-refused threshold restart) |
| 13 | Low | API doesn't validate adversarial mount_path | Closed (validateMountPath at API layer) |
| 14 | Critical UX | Frontend bindings UI has no path to deliver secrets | Closed (server-side delivery — no frontend change required) |
