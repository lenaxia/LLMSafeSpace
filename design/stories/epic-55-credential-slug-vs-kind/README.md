# Epic 55: Credential Slug vs. Kind — Schema Disambiguation

**Status:** Planning
**Created:** 2026-06-24
**Last Revision:** 2026-06-25 (review pass 4: added `ALTER COLUMN provider DROP NOT NULL` to the migration SQL block per recommended option A (N5 — was prose-only); extended the DAL enumeration to cover `ListCredentials`, `GetCredential`, `UpdateCredential` (N6 — projection/update-clause gaps); corrected the `HasUserProviderCredential` callsite citation (N7 — `ensureFreeTierCredential` does not invoke it; zero production callsites at HEAD); added two integration regression tests pinning the new read/update paths).

**Earlier Revisions:**
- 2026-06-25 (review pass 3: added "DAL code paths affected by the constraint swap" section enumerating the five queries in `pg_credential_store.go` that depend on the old unique constraint or the NOT NULL on `provider` (N3); recommended option A — drop NOT NULL on `provider` in the migration — to reconcile the schema with the API surface (N4); added two integration regression tests pinning both behaviors).
- 2026-06-25 (review pass 2: corrected slug regex inconsistency between SQL and constraints table (N1 — both now use `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`); fixed backfill CASE producing `kind='azure'` not in CHECK enum (N2 — now maps `provider='azure'` → `kind='azure_openai'` explicitly); added regression tests for both classes; added explicit "API and DB regex are byte-identical" property test).
- 2026-06-24 (review pass 1: corrected backfill SQL ordering bug, added missing CHECK constraints, fixed session path to honor `XDG_DATA_HOME=/workspace/.local`, dropped misleading trigger-based rollback story, corrected `orgs.go` line citation, added Q4 covering the `opencode-relay` slug-reservation collision, added test cases for adversarial backfill inputs and reserved-slug rejection, added explicit FK-relationship statement)
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
-- We canonicalize each role from the existing value.
--
-- Slug derivation MUST lowercase before stripping non-alphanumerics, then
-- trim leading/trailing hyphens, otherwise uppercase characters in the
-- original `provider` value would be replaced with `-` (the negated
-- character class [^a-z0-9-] matches them) and produce strings that
-- violate the CHECK regex below. BTRIM strips any leading/trailing
-- hyphens left after the squeeze.
UPDATE provider_credentials SET
  slug = BTRIM(REGEXP_REPLACE(LOWER(provider), '[^a-z0-9]+', '-', 'g'), '-'),  -- slug-safe identity
  kind = CASE
    -- Direct passthrough for kinds whose name is a single token that
    -- matches both the legacy `provider` value and the CHECK enum.
    WHEN provider IN ('openai', 'anthropic', 'google', 'opencode', 'bedrock', 'cohere', 'mistral', 'perplexity', 'groq', 'xai', 'openrouter', 'together') THEN provider
    -- Explicit remappings: legacy `provider` value differs from CHECK enum value.
    -- 'azure' was historically used for what is now 'azure_openai'.
    WHEN provider = 'azure' THEN 'azure_openai'
    -- 'vertex' has the same value in the CHECK enum but was never produced
    -- by the legacy provider column — listed here for symmetry so the CASE
    -- and the CHECK enum stay aligned. If a future row ships with
    -- provider='vertex' it maps cleanly.
    WHEN provider = 'vertex' THEN 'vertex'
    ELSE 'openai_compatible'  -- generic fallback for free-form names
  END;

-- Cluster inventory has 4 rows; the migration is preceded by a manual
-- review per the Risks table (the 'thekao cloud' free-form name might
-- be misclassified as openai_compatible when it's actually anthropic-
-- shaped). The post-backfill `slug IS NOT NULL` invariant holds for
-- every row in the current inventory.

ALTER TABLE provider_credentials
  ALTER COLUMN slug SET NOT NULL,
  ALTER COLUMN kind SET NOT NULL,
  ADD CONSTRAINT provider_credentials_slug_check
    CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'),
  ADD CONSTRAINT provider_credentials_kind_check
    CHECK (kind IN ('openai', 'anthropic', 'google', 'opencode', 'bedrock',
                    'azure_openai', 'vertex', 'cohere', 'mistral', 'perplexity',
                    'groq', 'xai', 'openrouter', 'together', 'openai_compatible'));

