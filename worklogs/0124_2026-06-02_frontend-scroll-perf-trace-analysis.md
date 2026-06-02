# Worklog: Frontend Scroll Perf — Trace Analysis & Fix Validation

**Date:** 2026-06-02
**Session:** Analyzed Chrome DevTools performance traces of `safespace.thekao.cloud` chat page; diagnosed scroll jank, validated commit `c080f5f` fixed it, and identified the next-tier optimization.
**Status:** Complete (analysis + one shipped fix). Follow-up optimization queued, not yet implemented.

---

## Objective

User reported choppy, non-smooth scrolling on the chat view. Goal: identify the root cause from a captured Chrome DevTools trace, ship a fix, then validate via a follow-up trace and recommend whether further optimization is justified.

---

## Work Completed

### Trace 1 analysis (`Trace-20260601T232838.json`, 8.6 MB, ~5.2s capture)

Trimmed to 1500–3800ms window (active scroll period) for analysis. Findings:

- **Main thread busy 80% of window** (1842ms blocked / 2300ms).
- **Two giant blocking calls** to vendor function `ae` at `vendor-DAxIHGAX.js:25:1337`: 771ms @ 2135ms and 737ms @ 2920ms. Each was triggered by a `scroll` EventDispatch (not rAF — the handler was synchronous on the scroll listener).
- **1381ms of forced synchronous layout** across 12 nested `UpdateLayoutTree` calls inside `ae()` — classic read-then-write DOM thrashing pattern, 90% of `ae`'s runtime.
- **Two BeginFrame gaps of 700ms and 600ms** during the freeze — visually equivalent to ~42 dropped frames at 60Hz.
- **250ms cumulative GC stall** at 1577ms (`MajorGC` + `CppGC.AtomicCompact`) — heavy allocation churn, likely from emotion CSS-in-JS injection.
- **6,226 `UpdateLayer` events across 59 distinct compositor layers** — every layer updating ~108x per second.
- 46 wheel + 35 scroll events dispatched on main thread; wheel handlers themselves were sub-ms, but scroll handlers were the killer.
- Identified Bitwarden-style autofill extension MutationObserver as a minor amplifier (`bootstrap-autofill-overlay.js`), not in our control.

Root cause: react-diff-viewer (emotion-styled) was mounted in DOM for every collapsed tool call. Each emotion `insertRule` invalidated the style system; with N collapsed tools, every scroll-driven layout read forced a full style+layout flush of the entire diff-viewer tree.

### Fixes shipped (commit `c080f5f`, frontend)

1. `MessageList.tsx`: scroll handler now uses rAF gating. Reads of `scrollHeight`/`scrollTop`/`clientHeight` happen at most once per animation frame. rAF is cancelled on unmount.
2. `MessagePart.tsx`: `ToolDetails` defers child rendering (including `ReactDiffViewer` + emotion CSS injection) until `<details onToggle>` fires. Collapsed tool calls no longer pay the layout cost.

### Trace 2 validation (`Trace-20260602T003305.json`, 18 MB, ~5.4s capture)

Captured after the fix shipped. Same chat session and similar interaction pattern. Compared like-for-like:

| Metric | Trace 1 (before) | Trace 2 (after) | Delta |
|---|---|---|---|
| Forced sync layout | 1381ms | 64ms | **−95%** |
| Total `UpdateLayoutTree` time | 1383ms | 108ms | **−92%** |
| Max single `UpdateLayoutTree` | 401ms | 8.9ms | **−98%** |
| Main-thread busy % | 80% | 38% | **−42pp** |
| Max BeginFrame gap during scroll | 700ms | 250ms (and not during scroll) | smoothed |
| Long tasks ≥100ms | 4 (all scroll-related) | 4 (none scroll-related) | shifted to data path |

The scroll-thrashing pathology is gone. The remaining "no big task" 80–250ms BeginFrame gaps in Trace 2 are idle periods between user actions (page sitting still), not jank.

### Remaining hotspot identified (not yet fixed)

Trace 2's top blocking task is now a 227ms TimerFire @ 843ms whose `setTimeout(0)` was installed by `query-CnIXsftV.js:1:2185` (TanStack Query's `notifyManager.scheduleNotify`). Stack trace: `cs → h → batch`. The same library is responsible for the only user-perceptible click stutter in the trace: a 165ms `click` EventDispatch @ 2967ms that runs entirely inside one `RunMicrotasks` (164ms) inside a single FunctionCall in the vendor bundle (i.e. React rendering).

Pattern: every TanStack Query subscriber re-renders synchronously when a query updates, regardless of whether the slice they read changed. With many `useQuery` consumers in the chat page, one cache write fans out into a tree-wide re-render.

