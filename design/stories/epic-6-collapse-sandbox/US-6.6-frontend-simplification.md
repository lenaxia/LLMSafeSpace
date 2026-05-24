# US-6.6: Frontend Simplification

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.5

## Objective

Remove all sandbox awareness from the frontend. After this story, the frontend talks exclusively in workspace IDs for session, message, and event operations. No sandbox resolution, no sandbox state tracking.

## User Interaction Scenarios (After)

These are the complete user flows the frontend must support:

### 1. First-time user
1. Register → login → empty workspace list
2. Click "+" → NewWorkspaceDialog → `POST /workspaces` → workspace created (phase: Pending → Creating → Active)
3. Auto-call `POST /workspaces/:id/sessions/new` → returns `{ workspaceId, workspacePhase, sessionId, resumed }`
4. Navigate to `/chat/:workspaceId/:sessionId`
5. Workspace transitions to Active → chat enabled

### 2. Returning user with active workspace
1. Login → workspace list shows workspaces with phases
2. Click workspace → load sessions via `GET /workspaces/:id/sessions`
3. Click session → load history via `GET /workspaces/:id/sessions/:sid/message`
4. Send message via `POST /workspaces/:id/sessions/:sid/message`
5. SSE events via `GET /workspaces/:id/events`

### 3. Returning user with suspended workspace
1. Click workspace → status shows "Suspended"
2. SuspendedBanner shown with "Activate" button
3. Click Activate → `POST /workspaces/:id/activate` → workspace resumes
4. Poll status until Active → chat enabled

### 4. New session on existing workspace
1. Click "+" in sessions panel → `POST /workspaces/:id/sessions/new`
2. EnsureSession handles: if suspended → resumes; waits for Active; creates session
3. Navigate to new session

### 5. Workspace creating/resuming (transitional states)
1. Status shows "Creating" or "Resuming" with spinner
2. Poll `GET /workspaces/:id/status` until phase = Active
3. Once Active → enable chat input

## Current Sandbox References (Must Remove)

### `frontend/src/api/sessions.ts`
```typescript
// BEFORE: routes through sandbox
export const sessionsApi = {
  create: (sandboxId: string, title?: string) =>
    api.post<{ id: string }>(`/sandboxes/${sandboxId}/sessions`, title ? { title } : {}),
};
```
**AFTER:** Delete this file entirely. Session creation goes through `workspacesApi.ensureSession()` or the proxy route `POST /workspaces/:id/sessions`.

### `frontend/src/api/messages.ts`
```typescript
// BEFORE: routes through sandbox
export const messagesApi = {
  getHistory: (sandboxId: string, sessionId: string) =>
    api.get<Message[]>(`/sandboxes/${sandboxId}/sessions/${sessionId}/message`),
  send: (sandboxId: string, sessionId: string, req: SendMessageRequest) =>
    streamRequest(`/sandboxes/${sandboxId}/sessions/${sessionId}/message`, req),
};
```
**AFTER:**
```typescript
export const messagesApi = {
  getHistory: (workspaceId: string, sessionId: string) =>
    api.get<Message[]>(`/workspaces/${workspaceId}/sessions/${sessionId}/message`),
  send: (workspaceId: string, sessionId: string, req: SendMessageRequest) =>
    streamRequest(`/workspaces/${workspaceId}/sessions/${sessionId}/message`, req),
};
```

### `frontend/src/api/events.ts`
```typescript
// BEFORE: SSE connects to sandbox
eventSource = new EventSource(`${apiBaseUrl}/sandboxes/${sandboxId}/events`, ...);
// sendBeacon abort:
const url = `${apiBaseUrl}/sandboxes/${sandboxId}/sessions/${sessionId}/abort`;
```
**AFTER:**
```typescript
eventSource = new EventSource(`${apiBaseUrl}/workspaces/${workspaceId}/events`, ...);
const url = `${apiBaseUrl}/workspaces/${workspaceId}/sessions/${sessionId}/abort`;
```
Function signature: `createEventStream(workspaceId: string, ...)` (was `sandboxId`).

### `frontend/src/api/workspaces.ts`
Remove:
- `createSandbox(workspaceId, runtime)` — no longer needed
- `getSandboxes(id)` — no longer needed

