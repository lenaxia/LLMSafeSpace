# Worklog: Org Creation Slug Validation + Better 400 Errors

**Date:** 2026-06-26
**Session:** Diagnose and fix the 400 Bad Request returned by `POST /api/v1/orgs` for every user-friendly org slug; relax slug validation; surface per-field validation details so future failures are self-explanatory.
**Status:** Complete

---

## Objective

Two user-facing requests:

1. **Bug:** Creating an organisation in the chat UI returned `400 Bad Request` with the opaque body `{"error":"invalid request body"}` for any multi-word org name. The user discovered this came down to "org name can't have spaces" — but actually the org *name* accepts spaces; the *slug* derived from the name was what the server rejected.
2. **Architectural question:** "Should we create a centralised validation library so frontend and backend are always in sync?" — answered with a recommendation (yes, via OpenAPI codegen) but deferred to a follow-up story; this session ships only the minimal fix.

---

## Work Completed

### 1. Root cause

`pkg/types/orgs.go` declared the slug field as:

```go
Slug string `json:"slug" binding:"required,min=2,max=50,alphanum"`
```

Gin's `alphanum` tag (from go-playground/validator/v10) rejects anything outside `[A-Za-z0-9]` — so hyphens, underscores, and spaces all fail.

The frontend's `slugify()` (`frontend/src/components/settings/OrgSettingsTab.tsx:24`) produces hyphenated lowercase slugs from human-readable names (`"My Org"` → `"my-org"`), so every multi-word name was guaranteed to fail server-side validation. The opaque 400 body gave no hint as to which field was wrong.

Confirmation via a standalone validator test (see "Tests Run") proved hyphenated slugs are rejected. Pods in the cluster were too recent to contain the user's specific request_id but the 32-byte response body matched `{"error":"invalid request body"}` exactly, locking in the diagnosis.

### 2. Backend fix — slug validator

Added `pkg/types/validators.go` with a custom `slug` validator registered on Gin's binding engine in `init()`:

```go
var slugPattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)
```

Pattern accepts letters (case-insensitive), digits, and single hyphens between segments. Rejects leading/trailing/consecutive hyphens, underscores, dots, slashes, punctuation, and non-ASCII.

Mixed case is *accepted by the validator* and *lowercased by the handler* before persistence (the existing `strings.ToLower(req.Slug)` call in `Create`). This was a user-chosen design decision — see Key Decisions §2.

`CreateOrgRequest.Slug` and `UpdateOrgRequest.Slug` binding tags updated from `alphanum` → `slug`.

**Critical gotcha discovered during testing:** Gin's `defaultValidator.lazyinit()` calls `SetTagName("binding")`, so the binding engine reads the `binding:` struct tag, **not** the validator package's default `validate:` tag. An earlier test using `validate:"slug"` silently passed every input because the tag was unrecognised. The final tests use `binding:"slug"` and the fix is verified end-to-end.

### 3. Backend fix — per-field validation errors

Added `api/internal/handlers/binding_errors.go` with `bindingErrorResponse(err, model)`:

- For `validator.ValidationErrors`: returns `{"error":"validation failed", "details":{"<jsonField>":"<message>"}}` keyed by the struct's JSON tag name (so frontend knows which form input to highlight).
- For `json.SyntaxError` / `json.UnmarshalTypeError`: returns the generic `{"error":"invalid request body"}` (no field to attribute).
- Defensive: never returns nil.

Wired into four org handlers: `Create`, `Update`, `AddMember`, `ChangeMemberRole`. All four previously emitted the opaque `"invalid request body"` for every binding failure.

### 4. Frontend fix — surface details

Extended `ApiError` (`frontend/src/api/types.ts`) with an optional `details?: Record<string, string>` field.

`OrgSettingsTab.tsx` `handleSubmit` now extracts the first field from `e.body.details` on 400 responses and renders it with a friendly label (`"Slug: Must be letters, digits, and single hyphens..."` instead of `"validation failed"`).

### 5. Pre-existing contract-test drift fixed

