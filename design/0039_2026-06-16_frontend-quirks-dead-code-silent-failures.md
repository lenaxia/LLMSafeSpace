# Frontend — Quirks, Dead Code, and Silent Failures (Aggressive Pass)

**Date:** 2026-06-16
**Status:** Analysis / proposal only — no code changes
**Scope:** `frontend/` — the "weird quirks and implementation weirdness" the user flagged. This is the aggressive pass that `0037` and `0038` were too conservative to surface.
**Companion to:** `0037` (busy indicators), `0038` (broader findings)

---

## 0. Method

After the user's pushback ("really? you didn't find any other weird quirks?"), I ran targeted pattern searches for the fingerprints of LLM-generated code drift: lint-suppressions, type escapes, stray console logs, empty catch blocks, comment archaeology (worklog/bug references), duplicate API methods, dead exports, parallel implementations, and silent failures. Every item below was validated by reading the code and (where it touched the backend) cross-checking against the Go side. No item is included on suspicion alone.

---

## 1. Silent UX failure: workspace "Auto-Suspend" settings are discarded [Severity: High | Confidence: High]

**The most serious finding in this pass.** This is not dead code — it is a live, user-facing feature that silently does nothing.

- `WorkspaceSettingsDrawer.tsx:31-32, 111-130` collects `autoSuspend` (Toggle) and `idleMinutes` (NumberInput, 5–10080) from the user, with a warning banner ("Disabling auto-suspend will keep this workspace running indefinitely, consuming compute minutes and potentially causing unexpected costs") at `:119`.
- On Save, the drawer calls `onSave({ autoSuspendEnabled, autoSuspendIdleMinutes })` (`WorkspaceSettingsDrawer.tsx:54-57`).
- The caller wires it to **nothing**: `Sidebar.tsx:421` — `onSave={async () => {}}`. The settings object is received and dropped on the floor.
- The backend feature is **real and enforced**: `pkg/crds/workspace_crd.yaml:78` defines `spec.autoSuspend`; `api/internal/services/workspace/workspace_service.go:867-877` populates it from the **global** instance settings (`workspace.autoSuspend.enabled`, `workspace.autoSuspend.idleTimeoutMinutes` in `pkg/settings/schema.go:66-67`); the controller honours it.

So: the user sees a per-workspace Auto-Suspend toggle, can flip it, gets a confirmation Save, and the workspace behaves per the *global* default regardless. There is **no per-workspace autoSuspend API path** in the backend at all (grep finds the CRD field but no PATCH/PUT handler that accepts it from the frontend).

**This is worse than dead code** — it actively misleads. A user who disables auto-suspend expecting their workspace to stay up may find it suspended anyway; a user who expects their "12 hour" per-workspace timeout is actually getting the global default.

**Proposal Q1 — Either wire it or remove it. Do not leave it as-is.**
- **Wire**: requires a backend PATCH endpoint for `spec.autoSuspend` per workspace + frontend `onSave` PUT. Non-trivial; needs its own story.
- **Remove**: delete the Auto-Suspend section from `WorkspaceSettingsDrawer.tsx:111-130` and the unused `WorkspaceSettings` shape. Direct users to the global instance setting in admin settings.

Either is defensible; the current state is not. **Ask the user which.**

---

## 2. `useChatStream`'s `AbortController` is dead theater [Severity: Medium | Confidence: High]

`useChatStream.ts:15,49,123,129-131` creates an `AbortController`, stores it in a ref, and exposes `abort()` which calls `abortRef.current?.abort()`. The UI wires a prominent red "Stop generating" button to it (`Composer.tsx:72-80`).

But: the controller's `signal` is **never passed to anything**. `sendAsync` (`useChatStream.ts:59-62`) does not accept a signal; `messages.ts:88` `sendAsync` does not accept one either. So `abort()` aborts nothing — the in-flight POST completes regardless of how many times the user mashes Stop.

The thing that *actually* stops the agent is a **separate** call in `ChatPage.tsx:953`: `workspacesApi.abortSession(workspaceId, sessionId)` — a server-side abort that tells the proxy/opencode to stop. `useChatStream.abort()` is called right after it (`ChatPage.tsx:954`) but does nothing useful.

Net effect today: the Stop button works (because `abortSession` works), but the `AbortController` machinery in `useChatStream` is pure cargo-cult — 5 lines + a ref + a useState cycle that accomplish nothing. A maintainer reading `useChatStream` would reasonably believe `abort()` cancels the network request; it does not.

