# Worklog: Epic 27a Design Audit — Rounds 6–10

**Date:** 2026-06-03
**Scope:** Continued audit and repair of
`design/stories/epic-27a-credential-reload-foundation/README.md` and
`design/stories/epic-27b-credential-reload-polish/README.md`.
Worklog 0131 covered rounds 1–5. This worklog covers rounds 6–10.

---

## Context

Rounds 1–5 (worklog 0131) corrected the most structurally broken parts of the epic:
wrong table names, phantom files, cross-package import violations, ON DELETE CASCADE
on a soft-delete table, and the `pgxpool` vs `*sql.DB` cross-pool transaction
impossibility. By round 5 the design was structurally sound.

Rounds 6–10 progressively tightened the remaining issues — from wrong story
assignments and missing interface extensions down to incorrect pseudocode identifiers
and logging patterns. Each round found fewer and less severe issues.

---

## Round 6 Findings and Fixes

**R6-1 — CRITICAL: Bug 12 `ON DELETE CASCADE` conflicts with soft-delete pattern**

The Bug 12 fix added `FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE`.
`users` are hard-deleted. `workspaces` use soft-delete (`deleted_at`). A CASCADE would
hard-delete workspace rows when a user is deleted, bypassing the soft-delete path,
losing audit records, and orphaning live Kubernetes CRD objects.

Fixed: changed to `ON DELETE RESTRICT`. Added a 10-line SQL comment explaining the
invariant: delete/reassign all workspaces first, then delete the user. Updated migration
tests to assert that `DeleteUser` fails while workspaces exist, and succeeds after
workspaces are soft-deleted.

**R6-2 — `handlers.RespondWithError` called with package qualifier from inside `package handlers`**

Pseudocode called `handlers.RespondWithError(c, err)` from inside `package handlers`.
In Go, you never qualify within the same package. Fixed to `RespondWithError(c, err)`
throughout all handler pseudocode in both epics.

**R6-3 — `database.ErrNoAgentStateRow` import creates layering violation**

`agent_reload.go` imported `database` package to reference `database.ErrNoAgentStateRow`.
`package handlers` must not import `package database`. Fixed: moved `ErrNoAgentStateRow`
to `api/internal/errors/errors.go` (the shared `apierrors` package). Both `database.go`
and `agent_reload.go` import `apierrors`; neither imports the other. Added layering
rationale comment.

**R6-4 — `reloadOne` in 27b silently discards `getPassword` error**

```go
func() string { pw, _ := h.getPassword(ctx, workspaceID); return pw }()
```
Replaced with explicit error check: `pw, err := h.getPassword(...); if err != nil { return fail(...) }`.

**R6-5 — Stray Go comment outside code block in 27b**

`// Workers exit promptly...` was in Markdown prose instead of inside the code block.
Moved inside the `BulkReload` code block on the relevant goroutine line.

**R6-6 — Duplicate item #6 in 27a Success Criteria**

Two items numbered 6. Renumbered: Bug 11 = 4, Bug 12 = 5, RestartWorkspace = 6,
No Bug 2 = 7, Worklog 0127 preserved = 8.

**R6-7 — `ListPendingReloadWorkspaces` SELECT omitted `w.user_id`**

`WorkspaceListItem.UserID` would be silently empty. Added `w.user_id` to SELECT and
replaced "standard scan loop" stub with a complete 11-field scan implementation.

**R6-8 — `WorkspacePasswordGetter` extraction missing `Invalidate()` method**

`ProxyHandler.invalidateCaches` calls `delete(h.pwCache, workspaceID)` directly.
After extraction, the cache lives in `WorkspacePasswordGetter`. Added `Invalidate()`
and `InvalidateAll()` methods to the type spec. Updated `invalidateCaches` description
to call `h.passwordGetter.Invalidate(workspaceID)`.

**R6-9 — API-side `httpClient.Timeout: 5s` fires before agentd's 10s dispose timeout**

API client would cut the connection before agentd's structured error could be returned.
Changed to `15 * time.Second` with explanation: agentd's deadline must fire first.

**R6-10 — How `secrets.go`/`workspaces.go` access `MarkCredentialChanged` unspecified**

