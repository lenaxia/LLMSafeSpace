# Worklog: Settings Resource Validation + Normalization

**Date:** 2026-06-18
**Session:** Fix the production "8gi" failure end to end and prevent the class entirely
**Status:** Complete (PR pending)

---

## Objective

A user reported that the "Create workspace" button returned the cryptic error:

```
internal_error: workspace_creation_failed (admission webhook
"vworkspace.llmsafespace.dev" denied the request:
spec.resources.memory "8gi": memory "8gi" does not match
^[0-9]+(Ki|Mi|Gi)$)
```

Every workspace creation was broken for every user. Diagnosis required tracing through three layers (Workspace CRD → API service `buildWorkspaceCRD` → `instance_settings` table) to find the bad value.

Root cause: an admin had earlier saved `workspace.defaultResources.memory = "8gi"` (lowercase suffix) through the admin settings UI. The setting had no `Pattern` in the schema, so the value passed validation, reached the database, and lay dormant until the next workspace creation when the controller piped it into the Workspace CRD spec — at which point the validating webhook rejected it.

User direction: "normalize UX input values so we don't run into this issue."

## Work Completed

### Backend: schema patterns + normalization

- `pkg/settings/schema.go`: added `Pattern` to `workspace.defaultResources.cpu` (`^([0-9]+m|[0-9]+\.[0-9]+)$`) and `workspace.defaultResources.memory` (`^[0-9]+(Ki|Mi|Gi)$`). These mirror the validating webhook regex so `Validate()` rejects anything the webhook would.

- `pkg/settings/normalize.go` (new): `Normalize(def, value)` canonicalizes unambiguous near-misses (lowercase units, `KB`/`MB`/`GB` → `Ki`/`Mi`/`Gi`, whitespace) before validation. Bare single-letter units (`8K`/`8M`/`8G`) are deliberately NOT mapped — they're ambiguous in Kubernetes Quantity grammar (decimal vs binary), so they pass through to the pattern rejection.

- `pkg/settings/instance_service.go` and `user_service.go`: `Set()` now runs `Normalize()` before `Validate()`, so an admin who types `"8gi"` gets `"8Gi"` stored.

### Backend tests (test-driven)

`pkg/settings/schema_test.go`:
- `TestInstanceSettings_ResourceQuantitiesHavePatterns`: every resource-quantity setting must declare a Pattern.
- `TestInstanceSettings_ResourcePatternsAgreeWithWebhook`: drift guard — schema patterns must equal webhook regex.
- `TestValidate_Memory_RejectsLowercaseUnit`: direct regression for the bug — `"8gi"` rejected if not normalized first.
- `TestValidate_CPU_RejectsBogusValues`: same for CPU.
- `TestValidate_StorageSize_RejectsBogusValues`: drift guard for storage.

`pkg/settings/normalize_test.go` (new):
- `TestNormalize_Memory_LowercaseUnit`, `TestNormalize_Memory_WhitespaceAndCaseSplit`: positive cases.
- `TestNormalize_Memory_AlreadyCanonical`: idempotence.
- `TestNormalize_Memory_AmbiguousFallsThrough`: `"banana"`, `"-1Gi"`, `"8 G"` pass through unchanged so `Validate()` rejects them.
- `TestNormalize_CPU_SuffixCase`: `"500M"` → `"500m"`.
- `TestNormalize_StorageSize_LowercaseUnit`: `"15gi"` → `"15Gi"`.
- `TestNormalize_NonResourceSettings_PassThrough`: `instance.name` and other free-form strings untouched.
- `TestNormalize_PreservesNonStringTypes`: bool/int/enum unchanged.
- `TestNormalize_ThenValidate_FixesTheBug`: end-to-end pin of the fix.

`pkg/settings/instance_service_edge_test.go`:
- `TestInstanceService_Set_NormalizesMemoryQuantity`: `Set("8gi")` succeeds, stored value is `"8Gi"`.
- `TestInstanceService_Set_NormalizesMemoryUnitVariants`: matrix of inputs.
- `TestInstanceService_Set_StillRejectsGarbage`: `"banana"`/`"8 G"`/`"8.5Gi"` still rejected.

### Frontend: pattern hint + aria-invalid + normalization

- `frontend/src/components/settings/SettingsForm.tsx`: `StringInput` now consults `def.pattern`. On commit (blur or Enter), the typed value is run through `normalizeSettingValue()`, then pattern-checked. If valid, the canonical form is committed and the visible input updates so the user sees the auto-correction. If invalid, the input gets `aria-invalid="true"`, an `aria-describedby` error message ("Value does not match required format. Example: 1Gi"), and a destructive border. Typing is never blocked mid-flight; only commit is gated.

- `frontend/src/lib/settingsNormalize.ts` (new): mirrors `pkg/settings/Normalize()`. Memory and CPU rules identical to backend so a curl client and a UI client produce the same wire payload. Imports nothing — pure string transforms.

### Frontend tests

