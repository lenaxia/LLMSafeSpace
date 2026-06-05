# 0157 — Epic 30: Unified Credential Model — Design Phase

**Date:** 2026-06-05
**Session type:** Investigation + Design
**Status:** Complete — implementation starting next session

---

## Context

Investigated why workspace `72ae4451-0a89-473f-989d-88c7736e9f76` returned `{"models":[],"currentModel":""}` from the `/models` endpoint. This led to a full audit of the credential/secret pipeline and the creation of Epic 30.

---

## Root cause investigation

### What we found

1. **`models: []` root cause:** Every opencode provider starts `enabled: false`. `OpencodePlugin` sets `apiKey="public"` for the free-tier path but never sets `provider.enabled`. Without `OPENCODE_AUTH_CONTENT` or user credentials, `catalog.model.available()` returns `[]`.

2. **Immediate fix (deployed):** Commit `0112a05` adds `OPENCODE_AUTH_CONTENT = '{"opencode":{"type":"api","key":"public"}}'` to `pod_builder.go`. This feeds `AccountPlugin` and sets `provider.enabled`. Deployed as `ts-1780613743`.

3. **The two-system problem:** The investigation revealed the credential system is split:
   - **User `llm-provider` secrets** (`user_secrets` table) → full pipeline → opencode. Works end-to-end. No UI existed for users to create them.
   - **Admin `credential_sets`** (`credential_sets` table, `AdminCredentialsTab`) → dead end. Nothing downstream reads from it. Zero runtime effect.
   - **`OPENCODE_AUTH_CONTENT`** → hardcoded in `pod_builder.go`. Separate from both systems.

4. **No user LLM provider UI:** The `SecretsTab` handles generic secrets (`ssh-key`, `git-credential`, etc.) but never had an `llm-provider` option. Users had no way to add their own LLM credentials. The admin `AdminCredentialsTab` (for `credential_sets`) was the only UI, but it had zero effect.

---

## What we designed

### Epic 30: Unified Credential Model

Replaces the three-system split with:

- **Single `provider_credentials` table** (`owner_type`: `user`/`org`/`admin`)
- **`workspace_credential_bindings`** with `source_type` + `within_priority` two-key sort
- **`credential_auto_apply`** for admin/org auto-provisioning
- **`credential_backfill_jobs`** for async backfill tracking
- **`PrepareSecretsForInjection` rewrite** reads from the unified table, merges by priority, dispatches decryption by owner type
- **`EnsureFreeTierCredential`** + **`BackfillFreeTierBindings`** at API startup replace the hardcoded `OPENCODE_AUTH_CONTENT`
- **User LLM provider UI** (new settings tab)
- **Admin credential CRUD** replaces dead `AdminCredentialsTab`
- **`classifyAvailability`** replaces `classifyTier` hardcode; adds `platformOpencode` distinction for relay routing

Full design at `design/stories/epic-30-unified-credential-model/README.md`.

### Key architectural decisions

| Decision | Rationale |
|---|---|
| `SecretAndCredentialStore` combined interface (Option A) | Eliminates separate `credStore` field on `SecretService`; `store` satisfies both `SecretStore` and `CredentialStore` directly |
| `AdminKeyDeriver func(string) []byte` callback | Keeps `deriveServerKey` private to `api/internal/app`; no import cycle from `pkg/secrets` |
| `buildNonLLMSecrets` lazy `GetDEK` | Admin-only workspaces (no user secrets) don't require an active session |
| `reset()` attribution for relay wipe | `reset()` at materializer line 408 deletes `AgentConfigPath` on every `Materialize` call — NOT `FlushProviders`. Relay re-injection must happen after every `Materialize`, unconditionally |
| `platformOpencode bool` in `isZeroCostOpencode` | Users with their own opencode key must NOT have their zero-cost models routed through the platform relay |
| Startup seeding + automatic backfill | `EnsureFreeTierCredential` + `BackfillFreeTierBindings` called at API startup; no manual admin action post-cutover |

### Cutover order

```
Step 1: US-30.1  — Schema (new tables, drop credential_sets + pkg/credentials)
Step 2: US-30.2  — Admin credential CRUD API
Step 3: US-30.3  — Auto-apply + CreateWorkspace seeding hook
Step 4: US-30.11 — Redis model cache
Step 5: US-30.4  — Remove OPENCODE_AUTH_CONTENT; API startup seeding (GATE: verify ConfigProviderPlugin behavior first)
Step 6: US-30.5  — Rewrite PrepareSecretsForInjection
Step 7: US-30.9  — classifyAvailability
Step 8: US-30.6  — User LLM provider CRUD API
Step 9: US-30.10 — SetModel round-trip fix
```

US-30.5 MUST NOT deploy before US-30.4.

### Pre-flight checks before any deployment

1. Epic 27a deployed (`workspace_agent_state` exists)
2. `LLMSAFESPACE_MASTER_SECRET` set in K8s credentials Secret
3. `SELECT COUNT(*) FROM user_secrets WHERE type='llm-provider'` = 0

---

## Design review process

The epic went through 7 rounds of skeptical review (rounds 2–6 delegated to a Task agent). Issues found and fixed per round:

| Round | Issues found | Highest severity |
|---|---|---|
| 1 | 9 | `UNIQUE(COALESCE)` invalid SQL; admin API shape mismatch |
| 2 | 10 | SQL bug; `setModel` serial calls; wrong package for `CredentialStore` |
| 3 | 11 | Helm seeder non-existent; `deriveServerKey` import violation; `AsyncAuditLogger` typing |
| 4 | 10 | Migration binary doesn't exist; `OPENCODE_AUTH_CONTENT` cutover gap; relay wipe on reload |
| 5 | 9 | `credSvc` variable reuse; `pgStore` scope; down migration FK violation; `buildNonLLMSecrets` GetDEK |
| 6 | 18 | `relayBase` out of scope; `reset()` misattributed; `user_secrets.llm-provider` silent data loss; `credStore` field contradiction; `platformOpencode` not plumbed |
| 7+ | ~10 remaining documentation nits | Pseudocode compile errors, internal consistency |

**Final assessment:** Architecture is sound. Remaining issues are pseudocode detail that implementation will resolve naturally. Ready to implement.

---

## Known gaps / follow-ups

- **Key rotation for admin credentials:** When `LLMSAFESPACE_MASTER_SECRET` is rotated, all admin `provider_credentials` become undecryptable. Only the free-tier key is re-encrypted by `EnsureFreeTierCredential`. A `POST /admin/provider-credentials/:id/re-encrypt` endpoint is deferred to Epic 31+.
- **Silent credential substitution:** If a user's explicit key fails to decrypt, the next lower-priority credential (admin) is used silently. Only a server-side Warn log fires. User notification deferred to Epic 31+.
- **Org-level credentials:** `owner_type='org'` column exists in schema as extension point; `decryptBinding` returns error for this type. Implemented in Epic 29 (Organizations).
- **`ConfigProviderPlugin` vs `AccountPlugin` behavior:** The assumption that `apiKey:"public"` in `config.json` produces the same free-tier model catalog as `OPENCODE_AUTH_CONTENT` is **unverified**. Must be confirmed in a live test before US-30.4 ships.
