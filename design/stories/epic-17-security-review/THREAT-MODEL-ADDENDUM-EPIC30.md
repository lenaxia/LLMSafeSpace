# Threat Model Addendum — Epic 30: Unified Credential Model

**Date:** 2026-06-05
**Scope:** New attack surface introduced by migration 000015 and the Epic 30 implementation
**Supersedes:** Nothing — this supplements THREAT-MODEL.md v1.4
**Status:** Active

---

## 1. What Changed in Epic 30

Epic 30 replaced three disconnected credential systems with a single unified pipeline:

| Component | Before | After |
|-----------|--------|-------|
| Admin credentials | `credential_sets` table (dead wiring — no runtime effect) | `provider_credentials` + `credential_auto_apply` tables (active, injected into every workspace) |
| User credentials | `user_secrets` with `type='llm-provider'` | `provider_credentials` with `owner_type='user'`, encrypted with user DEK |
| Free-tier key | Hardcoded in `controller/internal/workspace/pod_builder.go` | Real admin credential seeded at API startup (`EnsureFreeTierCredential`) |
| Injection pipeline | Single-source (`user_secret_bindings` only) | Multi-source merge: user explicit > user auto > admin explicit > admin auto; priority dedup by provider |
| Pod credential env var | `OPENCODE_AUTH_CONTENT` hardcoded string | Not present; credentials come from materializer init container via `workspace-secrets-<id>` Secret |

---

## 2. New Attack Surface

### 2.1 Server-Side Master Key (Admin KEK)

**Description:** Admin provider credentials are encrypted at rest with a server-held master key (`LLMSAFESPACE_MASTER_SECRET`). The master secret is present in the API server process memory and as a Kubernetes Secret or environment variable.

**Blast radius:** Compromise of the master secret decrypts ALL admin provider credentials simultaneously. This is a higher-value target than the pre-Epic-30 state (previously admin credentials had no runtime effect and were not worth stealing).

**Threats:**
- T-E30-1: Exfiltration of `LLMSAFESPACE_MASTER_SECRET` from environment via API server memory exposure (e.g., debug endpoint, SSRF to `169.254.169.254`)
- T-E30-2: Exfiltration of `LLMSAFESPACE_MASTER_SECRET` from Kubernetes Secret via RBAC misconfiguration
- T-E30-3: Timing oracle on admin credential encryption/decryption (`crypto/aes-256-gcm` — constant time; low risk but document)

**Existing mitigations:** AES-256-GCM encryption; master secret not logged; API server runs with `readOnlyRootFilesystem`; K8s Secret access requires `secrets/get` RBAC.

**Gaps:** No master secret rotation procedure exists. No detection for unexpected reads of the K8s Secret containing master key. Post-Epic-30 pentest should probe T-E30-1 and T-E30-2 explicitly.

---

### 2.2 `credential_auto_apply` — Automatic Injection Without Explicit User Action

**Description:** Admin can configure `credential_auto_apply` rules targeting `all`, specific users, or specific workspaces. When `SeedWorkspaceCredentials` runs at workspace creation (or during `BackfillFreeTierBindings` at startup), credentials are injected into `workspace_credential_bindings` without any user action.

**Threats:**
- T-E30-4: Admin adds a credential with a compromised API key and sets `auto_apply → all`. All future workspaces silently receive the poisoned key. Users have no visibility that an admin credential is being used.
- T-E30-5: IDOR in `SeedWorkspaceCredentials` — if the workspace owner check is bypassed, an attacker with admin access could bind credentials to workspaces they don't own.
- T-E30-6: `auto_apply` rule with `target_type='user'` / `target_id=victim_user_id` allows a rogue admin to inject a specific user's workspaces with a monitoring credential (exfiltrates user prompts to attacker-controlled provider endpoint via `base_url`).

**Existing mitigations:** `credential_auto_apply` is admin-only (requires `AdminGuard` middleware). Priority merge — user-explicit bindings override admin auto-apply. Audit log on binding creation.

**Gaps:** T-E30-6 has no user-facing notification that an admin credential is active on their workspace. Users cannot currently see which provider credentials are injected — this information is not exposed by the API. **Recommended:** Add `GET /workspaces/:id/provider-credentials` endpoint returning provider names (not key values) so users can audit what's active.

---

### 2.3 `EnsureFreeTierCredential` — Startup Database Write

**Description:** At every API server startup, `EnsureFreeTierCredential` upserts a free-tier `opencode` credential and runs `BackfillFreeTierBindings` which inserts `workspace_credential_bindings` rows for all workspaces that don't yet have an opencode binding.

**Threats:**
- T-E30-7: Startup-time DB write modifies existing workspaces. If the free-tier credential API key is compromised (e.g., opencode.ai is breached), every workspace is automatically affected on next API restart.
- T-E30-8: If `LLMSAFESPACE_MASTER_SECRET` changes between deployments, `BackfillFreeTierBindings` will silently fail for workspaces that have bindings encrypted with the old key. Recovery path is undefined.
- T-E30-9: If an attacker can trigger an API restart (e.g., via OOM, crash loop), they can observe `BackfillFreeTierBindings` activity in audit logs to enumerate workspace IDs across all users.

**Existing mitigations:** `BackfillFreeTierBindings` uses `ON CONFLICT DO NOTHING` — idempotent. Master secret absent → startup skips seeding with a warning (non-fatal). Free-tier key uses `apiKey: "public"` — no private credential.

