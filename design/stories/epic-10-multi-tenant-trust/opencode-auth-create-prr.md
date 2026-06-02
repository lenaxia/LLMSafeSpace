# PRR: Provider State Hot-Reload After Credential Injection

**Date:** 2026-06-02
**Status:** Draft (Rewritten — previous version based on incorrect assumptions)
**Type:** Upstream contribution (anomalyco/opencode)
**Motivation:** Enable credential rotation without aborting in-flight LLM calls

---

## Problem

When LLMSafeSpace rotates provider credentials (e.g., user updates their Anthropic key), the new key must reach the running opencode instance. Today, the only way to make opencode pick up new credentials is instance disposal — which aborts any in-flight LLM streaming call.

### Current credential injection paths

| Path | Method | In-flight calls | Sessions |
|------|--------|----------------|----------|
| `PATCH /global/config` | Writes config, disposes ALL instances | **Aborted (all)** | History preserved (SQLite), active streams killed |
| `PUT /auth/:providerID` + `POST /instance/dispose` | Writes auth.json, disposes THIS instance | **Aborted (this instance)** | History preserved, active streams killed |
| `PUT /auth/:providerID` alone | Writes auth.json, no state refresh | None affected | Stale — new key not used until instance recreated |

None of these allow: "inject new key → new calls use it → in-flight calls finish naturally."

### Why in-flight calls are aborted

The provider `InstanceState` holds a `Map<hash, SDK>` cache. Each SDK is an AI SDK provider object created with `apiKey` baked in at factory time:

```typescript
const loaded = createAnthropic({ apiKey: "sk-ant-..." })
s.sdk.set(key, loaded)
```

Instance disposal runs `disposeInstance(directory)` which calls all registered disposers, including `ScopedCache.invalidate`. This tears down the entire instance — sessions, tools, LSP, file watchers, and the active prompt execution context.

### Why selective invalidation would work

`InstanceState.invalidate(state)` is exported and invalidates a single `ScopedCache` entry. If called on only the provider state:

- The cache entry is marked stale
- Next `InstanceState.get(state)` recomputes: re-reads `auth.json`, rebuilds provider catalog, creates fresh SDK objects
- The OLD SDK object in memory is NOT garbage collected — any in-flight prompt holding a reference to it continues streaming until natural completion
- Sessions, tools, MCP, LSP, file watchers — all unaffected (separate `InstanceState` instances)

---

## Validated Assumptions

| # | Claim | Evidence | File |
|---|-------|----------|------|
| V1 | `Auth.Service` has `get`, `all`, `set`, `remove` — no `create` method | Interface definition | `src/auth/index.ts:49-53` |
| V2 | `auth.set()` writes to `auth.json` and returns — no events emitted, no state invalidation | Implementation reads file, merges, writes — nothing else | `src/auth/index.ts:69-77` |
| V3 | `PUT /auth/:providerID` already exists in Control API | Route definition + handler | `groups/control.ts:37-49`, `handlers/control.ts:14-20` |
| V4 | Provider state is `InstanceState<State>` — a `ScopedCache` | `InstanceState.make<State>()` call | `src/provider/provider.ts:1194` |
| V5 | SDK objects are cached in `State.sdk: Map<string, BundledSDK>` keyed by hash of `{providerID, npm, options}` | `s.sdk.set(key, loaded)` | `src/provider/provider.ts:1658,1682` |
| V6 | `apiKey` is part of the hash key AND baked into the SDK factory call | `options["apiKey"] = provider.key` before hash + factory | `src/provider/provider.ts:1576,1653` |
| V7 | `InstanceState.invalidate` is exported and invalidates one cache entry | Public export | `src/effect/instance-state.ts:67` |
| V8 | Instance disposal kills ALL `InstanceState` caches via `registerDisposer` → `ScopedCache.invalidate` | Disposal mechanism | `src/effect/instance-state.ts:39-40`, `src/effect/instance-registry.ts:7` |
| V9 | Sessions are persisted in SQLite (survive disposal) | Drizzle ORM with `SessionTable` | `src/session/message-v2.ts:32` |
| V10 | TUI after `auth.set` calls `instance.dispose()` explicitly — no automatic refresh | Client-side disposal | `cli/cmd/tui/component/dialog-provider.tsx:400` |
| V11 | `Provider.Service.Interface` does not expose invalidation | Interface has only `list`, `getProvider`, `getModel`, `getLanguage`, `closest`, `getSmallModel`, `defaultModel` | `src/provider/provider.ts:1004-1020` |
| V12 | The provider `state` variable is local to `Provider.layer` — inaccessible from handlers | Created inside `Layer.effect` closure | `src/provider/provider.ts:1194` |
| V13 | Control API (`PUT /auth/:providerID`) is root-scoped — no instance context, no `Provider.Service` | `ControlApi` in `RootHttpApi`, provided by `rootApiRoutes` without `instanceContextLayer` | `server.ts:119-121` |
| V14 | `ProviderApi` (instance-scoped) already has access to `Provider.Service` | Handler resolves `yield* Provider.Service` | `handlers/provider.ts:45` |
| V15 | `ScopedCache.invalidate` marks entry stale; existing references held by in-flight code are NOT interrupted | Effect's `ScopedCache` is a lookup cache, not a resource manager — invalidation affects next lookup only | `instance-state.ts:67-70` + `session/llm.ts:101` resolves language model ONCE at prompt start |