Update `ensureSession` response type:
```typescript
// BEFORE
export interface EnsureSessionResponse {
  sandboxId: string;
  sandboxPhase: string;
  sessionId: string;
  resumed: boolean;
}
// AFTER
export interface EnsureSessionResponse {
  workspaceId: string;
  workspacePhase: string;
  sessionId: string;
  resumed: boolean;
}
```

### `frontend/src/api/types.ts`
Remove:
- `SandboxListItem` interface

### `frontend/src/hooks/useEventStream.ts`
```typescript
// BEFORE: takes sandboxId
export function useEventStream(sandboxId: string | undefined, onEvent: ...)
// AFTER: takes workspaceId
export function useEventStream(workspaceId: string | undefined, onEvent: ...)
```
EventSource URL: `/workspaces/${workspaceId}/events`

### `frontend/src/hooks/useMessageHistory.ts`
```typescript
// BEFORE: takes sandboxId
export function useMessageHistory(sandboxId: string | undefined, sessionId: string | undefined)
// AFTER: takes workspaceId
export function useMessageHistory(workspaceId: string | undefined, sessionId: string | undefined)
```
Query key: `["messages", workspaceId, sessionId]`

### `frontend/src/hooks/useChatStream.ts`
```typescript
// BEFORE: takes sandboxId
export function useChatStream(sandboxId: string | undefined, sessionId: string | undefined)
// AFTER: takes workspaceId
export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined)
```
Calls `messagesApi.send(workspaceId, sessionId, ...)` and `registerTabCloseAbort(workspaceId, sessionId)`.

### `frontend/src/hooks/useWorkspaces.ts`
Remove: `useWorkspaceSandboxes(workspaceId)` hook.

### `frontend/src/pages/ChatPage.tsx`
**Major rewrite.** Currently:
1. Fetches `useWorkspaceSandboxes(workspaceId)` → extracts `sandboxes?.[0]`
2. Checks `sandbox.phase === "Running"` → derives `sandboxId`
3. Passes `sandboxId` to `useChatStream`, `useMessageHistory`, `useEventStream`
4. Auto-creates session via `sessionsApi.create(sandboxId)`

**After:**
1. Fetches `useWorkspaceStatus(workspaceId)` → checks `status.phase === "Active"`
2. If Active → workspace IS the proxy target (workspaceId)
3. Passes `workspaceId` to `useChatStream`, `useMessageHistory`, `useEventStream`
4. Auto-creates session via `workspacesApi.ensureSession(workspaceId)`
5. No sandbox concept anywhere

### `frontend/src/components/layout/Sidebar.tsx`
Already mostly workspace-centric. Remove:
- The `sandboxId` from `createMutation` response handling (use `workspaceId` only)

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/api/sessions.ts` | **Delete** — session creation via ensureSession or proxy |
| `frontend/src/api/messages.ts` | Rekey: `sandboxId` → `workspaceId` in all paths |
| `frontend/src/api/events.ts` | Rekey: `sandboxId` → `workspaceId` in SSE URL and abort beacon |
| `frontend/src/api/workspaces.ts` | Remove `createSandbox`, `getSandboxes`; update `EnsureSessionResponse` type |
| `frontend/src/api/types.ts` | Remove `SandboxListItem` |
| `frontend/src/hooks/useEventStream.ts` | Parameter: `sandboxId` → `workspaceId` |
| `frontend/src/hooks/useMessageHistory.ts` | Parameter: `sandboxId` → `workspaceId` |
| `frontend/src/hooks/useChatStream.ts` | Parameter: `sandboxId` → `workspaceId` |
| `frontend/src/hooks/useWorkspaces.ts` | Remove `useWorkspaceSandboxes` |
| `frontend/src/pages/ChatPage.tsx` | Major rewrite: remove sandbox resolution, use workspaceId directly |
| `frontend/src/api/contract-fixtures.json` | Update to match new response shapes |
| `frontend/src/api/contract.test.ts` | Update contract tests |

## Acceptance Criteria

1. `grep -r "sandbox" frontend/src/` returns zero matches (case-insensitive, excluding node_modules)
2. `npm run build` passes with zero errors
3. `npm run test` passes
4. User flow: login → create workspace → auto-session → send message → receive response
5. User flow: suspended workspace → activate → chat resumes
6. User flow: new session on active workspace
7. SSE events received via `/workspaces/:id/events`
8. Tab close sends abort beacon to `/workspaces/:id/sessions/:sid/abort`
9. Message history loads via `/workspaces/:id/sessions/:sid/message`
10. No `SandboxListItem` type exists in frontend code
