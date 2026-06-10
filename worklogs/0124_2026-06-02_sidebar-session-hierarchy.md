# Worklog: Sidebar Session Hierarchy — Subagent Sessions Nested Under Parents

**Date:** 2026-06-02
**Session:** Add hierarchical nav for opencode `task` tool subagent sessions; collapse/expand parents; orphans group
**Status:** Complete

---

## Objective

opencode's `task` tool spawns subagent sessions (e.g. `@explore`) as
**separate sessions** with `parentID` pointing at the user-visible parent.
Before this change those subagent sessions appeared as flat top-level
entries in the sidebar — the user couldn't tell which were spawned by
which `task` invocation, and the chat view bubbled their permission
prompts up to the parent (worklog 0122) without any visual context.

Goal: render the sidebar as a tree where subagent sessions nest under
their parent, parents are collapsible, the active session's ancestor
chain auto-expands, and orphaned sessions (parent deleted) collect under
a synthetic "Orphaned subtasks" group.

---

## Investigation findings

### Parent ID propagation — three sources, none persistent

- **opencode `GET /session/:id`** returns the full session info including `parentID`. Live-verified against the user's workspace pod.
- **opencode `session.updated` SSE event** carries the same info inside `properties.info.parentID`. The proxy already extracts `title` from this event via `persistTitleFromEvent`; extending it to also capture `parentID` is a one-line addition.
- **opencode `GET /session`** lists all sessions with their `parentID`. Useful as a one-shot backfill.

None of the three was being persisted: the `session_index` schema (worklog `000003_session_index.up.sql`) had no parent column. Subagent sessions were therefore invisible to the sidebar layer above the agent.

### Decision: persist in DB, not fetch live

I considered three sources for the sidebar hierarchy:

1. **Fetch from opencode `/session` on every sidebar load** — dead-on-suspended, adds a round-trip per render, doesn't survive pod restart.
2. **Frontend N+1 against `GET /workspaces/:id/sessions/:sid`** — N+1 across all sessions, dead-on-suspended.
3. **Persist `parent_session_id` in `session_index`** — survives pod lifecycle, no round-trip on render, populated from existing SSE events.

(3) was the clear win. The migration is additive (nullable column, no FK to avoid ordering problems with `session.created` events), and all three opencode sources can write into it without conflict.

### Recovery: lost subtask permission work

While starting this task I discovered the previous turn's subtask
permission bubbling work (`session_parents.go`, `proxy_subtask_permission_test.go`,
worklog 0121) was missing from the working tree. Investigation showed it
was preserved in dangling commit `f4a4c64` from a `git stash` operation
that included untracked files. Recovered via `git show f4a4c64:<path>`
into `/tmp/` and copied back. The worklog was renamed from `0121` to
`0122` to avoid colliding with `0121_2026-06-02_frontend-scroll-perf-trace-analysis.md`
that the team had landed on remote.

---

## Implementation

### 1. Schema (`migration 000012`)

Additive ALTER on `session_index`:

```sql
ALTER TABLE session_index ADD COLUMN IF NOT EXISTS parent_session_id TEXT;
CREATE INDEX IF NOT EXISTS idx_session_index_parent
    ON session_index (workspace_id, parent_session_id)
    WHERE parent_session_id IS NOT NULL;
```

The partial index is keyed on `(workspace_id, parent_session_id)` because the only access pattern is "list children of parent X" within a workspace. NULL parents (top-level) are excluded so the index stays small even for installations with millions of standalone sessions.

Mirrored to `charts/llmsafespace/migrations/` per the
chart/source-of-truth convention.

### 2. Type system

`types.SessionListItem.ParentID` (Go) and `SessionListItem.parentId` (TS)
both with `omitempty` / optional semantics. Matched in the generated
contract fixture so the existing `contract.test.ts` round-trip catches drift.

### 3. DB layer

- **`ListSessionIndex`** — added `parent_session_id` to the SELECT, NULL→empty-string conversion via `sql.NullString`. Existing callers untouched.
- **`UpsertSessionParent`** — new method, mirrors the shape of `UpsertSessionTitle`. ON CONFLICT updates the parent column; idempotent.

Wired through both interfaces (`DatabaseService`, `SessionIndexService`) and all four mock implementations.

### 4. Population paths

Three independent writers, each layered for resilience:

1. **`session.updated` SSE** (`persistTitleFromEvent`): every time opencode emits an updated session info, we pull `info.parentID` alongside `info.title`. This is the steady-state path — a fresh subagent session writes its parent within milliseconds of being created.
2. **`fetchAndPersistTitle`** (called after a successful message round-trip): performs `GET /session/:id` and writes both title and parent. Catches the rare case where the session was created before our SSE listener attached.
3. **`BackfillSessionParents`** (called by the sidebar's `GET /workspaces/:id/sessions` handler): one-shot `GET /session` reconciliation, gated by an in-memory `parentBackfilled` map so the cost is amortized to a single pod request per workspace per process lifetime. Failures clear the gate (so a transient network blip doesn't permanently disable hierarchy). Cleared by `invalidateCaches` so a workspace suspend/restart re-runs against the new pod.