### User-impact estimate (honest)

Of the optimizations identified, only the TanStack Query fix would produce a user-detectable improvement on the captured workload:

- Click responsiveness: 165ms → ~50ms (crosses Nielsen "instant" threshold of 100ms; clearly under Google INP "good" of 200ms with headroom).
- Init query stall: 227ms → near-zero (currently happens before first user input, so not directly perceived, but eliminates a latent jank window if a click lands during it).
- Scaling: re-render cost stops scaling linearly with conversation length and subscriber count.

Other items considered and rejected as optimizations:

- Layer count (121 in Trace 2): compositor was busy only 36ms in a 5.4s window — not load-bearing.
- 126ms `ParseHTML` at init: completes before FCP (540ms), invisible to user.
- CSS transition cancels (6 in window): each is sub-ms.
- Code-splitting `vendor-DAxIHGAX.js`: LCP already 1090ms, no clear win.
- Browser extension overhead (252ms across Bitwarden + uBlock): not ours.

---

## Key Decisions

1. **Ship `c080f5f` immediately** — the trace evidence was unambiguous (1381ms forced layout in 2.3s, 700ms freezes), and the two-line architectural change (rAF throttling + lazy mount on `<details>`) carried no semantic risk. Validated by Trace 2 showing 92–98% reductions on every measured layout metric.

2. **Defer the TanStack Query `select`/memoization work** — it is the next-most-valuable optimization but is **not shipped this session**. Decision rationale: scroll fix moved the app from "noticeably broken" to "fine"; Query fix moves it from "fine" to "polished." Worth doing for users on weaker hardware and as conversation size grows, but not urgent. To be picked up as its own task with proper TDD on the affected components.

3. **Skip layer-count, code-splitting, and CSS-transition optimizations entirely** — diminishing returns. Premature optimization erodes maintainability; touching them now adds churn without measurable user benefit. Revisit only on a concrete user complaint or a regression in a future trace.

4. **Lazy-mount on `<details>` is a reusable pattern** — flagged as worth extracting into a shared `<LazyDetails>` component the next time a similar heavy-collapsed-widget need arises. Not extracted preemptively.

---

## Blockers

None. Next-step work (TanStack Query optimization) is queued, not blocked.

---

## Tests Run

No automated tests run in this session — work was diagnostic analysis of captured Chrome DevTools traces, not code changes. The shipped fix (`c080f5f`) was committed prior to this session by the user, and Trace 2 captured after deployment serves as the empirical regression test: layout-thrashing metrics dropped 92–98% on the same workload.

To validate any future TanStack Query optimization, the verification protocol is:

1. Capture a fresh trace performing the same chat-page click sequence.
2. Confirm the 165ms click EventDispatch drops below 100ms (no internal `RunMicrotasks` exceeding ~80ms).
3. Confirm the 227ms TimerFire near init drops below 50ms.
4. Run React DevTools Profiler during a click; confirm committed-component count drops materially (target: <10 components per click commit, vs. ~50–100 today).
5. INP score in DevTools Performance Insights stays in "Good" (<200ms) across a full mixed session.

---

## Next Steps

1. **Implement TanStack Query subscription narrowing** in the chat page query hooks. Add `select` to each `useQuery` that fans out to many components so subscribers bail out of re-render when their slice is unchanged. Pair with `React.memo` on `MessageRow`/`ToolDetails`/`MessagePart` and verify with React DevTools Profiler that committed-component count drops on a query update.
2. **Capture Trace 3** after the Query fix lands and confirm the 165ms click drops below 100ms and the 227ms init timer drops below 50ms (per verification protocol above).
3. **Consider extracting `<LazyDetails>`** as a shared component once a second use site appears; do not extract preemptively.
4. **Optional**: add a perf budget / Lighthouse CI check on INP so regressions in this area are caught automatically rather than via ad-hoc traces.

---

## Files Modified

None this session — work was trace analysis and recommendations. The shipped fix (`c080f5f`) modified:

- `frontend/src/components/chat/MessageList.tsx` (rAF-throttled scroll handler)
- `frontend/src/components/chat/MessagePart.tsx` (`ToolDetails` lazy mount on `<details onToggle>`)

Worklog created:

- `worklogs/0121_2026-06-02_frontend-scroll-perf-trace-analysis.md` (this file)

Reference traces analyzed (not in repo; user's local Downloads):

- `Trace-20260601T232838.json` (before fix, 8.6 MB)
- `Trace-20260602T003305.json` (after fix, 18 MB)