-- Replace the unique constraint to key on slug instead of provider.
ALTER TABLE provider_credentials DROP CONSTRAINT provider_credentials_owner_type_owner_id_provider_key;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_owner_slug_uniq
  UNIQUE (owner_type, owner_id, slug);

-- Drop NOT NULL on the legacy provider column so new INSERTs that omit
-- it (per the API surface change at line 167) do not fail. Existing
-- rows keep their snapshot value (the backfill UPDATE above ran against
-- rows whose provider was already non-NULL); new rows can omit it.
-- See the "DAL code paths affected" section for why this is required
-- in lockstep with the constraint swap (N4 from the review).
ALTER TABLE provider_credentials ALTER COLUMN provider DROP NOT NULL;

-- Keep `provider` (now nullable, frozen at its post-backfill value for
-- existing rows) for one release as the legacy compatibility column.
-- Drop the column entirely in Release N+1 after the wire-format change
-- has rolled out and all sessions have re-pinned (or been migrated;
-- see "Session migration" below).
```

Notes:

- The two CHECK constraints (`slug_check`, `kind_check`) are part of the **same migration**, not a deferred DDL step. They run after `SET NOT NULL` so the post-backfill invariants are enforced atomically.
- The slug regex permits 1–64 chars: the leading `[a-z0-9]` covers length 1; the optional `([a-z0-9-]{0,62}[a-z0-9])` group adds 1–63 more chars (with the trailing anchor disallowing a trailing hyphen). API-layer validation must use the same regex to keep the two layers in sync.
- `kind` enum is hard-enforced at the DB layer (Q1 resolution: hard enum). Adding a new kind requires a coordinated migration + Go const update.

**Foreign-key relationships are unaffected.** `model_allowlist` (TEXT[] column on the same table), `workspace_credential_bindings` (FK on `credential_id` UUID PK, migration 000015:36), and `credential_auto_apply` (same) all reference the credential by its UUID PK, not by `provider`. The split adds two columns and a new unique constraint without touching any FK.

**Three columns, three jobs:**

| Column | Role | Constraints |
|---|---|---|
| `kind` | SDK-class discriminator. Enum of known SDK adapters. | `CHECK (kind IN (...))` — `openai`, `anthropic`, `google`, `bedrock`, `openai_compatible`, `azure_openai`, `vertex`, ... |
| `slug` | Unique-per-owner identity. Stable, slug-safe, user-supplied. | `UNIQUE(owner_type, owner_id, slug)`; `CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$')` (1–64 chars; leading and trailing alphanumeric required; hyphens allowed internally) |
| `name` | UX-only display label. Free-form. | Already exists; no change. |

**Wire format change:**

`pkg/agent/opencode/format.go:87` changes from `cfg.Provider[p.Provider]` to `cfg.Provider[p.Slug]`. opencode now sees `custom-thekao` (or whatever the user named the slug) instead of `custom`. The SDK class for routing is read from `kind` — the agentd materializer maps `kind` → `pkg/agent/opencode` provider-class adapter.

**API surface change:**

`POST /api/v1/provider-credentials` and `POST /api/v1/orgs/:id/provider-credentials` add a required `slug` field. `provider` becomes optional in the request and is renamed `kind` in the response. Frontend updated to render the slug picker (with default suggestion derived from the name) and the kind dropdown (constrained to enum).

### DAL code paths affected by the constraint swap (N3 + N4)

The constraint swap from `UNIQUE(owner_type, owner_id, provider)` to `UNIQUE(owner_type, owner_id, slug)` and the optionality change to `provider` produce two classes of code-path defect that the migration alone does not catch. Both must land in lockstep with the migration, in the same release.

**N3 — `ON CONFLICT (owner_type, owner_id, provider)` arbiter inferences break.**

