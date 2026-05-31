# Worklog: Epic 16 US-16.8–16.12 — Frontend Agent Input Requests

**Date:** 2026-05-31
**Session:** Implement the frontend half of Epic 16 (agent question/permission prompts)
**Status:** Complete

---

## Objective

Ship the frontend stories US-16.8 through US-16.12 so that when the agent asks a question or requests permission, the browser renders interactive prompts and the user can respond.

---

## Stated assumptions and validation

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | Backend already emits normalized `agent.question` / `agent.permission` SSE events | Verified: worklog 0096 fixed the envelope extraction bug; backend publishes correctly |
| A2 | The `WorkspaceStreamEvent` union in types.ts needs extending for the new event types | Verified: only had `WorkspacePhaseEvent | SessionStatusEvent | OpenCodeEvent` |
| A3 | ChatView accepts a `prompts` slot for rendering above the Composer | Implemented: added optional `prompts?: React.ReactNode` prop |
| A4 | The existing `handleSSEEvent` callback in ChatPage can be extended with new `else if` branches | Verified: clean extension point after the `opencode.event` block |
| A5 | React Query pre-seeded cache is needed for integration tests (avoids async query resolution timing) | Verified: tests failed with `mockResolvedValue` alone; pre-seeding `setQueryData` makes tests deterministic |

---

## Work Completed

### US-16.8: Frontend Types + API Client
- Added `QuestionOption`, `QuestionInfo`, `QuestionRequest`, `PermissionRequest` types to `api/types.ts`
- Added `AgentQuestionEvent`, `AgentQuestionResolvedEvent`, `AgentPermissionEvent`, `AgentPermissionResolvedEvent` SSE event types
- Updated `WorkspaceStreamEvent` union to include all new event types
- Created `api/input.ts` with `inputApi` methods: `questionReply`, `questionReject`, `permissionReply`, `listQuestions`, `listPermissions`
- Created `api/input.test.ts` with 6 tests (all pass)

### US-16.9: QuestionPrompt Component
- Created `components/chat/QuestionPrompt.tsx`
- Single/multi-select options with toggle behavior
- Custom text input for every question
- Submit (disabled until all answered) and Dismiss buttons
- Loading state, error display
- Created `components/chat/QuestionPrompt.test.tsx` with 11 tests (all pass)

### US-16.10: PermissionPrompt Component
- Created `components/chat/PermissionPrompt.tsx`
- Shows permission type (mapped to human-readable labels) and patterns
- Three actions: Allow once, Allow always, Deny
- Deny shows feedback input on first click, confirms on second
- Created `components/chat/PermissionPrompt.test.tsx` with 8 tests (all pass)

### US-16.11: ChatPage Integration
- Added `pendingQuestions` and `pendingPermissions` state
- Extended `handleSSEEvent` to handle `agent.question`, `agent.question.resolved`, `agent.permission`, `agent.permission.resolved`
- Deduplication by request ID
- Session filtering (only show prompts for current session)
- Renders prompts via ChatView's `prompts` prop

### US-16.12: Clear Prompts on Session Idle/Error
- `session.status: idle` for current session → clear both arrays
- `session.error` for current session → clear both arrays
- Different session's idle/error → no-op

### Integration Tests
- Created `pages/ChatPage.input.test.tsx` with 11 integration tests covering the full lifecycle
- Pre-seeded React Query cache for deterministic rendering

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Add `prompts` prop to ChatView rather than rendering in ChatPage directly | Keeps prompts positioned correctly (above Composer, below messages) without breaking ChatView's layout encapsulation |
| Pre-seed query cache in integration tests | React Query async resolution creates flaky timing; pre-seeding makes tests deterministic and fast (192ms for 11 tests) |
| Use `?? []` / `?? ""` for array access in QuestionPrompt | TypeScript strict mode requires safe access; avoids `!` assertions |
| Deny requires two clicks (show feedback → confirm) | Matches US-16.10 spec: feedback is optional but the UI should make it easy to provide |

---

## Tests Run

```bash
# All frontend tests
cd frontend && npx vitest run
  → 537 tests pass (71 files), 0 regressions

# TypeScript
cd frontend && npx tsc --noEmit
  → clean (0 errors)
```

---

## Files Modified

- `frontend/src/api/types.ts` — added 7 new types + extended WorkspaceStreamEvent union
- `frontend/src/api/input.ts` — NEW: API client for question/permission endpoints
- `frontend/src/api/input.test.ts` — NEW: 6 tests
- `frontend/src/components/chat/QuestionPrompt.tsx` — NEW: question prompt component
- `frontend/src/components/chat/QuestionPrompt.test.tsx` — NEW: 11 tests
- `frontend/src/components/chat/PermissionPrompt.tsx` — NEW: permission prompt component
- `frontend/src/components/chat/PermissionPrompt.test.tsx` — NEW: 8 tests
- `frontend/src/components/chat/ChatView.tsx` — added `prompts` prop
- `frontend/src/pages/ChatPage.tsx` — state, event handling, prompt rendering
- `frontend/src/pages/ChatPage.input.test.tsx` — NEW: 11 integration tests

---

## Next Steps

1. **US-16.13: E2E integration tests** — Playwright tests exercising the full flow (requires running API + frontend)
2. **Deploy and validate** — Build new frontend image, deploy, trigger a question/permission in a live workspace
3. **Polish** — Keyboard navigation (Escape to dismiss), animations, responsive layout
