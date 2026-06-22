# Worklog: Inline permission/question prompts into chat stream + AgentPrompt refactor

**Date:** 2026-06-22
**Session:** Refactor permission and question prompts to render inside the scrolling message list (all viewports), and extract shared `AgentPrompt` wrapper component.
**Status:** Complete

---

## Objective

Permission and question prompts were rendered as a pinned sibling **outside** the scrollable `MessageList` — a non-scrolling block above the composer. On mobile this obscured the conversational context the prompt referred to, making it hard to read what the agent was asking about before answering. The user asked to render prompts inline with the chat messages so they scroll alongside content, and to extract a shared component since the two prompts are structurally near-identical.

---

## Work Completed

### Extracted shared `AgentPrompt` wrapper component

**`frontend/src/components/chat/AgentPrompt.tsx`** (new)

Owns the shared chrome that `PermissionPrompt` and `QuestionPrompt` previously duplicated:

- Themed card (`role="dialog"`, `aria-label`, rounded border, bg)
- Header row (icon + title + optional ✕ dismiss button)

Props: `variant: "permission" | "question"`, `title?`, `onDismiss?`, `dismissDisabled?`, `dismissLabel?`, `children`.

Variant config drives color theme, icon, default title, and aria-label:
- `permission` → amber theme, ⚠️ icon, "Permission required"
- `question` → blue theme, 🤖 icon, "Agent has a question"

The error banner, body content, and action buttons remain in each prompt component (they differ in structure and position).

**`frontend/src/components/chat/AgentPrompt.test.tsx`** (new) — 12 tests covering: role/aria-label per variant, default + custom titles, dismiss button presence/absence/click/disabled, children rendering, theme classes per variant.

### Refactored `PermissionPrompt` and `QuestionPrompt`

**`frontend/src/components/chat/PermissionPrompt.tsx`** — replaced the manual card div + header with `<AgentPrompt variant="permission">`. Body (intent line, patterns, feedback input, action buttons) unchanged. No `onDismiss` prop (permission has no quick-dismiss — only the reject action with feedback flow).

**`frontend/src/components/chat/QuestionPrompt.tsx`** — replaced the manual card div + header + ✕ button with `<AgentPrompt variant="question" onDismiss={handleDismiss} dismissDisabled={submitting}>`. The footer "Dismiss" button is preserved (both ✕ and footer button call `handleDismiss` → `questionReject`; this matches previous behavior exactly).

All 21 existing tests (8 permission + 13 question) pass unchanged — the refactor preserves all selectors (`getByRole("dialog")`, `getByLabelText("Dismiss")`, `getByText(...)`, button names).

### Moved prompts into the scrolling message stream

**`frontend/src/components/chat/MessageList.tsx`**:

1. Added `trailingPrompts?: ReactNode` prop, rendered inside the scroll container after `streamingBubble` and before `bottomRef` (mirrors the existing `streamingBubble` precedent for injecting content into the stream tail).
2. Updated the empty-state guard from `messages.length === 0 && !streamingBubble` to also check `&& !trailingPrompts` — so a prompt arriving with zero messages shows the prompt instead of "Send a message to start the conversation."
3. Added `hasTrailingPrompts` boolean to the stick-to-bottom `useLayoutEffect` dependency array, so the container scrolls to show a newly-arrived prompt when the user is at the bottom (previously prompts were always pinned visible; now they're in-stream and need the scroll trigger).

**`frontend/src/components/chat/ChatView.tsx`**:

- Passes `trailingPrompts={prompts}` to `MessageList`.
- Removed the `{prompts && <div className="px-4">{prompts}</div>}` line that rendered prompts outside the scroll area.

This is a single render path for all viewports — no mobile/desktop branching needed.

**Tests added:**
- `MessageList.test.tsx`: 4 new tests (trailingPrompts inside scroll container, trailingPrompts replaces empty state, ordering after streaming bubble, auto-scroll on prompt appearance).
- `ChatView.test.tsx`: 2 new tests (prompts inside `[role="log"]` container, prompts render with zero messages).

### Fixed pre-existing lint error

**`frontend/src/api/orgs.ts:32`** — `CreateOrgResponse` was an empty interface extending `OrgResponse`, flagged by `@typescript-eslint/no-empty-object-type`. Changed to `export type CreateOrgResponse = OrgResponse;` (semantically identical, satisfies the rule). Pre-existing, not introduced by this change, fixed per Rule 5.

---

## Key Decisions

| Decision | Rationale |
|---|---|
| All-viewports inline (not mobile-only conditional) | Single render path is simpler; inline is better UX everywhere, not just mobile. The user explicitly chose this. |
| Shared chrome, separate bodies (not single mega-component) | Permission body (3 fixed actions + deny feedback) and question body (N sub-questions with multi-select + free-text) branch heavily. A `variant` switch inside one component would fight Rule 4's "not overly complex." Shared wrapper + separate body components is the clean separation. |
| Error banner stays in each body | The error is positioned differently relative to each body (permission: between patterns and buttons; question: after all questions, before footer). Centralizing it would change UX position or require a slot prop — over-engineering for 1 line of JSX. |
| `hasTrailingPrompts` boolean in useLayoutEffect deps | Derived from `trailingPrompts != null` — stable boolean that only flips when prompts appear/disappear, avoiding spurious re-scrolls on every re-render. The MutationObserver handles the streaming case; this handles the non-streaming case. |

---

## Blockers

None.

---

## Tests Run

```
npx vitest run src/components/chat/AgentPrompt.test.tsx → 12 passed
npx vitest run src/components/chat/PermissionPrompt.test.tsx → 8 passed
npx vitest run src/components/chat/QuestionPrompt.test.tsx → 13 passed
npx vitest run src/components/chat/MessageList.test.tsx → 25 passed (was 21, +4 new)
npx vitest run src/components/chat/ChatView.test.tsx → 16 passed (was 12, +4 new: 2 inline-rendering + 2 integration with real prompts)
npm run test (full suite) → 1226 passed (114 files)
npm run typecheck → clean
npm run lint → clean (0 errors, 0 warnings)
npm run build → success
```

---

## Next Steps

- Branch, push, open PR for automated review.
- No follow-up work identified — the refactor is complete and self-contained.

---

## Files Modified

- `frontend/src/components/chat/AgentPrompt.tsx` (new)
- `frontend/src/components/chat/AgentPrompt.test.tsx` (new)
- `frontend/src/components/chat/PermissionPrompt.tsx` (refactored to use AgentPrompt)
- `frontend/src/components/chat/QuestionPrompt.tsx` (refactored to use AgentPrompt)
- `frontend/src/components/chat/MessageList.tsx` (trailingPrompts prop + empty-state guard + scroll trigger)
- `frontend/src/components/chat/MessageList.test.tsx` (+4 tests)
- `frontend/src/components/chat/ChatView.tsx` (route prompts into MessageList, remove external div)
- `frontend/src/components/chat/ChatView.test.tsx` (+4 tests: 2 inline-rendering + 2 integration with real PermissionPrompt/QuestionPrompt)
