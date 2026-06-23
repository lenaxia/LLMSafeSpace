# Worklog: Password-reset secret hygiene + zero-knowledge copy

**Date:** 2026-06-23
**Session:** Make the user-facing "Zero Knowledge" security copy on the provider-keys settings page defensible by fixing the reset flow it describes.
**Status:** Complete.

---

## Objective

The "My LLM Provider Keys" settings page gained a zero-knowledge security explainer claiming that reset-by-email deletes the user's saved keys and that secrets are only decrypted for active workspaces. Verification against the code showed three gaps between the copy and reality: (1) reset silently 500'd for any user who already had keys, (2) reset did not delete or suspend anything, (3) relaunch could re-materialize stale plaintext. Close the gaps so the shipped copy is accurate.

---

## Work Completed

### Frontend copy (the ask)
- `UserProviderCredentialsTab.tsx`: removed the per-workspace bind/unbind UI (keys are auto-bound to all of a user's workspaces — an enforced backend invariant, so bind/unbind was redundant; unbind even returned 409 on the common auto-bound case). Added the zero-knowledge explainer block in layperson terms.

### Phase 0 — user_keys PRIMARY KEY blocker (prerequisite for everything)
- `InitializeUserKeys` did a plain `INSERT INTO user_keys` where `user_id` is the PK, so reset 500'd at `password_reset.go:249` for every user with existing keys — and because bcrypt is updated first (commits), the user landed in a state where they could log in but `UnlockDEK` failed forever against the stale wrap (permanent self-DoS of the secret store). Tests passed only because they used an in-memory fake that didn't mirror the constraint.
- Fix: `PgKeyStore.CreateUserKey` → `INSERT ... ON CONFLICT (user_id) DO UPDATE` (UPSERT). Flipped `TestKeyService_InitializeUserKeys_DuplicateUser` → `..._Reinit_Upserts` asserting reinit produces a fresh DEK; updated the in-memory mock to UPSERT semantics.

### Phase 1 — suspend-on-reset
- `workspace.Service.NeutralizeUserWorkspaces`: lists the user's workspaces, suspends Active ones (kills live pods → in-memory + `/sandbox-cfg` tmpfs keys die with the pod), scrubs every `workspace-secrets-*` K8s Secret (non-Active conflicts ignored; NotFound ignored).
- Wired into `Confirm` via a nilable `SetWorkspaceNeutralizer` setter (mirrors `SetSecretStore` precedent); type-asserted in `app.go` like the existing `sessionRevoker`.

### Phase 2 — delete-on-reset (makes "your saved keys will be deleted" literal)
- `database.Service.PurgeUserSecrets`: deletes `provider_credentials` (user-owned) + `user_secrets` (FK `ON DELETE CASCADE` clears bindings). Wired via `SetSecretPurger`. Best-effort (non-fatal): the DEK reinit already cryptographically erases the old ciphertext.
- After Phase 2 + Epic 35, relaunch yields no user secrets materialized (DB empty → fresh fetch returns nothing user-owned).

### Phase 2b — agentd wipe-on-empty: INVESTIGATED & REVERTED
- Attempted to make `runMaterializeCommand` wipe stale PVC plaintext when `secrets.json` is absent. Adversarial review (existing test `TestMaterializeSubcommand_MissingSecretsFile_AppliesWorkspaceConfig`) caught that `agent-config.json` legitimately merges the platform relay provider with user keys — a blanket `reset()` would nuke the relay config and break free-tier LLM access for zero-credential users. Reverted; the stale `agent-config.json` user-provider keys are a PVC-at-rest item, not a reset-flow item.

### Epic 35 doc — PVC-at-rest gap tracked, not assumed closed
- `design/stories/epic-35-secretless-credential-injection/README.md`: added "Known Gap: Materialized Plaintext on PVC" section. Epic 35 eliminates the etcd/K8s-Secret vector but preserves `materialize`, which writes plaintext to PVC-backed paths (`/tmp/agent-config.json`, `/home/sandbox/.secrets/*`, …). Documented the suspend-window exposure and the two fix options (redirect materialize output to tmpfs, or a suspend-time wipe) so the implementation carries the requirement rather than silently assuming goal #2 closed.

---

## Key Decisions

- **Setter injection over constructor args** for the new reset collaborators (`SetSecretPurger`, `SetWorkspaceNeutralizer`). Avoids churning 16 test constructor call sites; matches the `SetSecretStore`/`SetAPIKeyStore` precedent in `pkg/secrets`.
- **Best-effort cleanup semantics** for purge + neutralize (log + continue), mirroring the existing `RevokeAllUserSessions` step. The DEK reinit is the cryptographic guarantee; cleanup makes the copy literal and tightens runtime exposure but must not fail the reset.
- **Keep the `workspace-secrets-*` K8s scrub** even though Epic 35 will eventually make it a no-op. It is correct and necessary today (Epic 35 is Not Started) and degrades gracefully to a NotFound no-op once Epic 35 ships — forward-compatible, not tech debt.
- **Did NOT build a PVC-at-rest mitigation in this change.** Scoping it here would risk opencode's config-loading assumptions (option 1) or touch the critical agentd shutdown path (option 2). Tracked in the Epic 35 doc instead.

---

## Blockers

None.

---

## Tests Run

- Frontend: `npm run typecheck`, `npm run lint`, `npm run test` (12/12 in the tab; full suite 1251/1251 earlier in session).
- Go: `go build ./...`, `go vet` on affected packages, `go test` on `pkg/secrets`, `api/internal/handlers`, `api/internal/services/workspace`, `api/internal/services/database`, `cmd/workspace-agentd`, `pkg/agentd`, `api/internal/app` — all pass.
- New tests: UPSERT reinit, `NeutralizeUserWorkspaces` (suspend+scrub, no-op, non-Active conflict), `PurgeUserSecrets` (happy + DB error), reset handler purge/neutralize invocation + non-fatal failure.

---

## Next Steps

- Implement Epic 35, folding in the PVC-at-rest requirement now documented in its README (US-35.x: redirect materialize output to tmpfs, or add a suspend-time wipe) so goal #2 closes.
- Consider surfacing the recovery key in the frontend (`AuthProvider.register` currently discards `res.recoveryKey`), so the recovery-key flow is usable by web users — currently backend-only.
- Run the PG integration tests (`-tags=integration`) against a real DB to exercise the UPSERT path end-to-end (the unit tests use an in-memory mock).

---

## Files Modified

- `frontend/src/components/settings/UserProviderCredentialsTab.tsx`
- `frontend/src/components/settings/UserProviderCredentialsTab.test.tsx`
- `pkg/secrets/pg_key_store.go`
- `pkg/secrets/key_service_test.go`
- `api/internal/services/database/database.go`
- `api/internal/services/database/database_test.go`
- `api/internal/services/workspace/workspace_service.go`
- `api/internal/services/workspace/workspace_service_test.go`
- `api/internal/handlers/password_reset.go`
- `api/internal/handlers/password_reset_test.go`
- `api/internal/app/app.go`
- `design/stories/epic-35-secretless-credential-injection/README.md`