`frontend/src/lib/settingsNormalize.test.ts` (new): 11 tests pinning the canonicalization rules.

`frontend/src/components/settings/SettingsForm.test.tsx`: 14 new tests covering:
- pattern validation: invalid value not submitted, error visible via aria-invalid + aria-describedby
- pattern hint priority (placeholder > pattern example > pattern itself)
- typing never blocked mid-flight (only commit is gated)
- error clears when user replaces invalid with valid
- normalization auto-corrects unambiguous near-misses (`"8gi"` → `"8Gi"`, `"8GB"` → `"8Gi"`, whitespace, `"500M"` → `"500m"`)
- ambiguous inputs (`"8 G"`) and garbage (`"banana"`) get the rejection path, value not silently mangled
- non-resource patterned strings (`instance.name`) not touched by normalization

## Production fix

Independent of the code change, the deployed cluster had `"8gi"` sitting in `instance_settings` blocking every new workspace. Fixed in-place:

```sql
UPDATE instance_settings SET value = '"8Gi"'
WHERE key = 'workspace.defaultResources.memory';
```

`InstanceService` cache TTL is 60 seconds, so I also restarted `llmsafespace-api` to clear the cache immediately. Workspace creation works again on production.

## Key Decisions

1. **Two-stage policy: Normalize then Validate.** The alternative was strict rejection only ("retype it correctly"). User explicitly chose normalize-then-validate so honest typos get auto-corrected. Garbage still fails.

2. **Mirror the normalizer on backend AND frontend.** Originally I considered backend-only normalization (curl clients benefit, frontend stays simple). Mirroring on the frontend lets the user *see* the auto-correction land in the input, which is meaningful UX feedback. Wire payload is identical either way; this is purely about what the user sees.

3. **`KB`/`MB`/`GB` → `Ki`/`Mi`/`Gi` (binary).** Decision in `memoryUnitMap`. Most workspace operators size workloads in powers of 2; the difference between 1 GB (10^9 bytes) and 1 GiB (2^30 bytes) is below the noise floor. Choosing the binary unit silently is the safer default — the user gets at least the GB they asked for.

4. **Bare single-letter units (`8K`, `8M`, `8G`) NOT normalized.** In Kubernetes Quantity grammar these are decimal units distinct from `Ki`/`Mi`/`Gi` (binary). A user typing `"8G"` could plausibly mean either — pass through to the pattern rejection so they pick consciously.

5. **Resource-key allowlist for normalization, not type-based dispatch.** `Normalize()` switches on `def.Key` (`workspace.defaultResources.memory`, etc.) rather than detecting "this looks like a quantity." Trimming whitespace from `instance.name` would be surprising for a name field; opt-in per setting is conservative.

6. **Drift guard test (`TestInstanceSettings_ResourcePatternsAgreeWithWebhook`).** If the webhook regex changes in `controller/internal/webhooks/workspace_webhook.go`, the test fails until the schema is updated in lockstep. Prevents the schema-vs-webhook divergence that caused the original bug.

## Alternatives Considered

- **Strict rejection only.** Less code; user has to retype. Considered and explicitly rejected by the user.
- **Backend-only normalization.** Simpler; the user sees the correction land only after the API roundtrip. Rejected — the visible auto-correction in the input is friendlier.
- **Frontend-only normalization.** Doesn't help curl/kubectl clients. Rejected — backend defense-in-depth matters.
- **Auto-trim free-form strings (instance.name, MOTD).** Rejected — surprising for name fields. Opt-in per setting.

## Blockers

None.

## Tests Run

```
$ go test ./pkg/settings/...
ok  	github.com/lenaxia/llmsafespace/pkg/settings	0.104s

$ go test ./api/internal/services/workspace/... ./api/internal/handlers/... -short
ok  	github.com/lenaxia/llmsafespace/api/internal/services/workspace	0.903s
ok  	github.com/lenaxia/llmsafespace/api/internal/handlers	63.713s

$ go vet ./...
(no output)

$ golangci-lint run --timeout=5m
0 issues.

$ make repolint
ok    migrations sequence (36 migrations, max version 36)
ok    worklogs sequence (361 worklogs, max 0362, grandfathered <0097)
ok    worklogs no mainline collisions (next available: 0363)
ok    chart migrations match api/migrations/
ok    CRD drift (8 bindings checked)
repolint: all checks passed

$ cd frontend && npx tsc --noEmit
(no output)

$ npx vitest run
Test Files  109 passed (109)
     Tests  1140 passed (1140)
```

## Next Steps

- Merge the PR.
- (Out of scope, longer-term) extend `Normalize()` to other resource-shaped fields if more get added (e.g. ephemeral-storage caps, GPU quantities). The pattern is established; it's a one-line case to add per setting.
- (Out of scope) consider adding a "preview" indicator on the form: small `→ 8Gi` annotation while the user is typing `"8gi"`, before they blur. Lower priority — the on-blur auto-correction is already discoverable.

## Review-Feedback Addendum (2026-06-19)