Running `go test ./pkg/types/...` regenerated `frontend/src/api/contract-fixtures.json`, exposing pre-existing drift: someone added `ActiveSessionsResponse` to the Go contract generator (`pkg/types/contract_test.go:47`) but never updated the TS side. The fixture file on main was therefore stale.

Fixed by:
- Adding `ActiveSessionsResponse` to `frontend/src/api/types.ts`
- Adding a contract assertion in `frontend/src/api/contract.test.ts`
- Including `ActiveSessionsResponse` in the `testedKeys` list

### 6. Test coverage added

**Go:**
- `pkg/types/validators_test.go` — registration check, 8 accepted shapes, 13 rejected shapes, empty-string semantics.
- `api/internal/handlers/binding_errors_test.go` — 5 field-level cases, malformed JSON fallback, nil-input fallback, unknown-error-type fallback.
- `api/internal/handlers/orgs_test.go` — 5 new `TestOrgsHandler_Create_*` tests covering the hyphenated-slug happy path, parameterised slug validation table (15 cases), per-field details for slug + email, malformed JSON, and the non-platform-admin 403 path.

**TS:**
- `frontend/src/components/settings/OrgSettingsTab.test.tsx` — new test asserting the per-field details are rendered with a friendly label.

---

## Key Decisions

1. **Allow hyphens in slugs (not strip them)** — chosen by the user. Standard URL-friendly convention (GitHub, Slack). Backwards-compatible with the frontend's existing `slugify()`.

2. **Accept mixed-case slugs, lowercase server-side** — chosen by the user. Slightly more lenient than the strict "reject uppercase" alternative; preserves the existing `TestCreateOrg_Admin_SlugLowercased` test's behavior, which sends `"AcMeCo"` and expects `"acmeco"` to land in the DB.

3. **Per-field error response shape** — `{"error": "<top-level>", "details": {"<jsonField>": "<message>"}}`. Chose this over the alternative used by `api/internal/middleware/validation.go` (`{"error": {"code": ..., "message": ..., "details": ...}}`) because the latter wraps `error` as an object, which breaks `ApiClientError`'s constructor (`super(body.error)` expects a string). The simpler shape is forward-compatible with the validation middleware path — both produce a `details` map with the same key shape.

4. **Don't ship a centralised validation library in this PR** — the user asked whether to build one. The architecturally correct answer is yes (most likely via OpenAPI codegen, since swaggo is already in the stack), but doing it here mixes scope. The slug pattern in this PR is forward-compatible with extraction into a shared spec later — nothing is throwaway. Opened in this worklog under "Next Steps".

5. **Upgraded three additional org handlers** (`Update`, `AddMember`, `ChangeMemberRole`) — for consistency. All four were using the same opaque error. Catching them in one PR avoids drift between handlers that semantically should behave identically.

6. **Fixed the pre-existing contract drift** — the user authorised the fix. Per README §5 ("no pre-existing errors are acceptable") this would have been required anyway; explicit confirmation kept scope visible.

---

## Blockers

None. The pre-existing flake in `TestProxy_SessionLeak_CleanedUpOn503` is reproducible on mainline (confirmed via `git stash` round-trip) and is out of scope.

---

## Tests Run

**Backend:**

```
$ go build ./... 2>&1 | head -10
(clean)

$ go vet ./...
(clean)

$ go test -timeout 30s -count=1 -run "TestSlug|TestBindingError|TestOrgsHandler_Create|TestCreateOrg" ./pkg/types/ ./api/internal/handlers/
ok  	github.com/lenaxia/llmsafespaces/pkg/types	0.015s
ok  	github.com/lenaxia/llmsafespaces/api/internal/handlers	0.078s

$ go test -timeout 60s -count=1 -run "Test[^P]" ./api/internal/handlers/
ok  	github.com/lenaxia/llmsafespaces/api/internal/handlers	56.987s

$ go test -timeout 30s -count=1 ./pkg/types/...
ok  	github.com/lenaxia/llmsafespaces/pkg/types	0.008s
```

**Frontend:**

```
$ npx tsc --noEmit
(clean)

$ npx vitest run
 Test Files  110 passed (110)
      Tests  1198 passed (1198)
```

