# PRR: Contribute API Key Auth Endpoint to opencode

**Date:** 2026-06-02
**Status:** Draft
**Type:** Upstream contribution (anomalyco/opencode)
**Motivation:** Enable hot-reload of LLM provider credentials without session disposal

---

## Problem

opencode has two ways to inject provider credentials:

1. **Config file (`PATCH /global/config`)** — Writes merged config, then calls `disposeAllInstancesAndEmitGlobalDisposed`. All active sessions are killed. Users lose in-flight conversations.

2. **Auth service (`Auth.create`)** — Writes to `account.json`, emits `Event.Switched`, AccountPlugin triggers `catalog.transform` which updates provider credentials in-memory. Sessions are preserved.

Path 2 is the correct hot-reload mechanism, but it has **no HTTP endpoint** for API key credentials. The only HTTP surface for auth is OAuth flows (`POST /provider/:providerID/oauth/authorize` + callback). Direct API key injection is only available internally via `Auth.create({type: "api", key: "..."})`.

This forces external orchestrators (like LLMSafeSpace's workspace-agentd) to use path 1, destroying sessions every time credentials are rotated.

---

## Proposed Change

Add a new HTTP endpoint to the provider API group:

```
POST /provider/:providerID/auth/key
```

### Request Body

```json
{
  "key": "sk-ant-api03-...",
  "metadata": {                    // optional
    "baseURL": "https://custom.endpoint/v1"
  }
}
```

### Behavior

1. Validate that `key` is non-empty string
2. Call `Auth.create({ serviceID: providerID, credential: { type: "api", key, metadata } })`
3. This triggers:
   - Write to `account.json`
   - `Event.Switched` emission
   - `AccountPlugin.catalog.transform` → sets `provider.options.aisdk.provider.apiKey`
   - Catalog state updates live
4. Return the created `Auth.Info` (minus the raw key — return `id` and `serviceID` only)

### Response

```json
{
  "id": "acc_01J...",
  "serviceID": "anthropic",
  "description": "default"
}
```

### Errors

| Status | Condition |
|--------|-----------|
| 400 | Missing `key` field or empty string |
| 400 | Invalid `providerID` (not a known or configured provider) |
| 500 | Failed to write auth file |

---

## Why This Endpoint Doesn't Exist Today

opencode's auth model was designed for interactive flows:
- OAuth: User clicks "Connect" in TUI → browser redirect → callback
- API keys: User types key into TUI prompt → directly calls `Auth.set()`

There's no use case in the standalone opencode TUI for programmatic API key injection. The gap exists because opencode was designed as a standalone tool, not as an embedded agent runtime managed by an orchestrator.

---

## Alternative Considered: Write to `account.json` directly

We could bypass the HTTP API and write to `~/.local/share/opencode/account.json` directly from workspace-agentd.

**Rejected because:**
- `Auth.create()` emits `Event.Switched` which triggers `catalog.transform`. Writing the file directly doesn't trigger this event — the catalog won't update until next instance creation.
- File format is an internal implementation detail subject to change
- Violates process boundaries (agentd shouldn't know opencode's data layout)

---

## Implementation Plan

### In opencode (upstream PR)

**File: `packages/opencode/src/server/routes/instance/httpapi/groups/provider.ts`**

Add endpoint to `ProviderApi`:

```typescript
HttpApiEndpoint.post("setApiKey", `${root}/:providerID/auth/key`, {
  params: { providerID: ProviderV2.ID },
  query: WorkspaceRoutingQuery,
  payload: Schema.Struct({
    key: Schema.String.pipe(Schema.nonEmptyString()),
    metadata: Schema.optional(Schema.Record(Schema.String, Schema.String)),
  }),
  success: described(Schema.Struct({
    id: Auth.ID,
    serviceID: Auth.ServiceID,
  }), "API key credential created"),
  error: ProviderAuthApiError,
})
```

**File: `packages/opencode/src/server/routes/instance/httpapi/handlers/provider.ts`**

Add handler:

```typescript
const setApiKey = Effect.fn("ProviderHttpApi.setApiKey")(function* (ctx) {
  const { providerID } = ctx.params
  const { key, metadata } = ctx.payload
  
  const account = yield* auth.create({
    serviceID: Auth.ServiceID.make(providerID),
    credential: new Auth.ApiKeyCredential({
      type: "api",
      key,
      metadata,
    }),
  })
  
  if (!account) {
    return yield* Effect.fail(new ProviderAuthApiError({
      name: "BadRequest",
      data: { providerID, message: "Failed to create credential" },
    }))
  }
  
  return { id: account.id, serviceID: account.serviceID }
})
```

### In LLMSafeSpace (after upstream merges)

Replace `PATCH /global/config` with:

```go
// For each staged provider:
POST http://localhost:{port}/provider/{providerID}/auth/key
Body: {"key": "sk-...", "metadata": {"baseURL": "..."}}
```

This eliminates session disposal entirely.

---

## Acceptance Criteria

1. `POST /provider/anthropic/auth/key` with valid key → 200, provider appears in `GET /provider` list as enabled
2. Active session before key injection continues working after injection (session NOT disposed)
3. New sessions use the injected key immediately
4. `POST /provider/anthropic/auth/key` with empty key → 400
5. Calling the endpoint multiple times updates (not duplicates) the credential for that provider
6. Credential persists across opencode restart (written to `account.json`)

---

## Questions for Upstream Maintainers

1. **Is this endpoint welcome?** opencode is moving toward being embedded in orchestrators (VS Code extension, web IDE, LLMSafeSpace). Programmatic credential management seems aligned with this direction.

2. **Should this replace or complement OAuth?** For providers that support both OAuth and API keys (e.g., Anthropic via Console), should the endpoint coexist with the OAuth flow?

3. **Should there be a `DELETE /provider/:providerID/auth/key`?** For credential revocation without using the generic `Auth.remove()`.

4. **Naming preference:** `POST /provider/:providerID/auth/key` vs `PUT /provider/:providerID/credential` vs `POST /provider/:providerID/authenticate`?

---

## Timeline

- **Step 2 (now):** Ship with `PATCH /global/config`. Sessions lost on credential change. Acceptable for MVP.
- **Upstream PR:** Submit after Step 2 ships. Low urgency — session loss is annoying but not blocking.
- **Step 3 (after merge):** Switch agentd to use `POST /provider/:providerID/auth/key`. Sessions preserved.