The first review (PR #269) raised three substantive findings, all addressed in a follow-up commit on the same branch:

1. **Zero-magnitude validation gap.** The original schema regex `^[0-9]+(Ki|Mi|Gi)$` accepted `"0Gi"` because `[0-9]+` matches `"0"`. Webhook's `parseMemoryMi` rejects `n < 1`, so `"0Gi"` was the same failure class as `"8gi"`: passes settings, breaks workspace creation. Tightened the schema and webhook patterns to `^[1-9][0-9]*(Ki|Mi|Gi)$`. Added probes for `"0Gi"`, `"0Mi"`, `"0Ki"`, `"00Gi"` to `TestValidate_Memory_RejectsLowercaseUnit` and storage equivalents.

2. **Drift guard didn't actually guard webhook drift.** The first version compared schema patterns against test-local constants (`expectedMemoryPattern` etc.), so a developer could change the webhook regex without breaking the test. Replaced with two layered guards:
   - Created `pkg/settings/quantity_patterns.go` with canonical exported constants `MemoryQuantityPattern`, `StorageQuantityPattern`, `CPUQuantityPattern`. Schema references these directly (no literal strings).
   - Added `TestWebhookRegexAcceptsSameInputsAsSettingsPattern` in the webhook's own test file, which imports `pkg/settings`, compiles both the canonical patterns and the webhook's regex variables, and probes them with the same input matrix (including zero-magnitude). If either drifts, the test fails on the first input where they diverge.

3. **Two unrelated changes in one PR.** Branch had a `fix(relay)` commit (`dd62cce1`) bundled with the settings work. Rebased to drop it; the relay fix is preserved as its own branch `fix/relay-create-on-first-deploy` for a separate PR.

Plus minor:

4. **Speculative `error` return on `Normalize()`.** Reviewer correctly identified this as YAGNI. Dropped the `error`; `Normalize` now has signature `func Normalize(def SettingDef, value any) any`. Both callers (`InstanceService.Set`, `UserService.Set`) simplified accordingly.

5. **`SchemaVersion` not bumped.** Per `pkg/settings/schema.go:6` ("Incremented on any schema change (add/remove/modify keys)"), adding `Pattern` and tightening `Pattern` on existing keys is a "modify keys" event. Bumped from `2` → `3` and updated the canary in `sdks/canary/go/scenarios/s-user-settings/main.go` so it doesn't fail on the version mismatch.

6. **`TestNormalize_Memory_AmbiguousFallsThrough` swallowed errors silently.** The `if err != nil { continue }` made the test pass vacuously if `Normalize` ever started returning errors. Eliminated by removing the error path entirely (Finding 4 above) — the test now asserts equality directly without an err check.

## Files Modified

| File | Change |
|---|---|
| `pkg/settings/quantity_patterns.go` | **New (review feedback)**: canonical exported constants for memory/CPU/storage Quantity regexes |
| `pkg/settings/schema.go` | `Pattern` on `defaultResources.{cpu,memory}` references canonical constants; magnitude `[1-9][0-9]*`; `SchemaVersion` 2→3 |
| `pkg/settings/normalize.go` | New: `Normalize(def, value) any` (no error return per review) |
| `pkg/settings/normalize_test.go` | New: 9 unit tests; `_AmbiguousFallsThrough` no longer swallows |
| `pkg/settings/schema_test.go` | Replaced test-local `expected*Pattern` constants with `TestInstanceSettings_ResourcePatternsUseCanonicalConstants`; added `0Gi`/`0Mi`/`0Ki`/`00Gi` zero-magnitude probes |
| `pkg/settings/instance_service.go` | `Set()` runs `Normalize()` before `Validate()`; signature follows new `any` return |
| `pkg/settings/user_service.go` | Same |
| `pkg/settings/instance_service_edge_test.go` | New: 3 integration tests covering normalization end-to-end |
| `controller/internal/webhooks/workspace_webhook.go` | `memoryPattern` and `storageSizePattern` tightened to `[1-9][0-9]*` magnitude |
| `controller/internal/webhooks/workspace_webhook_test.go` | **New (review feedback)**: `TestWebhookRegexAcceptsSameInputsAsSettingsPattern` imports `pkg/settings` and probes both layers' regexes |
| `sdks/canary/go/scenarios/s-user-settings/main.go` | `expectedSchemaVersion` 2→3 |
| `frontend/src/lib/settingsNormalize.ts` | New: TypeScript port of the Go normalizer |
| `frontend/src/lib/settingsNormalize.test.ts` | New: 11 tests pinning the TS canonicalization rules |
| `frontend/src/components/settings/SettingsForm.tsx` | `StringInput` consults `def.pattern`, normalizes on commit, shows aria-invalid + helpful error |
| `frontend/src/components/settings/SettingsForm.test.tsx` | 14 new tests for pattern validation + normalization paths |
| `worklogs/0383_2026-06-18_settings-validation-normalization.md` | This file |