**Gaps:** T-E30-9 — workspace IDs are enumerable via timing of startup backfill (low severity; workspace IDs are UUIDs and already returned by authenticated API). T-E30-8 — no documentation for master secret rotation procedure.

---

### 2.4 User Provider Credential DEK Dependency

**Description:** User provider credentials are encrypted with the user's DEK (Data Encryption Key) cached in Redis from their active session. If the user has no active session, the DEK is unavailable and injection falls back to admin credentials only.

**Threats:**
- T-E30-10: User credential injection silently fails if DEK is unavailable (expired session, Redis failure). The workspace starts with admin credentials only and the user's paid-model access disappears without error surfacing to the user or any audit log entry.
- T-E30-11: Redis DEK cache eviction (TTL, eviction policy, flushdb) causes workspace injection to silently downgrade from user credentials to free-tier for all active users simultaneously.

**Existing mitigations:** `PrepareSecretsForInjection` falls through gracefully — admin credentials still inject. No crash on DEK unavailability.

**Gaps:** T-E30-10 produces no audit log entry and no user-facing signal. **Recommended:** When DEK is unavailable and user credentials exist, log a `credential_injection_downgrade` audit event. Return `WorkspaceConditionCredentialsAvailable=Degraded` (Epic 08 US-8.5) rather than missing.

---

### 2.5 `provider_credentials` Table — Cross-Tenant Isolation

**Description:** All provider credentials (admin and user) live in a single `provider_credentials` table with an `owner_type`/`owner_id` discriminator. Isolation relies entirely on application-level WHERE clauses.

**Threats:**
- T-E30-12: SQL injection in credential CRUD handlers bypasses owner filter and reads another user's credential rows. Current handlers use parameterized queries (low risk).
- T-E30-13: Mass assignment — if credential update handler accepts unchecked JSON fields, an attacker could modify `owner_id` or `owner_type` on their own credential to impersonate admin context.
- T-E30-14: `PrepareSecretsForInjection` queries `workspace_credential_bindings JOIN provider_credentials`. If binding table is writable by a non-admin user via a logic bug, they could inject arbitrary credentials into another user's workspace.

**Existing mitigations:** Parameterized queries throughout. `user_provider_credentials.go` has explicit ownership verification on Bind/Unbind. Admin endpoints behind `AdminGuard`.

**Gaps:** No row-level security (RLS) in PostgreSQL — all isolation is application-level. Post-Epic-30 pentest should include a dedicated horizontal privilege escalation test for T-E30-13 and T-E30-14.

---

## 3. Impact on Existing Threat Model

### 3.1 Section updated: Credential Storage (previously Section 4.3)

Previous threat model described `user_secrets` as the credential store. This is now superseded:
- `user_secrets` still exists for non-LLM secrets (SSH keys, env vars, etc.)
- LLM provider credentials now live in `provider_credentials`
- The `pkg/credentials` package is deleted — all references in the threat model to it are stale

### 3.2 Section updated: Controller Credential Injection (previously Section 5.2)

`OPENCODE_AUTH_CONTENT` env var on workspace pods no longer exists. The controller's `pod_builder.go` no longer contains the free-tier key. Credential injection happens exclusively via:
1. `workspace-secrets-<id>` Kubernetes Secret (written by API materializer)
2. Mounted at `/workspace-cfg/` in the init container
3. Read by `workspace-agentd` at boot

The threat surface for credential exfiltration from controller memory is reduced. The new surface is the `workspace-secrets-<id>` K8s Secret (scope: single workspace; existing RBAC mitigations apply).

### 3.3 No change: User prompt exfiltration via provider MITM

The existing threat (T-5.3 in main threat model) — that a compromised provider `base_url` could exfiltrate user prompts — is **unchanged in kind but increased in blast radius**. Previously this required compromising a user's own secrets; now it can be accomplished by an attacker who controls an admin credential with `auto_apply → all` and a malicious `base_url`. See T-E30-6 above.

---

## 4. Recommended Pentest Additions

The following test cases should be added to the next pentest cycle (phases 2-7 postfix):

| ID | Test | Priority |
|----|------|----------|
| PT-E30-1 | Attempt to read `LLMSAFESPACE_MASTER_SECRET` via SSRF to metadata endpoint | High |
| PT-E30-2 | Attempt to read master K8s Secret via direct API call with service account token | High |
| PT-E30-3 | Attempt to modify `owner_type` or `owner_id` via PATCH on own user credential | High |
| PT-E30-4 | Attempt to bind another user's credential to own workspace | High |
| PT-E30-5 | Verify `auto_apply` rules require admin role; attempt creation as non-admin | Medium |
| PT-E30-6 | Create admin credential with `base_url` pointing to attacker server; verify prompts reach it | Medium |
| PT-E30-7 | Enumerate workspace IDs via `BackfillFreeTierBindings` startup audit timing | Low |
| PT-E30-8 | Verify DEK unavailability produces audit log and user-visible signal | Low |

---

## 5. Open Issues Requiring Follow-Up

| Issue | Owner | Priority |
|-------|-------|----------|
| No `GET /workspaces/:id/provider-credentials` transparency endpoint | API team | Medium |
| No master secret rotation procedure documented | Ops | Medium |
| `credential_injection_downgrade` audit event not emitted | API team | Medium |
| Post-Epic-30 pentest (PT-E30-1 through PT-E30-8) not yet scheduled | Security | High |
| Threat model addendum not reviewed by second engineer | Security | High |