PostgreSQL infers the arbiter constraint from the `ON CONFLICT` column list. Once the unique index over `(owner_type, owner_id, provider)` no longer exists, every query that uses this arbiter raises `there is no unique or exclusion constraint matching the ON CONFLICT specification`. The bootstrap path is the most user-visible victim: API startup calls `ensureFreeTierCredential` (`api/internal/app/secrets_adapters.go:715`) → `UpsertFreeTierCredential` (`pkg/secrets/pg_credential_store.go:58–63`) → `ON CONFLICT (owner_type, owner_id, provider)`. Without the fix, the API does not start.

Affected DAL queries (verified against `pkg/secrets/pg_credential_store.go` at HEAD `e5f136a9`):

| File:line | Query | Required change |
|---|---|---|
| `pg_credential_store.go:58–63` | `UpsertFreeTierCredential` — `INSERT … ON CONFLICT (owner_type, owner_id, provider) DO UPDATE` | Change arbiter to `(owner_type, owner_id, slug)`; populate `slug` and `kind` columns in the INSERT list |
| `pg_credential_store.go:18–22` | `GetWorkspaceCredentials` — `SELECT pc.id, pc.owner_type, pc.owner_id, pc.provider, …` | Add `pc.slug, pc.kind` to the projection so callers receive the new identity/class fields |
| `pg_credential_store.go:167–174` | `BackfillFreeTierBindings` — `WHERE pc.owner_type = 'admin' AND pc.owner_id = '_platform' AND pc.provider = 'opencode'` | Switch the filter to `pc.slug = 'opencode'` (the post-migration equivalent identity), with the constant kept in lockstep with the slug-reservation list (Q4) |
| `pg_credential_store.go:182–195` | `HasUserProviderCredential(userID, provider)` | Reframe as `HasUserProviderCredential(userID, slug)` or add a parallel `HasUserProviderKind` helper. Verified at HEAD: zero production callsites — the method is on the interface (`credential_store.go:69`) and implemented but only test mocks invoke it. Verify during implementation before changing the signature. |
| `pg_credential_store.go:222–226` | `CreateCredential` — `INSERT … (owner_type, owner_id, name, provider, ciphertext)` | Add `slug, kind` to the INSERT column list so new rows populate them |
| `pg_credential_store.go:232–255` | `ListCredentials` — `SELECT id, ..., provider, ...` | Add `slug, kind` to the projection so callers building agent-config see the new identity/class fields |
| `pg_credential_store.go:260–276` | `GetCredential` — `SELECT id, ..., provider, ...` | Same projection gap; used by credential-detail handlers and credential-edit flows |
| `pg_credential_store.go:292–303` | `UpdateCredential` — `SET provider = COALESCE(NULLIF($5, ''), provider), ...` | Add `slug, kind` to the SET clause so credential updates can modify them |

These eight queries are exhaustive for `pg_credential_store.go`; `org_credential_store.go` uses no `ON CONFLICT` over `provider` and reads only via the views above. The `AsyncAuditLogger` wrapper (`pkg/secrets/pg_secret_store.go:678–711`) implements the same `CredentialStore` interface as a delegating production wrapper — a signature change to `HasUserProviderCredential` is compiler-enforced across both implementations. Tests in `pkg/secrets/credential_store_integration_test.go` and `pkg/secrets/pg_integration_test.go` exercise every path and must be updated in lockstep.

**N4 — `provider TEXT NOT NULL` versus the optional API surface.**

Migration `000015:18` declares `provider TEXT NOT NULL`. The deprecation timeline above keeps `provider` for one release without a trigger that auto-populates it, and the API surface change makes `provider` optional in the request. Three facts (column-NOT-NULL, no trigger, API-optional) are irreconcilable: a `CreateCredential` request that omits `provider` fails the NOT NULL constraint, returning a 500 with no user-facing guidance.

Three options to reconcile, ordered by cost:

A. **Drop the NOT NULL** in the same migration: `ALTER TABLE provider_credentials ALTER COLUMN provider DROP NOT NULL`. Existing rows keep their snapshot value (the `UPDATE` in the backfill ran against rows whose `provider` was already non-NULL); new rows can omit it. Rollback safety: pre-migration code that read `provider` will see NULL for new rows it never created — acceptable because pre-migration code never created NULL-provider rows. Recommended.