### Previously incorrect assumptions (from v1 of this PRR)

| # | False claim | Reality |
|---|------------|---------|
| ~~P1~~ | `Auth.create` exists and emits `Event.Switched` | No such method or event exists |
| ~~P2~~ | `AccountPlugin` triggers `catalog.transform` | No `AccountPlugin` or `catalog.transform` in the codebase |
| ~~P3~~ | Path 2 preserves sessions without disposal | TUI explicitly disposes after `auth.set` |
| ~~P4~~ | No HTTP endpoint exists for API key injection | `PUT /auth/:providerID` already exists in Control API |

---

## Proposed Change

Add a `refreshAuth` method to `Provider.Service.Interface` that invalidates the provider `InstanceState`, forcing credential re-read on next access without instance disposal.

### Interface change

```typescript
// src/provider/provider.ts — add to Interface
export interface Interface {
  // ... existing methods ...
  readonly refreshAuth: () => Effect.Effect<void>
}
```

### Implementation

```typescript
// Inside Provider.layer, after state is created:
const refreshAuth = Effect.fn("Provider.refreshAuth")(function* () {
  yield* InstanceState.invalidate(state)
})

// Add to Service.of:
return Service.of({
  list,
  getProvider,
  getModel,
  getLanguage,
  closest,
  getSmallModel,
  defaultModel,
  refreshAuth,  // new
})
```

### Wiring challenge: Control API is root-scoped, Provider is instance-scoped

The `PUT /auth/:providerID` endpoint lives in `ControlApi` → `RootHttpApi` → `rootApiRoutes`. This layer has **no instance context** and no access to `Provider.Service` (which is created per-instance inside `InstanceStore.load()`).

Two options for wiring:

**Option A: Add a new instance-scoped endpoint (preferred)**

Add `POST /provider/refresh` to the existing `ProviderApi` group (which already has instance context + Provider.Service):

```typescript
// src/server/routes/instance/httpapi/groups/provider.ts — add endpoint
HttpApiEndpoint.post("refresh", `${root}/refresh`, {
  query: WorkspaceRoutingQuery,
  success: described(Schema.Boolean, "Provider state refreshed"),
})
```

```typescript
// src/server/routes/instance/httpapi/handlers/provider.ts — add handler
const refresh = Effect.fn("ProviderHttpApi.refresh")(function* () {
  yield* provider.refreshAuth()
  return true
})
```

The caller sequence becomes:
```
PUT /auth/:providerID       → writes auth.json (root, no instance context)
POST /provider/refresh      → invalidates provider state (instance-scoped)
```

**Option B: Trigger invalidation from within Auth.Service.set() via callback**

Register a callback when the provider layer initializes that `Auth.Service` can call after any `set()`:

```typescript
// Provider.layer registers an on-auth-change hook
auth.onSet(() => InstanceState.invalidate(state))
```

This auto-refreshes on every auth write, but requires extending `Auth.Service` interface. More invasive.

**Recommendation:** Option A. Two explicit calls from the orchestrator. The first is already available today (`PUT /auth/:providerID`). The second (`POST /provider/refresh`) is the new endpoint. This keeps the control handler simple, doesn't require architectural changes to the layer system, and fits opencode's existing pattern of explicit instance-scoped operations.

