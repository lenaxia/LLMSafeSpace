# Epic 47: Frontend Architecture Consolidation

**Status:** Design Complete — Ready for Implementation
**Created:** 2026-06-18
**Depends On:** None (self-contained frontend work; US-47.4 touches backend settings + workspace service)
**Evidence Base:** `design/0037`–`0040` (validated analysis, merged via PR #219)

---

## Problem Statement

The frontend was 100% LLM-developed across many independent sessions. The code is cleanly typed and lint-clean (no `@ts-ignore`, no `as any`, no `TODO`/`FIXME`), but successive sessions left behind **parallel implementations**, **silent failures**, and **dead code** — the classic architectural drift of LLM-assisted development where each session invents its own pattern without reconciling with existing ones.

Three categories of issue, each validated against the codebase (`design/0037`–`0040`, PR #219 review independently confirmed every headline finding):

1. **Silent failures.** The per-workspace Auto-Suspend UI collects input and silently discards it (`onSave={async () => {}}`). The `AbortController` in `useChatStream` creates a controller whose `signal` is never passed to the fetch — pure dead theater.

2. **Dual mechanisms for the same concept.** Server-busy state is derived independently in `ChatPage` (`serverBusy`/`sseHasDrivenBusy`) and `SessionActivityProvider` (`busySessions`), both reducing the same `session.status` SSE event. Sixteen settings/org-admin components hand-roll `useState(true)` + try/catch fetch logic while the rest of the app uses TanStack Query.

3. **Dead code masquerading as architecture.** `lib/stream.ts` (130 lines, HTTP-streaming parser orphaned by the SSE move), `api/events.ts` (BroadcastChannel SSE client never wired), `AdminCredentialsTab.tsx` (backend table dropped in migration 000015), plus dead hooks, dead API methods, and a duplicate session-list cluster.

---

## Scope Boundaries

### In scope
- Frontend TypeScript/React code (`frontend/src/`)
- US-47.4 backend changes: Tier-3 autoSuspend setting + `WorkspaceService` dependency injection (minimal, contained)
- US-47.7 backend change: normalise `SessionListItem.status` vocabulary (`"active"` → `"busy"`)

### Out of scope (owned by other epics)
- **Backend codebase debt** — owned by Epic 46 (codebase-debt-audit). Epic 46 addresses Go-side issues (god files, `interface{}`, context propagation, single-writer agent-config.json). Epic 47 is frontend-only except the two minimal backend touches above.
- **ChatPage god-component refactor** — extracting `ChatSessionProvider` is a larger architectural change. US-47.5 fixes the busy-indicator symptom incrementally; the full ChatPage decomposition is deferred.
- **Cross-tab SSE multiplexing** — the documented BroadcastChannel scheme (`design/0026` §10.3) was never built. Updating the design doc to match reality is tracked as a documentation item, not a story.
- **Compaction-threshold redesign** — the 50% heuristic in `ChatPage` is documented as questionable but is a product decision, not an engineering fix.

---

## Story Index

### Phase 1 — Dead Code & Silent Failures (zero-risk, prerequisite for later phases)

| Story | Title | Effort | Risk |
|-------|-------|--------|------|
| US-47.1 | Dead-code sweep — remove all orphaned frontend files | Trivial | None |
| US-47.2 | Remove per-workspace autoSuspend UI (silent failure) | Trivial | None |
| US-47.3 | Remove dead AbortController from useChatStream | Trivial | None |

### Phase 2 — Account-Level AutoSuspend (new feature)

| Story | Title | Effort | Risk |
|-------|-------|--------|------|
| US-47.4 | Add Tier-3 autoSuspend setting + workspace-creation resolution | Medium | Low |

### Phase 3 — Busy/Loading Mechanism Unification

| Story | Title | Effort | Risk |
|-------|-------|--------|------|
| US-47.5 | Unify server-busy state source (ChatPage → SessionActivityProvider) | Medium | Medium (most-tested area) |
| US-47.6 | Consolidate loading/busy primitives (Spinner + BusyIndicator) | Low | Low |
| US-47.7 | Normalise active/busy vocabulary (backend SessionListItem.status) | Low | Low |

### Phase 4 — Performance & Idiom

| Story | Title | Effort | Risk |
|-------|-------|--------|------|
| US-47.8 | Gate wsLog + remove stray console.log | Trivial | None |
| US-47.9 | Remove per-message useNow + fix queryCache hot path + replace ref+counter | Low | Low |

### Phase 5 — Architecture Consolidation

| Story | Title | Effort | Risk |
|-------|-------|--------|------|
| US-47.10 | Migrate settings/org-admin to TanStack Query | High volume / mechanical | Low |
| US-47.11 | Route code-splitting (lazy-load org-admin + settings) | Low | None |
| US-47.12 | Fold SecretsTab llm-provider into UserProviderCredentialsTab | Medium | Low |

---

## Execution Plan

**Phase 1 first** — dead-code sweep is zero-risk and shrinks every later diff by removing misleading parallel implementations. One PR, all three stories.

**Phase 2 independent** — US-47.4 can proceed in parallel with Phase 3/4. It is the only story requiring backend changes and has no frontend dependencies.

**Phase 3 before Phase 4** — US-47.5 (busy unification) should land before US-47.9 (which touches the same `ChatPage` area for the ref+counter fix). US-47.6 (primitives) follows US-47.5 (mechanism) so the busy concept has one state source before unifying its rendering.

**Phase 5 last** — US-47.10 (Query migration) is high-volume and mechanical; US-47.11 (code-splitting) comes naturally after; US-47.12 (provider fold) needs a data-migration decision.

Each story is independently shippable. No "big bang."

---

## Success Criteria

- Zero dead frontend code (every exported symbol has at least one production importer)
- One state-management paradigm for server data (TanStack Query everywhere)
- One busy-state derivation (SessionActivityProvider is the single source)
- One loading primitive (Spinner) + one busy primitive (BusyIndicator)
- No silent UI failures (every form input either persists or is removed)
- `npm run typecheck && npm run test && npm run lint && npm run build` pass on every PR