**Not run:** `./api/internal/handlers/` with the `TestProxy_SessionLeak_CleanedUpOn503` test included — pre-existing hang documented above. Behaviour is identical on `main` (verified by stash round-trip), so this is not a regression.

---

## Adversarial Self-Review

Per README §11, conducted a structured Phase 1/2/3 review. Phase 1 produced 23 candidate findings. Phase 2 validation:

- **False alarms (16):** documented inline (validator scope, init() ordering, package import order, JSON serialization order, etc.).
- **Real findings remediated (5):** TypeScript type error on optional `details` (fixed); other org handlers using opaque error (fixed); pre-existing contract drift surfaced by Go test run (fixed); frontend test coverage missing for new error shape (added); Gin uses `binding:` tag not `validate:` (caught and fixed during test development).
- **Deferred (1):** frontend doesn't pre-validate slug client-side. Documented in Next Steps as part of the centralized-validation follow-up.

Zero unresolved real findings *from my own review.*

### Iteration 1: Automated PR review (PR #427)

The repository's PR-review bot caught one **real correctness gap** that my self-review missed:

> The Update handler does not lowercase `req.Slug` — contradicts the PR's stated design.
> `POST /api/v1/orgs` with `slug: "My-Org"` → stored as `"my-org"` ✓
> `PUT /api/v1/orgs/:id` with `slug: "My-Org"` → stored as `"My-Org"` ✗

The asymmetry was prevented from causing duplicate-slug data corruption only by the DB's case-insensitive `idx_orgs_slug_lower_active` index (migration 000030) and the case-insensitive `GetOrgBySlug` lookup. But the stored case would have been inconsistent between Create-vs-Update paths, which matters for SSO URL routing (`/auth/sso/:orgSlug/*`) and display consistency.

**Remediation (this iteration):**
- Added `req.Slug = strings.ToLower(req.Slug)` to `Update` immediately after `ShouldBindJSON` — mirrors `Create`'s behavior.
- Added three new Update-path tests (TDD red → green):
  - `TestOrgsHandler_Update_HyphenatedSlug` — accepts hyphenated input.
  - `TestOrgsHandler_Update_BadSlugReturnsDetails` — rejects invalid input with per-field details, confirms bad data does not reach the store.
  - `TestOrgsHandler_Update_SlugLowercased` — verifies the missing `ToLower` is now applied (this test was red before the fix, green after).
- Addressed the reviewer's style finding about duplicated `bindingErrorMessage` vs `getValidationErrorMessage` by adding reciprocal cross-reference comments in both files. Full extraction is deferred to the centralized-validation follow-up.

The lesson is generic: when a binding tag is changed on a request type, every handler that consumes that type is in-scope for the change. My self-review checked the validator's behavior but not whether the *handler-side post-processing* (lowercase, trim, etc.) was symmetric between the affected handlers. Next time: when changing a binding tag, also `grep` for every handler that uses the type and confirm the post-bind processing is identical or intentionally different.

### Iteration 2: Automated PR review (PR #427) — APPROVE with one non-blocking gap

The reviewer approved the second iteration but flagged one low-risk gap: the new `bindingErrorResponse` was wired into `AddMember` and `ChangeMemberRole`, but no handler-level test verified those two paths return per-field details (only the helper was unit-tested in isolation, and only Create/Update had integration tests).

**Remediation (this iteration):**
- `TestOrgsHandler_AddMember_MissingUserIdReturnsDetails` — POSTs `{"role":"member"}` (missing `userId`), asserts 400 with `details.userId`.
- `TestOrgsHandler_AddMember_MissingRoleReturnsDetails` — POSTs `{"userId":"x"}` (missing `role`), asserts 400 with `details.role`.
- `TestOrgsHandler_ChangeMemberRole_MissingRoleReturnsDetails` — PUTs `{}`, asserts 400 with `details.role`.

These close the gap and make it impossible to regress the wiring of either handler without a test catching it.

---

## Next Steps

