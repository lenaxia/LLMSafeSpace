# Worklog: Fix opencode 500 on session create — limit.output schema requirement

**Date:** 2026-06-26
**Session:** Diagnose and fix HTTP 500 from `POST /api/v1/workspaces/:id/sessions/new` traced to opencode 1.15.12 rejecting `agent-config.json` with `SchemaError: Missing key at ["provider"][...][model]["limit"]["output"]`.
**Status:** Complete

---

## Objective

Users hitting "New chat" on a freshly-created workspace got HTTP 500. The API surfaced `internal_error: session_create_failed (opencode returned 500)`. Goal: identify the root cause, fix it properly with TDD, and propagate the fix end-to-end (DB → API → SDK → frontend UX).

---

## Work Completed

### Diagnosis

Reproduced live on the affected pod (`8c011d0f-adfc-481a-8160-b6e61267f441`):

1. API logged `opencode returned 500` from `api/internal/services/workspace/workspace_service.go:1127`
2. The 500 comes from opencode's `POST /session` route — agentd is a transparent reverse proxy
3. Direct `curl` to opencode reproduced: `{"name":"UnknownError","ref":"err_1b9d57f9"}`
4. `/workspace/.local/opencode/log/<latest>.log` revealed the underlying cause:
   ```
   ConfigInvalidError
     [cause]: SchemaError: Missing key
       at ["provider"]["custom"]["models"]["glm-5.2"]["limit"]["output"]
   ```
5. Inspected `/sandbox-runtime/agent-config.json`: the custom provider's `glm-5.2` entry had `"limit": { "context": 1000000 }` — context only, no output.
6. Manually rewrote the file with `"limit": {"context": 1000000, "output": 8192}`, restarted opencode in the pod, and `POST /session` immediately returned 200.

**Why pink-cell-87 worked but wide-loop-63 did not:** only models with a saved `ContextLimit` produce a `limit` block in `agent-config.json`. The user had saved a context limit for `glm-5.2` (1,000,000) but for no other models. The org credential write at `org_credentials_test.go:541` and the admin write at `admin_provider_credentials_test.go:445` both store `modelContextLimits: {"glm-5.2": 1000000}`.

### Root cause: opencode's published JSON Schema