Added `WorkspaceMetadataUpdater` extension note and the `CredentialStateWriter` interface
design. (This was later superseded by rounds 7–10 which refined the interface design
significantly.)

---

## Round 7 Findings and Fixes

**R7-1 — `WorkspaceMetadataUpdater` wrong file reference; wrong interface to extend**

Design said "see `secrets.go:28-30`" but `WorkspaceMetadataUpdater` is in `models.go:23`.
More importantly, adding `MarkCredentialChanged(*sql.Tx)` to `WorkspaceMetadataUpdater`
violates ISP — mixing workspace display-field updates with credential state mutation.

Fixed: introduced a new dedicated `CredentialStateWriter` interface with a single method.
`SecretsHandler` gains a `SetCredentialStateWriter` setter, matching the existing
`SetWorkspaceOwnerVerifier` nil-safe pattern. `WorkspaceMetadataUpdater` is unchanged.
Added `api/internal/handlers/models.go` note to Files Likely Affected confirming no change.

**R7-2 — `api/internal/handlers/workspaces.go` does not exist**

Three references to this phantom file. Binding handlers (`SetBindings`, `GetBindings`)
are on `SecretsHandler` in `secrets.go:273-310`. Removed all phantom references.

**R7-3 — `interfaces.DatabaseService` not updated with new methods**

`workspace.Service.dbService` is typed as `apiinterfaces.DatabaseService`. Any new
method called through `dbService` must be in the interface. Added A17 documenting which
methods need to be in `DatabaseService` (only `ListPendingReloadWorkspaces` in 27b;
the three 27a methods are consumed through handler-layer interfaces directly).
Added `interfaces.go` and `mocks/database.go` to Files Likely Affected for 27b.

**R7-4/5/6/8 — Various stale comments and wiring values**

- Struct comment `// 5s timeout` after R6-9 changed it to 15s — fixed.
- A12 said "US-27a.5 moves RespondWithError" but it's US-27a.7 — fixed.
- 27b wiring used `5 * time.Second` inconsistently with 27a's 15s decision — fixed.
- Cosmetic indentation on `MarkAgentReloaded` closing brace — fixed.

---

## Round 8 Findings and Fixes

**R8-1 — A17 incorrectly added 3 Epic 27a methods to `DatabaseService`**

`MarkCredentialChanged`, `GetLastCredentialChangedAt`, `MarkAgentReloaded` are called
through `CredentialStateWriter` and `AgentStateStore` — handler-layer interfaces
satisfied by the concrete `*database.Service` directly from `app.go`. They do NOT
flow through `workspace.Service.dbService`. Adding them to `DatabaseService` would
force `MockDatabaseService` to implement methods it never uses through that interface.

Fixed A17 to state: none of the three 27a methods need to be in `DatabaseService`.
Only `ListPendingReloadWorkspaces` (27b) needs to go there. Removed the three methods
from the `DatabaseService` extension snippet.

**R8-2 — Stale import note said `ErrNoAgentStateRow` "is defined in this file"**

Still said so after R6-3 moved it to `apierrors`. Replaced with accurate description.

**R8-3/4 — Orphaned "Wired into" bullets and stray leading spaces**

Formatting defects from R7-1 edit. Added `**Wired into:**` header. Fixed indentation.

---

## Round 9 Findings and Fixes

**R9-1 — CRITICAL: Design Principle 5 "atomic transaction" architecturally impossible**

`SecretService.SetBindings` uses `pgxpool.Pool` internally (`pg_secret_store.go:280`).
`database.Service.MarkCredentialChanged` uses `*sql.DB` (`database.go:37`). These are
two separate connection pools to the same PostgreSQL server. PostgreSQL transactions are
connection-scoped. There is no mechanism to join a `pgxpool.Tx` and a `*sql.Tx` into
one atomic unit.

The design's claim that "both writes commit in a single transaction" was architecturally
impossible.

Fixed:
- Removed `*sql.Tx` parameter from `MarkCredentialChanged`. It now uses `ExecContext`
  with an implicit auto-commit single-statement transaction.
- Updated `CredentialStateWriter.MarkCredentialChanged` signature accordingly.
- Updated Design Principle 5 to "best-effort sequential": `SetBindings` first; if it
  succeeds, `MarkCredentialChanged` in its own auto-commit write immediately after.