**Proposal Q2 — Delete the `AbortController` from `useChatStream`.** Keep the `abort` name in the return shape (callers use it) but make it a no-op or remove the call site at `ChatPage.tsx:954` (the `abortSession` call is what matters). Add a comment at `ChatPage.tsx:953` explaining that server-side abort is the real mechanism. If you want client-side cancellation of the POST too, pass the signal into `messages.ts:sendAsync` and onward to `api.post` (the `api` client supports it) — but that is a separate, opt-in change.

---

## 3. Two parallel data-fetching paradigms: TanStack Query vs hand-rolled `useState` [Severity: Medium | Confidence: High]

The chat/sidebar half of the app uses TanStack Query idiomatically (`useQuery`, `useMutation`, cache invalidation, functional `setQueryData`). The **entire settings + org-admin surface** does not. **15 components** hand-roll the same `useState(true)` loading + `useState('')` error + `useEffect` fetch + `try/catch/finally` pattern:

```
src/components/org-admin/OrgAdminLayout.tsx:11
src/components/org-admin/OrgMembersTab.tsx:22
src/components/org-admin/OrgWorkspacesTab.tsx:15
src/components/org-admin/OrgAuditTab.tsx:14
src/components/org-admin/OrgCredentialsTab.tsx:19
src/components/settings/RelayTab.tsx:8
src/components/settings/RelayStatusDashboard.tsx:21
src/components/settings/OrgSettingsTab.tsx:148
src/components/settings/AdminProviderCredentialsTab.tsx:38
src/components/settings/UserProviderCredentialsTab.tsx:41
src/components/settings/ApiKeysTab.tsx:20
src/components/settings/SecretsTab.tsx:39
src/components/settings/AdminCredentialsTab.tsx:18
src/components/settings/RelaySetupWizard.tsx:9
src/components/settings/UserSettingsTab.tsx:11
```

33 separate `setLoading(true)/setLoading(false)` call sites. The cost of this divergence is real:
- **No caching.** Every tab mount re-fetches. Navigating away and back re-hits the API.
- **No background refetch / stale-while-revalidate.** Settings data is fetched once and goes stale silently.
- **No request deduplication.** Two components mounting the same data race.
- **No invalidation hooks.** After a mutation in one tab, sibling tabs don't refresh until remount.
- **Inconsistent error UX.** Each component rolls its own `{error && <p className="text-red-500">…</p>}`.
- **`useUserSettings` (`hooks/useUserSettings.ts`) went a third way** — a custom `useSyncExternalStore` + singleton store + localStorage. That one is at least justified (cross-component shared mutable state with persistence), but it adds a *third* state paradigm alongside Query and hand-rolled.

This is the canonical fingerprint of "100% LLM-developed": one prompt chain invented the Query-based chat, another invented the useState-based settings, and nobody reconciled them.

**Proposal Q3 — Migrate the settings/org-admin surface to TanStack Query.** This is the right level of abstraction (one state paradigm for server data, project already depends on Query) and a strict improvement (caching, dedup, invalidation come for free). It is non-trivial in volume (15 files) but mechanical in kind — each migration follows the same shape. Do it as one focused PR per tab or one epic with a migration checklist. Not urgent, but it should be on the roadmap.

---

## 4. Dead-code inventory (beyond `0037` F4/F5)

`0037` already documented the dead `SessionItem`/`SessionList`/`workspace/WorkspaceSessionList` cluster and the dead `api/events.ts` BroadcastChannel client. This pass found more:

### 4.1 `lib/stream.ts` — the entire HTTP-streaming parser is dead [Severity: Medium]

`lib/stream.ts` (130 lines + `stream.test.ts` + `stream.edge.test.ts`) implements `extractStreamText` / `ParsedStreamResult` for parsing a progressively-streamed JSON response from the proxy. Grep for `extractStreamText` and `ParsedStreamResult` outside the file and its tests returns **nothing**. ChatPage's `parseStreamEvent` (`ChatPage.tsx:391`) is a **different function** that parses SSE *events*, not HTTP stream chunks. The streaming-rendering approach was replaced by SSE-based rendering (`message.part.delta`/`message.part.updated` events) and the old chunk parser was orphaned.

### 4.2 Two dead API methods with a latent type bug [Severity: Low]

`api/workspaces.ts` defines three session-list methods hitting two endpoints:
- `getSessions` (`:54`) → `GET /workspaces/:id/sessions` → `SessionListItem[]` — **used** (by Sidebar inline `useQuery`).
- `getActiveSessions` (`:55`) → `GET /workspaces/:id/sessions/active` → `ActiveSessionsResponse` — **dead** (grep finds no production caller).
- `getWorkspaceSessions` (`:56`) → `GET /workspaces/:id/sessions` → **`WorkspaceListItem[]`** — **dead AND wrong-typed**. Same endpoint as `getSessions` but typed as `WorkspaceListItem[]` (workspace objects) instead of `SessionListItem[]` (session objects). A latent bug if anyone ever reaches for it.