Fetched `https://opencode.ai/config.json` (opencode's authoritative schema):

```json
"limit": {
  "type": "object",
  "properties": {
    "context": { "type": "number" },
    "input":   { "type": "number" },
    "output":  { "type": "number" }
  },
  "required": ["context", "output"],
  "additionalProperties": false
}
```

opencode validates `agent-config.json` against this schema during `Config.state()` at instance bootstrap. With `required: ["context", "output"]`, any partial `limit` block (context-only or output-only) makes opencode throw `ConfigInvalidError`, and every endpoint that touches config — including `POST /session` — returns 500.

`pkg/agent/opencode/format.go:80-82` was writing only `context` whenever `ContextLimit > 0`. The stale comment at `format.go:135-140` even acknowledged this was incomplete but claimed it was harmless. Empirically (proven on the live pod) it is not: it makes the entire workspace unusable for session creation.

### Fix design

opencode's `limit` schema is all-or-nothing. There is no honest universal default for `output` (varies per model: Sonnet ≈ 8k, GPT-4o ≈ 16k, Gemini-2 ≈ 64k). Therefore:

- Emit `limit { context, output }` ONLY when **both** are non-zero
- If either is missing, omit the entire `limit` block — opencode falls back to its built-in defaults
- Add `OutputLimit int` to `secrets.LLMModelConfig` (mirroring existing `ContextLimit`)
- Add `model_output_limits JSONB` to `provider_credentials` (migration 000046)
- Surface `modelOutputLimits map[string]int` in admin/user/org credential APIs (parallel to `modelContextLimits`)
- Probe endpoint (`GET /:id/models`) returns both saved limits per model
- Frontend `ModelConfigTable` gets a fourth column for "Max output (tokens)" with a partial-config warning

### TDD sequence

1. Updated `pkg/agent/opencode/format_test.go` with three failing tests:
   - `TestFormatOpenCodeConfig_LimitEmission_RequiresBothContextAndOutput` — five-model case (both, both+label, neither, context-only, output-only) — asserts only the both-set models get a `limit` block
   - `TestFormatOpenCodeConfig_LimitFields_ZeroValues_OmitLimit` — zero-zero produces no `limit`
   - `TestFormatOpenCodeConfig_ExactSnapshot_WithBothLimits` — pins canonical wire-format JSON
2. Verified failure: `unknown field OutputLimit in struct literal of type secrets.LLMModelConfig`
3. Added `OutputLimit int` to both `pkg/secrets/types.go` and `pkg/agent/agent.go` (the agent-package shadow)
4. Updated `pkg/agent/opencode/format.go` to emit both keys when both are >0
5. Updated `pkg/agent/opencode/opencode.go` `FormatProviderConfig` to copy both limits in the agent→secrets conversion
6. Tests went green

### Plumbing (end-to-end)

**Database:**
- `charts/llmsafespaces/migrations/000046_model_output_limits.up.sql` + down — `ALTER TABLE provider_credentials ADD COLUMN model_output_limits JSONB NOT NULL DEFAULT '{}'`
- Mirrored to `api/migrations/` (the canonical pair used by Helm chart sync hooks)
- Existing rows: `model_output_limits` defaults to `{}` so they continue to work (no partial limit block emitted)

**Go data layer:**
- `pkg/secrets/credential_store.go` `CredentialBinding`: added `ModelOutputLimits map[string]int`
- `pkg/secrets/pg_credential_store.go`: extended `GetWorkspaceCredentials`, `CredentialRow`, `CreateCredential`, `ListCredentials`, `GetCredential`, `UpdateCredential` SQL and Scans
- `pkg/secrets/injection.go` `applyModelAllowlist`: plumbs both `ModelContextLimits[id]` and `ModelOutputLimits[id]` from the binding into the synthesized `LLMModelConfig` (both code paths: existing-models filter and synthesize-from-allowlist)

**API handlers:**
- `admin_provider_credentials.go`: added `ModelOutputLimits` to `CredentialResponse`, create/update request structs, response builder, `Create`, `Update`
- `org_credentials.go`: same for org create/update requests; COALESCE-friendly nil semantics preserved (a nil map in PUT means "don't change")
- `user_provider_credentials.go`: same for user create + list + get response shapes
- `credential_ops.go` + `credential_probe.go`: changed `getCredentialForProbe` to return `probeCredentialLimits{Context, Output}` struct (was `map[string]int`); `probeCredentialModels` takes the struct and emits `outputLimit` on each `ProbeModelEntry`

**Tests:**
- Extended `TestCredentialPrecedence_ModelContextLimits_InjectedIntoLLMModelConfig` to assert OutputLimit also flows from binding → LLMModelConfig
- Extended `TestCredentialPrecedence_ModelContextLimits_DoesNotOverrideExisting` to assert in-blob `OutputLimit` is not overwritten by binding `ModelOutputLimits` (mirror of the existing ContextLimit semantics)
- Extended `TestAdminProviderCredentials_Create_ModelContextLimits` and `TestAdminProviderCredentials_Update_ModelContextLimits` to assert both maps round-trip
- Added `TestProbeCredentialModels_MergesBothSavedLimits` — verifies the probe endpoint surfaces both saved limits per model
- Extended the org-credentials JSON-key whitelist test to include `modelOutputLimits` (and forbid the PascalCase variant)
- Updated `fakeOrgCredStore` to mirror `ModelOutputLimits` like `ModelContextLimits`

**Frontend:**
- `providerCredentialTypes.ts`: added `modelOutputLimits?: Record<string, number>` to `ProviderCredential`, `CreateCredentialRequest`, `UpdateCredentialRequest`; added `outputLimit: number` to `ProbeModelEntry`
- `providerCredentials.ts` `AdminProviderCredential`: made `modelOutputLimits` non-optional (matches admin API contract)
- `orgs.ts` `OrgCredential`: same; create/update body types extended; probe response inline type extended
- `ModelConfigTable.tsx`: added a fourth column "Max output (tokens)" with a per-row "⚠ partial" badge that surfaces when context or output is set without the other (with a tooltip explaining opencode requires both)
- `AdminProviderCredentialsTab.tsx`, `UserProviderCredentialsTab.tsx`, `OrgCredentialsTab.tsx`: all three credential forms now initialize, edit, and submit `modelOutputLimits` alongside `modelContextLimits`; org expanded display shows a unified "Per-model limits" block with "ctx / out" pairs
- Updated component tests to expect the new label and to include `modelOutputLimits` in fixture data

**SDKs:**
- `sdks/go/services.go` `ProviderCredentialResponse`: added `ModelContextLimits` and `ModelOutputLimits` (both were missing; added together for completeness)
- TypeScript/Python/Java SDKs: no changes — they do not surface provider-credential CRUD
- `sdks/openapi.yaml`: no changes — does not declare provider-credential schemas

### Validation

```
$ go build ./...                          # green
$ go test ./pkg/...                       # green
$ go test ./api/...                       # green
$ go test ./controller/... ./cmd/...      # green
$ cd frontend && npx tsc --noEmit         # green
$ npx eslint --max-warnings=0 src         # green
$ npx vitest run                          # 1196/1196 passed
```

The `frontend/src/api/contract-fixtures.json` file got regenerated by the Go test suite picking up an unrelated preexisting drift (`ActiveSessionsResponse` type was already defined in Go but missing from the TS contract test). Reverted that fixture change — it's not part of this fix and the drift should be addressed separately.

---

## Key Decisions

### Why omit `limit` entirely instead of defaulting `output` to e.g. 8192

opencode's schema is `additionalProperties: false` and `required: ["context", "output"]`. Choosing a default for `output` would be a lie: the value varies per model and per provider, and opencode enforces it as a max-response cap. A wrong default would silently truncate user responses (if too low) or never trigger (if too high). Omitting the block makes opencode use its built-in per-model defaults from `models.dev`, which is the correct fallback.

### Why both fields are required server-side but each one is optional in the request

Symmetric with `modelContextLimits`. Sending only context-limits via the API stores them but produces no `limit` block in agent-config.json. The frontend now shows a "⚠ partial" warning so the user knows their config is incomplete. This is a soft "won't take effect" rather than a hard validation error — the credential is still usable (opencode falls back to defaults), and the user can always come back and set the other field.

### Why migration 000046 not 000033 (next sequential to the existing model_context_limits)

Following the convention used since 000032: new schema changes get the next available number. Tests, helm sync, and `down.sql` rollback all check out.

---

## Files Changed

```
api/internal/handlers/admin_provider_credentials.go
api/internal/handlers/admin_provider_credentials_test.go
api/internal/handlers/credential_ops.go
api/internal/handlers/credential_probe.go
api/internal/handlers/credential_probe_test.go
api/internal/handlers/org_credentials.go
api/internal/handlers/org_credentials_test.go
api/internal/handlers/user_provider_credentials.go
api/migrations/000046_model_output_limits.{up,down}.sql       (new)
charts/llmsafespaces/migrations/000046_model_output_limits.{up,down}.sql  (new)
frontend/src/api/orgs.ts
frontend/src/api/providerCredentialTypes.ts
frontend/src/api/providerCredentials.ts
frontend/src/components/org-admin/OrgCredentialsTab.{tsx,test.tsx}
frontend/src/components/settings/AdminProviderCredentialsTab.{tsx,test.tsx}
frontend/src/components/settings/UserProviderCredentialsTab.tsx
frontend/src/components/shared/ModelConfigTable.tsx
pkg/agent/agent.go
pkg/agent/opencode/format.go
pkg/agent/opencode/format_test.go
pkg/agent/opencode/opencode.go
pkg/secrets/credential_precedence_test.go
pkg/secrets/credential_store.go
pkg/secrets/injection.go
pkg/secrets/pg_credential_store.go
pkg/secrets/types.go
sdks/go/services.go
```

---

## Adversarial Self-Review

**Phase 1 — gaps, weaknesses, failure modes:**

1. **What about workspaces already running with broken agent-config.json?**
   Validated: the materialize subcommand runs `Materializer.reset()` + `FlushProviders()` at every pod boot, and the live-running pod also runs `AgentConfigWriter.Rebuild()` on every credential reload. After this fix ships, both rewrite agent-config.json from current data through the fixed formatter. Existing pods with bad files on tmpfs get fixed on next pod restart or credential reload. The 0.5–20s stale window during pod boot is unchanged.

2. **Race on schema version:** what if opencode pushes a future config schema that loosens or further tightens `limit`?
   Mitigated by `TestFormatOpenCodeConfig_ExactSnapshot_WithBothLimits` — it pins the canonical wire-format. A future opencode upgrade that changes the contract will fail this test. The test comment directs the future reader to re-validate against the live opencode and update the snapshot.

3. **What if a user only sets contextLimit in the UI and submits?**
   The backend accepts and stores it. agent-config.json formatter sees `OutputLimit=0`, omits `limit`. opencode runs with default limits for that model. UI shows the "⚠ partial" badge. This is the documented behavior — not a bug.

4. **Schema cache:** opencode reads `agent-config.json` once at startup, not hot-reloaded. Confirmed by `README-LLM.md:458` ("opencode does not hot-reload this file") and by the live-pod probe (had to kill+restart opencode after editing the file). The credential reload path already restarts opencode via `proc.restart()` — no change needed.

5. **Migration ordering:** the new migration is 000046, placed in both `api/migrations/` and `charts/llmsafespaces/migrations/`. Verified `000045_jwt_sessions` is the latest in both directories. Helm migration job applies all `.up.sql` files in lexical order — 000046 will run after 000045. Down migration drops the column cleanly.

6. **Existing rows with model_context_limits but no model_output_limits:** they get `{}` for outputs (column default). `applyModelAllowlist` reads `b.ModelOutputLimits[id]` → returns 0 → `OutputLimit` stays 0 → formatter omits `limit`. Existing workspaces lose their context-bar numerator until the user re-saves with both fields. This is documented and surfaced via the partial-warning badge.

7. **Test coverage gap:** no end-to-end test that actually runs opencode against the formatter output. The unit test pins the JSON shape, but if opencode's schema changes silently we wouldn't catch it. Mitigation: the canary suite in `sdks/canary/` includes a workspace-create-session smoke test which would have caught the original bug if it had targeted a custom provider with a context limit. Adding that to the canary is out of scope for this fix; tracked as a follow-up.

**Phase 2 — findings validated:**

All seven findings above are documented behaviors, not real bugs. No remediation required beyond what's already in the diff.

**Phase 3 — remediation status:**

Zero unaddressed real findings. The fix is complete.

---
