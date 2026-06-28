# NNNN_2026-06-27: Epic 55 frontend — slug picker + kind dropdown

## What landed (PR #432)

Aligns the credential UIs (admin, user, org) with the post-Epic-55 wire format. Each create-credential form now collects a **kind** (SDK class enum) and **slug** (per-owner identity) instead of a single conflated `provider` field. The slug appears in `agent-config.json` as the provider-map key — what opencode persists as `providerID` on session records. The kind selects which SDK adapter opencode loads.

## Shared types

`frontend/src/api/providerCredentialTypes.ts`:

- `ProviderCredential` + Create/Update request types swap `provider` for `kind` + `slug`.
- New exports for client-side validation:
  - `SDK_KINDS` — 15-value enum, set-identical to `pkg/secrets.ValidKinds` and the DB CHECK in `000001_initial_schema.up.sql` (order differs by design: TS order is UI-rendering order, with most common kinds first).
  - `SLUG_REGEX` — byte-identical mirror of the DB CHECK regex; used for early form-side feedback. Server also validates.
  - `slugFromName(name)` — name → slug conversion mirroring the SQL backfill expression. Powers the slug auto-suggest as the user types the name.

The matching cross-layer drift guard lives in `pkg/secrets/credential_identity_test.go` (Go) and `000001_initial_schema_test.sql` (DB). The TS side does not have an automated drift guard yet — comment notes say "keep in sync" but no test enforces it. PR-F (SDK updates) is the natural place to add a build-time generation step.

## Three credential tabs updated

`AdminProviderCredentialsTab`, `UserProviderCredentialsTab`, `OrgCredentialsTab`. Each tab now:

1. Renders cred row with **two badges**: slug (mono font, primary identity) and kind (uppercase, SDK class).
2. Replaces the free-form Provider text input with:
   - **SDK Kind** `<select>` populated from `SDK_KINDS` with empty default `— select SDK kind —` (forces explicit choice).
   - **Slug** `<input pattern=SLUG_REGEX.source>`, auto-suggested from name, editable, with help text explaining it's the agent-config.json provider-map key.
3. Validates required fields (name, kind, slug, apiKey) and slug format **client-side before the create request**, so DB CHECK violations no longer surface as opaque server errors.
4. Sends `kind+slug` to the API per the new request shape.

Auto-suggest clobber-protection: the slug field auto-populates from the name as the user types, but only while the slug matches the auto-suggested value. Once the user manually edits the slug, subsequent name changes won't overwrite it.

## Review-pass fixes

PR #432 first review (`875bd52e`) caught:

1. **Org tab kind default inconsistency** — `OrgCredentialsTab` defaulted `kind="openai"` while admin and user defaulted to `""`. A silent `openai` default could let a user accidentally create an `openai`-kind credential when they meant `openai_compatible` (LiteLLM/vLLM). Org tab now defaults to `""`, matching the other tabs.
2. **No unit tests for the new pure helpers.** Added `providerCredentialTypes.test.ts` with 12 tests covering `slugFromName` edge cases (lowercase, hyphen-collapse, leading/trailing strip, all-symbol → empty, 64-char truncation with trailing-hyphen cleanup, SQL-backfill-expression equivalence, property test that every non-empty output passes `SLUG_REGEX`), `SDK_KINDS` membership (legacy `'custom'` explicitly asserted absent), and `SLUG_REGEX` accept/reject coverage matching the DB CHECK shapes.
3. **No client-side slug-format-rejection coverage in any tab.** Added one test per tab asserting that submitting `kind=openai + slug="has space"` produces the `Slug must be 1–64 lowercase alphanumeric...` error and does NOT call `mockCreate`. Plus an `OrgCredentialsTab` test for the new empty-default `Kind and slug are required` path.

Second-pass review (`f43f6d12`) returned **APPROVE** with two non-blocking maintainer items, both addressed in this commit:

4. **Missing worklog** (this file).
5. **`Record<string, unknown>` retrofit at `OrgCredentialsTab.tsx:347`.** The update-credential call site was still typed as `Record<string, unknown>` even though `orgs.ts updateCredential` now has a structured shape (`kind?`/`slug?`/`name?`/...). Replaced with a precise inline type so TypeScript verifies field names/types at the boundary. Rule 1 compliance.

## What's NOT in this PR

- **PR-F (SDKs)**: Go + TS SDK type updates + canary fixtures. Tracked separately.
- **Auto-suggest preserve-manual-edit test**: the trickiest contract in the diff has no direct coverage. Lower priority; nice-to-have follow-up.
- **Automated drift guard for TS `SDK_KINDS` vs Go `ValidKinds`/DB CHECK**: the comment says "keep in sync" but no test enforces it. PR-F is the natural place to add a build-time generation step.

## Verification

- `npm run typecheck` clean.
- `npm run lint` clean.
- `npm test --run`: **1211 tests pass** (up from 1199 — 12 new helper tests + 4 new slug-validation tests across the three tabs).

## Refs

- design/stories/epic-55-credential-slug-vs-kind/README.md
- PR #430 — Epic 55 backend (merged).
- PR #431 — Free-tier plaintext follow-up (merged).
- PR #432 — this PR.
