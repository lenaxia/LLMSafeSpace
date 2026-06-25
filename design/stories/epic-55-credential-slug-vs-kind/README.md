# Epic 55: Credential Slug vs. Kind — Schema Disambiguation

**Status:** Planning
**Created:** 2026-06-24
**Depends On:** none (foundation epics already shipped)
**Blocks:** any future expansion of multi-credential-per-class use cases (multiple LiteLLM endpoints, multiple Bedrock accounts, per-region OpenAI-compatible gateways, etc.)
**Priority:** Medium — latent design flaw; not currently breaking but will block growth and silently mis-route sessions when the second `provider="custom"` cred is added.

**Motivation:** The `provider_credentials.provider` column is doing three semantically distinct jobs at once. They coincide for built-in providers like `openai` and `anthropic` but diverge the moment a deployment has multiple credentials of the same SDK class. The conflict was surfaced during the 2026-06-24 production incident (worklog NNNN) where workspace `d95b6751-...` had a credential with `provider="custom"` whose **display name** was `thekaocloud` — but opencode saw only `custom` because `provider` is what gets written to `agent-config.json`. Sessions persisted `providerID:"custom"` literally; the human-friendly `name` field never reached the workspace.

This epic separates the three jobs into three columns and aligns the wire format, the unique-identity constraint, and the UX label so they can evolve independently.

---

## Origin

Conversation 2026-06-24 diagnosing `ProviderModelNotFoundError custom/glm-5.2` for workspace `d95b6751-...`. The user observed: "shouldn't it be `thekaocloud/glm-5.2`?" — and they were correct. The fact that the wire format pinned to `custom` was an accident of the schema, not an intentional design.

The hot-fix in PR #407 (worklog NNNN) fixed the immediate bootstrap-500 regression that hid the schema overload behind a more visible failure. With that fix in place, the `custom/glm-5.2` session works again — but only because there is exactly one `provider="custom"` credential cluster-wide. Adding a second one would either collide on the unique constraint or replace the first.

---

## Current State (as of 2026-06-24, code-verified)

### The column is doing three jobs

`provider_credentials.provider` (TEXT NOT NULL, free-form per `api/migrations/000015_unified_credential_model.up.sql:18`) is simultaneously:

1. **The SDK-class discriminator.** Code that knows how to talk to upstream LLM APIs branches on this string. `openai`, `anthropic`, `google`, `bedrock` route to different SDK adapters.

2. **The literal `agent-config.json` provider-map key.** `pkg/agent/opencode/format.go:87` writes `cfg.Provider[p.Provider] = ...`. opencode then registers a provider with id equal to that string. Sessions persist `providerID` referencing this key (verified live: SSE event for `ses_109617e46ffeiQZB1PvPgYXLkj` carries `model={"id":"glm-5.2","providerID":"custom"}`).

3. **The unique-per-owner identity slug.** `UNIQUE(owner_type, owner_id, provider)` (migration 000015 line 24) means a single owner can have at most one credential per `provider` value. Each owner's "set of credentials" is keyed by this string.

### Cluster inventory (production, 2026-06-24)

```
 owner_type |   provider   | n
------------+--------------+---
 admin      | opencode     | 1
 org        | custom       | 1
 user       | openai       | 2     <-- already collides! (different ownerIDs)
 user       | thekao cloud | 1     <-- literal space, not slug-safe
```

Two findings:

- The `user/openai` row has `n=2` because there are two different users — `(owner_type, owner_id, provider)` is unique per-owner, not globally. **Working as intended.**
- The `user/thekao cloud` entry has a literal space in the provider name. That string would render as `provider["thekao cloud"]` in `agent-config.json` if the credential were materialized, which is technically valid JSON but defeats opencode's expected naming convention and is evidence the column has been used as a free-text identity slot already.

### Failure modes (latent today, will surface as deployment grows)