### Behavior (with Option A)

1. `PUT /auth/:providerID` with `{type: "api", key: "sk-new-..."}` → writes `auth.json`
2. `POST /provider/refresh` (with `?directory=...` query for workspace routing) → `provider.refreshAuth()` → `InstanceState.invalidate(state)` → cache entry marked stale
3. Next `InstanceState.get(state)` (triggered by next prompt) → re-reads `auth.json`, rebuilds provider catalog with new key, creates fresh SDK
4. In-flight call still holds reference to old SDK → continues streaming → completes naturally
5. Both calls return 200

### What happens to the `models` cache

The `State` struct contains both `sdk: Map<string, BundledSDK>` and `models: Map<string, LanguageModelV3>`. Both are rebuilt on state recomputation. The old `LanguageModelV3` references held by in-flight prompts remain valid — they're just no longer in the cache.

---

## Alternative Considered: Granular SDK cache invalidation

Instead of invalidating the entire provider `InstanceState`, we could expose only SDK cache clearing:

```typescript
readonly clearSDKCache: (providerID?: ProviderV2.ID) => Effect.Effect<void>
```

**Rejected because:**
- Provider state reads `auth.json` during initialization. Just clearing the SDK cache doesn't re-read credentials from disk — the `provider.key` in state still holds the old value.
- Full `InstanceState` invalidation is correct: it re-reads auth, rebuilds providers, and naturally produces new SDKs with updated keys.
- The cost of full provider state recomputation is ~10-50ms (file reads + SDK factory calls). Acceptable.

---

## Alternative Considered: File watcher on `auth.json`

Auto-detect changes to `auth.json` and invalidate provider state.

**Rejected because:**
- Adds complexity (debouncing, race conditions with concurrent writes)
- opencode's architecture is pull-based (lazy `ScopedCache`), not push-based
- The Control API already knows when auth changes — explicit invalidation is simpler and more predictable
- Would fire even for changes not relevant to the current instance

---

## Acceptance Criteria

1. `PUT /auth/anthropic` with new key → 200 (writes auth.json)
2. `POST /provider/refresh` → 200 (invalidates provider state)
3. Immediately start a prompt → uses new key (fresh SDK created)
4. If a prompt was in-flight during the refresh → it completes without error using the old key
5. After completion, next prompt uses new key
6. Session list before and after is identical (no sessions lost)
7. `GET /provider` after refresh shows provider as connected with new key

---

## Questions for Upstream Maintainers

1. **Is `Provider.refreshAuth()` the right abstraction?** Alternatively, should invalidation be triggered automatically inside `Auth.Service.set()` via a subscriber/hook pattern?

2. **Should refreshAuth be on Provider.Service or a separate RefreshService?** Provider already has 7 methods. Adding one more is minimal, but if other state (MCP, plugins) also needs selective refresh in the future, a centralized approach might be better.

3. **Should the Control API `PUT /auth/:providerID` handler auto-refresh?** Currently it just writes and returns. The question is whether the refresh should be opt-in (caller decides) or automatic (always refresh on write). Automatic is safer for orchestrators.

4. **Naming:** `refreshAuth` vs `invalidate` vs `reloadCredentials`?

---

## Implementation Complexity

- **Lines changed:** ~25 (interface addition, one method in Provider.layer, one new endpoint definition, one new handler)
- **Files touched:** `provider/provider.ts` (interface + implementation), `groups/provider.ts` (endpoint), `handlers/provider.ts` (handler)
- **Risk:** Low. `InstanceState.invalidate` is already used by the disposal system. `ProviderApi` already has instance context and Provider.Service access. No new layers or architectural changes.
- **Testing:** Unit test: call `refreshAuth`, verify next `list()` call returns updated provider state. Integration test: set key via `PUT /auth/:providerID` → call `POST /provider/refresh` → prompt → verify model connects with new key.

---

## Timeline

- **Now (Step 2):** Ship with `PUT /auth/:providerID` + `POST /instance/dispose` (targeted disposal). In-flight calls aborted but this is acceptable for MVP — credential rotation doesn't happen mid-conversation in practice.
- **Upstream PR:** Submit after Step 2 ships. Small, focused change (~25 lines).
- **After merge:** Switch agentd to: `PUT /auth/:providerID` → `POST /provider/refresh`. Two calls, but in-flight prompts preserved. No instance disposal needed.