The triple coverage means every realistic event sequence — fresh subagent in an active session, sidebar load against a long-lived workspace, post-restart, post-migration — populates the column reliably.

### 5. Frontend tree builder (`lib/sessionTree.ts`)

Pure function `buildSessionTree(SessionListItem[])` returns `{ roots, orphans }`:

- A child whose `parentId` is in the input list nests under that parent.
- A child whose `parentId` is NOT in the list goes under `orphans` (parent was deleted).
- Top-level sessions become `roots`.
- Cycle protection bounds tree depth at 16 (defensive — opencode never produces cycles).
- Children are not re-sorted; input order (which the API serves as `lastMessageAt DESC`) is preserved per parent.

Companion `ancestorChain(sessions, sessionId)` returns the ID chain
root-first for the auto-expand effect.

### 6. Sidebar (`components/layout/Sidebar.tsx`)

- New `SessionTreeRow` recursive component renders the tree. Each row has:
  - A chevron button (or invisible spacer for leaves) that toggles its child set.
  - Progressive left padding (`depth * 0.75rem`) so nested rows are visually obvious even with long titles.
  - Same kebab menu (rename / delete / copy link) at every depth.
- New `OrphansGroup` synthetic header renders only when `tree.orphans.length > 0`. Has its own collapse state separate from regular session expand state to avoid any chance of ID collision with a real session named e.g. `ses_orphans`.
- Default state: collapsed. A `useEffect` watching `[sessions, selectedSessionId]` calls `ancestorChain` and adds each ancestor (excluding the active session itself) to the expanded set. Manual user-collapses are not re-expanded — the `Set.add` is additive.

### 7. Backfill wiring

`registerWorkspaceRoutes` gained a `proxyHandler *handlers.ProxyHandler`
parameter (nil-safe). The `GET /:id/sessions` route calls
`proxyHandler.BackfillSessionParents(workspaceID)` after returning the DB
sessions to the client — the response is sent immediately, the backfill
runs in the background, and subsequent SSE events on the same session
list reflect any updates.

---

## Tests

### Go (24 new tests across 3 files)

| File | Test count | Scope |
| --- | --- | --- |
| `api/internal/services/database/session_index_test.go` | 4 | `ListSessionIndex` returns ParentID; NULL→empty; UpsertSessionParent SQL shape |
| `api/internal/handlers/proxy_backfill_test.go` | 5 | Happy path; idempotent; retry-after-failure; skip-when-not-active; invalidate-allows-retry |
| (existing) `proxy_subtask_permission_test.go` | 9 | Pre-existing — recovered + still passing |
| (existing) `session_parents_test.go` | 9 | Pre-existing — recovered + still passing |

### Frontend (29 new tests across 3 files)

| File | Test count | Scope |
| --- | --- | --- |
| `frontend/src/lib/sessionTree.test.ts` | 14 | Pure tree builder: nesting, orphans, cycles, ancestorChain |
| `frontend/src/components/layout/Sidebar.hierarchy.test.tsx` | 10 | Sidebar with hierarchical sessions: collapse default, expand-on-click, auto-expand chain, orphans group |
| `frontend/tests/e2e/sidebar-hierarchy.spec.ts` | 4 | Playwright browser tests with mocked API: subtask collapse/expand, navigate-to-subtask auto-expands, orphans group |

### Test results

```
go test -timeout 240s ./...      → all 40 packages pass
go vet ./...                      → clean
npx vitest run (frontend)         → 590/590 pass (was 561; added 29)
npx tsc --noEmit                  → clean
go test -run TestGenerateContractFixtures ./pkg/types/  → regenerated fixtures
```

The Playwright tests register correctly but the dev environment lacks
chromium-headless-shell so they only run in CI. The vitest hierarchy
tests cover the same scenarios in JSDOM.

---

## Key decisions