### 4.3 Two dead hooks [Severity: Low]

- `hooks/useSessions.ts` — exports `useSessions(workspaceId)`. The only reference outside the file is a comment in `ChatPage.tsx:127` ("Sidebar/useSessions owns the fetch lifecycle"). Sidebar actually fetches inline (`Sidebar.tsx:445`), not via this hook.
- `hooks/useWorkspaces.ts` — exports both `useWorkspaces` (the list hook) and `useWorkspaceStatus`. Only `useWorkspaceStatus` is imported anywhere (`ChatPage.tsx:6`). `useWorkspaces` is dead; the workspace list is fetched via inline `useQuery` in `ChatPage.tsx:64` and `Sidebar.tsx`.

### 4.4 Duplicate session-creation types [Severity: Low]

`api/workspaces.ts:12` defines `EnsureSessionResponse { workspaceId, workspacePhase, sessionId, resumed }`. `api/sessions.ts:3` defines `CreateSessionResponse { sessionId, workspaceId, workspacePhase, resumed }` — same fields, same shape, different name. Both wrap `POST /workspaces/:id/sessions/new`. Only `sessions.ts`'s version is used (`ChatPage.tsx:171`); `workspaces.ts`'s `ensureSession` method is dead. Pick one.

**Proposal Q4 — Delete the items in §4 in one cleanup PR.** Pure removal, zero risk, removes several misleading parallel implementations that could confuse future LLM (or human) contributors.

---

## 5. Two `markSessionSeen` effects for the same navigation [Severity: Low | Confidence: High]

`ChatPage.tsx:85-112` has two effects that both fire on `sessionId`/`workspaceId` change:

- **Effect 1 (`:85-93`)**: when the new session is ready, immediately calls `markSessionSeen(workspaceId, sessionId)` for the **new** session (`:90`).
- **Effect 2 (`:98-112`)**: on the same change, looks up `prevSessionRef.current` (the **old** session) and schedules a 1-second-debounced `markSessionSeen(wsId, sId)` for it (`:102-104`).

So a single navigation triggers two server `PUT .../seen` calls — one immediate for the new session, one delayed for the old. The intent (likely: "make sure the previous session gets a final seen stamp even if the user navigates quickly") is plausible but undocumented, and the dual logic is the kind of thing that grows when two attempts to solve mark-seen racing are layered rather than reconciled. The `eslint-disable-line react-hooks/exhaustive-deps` on effect 1 (`:93`) suggests the deps were massaged to avoid a loop rather than designed.

**Not a bug** — both calls are idempotent PUTs and the cost is one extra cheap request per navigation. But it is a quirk worth a comment or a consolidate.

---

## 6. No route-level code splitting [Severity: Low | Confidence: High]

`router.tsx` eagerly imports every page: `ChatPage`, `SettingsPage`, `OrgAdminLayout`, and all five `Org*Tab` components, plus `LoginPage`/`RegisterPage`. There is no `React.lazy` / `Suspense`. The entire org-admin section (which most users never visit) ships in the initial bundle.

For a tool where the primary surface is `/chat`, this is wasteful. The org-admin tabs in particular pull in their own (hand-rolled, per §3) data-fetch logic and would be natural lazy-boundary candidates.

**Proposal Q5 — Lazy-load the org-admin routes (and Settings).** Low effort, measurable bundle-size win. The `RequireAuth`/`GuestOnly` wrappers already handle a loading state via `<Spinner>`, so the `Suspense` fallback can reuse it.

---

## 7. Minor consistency quirks

### 7.1 Inconsistent `ui/` barrel usage [Severity: Low]

`components/ui/index.ts` exports `Button, Input, Card, Badge, Spinner, KebabMenu, LazyDetails, Tooltip`. Only **2 files** use the barrel (`LoginForm.tsx:3`, `RegisterForm.tsx:3`); **9+ files** import directly (`../ui/Button`, `../ui/Spinner`, etc.). Pick one convention. (Direct imports are slightly better for tree-shaking and refactor clarity; the barrel is slightly better for grep-ability. Either is fine; mixing is the issue.)

### 7.2 `WorkspaceSettingsDrawer` bypasses the typed API layer [Severity: Low]

`WorkspaceSettingsDrawer.tsx:37-38, 75` calls `api.get/api.put` directly for `/workspaces/:id/bindings` instead of going through a typed `workspacesApi.getBindings/setBindings`. This is the **only** component in the codebase that reaches past the `*Api` objects into raw `api`. The bindings endpoints have no typed wrapper at all — a gap in `api/workspaces.ts`.

### 7.3 Inconsistent SSE reconnect constants [Severity: Low]