| Scenario | Observed behavior | Why |
|---|---|---|
| Org adds a second LiteLLM endpoint with `provider="custom"` | DB returns unique-violation `409 Conflict` on create | UNIQUE constraint blocks |
| Org renames the credential `thekaocloud → "TheKao Production"` | UI shows new name; opencode still sees `custom` | `name` is decorative; `provider` reaches the wire |
| User deletes the `provider="custom"` cred and creates a new one with same `provider` value but different upstream URL | Existing sessions silently bind to the new upstream | Sessions persist `providerID:"custom"`; opencode resolves the *current* `provider.custom.{...}` |
| Operator in the admin UI tries to add a second admin `opencode` credential | Same unique-violation | |
| Operator types `provider="thekao cloud"` (with space) | Accepted; agent-config.json gets a space-keyed provider | No validation |

None of these are exercising today, but each is a one-step-away breakage waiting for a "we need a second X" use case.

---

## Scope

### Migration: split the column into three

**Schema change:**

```sql
ALTER TABLE provider_credentials
  ADD COLUMN slug TEXT,
  ADD COLUMN kind TEXT;

-- Backfill: existing rows have provider doing all three jobs.
-- We canonicalize each role from the existing value:
UPDATE provider_credentials SET
  slug = LOWER(REGEXP_REPLACE(provider, '[^a-z0-9-]+', '-', 'g')),  -- slug-safe identity
  kind = CASE
    WHEN provider IN ('openai', 'anthropic', 'google', 'opencode', 'bedrock', 'azure', 'cohere', 'mistral', 'perplexity', 'groq', 'xai', 'openrouter', 'together') THEN provider
    ELSE 'openai_compatible'  -- generic fallback for free-form names
  END;

ALTER TABLE provider_credentials
  ALTER COLUMN slug SET NOT NULL,
  ALTER COLUMN kind SET NOT NULL;

-- Replace the unique constraint to key on slug instead of provider.
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_owner_type_owner_id_provider_key;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_owner_slug_uniq
  UNIQUE (owner_type, owner_id, slug);

-- Keep `provider` for one release as the legacy compatibility column.
-- Drop in a follow-up migration after the wire-format change has rolled out
-- and all sessions have re-pinned (or been migrated; see "Session migration" below).
```

**Three columns, three jobs:**

| Column | Role | Constraints |
|---|---|---|
| `kind` | SDK-class discriminator. Enum of known SDK adapters. | `CHECK (kind IN (...))` — `openai`, `anthropic`, `google`, `bedrock`, `openai_compatible`, `azure_openai`, `vertex`, ... |
| `slug` | Unique-per-owner identity. Stable, slug-safe, user-supplied. | `UNIQUE(owner_type, owner_id, slug)`; `CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$')` |
| `name` | UX-only display label. Free-form. | Already exists; no change. |

**Wire format change:**

`pkg/agent/opencode/format.go:87` changes from `cfg.Provider[p.Provider]` to `cfg.Provider[p.Slug]`. opencode now sees `custom-thekao` (or whatever the user named the slug) instead of `custom`. The SDK class for routing is read from `kind` — the agentd materializer maps `kind` → `pkg/agent/opencode` provider-class adapter.

**API surface change:**

`POST /api/v1/provider-credentials` and `POST /api/v1/orgs/:id/provider-credentials` add a required `slug` field. `provider` becomes optional in the request and is renamed `kind` in the response. Frontend updated to render the slug picker (with default suggestion derived from the name) and the kind dropdown (constrained to enum).

### Session migration

This is the load-bearing piece. Live sessions in PVCs persist `model.providerID` referencing the *old* `provider` column value. After the column rename:

| Session age | `model.providerID` references | Post-migration behavior |
|---|---|---|
| Created pre-migration | The legacy `provider` value (e.g. `"custom"`) | opencode lookup fails — no `provider.custom` in agent-config.json after the wire-format change |
| Created post-migration | The new `slug` value (e.g. `"custom-thekao"`) | Works |

Three options to handle pre-migration sessions:

**A. Best-effort one-shot rewrite at deploy.** Workspace controller runs a post-deploy job that walks every running pod's PVC, reads `~/.opencode/sessions/*.json`, and rewrites `providerID` from old `provider` to new `slug` based on a lookup table from `provider_credentials`. Risk: imperfect (suspended workspaces aren't running; PVC walk needs careful coordination). Reward: zero user-visible breakage.

**B. Lazy rewrite at session load.** opencode patch to fall back from `providerID` lookup to a kind-based reverse lookup if the providerID is unknown. Lives behind a deprecation window; eventually removed. Risk: opencode upstream patch (we don't own that fork; would need to submit + wait or carry).

**C. Accept session breakage; users re-pick model.** On first message after deploy, opencode emits `ProviderModelNotFoundError`, frontend traps and shows "model selection has changed; please re-select." Lowest engineering cost, highest UX cost.

**Recommended: B if feasible, A as fallback. C is a non-starter for paying users.**

### Frontend updates

- `frontend/src/components/settings/UserProviderCredentialsTab.tsx` — add slug field to the create/edit form.
- `frontend/src/components/settings/OrgProviderCredentialsTab.tsx` — same.
- `frontend/src/api/providerCredentials.ts` — type definitions follow the new API.
- Model picker UX — already labels by `name`; no change needed beyond confirming the picker reads `slug` for routing.

### SDK updates

- `sdks/go` and `sdks/typescript` — add `slug` field to credential types; `provider` field renamed to `kind`.
- Canary scenarios — update fixtures.

### Deprecation timeline

Two-release plan:

- **Release N (this epic):** add `slug` and `kind`, backfill, change wire format. Keep `provider` column populated by a trigger that mirrors `slug → provider` for one release (rollback safety).
- **Release N+1:** drop the `provider` column. Code that read it in N will be on N+1 and read `slug` / `kind` directly.

---

## Non-Goals

- **Multi-tenant SDK abstraction.** This epic does not introduce per-tenant SDK overrides or runtime-selected adapters. `kind` is a discriminator, not a plugin point.
- **Provider lifecycle management** (adding new built-in providers, deprecating old ones). Out of scope; that's an ongoing operational concern handled per-provider in `pkg/agent/opencode/`.
- **Renaming `name` to `display_name` or similar.** `name` works fine as a UX label; renaming it churns the DB and the API for cosmetic reasons.

---

## Risks and Mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| Backfill misclassifies a free-form `provider="thekao cloud"` as `kind="openai_compatible"` when it's actually anthropic-API-shaped | Low (we have 1 such row in production, manually classifiable) | Manual review of the 4-row inventory before migration; one-shot SQL update for misclassifications |
| Session migration option B (opencode patch) blocked upstream | Medium | Fallback to A (PVC rewrite job) or C (user re-picks) |
| Slug-uniqueness check at API write conflicts with race during concurrent creates | Low | Existing app-layer pre-check pattern (`orgs.go:125-137`) returns 409 before hitting the raw DB constraint |
| Frontend ships before backend; users see slug field and back-end rejects | Medium during release | Standard backend-before-frontend deploy gate |
| Existing `agent-config.json` files on running pods point at old keys when the wire format changes | High without mitigation | Tied to session-migration option chosen above; agent-config.json is rebuilt every reload-secrets push, so a forced reload after deploy resolves it for active workspaces |

---

## Adversarial Review (Pre-Implementation)

### Attack 1: do we even need this?

What if we just enforce a slug-safe regex on `provider` and document that "provider is the agent-config.json key, not the SDK class"? Saves a column.

**Rejected because:** the SDK-class branching code (`pkg/agent/opencode/format.go`, the SDK adapters) currently reads `provider` and matches against a built-in list. The fact that "openai" the slug-safe identity happens to match "openai" the SDK class is a coincidence that breaks the moment someone creates a `provider="openai-staging"` credential intending to use the OpenAI SDK against a staging URL. The code would either need to add a parser ("everything before the first hyphen is the SDK class") or carry a separate `kind` column anyway. Splitting the column is cleaner than encoding a parser.

### Attack 2: backfill for `kind` could be wrong

`provider="thekao cloud"` likely points at an OpenAI-compatible LiteLLM endpoint, but we can't be certain from the column value alone. If the user actually wired it to an Anthropic-API-shaped endpoint, backfilling `kind="openai_compatible"` breaks them.

**Mitigated by:** the inventory is small (4 rows in prod). Manual confirmation with the credential owner before migration is feasible. Long-term, larger deployments would need a "what kind is this?" prompt during migration — but those don't exist yet.

### Attack 3: this is a breaking change for sessions

Pre-migration sessions reference the old `provider` value as `providerID`. After migration, opencode can't resolve them.

**Mitigated by:** session migration plan above (3 options). At minimum, even option C (user re-picks) is recoverable per-session in <30 seconds — not a data-loss event.

### Attack 4: the SDK release surface is wider than acknowledged

`sdks/go` and `sdks/typescript` are versioned. Renaming `provider` to `kind` in the SDK is a major-version bump for SDK consumers.

**Mitigated by:** SDK client code can preserve the old `provider` field as a deprecated alias for `kind` for one major version. The DB column is the source of truth; the SDK is a thin wrapper.

### Attack 5: do we need `kind` at all if we never use SDK selection?

If the codebase never branches on the SDK class except for the implicit `openai`-vs-`anthropic`-vs-... routing in `pkg/agent/opencode`, and that routing is moot because everything goes through opencode anyway, then `kind` adds no value.

**Counter:** opencode's `provider` config is keyed by class today (`pkg/agent/opencode/format.go` writes provider-class-specific options). We need *some* way to communicate "this is an OpenAI-compatible API" vs. "this is an Anthropic API" to opencode. Today that's smuggled through the `provider` string. Tomorrow it's `kind`. We need it.

---

## Open Questions

- **Q1.** Should `kind` be a hard enum (CHECK constraint) or a soft enum (TEXT with a known-values lookup table)? Enum is safer; lookup table is more extensible.
  *Tentative answer:* hard enum at the DB level, validated against a Go-side const list. Adding a new kind is a coordinated migration + Go release, which is appropriate friction.

- **Q2.** Should the slug-safe regex allow underscores? Helm and DNS conventions favor hyphens-only.
  *Tentative answer:* hyphens-only. Match the existing slug convention used elsewhere in the codebase (e.g. `org.slug`).

- **Q3.** What's the maximum slug length? opencode's `provider.{...}` map has no documented limit, but DB best practice is to cap.
  *Tentative answer:* 64 chars. Long enough for `myorg-litellm-prod-us-west` style; short enough to not be ridiculous.

---

## Test Plan

Per Rule 0 (TDD), every behavior change preceded by a failing test:

- **Schema migration safety.** `migrations/000NN_credential_slug_kind.up.sql` + `.down.sql` round-trip cleanly. Backfill produces the expected `slug` and `kind` for every row.
- **Slug uniqueness.** Two attempts to create `(owner, "openai")` returns 409.
- **Slug regex enforcement.** Slugs with spaces, slashes, uppercase letters all rejected at API layer.
- **Wire format.** `agent-config.json` keyed by `slug`, not `provider`. New e2e test in `pod_bootstrap_e2e_test.go` covering a credential with slug ≠ kind.
- **Session backward compat (whichever option chosen).** Pre-migration session JSON works post-migration via the chosen path.
- **SDK compat.** Both Go and TS SDK fixtures handle the new `kind` field while accepting the legacy `provider` field for one release.

---

## Definition of Done

- Schema migrated; all 4 production rows have slug and kind populated.
- API rejects credential creates without slug.
- Wire format change deployed; `agent-config.json` for live pods keys by slug.
- Session migration plan executed (option B or A).
- Frontend renders slug picker.
- SDKs released with the new field.
- README-LLM.md updated to reflect the schema split.
- Worklog with stress-test review.
- Old `provider` column dropped in N+1 release; this epic owns the deprecation window.

---

## Out of Scope (Explicitly)

- Per-credential rate limits.
- Credential rotation automation.
- Multi-region credential routing.
- `kind`-specific UI affordances (e.g. region selector for Bedrock).