1. **No FK on `parent_session_id`.** opencode can emit a child's `session.created` event before the parent's; a strict FK would require ordering coordination we don't have. Orphan handling at the UI layer is cheap and matches the requested UX.
2. **Three-writer redundancy is intentional.** Each path covers a different event sequence. Removing any one makes a class of session invisible to the sidebar (e.g. drop the SSE writer → fresh subagent sessions don't render until next page load). The cost is three places to keep in sync — small, since they all delegate to `UpsertSessionParent`.
3. **Backfill is opt-in via the `BackfillSessionParents` method**, not run on `Start()`. Triggering on the `/sessions` endpoint means it only runs for workspaces the user actually views — startup is fast even for installations with thousands of workspaces.
4. **Auto-expand is additive only.** Watching `[sessions, selectedSessionId]` and only calling `Set.add` (never `delete`) means a user who manually collapsed a parent won't have it yanked back open by the next list refresh. This was the most subtle UX bug avoided.
5. **Orphans group has its own expand state.** Rather than adding a sentinel ID to the regular `expanded` set, a separate `orphansExpanded: boolean` keeps the synthetic node clearly distinct from real sessions in the data layer.
6. **Recovery via `git fsck --lost-found`.** Worth documenting: when a stash includes untracked files and the worktree gets reset, the untracked files survive only as a dangling commit (the third parent of the stash merge object). `git show <dangling-sha>:<path>` recovers them without rebasing.

---

## Files added/modified

### New
- `api/migrations/000012_session_index_parent.{up,down}.sql`
- `charts/llmsafespace/migrations/000012_session_index_parent.{up,down}.sql`
- `api/internal/services/database/session_index_test.go`
- `api/internal/handlers/proxy_backfill_test.go`
- `frontend/src/lib/sessionTree.ts`
- `frontend/src/lib/sessionTree.test.ts`
- `frontend/src/components/layout/Sidebar.hierarchy.test.tsx`
- `frontend/tests/e2e/sidebar-hierarchy.spec.ts`

### Modified
- `pkg/types/types.go` — `SessionListItem.ParentID`
- `pkg/types/contract_test.go` — added QuestionRequest/PermissionRequest fixtures (with RootSessionID), added ParentID to SessionListItem fixture
- `api/internal/services/database/database.go` — `ListSessionIndex` SELECTs parent column; new `UpsertSessionParent`
- `api/internal/services/sessionindex/service.go` — `UpsertParent` method
- `api/internal/interfaces/interfaces.go` — `UpsertSessionParent` (DB), `UpsertParent` (sessionindex)
- `api/internal/mocks/database.go` — mock impl
- `api/internal/services/auth/auth_e2e_secrets_test.go`, `auth_sessionid_test.go`, `services/workspace/workspace_session_test.go`, `handlers/opencode_upgrade_test.go` — fake DB / sessionindex impls
- `api/internal/handlers/proxy.go` — extended `persistTitleFromEvent` and `fetchAndPersistTitle`; new `BackfillSessionParents` + `runParentBackfill`; cache invalidation hook
- `api/internal/server/router.go` — `registerWorkspaceRoutes` accepts `proxyHandler`; `/sessions` triggers backfill
- `frontend/src/api/types.ts` — `SessionListItem.parentId`
- `frontend/src/components/layout/Sidebar.tsx` — `WorkspaceSessionList` rewrite, new `SessionTreeRow`, `OrphansGroup`
- `frontend/src/api/contract-fixtures.json` — regenerated

---

## Next steps

1. Deploy and verify against the live cluster: open a workspace with a known subagent session (e.g. `ses_17b15a359ffeUU611BeylVPZwB` under `ses_17b1f034cffeTysii4ZtwVBvWW`) and confirm the child renders nested.
2. Backfill won't reach historical sessions on a Suspended workspace until the user resumes it. Acceptable per the design (no work happens for sessions the user isn't looking at), but worth noting if the user reports "missing parent" on a freshly-resumed workspace — it self-heals on the next sidebar refresh.
3. Consider exposing `parentId` on the OpenAPI spec for any external SDK consumers. Out of scope for this change.

---

## Live verification (post-deploy)

Deployed image `sha-9d801a2` (api) carrying my commits `4ba7a37` + `6cd9b23` plus follow-up CI/lint fixes from the team. After the helm upgrade applied migration `000012_session_index_parent`, the user reported sub-sessions rendering nested under their parent in the sidebar. End-to-end verified against the user's live session.

### Commit / push notes

- Two atomic commits, separated via temporary file moves so each commit stood on its own with a green build:
  - `4ba7a37 feat(api,frontend): bubble subagent permission/question prompts to parent session`
  - `6cd9b23 feat(api,frontend): nest subagent sessions under parents in sidebar nav`
- Pre-commit caught a misspelling (`behaviour`/`analogue` → `behavior`/`analog`) and a pre-existing `staticcheck` finding in `pkg/agent/opencode/format.go` (struct conversion); both fixed inline.
- One rebase conflict on `pkg/agent/opencode/format.go` against an interleaving team commit (`297310d style: Fix golangci-lint errors`) that fixed the same staticcheck independently — resolved by accepting the simpler upstream form (`json.MarshalIndent(orderedOutput(cfg), …)`).
- The remote CI Lint job continued to fail on a separate, pre-existing `contextcheck` violation in `cmd/workspace-agentd/secrets.go:218` introduced by the team's prior commit. Out of scope; called out in the previous turn's summary.

### Recovery footnote (revisited)

The session-parent work I had thought lost in the previous turn turned out to live in dangling commit `f4a4c64`. Documenting it explicitly so future me has a runbook:

```sh
# When `git status` shows missing untracked files post-rebase/stash-pop:
git fsck --lost-found            # lists dangling commits
git show <sha> --stat            # find the one with your missing files
git show <sha>:path/to/file > /tmp/recovered.go
```

Stashes that include untracked files (`git stash --include-untracked`, or `git stash` with `stash.includeUntracked = true`) save those untracked files as a third parent of the stash merge object. When the stash is popped and the worktree is later reset, that merge object becomes dangling — which is what happened the previous session. Worth noting that the `pre-pull stash for chat refresh investigation` Bash automation around `git pull` had an `--include-untracked` flag set; that's how the files got carried in and then lost.