- Added assumption A18 documenting the cross-pool impossibility with file:line citations.
- Added F.10 and F.11 to Failure-Prone Areas for the eventual-consistency window.
- Updated `MarkAgentReloaded` doc comment: removed "uncommitted" (no uncommitted state
  exists for an auto-commit write); rewrote to accurately describe what SELECT FOR UPDATE
  serialises against.
- Updated the redundancy note: "within a transaction" → "atomically in a single SQL
  UPSERT statement (auto-commit)".
- `DatabaseService` and `MockDatabaseService` — confirmed no changes needed for Epic 27a.

**R9-2/3/4/5 — R8 carry-forwards applied**

A17 correction, stale ErrNoAgentStateRow note, orphaned bullets, stray spaces.
All applied in this pass (should have been in round 8).

---

## Round 10 Findings and Fixes

**R10-1 — `isLLMProvider` undefined in handler pseudocode**

The call pattern used `if h.credStateWriter != nil && isLLMProvider` — but `isLLMProvider`
is not a variable that exists anywhere. `SecretsHandler.SetBindings` receives a list of
secret IDs and delegates to `h.svc.SetBindings` which returns only `error`. The handler
has no way to know the types of the bound secrets without an extra lookup.

Analysis of options:
- **Option A (unconditional call):** Always call `MarkCredentialChanged` after any
  `SetBindings`. False-positive banners for non-llm-provider changes, but no extra round-trip.
- **Option B (post-call GetBindings lookup):** Extra DB round-trip.
- **Option D (return diff from SetBindings):** `SecretService.SetBindings` already reads
  existing bindings for audit logging and validates each new secret individually. Both
  data sets are in memory. A `BindingsMutationResult` can be computed from them at zero
  extra query cost. This was chosen.

Added new story **US-27a.2b** covering:
- `pkg/secrets/bindings_diff.go` (new file): `BindingsMutationResult` value type,
  `computeBindingsDiff`, `sortedKeys`
- Updated `SetBindings` and `AddBindings` return signatures
- Handler call sites using `result.LLMProviderAffected`
- `DeleteSecret` gap documented as `FOLLOW-UP-27a-1`

Key design decisions documented:
- Value type (not pointer) for `BindingsMutationResult` — zero value safe on error path
- `sortedKeys` returns nil (not `[]string{}`) for empty map; callers use `len()`
- `GetBindings` error → conservative fallback: `LLMProviderAffected = true`,
  `AddedTypes = nil`, `RemovedTypes = nil` (nil-types are the "diff unavailable" sentinel)
- `computeAddBindingsDiff` eliminated — single `computeBindingsDiff(nil, newSecrets)`
  covers the additive-only case (nil iterates safely in Go)

**R10-2/3/4 — Test story assignment, stale comment, doc comment**

Three `TestSetBindings_*` tests moved from US-27a.4 to US-27a.2b.
"Within a transaction" comment updated. `MarkAgentReloaded` "uncommitted" doc comment
updated (these were consequence-of-R9-1 stale comments missed in the previous pass).

---

## Rounds 11–14 (micro-passes on US-27a.2b and FOLLOW-UP-27a-1)

Each pass focused on the new US-27a.2b material and the follow-up spec.

**Issues found and fixed across these passes:**