B. **Have the API write `kind` into `provider`** at INSERT time as a compatibility shim during Release N. Preserves NOT NULL at the cost of a small piece of "remember why we do this" logic. Worse than (A) because the shim must be removed in N+1, adding a second cleanup migration.

C. **Have the API write `slug` into `provider`** at INSERT time. Same shape as (B). Has the disadvantage that pre-migration `provider` values become non-comparable to post-migration ones (slug-shaped strings won't match the legacy SDK-class strings) — confuses any rollback or analytics that bridges the boundary.

**Recommended: (A).** Add `ALTER COLUMN provider DROP NOT NULL` to the migration; drop the column entirely in Release N+1.

### Session migration

This is the load-bearing piece. Live sessions in PVCs persist `model.providerID` referencing the *old* `provider` column value. After the column rename:

| Session age | `model.providerID` references | Post-migration behavior |
|---|---|---|
| Created pre-migration | The legacy `provider` value (e.g. `"custom"`) | opencode lookup fails — no `provider.custom` in agent-config.json after the wire-format change |
| Created post-migration | The new `slug` value (e.g. `"custom-thekao"`) | Works |

Three options to handle pre-migration sessions:

**A. Best-effort one-shot rewrite at deploy.** Workspace controller runs a post-deploy job that walks every PVC, reads `/workspace/.local/opencode/storage/session/**/*.json` (the path opencode writes when `XDG_DATA_HOME=/workspace/.local` is set — confirmed at `runtimes/base/tools/entrypoints/entrypoint-opencode.sh:14`, `controller/internal/workspace/pod_builder.go:579`, README-LLM.md:460), and rewrites `model.providerID` from the old `provider` value to the new `slug` based on a lookup table from `provider_credentials`. Suspended workspaces have PVCs but no running pod — the rewrite must run as a Job with the PVC mounted, NOT as a kubectl-exec into running pods. **Path verification required during S55-0 spike** before this option is selected: confirm the exact session-file glob and JSON shape opencode writes in the running production version (`opencode 1.15.12` per workspace status). Risk: imperfect (timing across many workspaces, PVC walk needs careful coordination). Reward: zero user-visible breakage.

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

- **Release N (this epic):** add `slug` and `kind`, backfill, change wire format.
  - The legacy `provider` column is **kept and frozen** at its post-backfill value via the migration's `UPDATE` step. We do **not** install a trigger that mirrors `slug` or `kind` back into `provider` — neither would preserve the original semantics. Mirroring `slug` discards the SDK-class meaning; mirroring `kind` discards the identity meaning. The honest path is to leave `provider` as a snapshot of "what this row used to be" and let any rollback read it directly.
  - Rollback semantics: if Release N must be rolled back to N-1 (which only knows `provider`), the snapshot value still maps the row to its pre-migration SDK class for the row's lifetime. New rows created in Release N would be missing from this snapshot, so a rollback is feasible but lossy for any newly-created credentials. This trade-off is documented in the migration's `down.sql`.
- **Release N+1:** drop the `provider` column. Code that read it in N will be on N+1 and read `slug` / `kind` directly. Rollback from N+1 to N is no longer feasible — by N+1 the new wire format has been live for a release and existing sessions reference it.

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
| Slug-uniqueness check at API write conflicts with race during concurrent creates | Low | Existing app-layer pre-check pattern (`orgs.go:146-158`, the "Pre-check for a clear 409" block) returns 409 before hitting the raw DB constraint |
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

- **Q3.** Slug length bounds: minimum and maximum.
  *Tentative answer:* 1–64 chars. Max 64 is long enough for `myorg-litellm-prod-us-west` style; short enough to not be ridiculous. Min 1 because rejecting single-char slugs adds no value. The CHECK regex `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$` enforces both bounds at the DB. The API regex MUST be identical so a slug accepted by the validator is accepted by the DB.

- **Q4.** Slug collision with the built-in `opencode-relay` provider key.
  The relay injector (`cmd/workspace-agentd/relay_injector.go`) hardcodes `opencode-relay` as a provider key in `agent-config.json` to advertise free-tier models. Once user-supplied slugs reach the wire format, a user picking slug `opencode-relay` would either silently shadow the relay or fail registration depending on which writer runs last.
  *Tentative answer:* reserve a small list of system slug values (`opencode-relay`, `opencode`) and reject them at the API layer. The reservation list lives next to the relay injector code so the two stay in lockstep. A future epic might generalize this to a `system_reserved_slugs` table; for now, a Go const slice is appropriate friction.

---

## Test Plan

Per Rule 0 (TDD), every behavior change preceded by a failing test:

- **Schema migration safety.** `migrations/000NN_credential_slug_kind.up.sql` + `.down.sql` round-trip cleanly. Backfill produces the expected `slug` and `kind` for every row.
- **Slug uniqueness.** Two attempts to create `(owner, "openai")` returns 409.
- **Slug regex enforcement.** Slugs with spaces, slashes, uppercase letters all rejected at API layer.
- **Slug regex matches DB CHECK.** Property test: every value the API regex accepts also passes the DB CHECK; every value the API rejects also fails the CHECK. Closes the layer-disagreement risk for slug min/max.
- **Backfill robustness against adversarial inputs.** Migration test fixture seeds rows with `provider` values `'OpenAI'`, `' my cred '`, `'--foo'`, `'X'`, `'thekao cloud'` — all must produce CHECK-valid slugs after backfill (this is the C1 regression test the original SQL would have failed).
- **Backfill produces CHECK-valid `kind` for every legacy `provider` value.** Property test: every value the backfill CASE can output passes the kind CHECK constraint. Catches the N2-class defect where the CASE produces a value the CHECK rejects (e.g. legacy `'azure'` mapping to `kind='azure'` would fail because the enum has `'azure_openai'`).
- **API and DB regex are byte-identical.** A code-level test asserts the API-layer slug regex string is equal to the DB CHECK regex string (both stored in a single Go const, used by both the validator and the migration generator). Catches N1-class drift where two regexes "look similar but disagree on edge cases" (1-char vs 2-char minimum).
- **`UpsertFreeTierCredential` succeeds after the constraint swap (N3).** Integration test: run the migration, then call `UpsertFreeTierCredential` twice in succession — second call must hit the new arbiter `(owner_type, owner_id, slug)` and produce a clean upsert, not raise `there is no unique or exclusion constraint matching the ON CONFLICT specification`. This is the bootstrap path; without this, the API does not start after the migration deploys.
- **`CreateCredential` succeeds when `provider` is omitted (N4).** Integration test: post a credential with `slug` and `kind` only, no `provider` field. The DAL INSERT must succeed (provider column either DROP NOT NULL'd per recommended option A, or compatibility-shimmed per option B/C — whichever is chosen, this test pins the behavior).
- **`ListCredentials` and `GetCredential` return `slug` and `kind` after the migration (N6).** Integration test: insert a credential with slug=`x` and kind=`openai`; call `ListCredentials` and `GetCredential`; assert both fields appear in the returned `CredentialRow`. Without this, the wire-format change at `format.go:87` would silently use a stale projection for credentials read via these paths — the same class of silent mis-routing this epic was created to prevent.
- **`UpdateCredential` can modify `slug` and `kind` (N6).** Integration test: insert a credential, call `UpdateCredential` to change slug or kind, assert the update is persisted. Catches the SET-clause gap that would leave the columns frozen.
- **Reserved slug rejection.** API write rejects `slug='opencode-relay'` and `slug='opencode'` at the validator layer (Q4). A property test confirms the reservation list matches the system providers the relay injector emits.
- **Wire format.** `agent-config.json` keyed by `slug`, not `provider`. New e2e test in `pod_bootstrap_e2e_test.go` covering a credential with slug ≠ kind.
- **Wire-format slug-collision invariant.** A slug containing characters that are valid JSON object keys but collide with opencode's model-ID namespacing (e.g. slug containing `/`) — the regex forbids it, but an assertion test locks the invariant in case a future regex relaxation accidentally allows it.
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