Already noted in `0038` §7 but worth restating in the quirks context: `MIN_RECONNECT_MS` is `2000` in `useEventStream.ts:5`, `1000` in `useUserEventStream.ts:7` and `sseConnection.ts:15` (the default). `MAX_RECONNECT_MS` is `30000` in all three (consistent). The min value should be a single shared constant — the two streams reconnect on meaningfully different schedules for no documented reason.

### 7.4 Stray `console.log` in `useSessionTitle` [Severity: Low]

`useSessionTitle.ts:39` — `console.log("[SessionTitle] streaming ended, refetching title for", sessionId)`. Unlike `wsLog` (gated as a deliberate timing logger, per `0038` P5), this is an unstructured debug log left in production. One-liner to remove.

### 7.5 Five `eslint-disable react-hooks/exhaustive-deps` [Severity: Low]

- `ThemeProvider.tsx:51`, `ModelSelector.tsx:77`, `ChatPage.tsx:93`, `ChatPage.tsx:184` — each suppresses the exhaustive-deps warning. Each is a yellow flag: sometimes legitimate (intentionally reading a ref / avoiding a loop), sometimes a real missing dependency masquerading as intentional. Worth a per-site audit: for each, either add the dep or add a comment explaining why it's excluded. `ChatPage.tsx:93` in particular (the first `markSessionSeen` effect, see §5) is suspicious.

---

## 8. What I deliberately did NOT flag

To avoid crying wolf:

- **The many empty `catch {}` / `.catch(() => {})` blocks** (33 sites). Most are legitimately fire-and-forget for non-critical side effects (`markSessionSeen`, `setUserSetting`, clipboard writes, alert-blocked fallbacks). A few *might* warrant logging (e.g. `ChatPage.tsx:329` swallows reconcileOnIdle errors with "stale state self-corrects" — defensible but worth a `console.warn` in dev). Not a systemic issue; case-by-case.
- **The `as unknown as` casts** — grep found none in production code. Clean.
- **`@ts-ignore` / `@ts-expect-error`** — grep found none. Clean.
- **`TODO`/`FIXME`/`HACK`** — grep found none (the two hits were `xxxxx` placeholders in `RelaySetupWizard`, not markers). Clean.

The absence of these common smell-signals is itself worth noting: the code is *typed* cleanly and free of explicit "fix me" markers. The quirks are structural (parallel implementations, silent UX failures, dead layers) rather than syntactic — consistent with LLM code that passes lint and typecheck but accumulates architectural drift across independent sessions.

---

## 9. Summary of new proposals (prioritised)

| ID | Problem | Severity | Complexity | Action |
|---|---|---|---|---|
| **Q1** | Auto-Suspend UI silently discards user input | **High** | Med (wire) / Trivial (remove) | **Ask user: wire or remove?** Do not leave as-is. |
| **Q2** | `useChatStream` AbortController does nothing | Medium | Trivial | Delete the controller; document server-side abort as the real path |
| **Q3** | 15 settings/org-admin components hand-roll fetch state | Medium | High volume / mechanical | Migrate to TanStack Query as a focused epic |
| **Q4** | Dead: `lib/stream.ts`, 2 API methods, 2 hooks, 1 dup type | Medium | Trivial | One cleanup PR |
| **Q5** | No route code-splitting | Low | Trivial | Lazy-load org-admin + settings |
| — | §5 dual markSessionSeen, §7 barrel/raw-api/constant inconsistencies, stray log, eslint-disables | Low | Trivial | Opportunistic cleanup |

**Highest-impact single action:** Q1 (the auto-suspend silent failure). It is the only item that is actively misleading end users right now.

---

## 10. Files examined (additional to 0037/0038)

- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx`
- `frontend/src/components/layout/Sidebar.tsx:408-412` (the `onSave={async () => {}}` site)
- `frontend/src/hooks/useChatStream.ts` (AbortController)
- `frontend/src/api/messages.ts` (sendAsync signal)
- `frontend/src/lib/stream.ts` + tests
- `frontend/src/api/workspaces.ts:54-56` (dead methods)
- `frontend/src/api/sessions.ts` (duplicate type)
- `frontend/src/hooks/useSessions.ts`, `useWorkspaces.ts`
- `frontend/src/router.tsx`
- `frontend/src/components/ui/index.ts`
- `frontend/src/components/org-admin/OrgWorkspacesTab.tsx` (sample of hand-rolled pattern)
- `frontend/src/hooks/useSessionTitle.ts:39`
- `api/internal/services/workspace/workspace_service.go:867-877` (backend autoSuspend)
- `pkg/crds/workspace_crd.yaml:78`, `pkg/settings/schema.go:66-67`