1. **Watch for the chat.safespaces.dev confirmation.** Once this PR is merged and deployed, ask the user to retry their org creation flow. Expected behavior: any user-friendly org name (e.g. `"My Org"`) auto-generates the slug `"my-org"`, the server accepts it, and the org is created. If the slug is bad (manually edited to include `_` or starts with `-`), the error message now reads `"Slug: Must be letters, digits, and single hyphens between segments (e.g. \"my-org\")"` rather than the opaque `"invalid request body"`.

2. **Open a follow-up story for centralised validation.** Recommended approach: extend the OpenAPI spec (`sdks/openapi.yaml`) with `pattern`/`minLength`/`maxLength` constraints for request types, then generate per-language validators (Go binding tags, TS Zod schemas, Python pydantic, Java bean-validation). Swaggo is already in the stack — the change is in directionality (spec → types, instead of today's types → spec via swag comments). Slug regex from this PR (`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`) drops straight into the spec.

3. **Fix `TestProxy_SessionLeak_CleanedUpOn503` flake** in a separate PR. Goroutine dump points to `proxy_request_buffer.go:141` and `:280`. Pre-existing on main, blocking full handler suite runs.

4. **Audit other handlers that use `c.ShouldBindJSON()` with the opaque `"invalid request body"` response.** `grep` shows 19 call sites across the codebase (`api/internal/handlers/{secrets,user_provider_credentials,proxy_handlers,org_sso,invitations,org_credentials,admin_provider_credentials,webhook,policies,org_billing}.go`). Each could benefit from the new `bindingErrorResponse()` helper. Out of scope here but cheap to do incrementally.

---

## Files Modified

**Backend (Go):**
- `pkg/types/orgs.go` — changed `binding:"alphanum"` → `binding:"slug"` on `CreateOrgRequest.Slug` and `UpdateOrgRequest.Slug`; added explanatory godoc.
- `pkg/types/validators.go` — **new**. Slug regex, validator, init() registration on Gin's binding engine.
- `pkg/types/validators_test.go` — **new**. Unit tests for the slug validator.
- `api/internal/handlers/orgs.go` — replaced 4 opaque `{"error":"invalid request body"}` responses with `bindingErrorResponse(err, &req)` (Create, Update, AddMember, ChangeMemberRole). Iteration 1: added `strings.ToLower(req.Slug)` to `Update` to mirror `Create`.
- `api/internal/handlers/binding_errors.go` — **new**. `bindingErrorResponse()` helper + message formatter. Iteration 1: added cross-reference comment to the duplicated switch in middleware/validation.go.
- `api/internal/handlers/binding_errors_test.go` — **new**. Unit tests for the helper.
- `api/internal/handlers/orgs_test.go` — added `userRole: admin` to test middleware so platform-admin-gated handlers exercise correctly; added 5 new `TestOrgsHandler_Create_*` tests. Iteration 1: added 3 new `TestOrgsHandler_Update_*` tests (hyphenated slug accepted, bad slug rejected with details, mixed-case lowercased). Iteration 2: added 3 new validation-detail tests for `AddMember` (missing userId, missing role) and `ChangeMemberRole` (missing role).
- `api/internal/middleware/validation.go` — Iteration 1: added cross-reference comment to `getValidationErrorMessage` pointing at `bindingErrorMessage`. No behavior change.

**Frontend (TS):**
- `frontend/src/api/types.ts` — added `details?: Record<string, string>` field to `ApiError`; added `ActiveSessionsResponse` interface (resolves pre-existing contract drift).
- `frontend/src/api/contract.test.ts` — added `ActiveSessionsResponse` to the import list, added an assertion, and added the key to the `testedKeys` array.
- `frontend/src/api/contract-fixtures.json` — regenerated via `go test ./pkg/types/...`. The diff is the addition of the `ActiveSessionsResponse` block that the Go generator was already producing but the file hadn't been regenerated for.
- `frontend/src/components/settings/OrgSettingsTab.tsx` — handle 400 + `details` by surfacing the first field-level message with a friendly label.
- `frontend/src/components/settings/OrgSettingsTab.test.tsx` — new test asserting the field-level message is rendered correctly.