| Issue | Fix |
|---|---|
| `bindings_diff_test.go` contents unspecified | Added `[diff]`/`[service]`/`[handler]` tags to all 14 US-27a.2b tests; added test plan legend; added `bindings_diff_test.go` `package secrets` declaration note |
| `TestComputeBindingsDiff_*` test names didn't name the function | Renamed all `[diff]` tests to `TestComputeBindingsDiff_*` |
| `TestGetBindingsFails` ambiguous — two GetBindings methods | Renamed to `TestSetBindings_StoreGetBindingsFails_ConservativeTrue` |
| `TestSortedKeys_EmptyMap_ReturnsNil` tests implementation not contract | Replaced with `TestSortedKeys_EmptyMap_LenZero` |
| US-27a.2b critical path incorrectly blocked US-27a.7 | US-27a.2b removed from critical path for US-27a.7; correct AND join node added for US-27a.9 |
| `sort` import missing note | Added to `bindings_diff.go` file description; confirmed `secret_service.go` needs no new import |
| `s.logger` referenced but `SecretService` has no logger field | Removed log call from conservative fallback; added comment explaining sentinel detection pattern instead |
| Duplicate diff functions | Merged `computeAddBindingsDiff` into `computeBindingsDiff(nil, newSecrets)` |
| `getErr` discard on SetBindings failure undocumented | Added comment: "failed binding write needs no notification — discarding getErr is correct" |
| "No extra DB queries" ambiguous for AddBindings nil case | Expanded to explain both callers |
| FOLLOW-UP-27a-1: type check before `GetBindingsForSecret` needed | Reordered: type check first to avoid unnecessary DB call for non-llm-provider secrets |
| FOLLOW-UP-27a-1: `store.DeleteSecret` should be `h.svc.DeleteSecret` | Fixed terminology throughout |
| FOLLOW-UP-27a-1: abandoned `DeleteSecretResult` approach left in doc | Removed entire `DeleteSecretResult` section, "Wait —" paragraph, and related pseudocode; replaced with direct statement of conclusion and reason Option D cannot apply to deletion |
| "Double read" claim was actually triple read | Corrected to "triple read" with enumerated list of all three `store.GetSecret` calls and their source lines |
| `SecretTypeLLMProvider` bare identifier in handler pseudocode | Changed to `secrets.SecretTypeLLMProvider` (package-qualified form used in `package handlers`) |
| `zap.String()`/`zap.Error()` in handler pseudocode; `zap` not imported | Changed to `h.warn("msg", "key", value)` — SecretsHandler's established logging pattern |
| `GetBindingsForSecret` failure recovery undocumented | Added: "user must manually trigger POST /workspaces/:id/agent/reload" |

---

## Final State

Both epics have passed 10+ full audit rounds. The most recent pass (round after the
final micro-fixes) found no functional or behavioral gaps. All pseudocode is consistent
with actual source patterns in the codebase.

**Epic 27a is ready for implementation.**

### Key architectural decisions made during auditing

1. **No auto-dispose.** Credential staging never triggers `DisposeInstance`. The user
   controls reload explicitly via the banner.

2. **Cross-pool non-atomicity is accepted.** `SetBindings` (pgxpool) and
   `MarkCredentialChanged` (sql.DB) cannot share a transaction. Best-effort sequential
   writes: binding first, then credential-state signal. False-positive banner (reload
   is a no-op) is preferred over missed banner.

3. **`BindingsMutationResult` returned from `SetBindings`/`AddBindings`.** Zero extra
   DB queries — uses data already in memory from the existing validation and audit logic.
   Handler checks `result.LLMProviderAffected`; no type-inspection code in the handler.

4. **`ErrNoAgentStateRow` lives in `apierrors`.** Both `database.go` and `agent_reload.go`
   import the shared errors package; neither imports the other. Layering preserved.

5. **Bug 12 FK is `ON DELETE RESTRICT`.** `workspaces` soft-delete; `users` hard-delete.
   CASCADE would hard-delete workspace rows, orphaning Kubernetes CRDs. RESTRICT enforces
   the correct application-level ordering: delete workspaces before deleting user.

6. **`CredentialStateWriter` stays in `package handlers`.** Considered moving to
   `pkg/secrets` (so `SecretService` could call it directly). Rejected: a side-effect
   that belongs to agent lifecycle does not belong in the secrets domain; ISP and SRP
   argue for the handler calling it explicitly; `WorkspaceOwnerVerifier` is an existing
   concession to the same pattern but two wrongs don't make a right.

7. **`DeleteSecret` FOLLOW-UP-27a-1 uses pre-flight `GetSecret`.** Option D
   (`DeleteSecretResult`) cannot be applied to deletion: workspace IDs must be captured
   before the FK cascade removes binding rows, but the secret's type is only confirmed
   via a pre-flight read. Triple `store.GetSecret` call is accepted.

### Open follow-up

`FOLLOW-UP-27a-1: MarkCredentialChanged on DeleteSecret` — fully specced in US-27a.2b.
Create a tracking ticket before implementation begins so it does not fall through.
Implementation is handler-layer only, same pattern as `SetBindings`.
