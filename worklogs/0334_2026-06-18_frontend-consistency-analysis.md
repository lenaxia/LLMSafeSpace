# Worklog: Frontend consistency analysis — busy indicators, quirks, consolidation roadmap

**Date:** 2026-06-16 through 2026-06-18
**Session:** Deep-dive the frontend UX for consistency improvements, document findings, open PR for visibility
**Status:** Complete

---

## Objective

The user asked to deep-dive the frontend UX — specifically whether the bouncing-dot busy indicator and the blue-circle busy indicator could share one mechanism, and more broadly what other consistency improvements, quirks, or dead code the 100%-LLM-developed frontend had accumulated. Document only; validate everything against the code.

---

## Work Completed

### Analysis pass 1 — busy indicators (0037)

- Mapped the two user-named indicators to actual code: bouncing dot = `StreamingIndicator.tsx`; blue circle = inline `Loader2 text-blue-500` in `Sidebar.tsx:371,792`.
- Traced both to their state sources: dual derivation of the same `session.status` SSE event via two independent connections (`/events` user stream → `SessionActivityProvider.busySessions`; `/workspaces/:id/session-events` workspace stream → `ChatPage.serverBusy`).
- Proposed P1 (unify busy mechanism — have ChatPage consume `useIsSessionBusy()`), P2 (one Spinner + one BusyIndicator primitive), P3 (delete dead session-list cluster), P4 (doc-only: streams cannot be merged).

### Analysis pass 2 — broader findings (0038)

- Validated the dual SSE streams carry genuinely different event types (CONFIRMED from backend `PublishToUser` vs `broker.Publish` — they cannot be merged).
- Found: `wsLog` ungated in prod (21 call sites); per-message `useNow` interval for a static timestamp; `queryCache.getAll()` on every sessions event; `contextBySessionRef` + version-counter workaround.
- Dismissed two false alarms (concurrent sessions-cache writers lose updates; dual useQuery workspaces) with rationale.

### Analysis pass 3 — quirks and silent failures (0039)

- **Headline**: workspace Auto-Suspend settings silently discarded (`Sidebar.tsx:421` → `onSave={async () => {}}`). Live user-facing feature that does nothing. Backend only honours global instance setting.
- Found: `useChatStream` AbortController is dead theater (signal never passed to sendAsync); 15 settings/org-admin components hand-roll fetch state vs TanStack Query; dead `lib/stream.ts` + dead API methods + dead hooks.
- Confirmed clean: no `@ts-ignore`, no `as any`, no `TODO/FIXME`.

### Analysis pass 4 — path-forward roadmap (0040)

- Synthesised all findings into a phased roadmap: Phase 0 (dead-code sweep), Phase 1 (fix silent failure), Phase 2 (busy unification), Phase 3 (perf hot paths), Phase 4 (Query migration + provider-UX fold).
- Identified the provider-UX overlap precisely: `SecretsTab`'s `llm-provider` type overlaps with `UserProviderCredentialsTab`; `AdminCredentialsTab` is dead (backend table dropped); relay is a distinct concept.
- Flagged 4 decision points for the user.

### PR #219

- Committed all four docs to `docs/frontend-consolidation-analysis` branch.
- PR received AI review: **APPROVE** with three non-blocking findings (line-number drift from Epic 44/45 merge, one incomplete citation, missing worklog).
- Addressed all three: refreshed drifted line numbers, added the missing `useChatStream.test.ts:13` citation, created this worklog.

---

## Key Decisions

- **Document only, no code changes** — per user instruction ("just document for now"). All proposals are actionable but unimplemented.
- **No assumptions unvalidated** — every claim was traced to `file:line` in both frontend and backend. Where a subagent gathered evidence, it was re-checked. Two false alarms were explicitly documented rather than silently dismissed.
- **Line numbers are point-in-time** — the docs were written against `0aa77fca`, then cross-checked against `515a5d81` after pull. The reviewer correctly identified drift; line numbers have been refreshed to match `515a5d81`.

---

## Blockers

None. The 4 decision points in 0040 §5 (Auto-Suspend wire-or-remove, provider UX migration, Query migration scope, wsLog gating) need user input before implementation phases can proceed, but the documentation itself is complete.

---

## Tests Run

No tests — documentation only. CI on PR #219: all frontend checks pass (unit, typecheck, build). Two Go test suites failed (`TestSessionAwareRestartDecision_DeferredRestart_AppliesWhenIdle` race) — pre-existing on main, unrelated to this docs-only PR.

---

## Next Steps

1. **Merge PR #219** (review approved, reviewer findings addressed).
2. **User decides** on the 4 decision points in 0040 §5.
3. **Phase 0** (dead-code sweep) is unblocking-free and zero-risk — recommended first action once decisions are made.

---

## Files Modified

- `design/0037_2026-06-16_frontend-busy-indicator-consistency-analysis.md` (created)
- `design/0038_2026-06-16_frontend-broader-findings.md` (created)
- `design/0039_2026-06-16_frontend-quirks-dead-code-silent-failures.md` (created)
- `design/0040_2026-06-16_frontend-path-forward-and-consolidation-roadmap.md` (created)
- `worklogs/0334_2026-06-18_frontend-consistency-analysis.md` (this worklog, created)
