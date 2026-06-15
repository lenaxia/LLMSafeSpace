# Epic 39: Provider Model Enrichment — Auto-Fetch Models, Per-Model Metadata, Context Window

**Status:** Design
**Created:** 2026-06-12
**Priority:** High
**Depends on:** Epic 30 (Unified Credential Model) — Epic 30 stabilises the credential storage and injection pipeline that this epic builds on top of
**Related:** Epic 36 (context usage bar — consumes `ContextLimit` output of this epic), docs/0049 (context limit gap), design conversation 2026-06-12 (BYOK endpoint compatibility analysis)

---

## Problem Statement

LLMSafeSpace is a multi-tenant BYOK (bring-your-own-key) system. Users and admins configure LLM providers by supplying an API key and optional base URL. Currently:

1. **Model list is not populated automatically.** When a user supplies credentials for a custom endpoint (e.g. a self-hosted LiteLLM proxy, Ollama, a corporate OpenAI-compatible gateway), the model list is empty until `model_enricher.go` runs at workspace start — and even then only model IDs are fetched, not metadata.

2. **Context window sizes are unknown for BYOK endpoints.** `/v1/model/info` is LiteLLM-specific and does not exist on OpenAI, Anthropic, Ollama, or other standard endpoints. There is no OpenAI-compatible standard for context window sizes. The only way to know a model's context window for an arbitrary endpoint is to ask the operator when they configure the credential.

3. **No per-model metadata is stored.** `LLMModelConfig` has only `id` and `label`. No context limit, output limit, capabilities, or display name beyond what the live `/v1/models` call returns (just IDs). The context bar shows "Unknown" for any model not in opencode's bundled catalog.

4. **The UI gives no feedback about which models a credential provides access to.** After saving a credential, the user sees only a name, provider name, and optional base URL. The model allowlist field is a free-text comma-separated input with no validation against reality.

### Why `/v1/model/info` is not the answer

`GET /v1/model/info` is a LiteLLM proprietary extension. It does not exist on:
- OpenAI (`api.openai.com`) — returns 404
- Anthropic — no OpenAI-compat model info endpoint
- Ollama — returns 404
- Any non-LiteLLM proxy

The OpenAI spec only exposes `GET /v1/models` (list of IDs) and `GET /v1/models/{id}` (id + owned_by + created). Neither carries `context_window` or `max_tokens`. **There is no cross-provider standard for context limits.** The correct design for a BYOK system is to let operators supply this information when configuring a credential.

---

## Goals

1. **Auto-fetch models** when a credential is saved (or base URL is updated). No user action required. A manual refresh button is available as a fallback.
2. **Store per-model metadata** (context window, max output, display name, enabled/disabled) alongside each credential in the database.
3. **Surface the model list in the provider UI** — both user-facing and admin-facing — with editable per-model metadata fields.
4. **Flow context limits through the existing pipeline** so the context bar works for BYOK endpoints without relying on opencode's bundled catalog or LiteLLM-specific endpoints.
5. **Maintain backward compatibility** — existing credentials with no model metadata continue to work; the model enricher at workspace start remains as a fallback discovery mechanism.

---

## Validated Assumptions

| # | Assumption | Evidence |
|---|---|---|
| V1 | `GET /v1/models` is the only reliably cross-provider model list endpoint | Verified against OpenAI spec, Anthropic docs, Ollama API reference, LiteLLM compat matrix. All support it. |
| V2 | `GET /v1/models` returns only `id`, `object`, `created`, `owned_by` — no context limits | Verified: OpenAI response schema, Ollama `/api/tags` adapted to `/v1/models` — only IDs. |
| V3 | `model_enricher.go` already calls `GET /v1/models` at workspace start and stores only IDs | Code: `cmd/workspace-agentd/model_enricher.go:133–173` |
| V4 | `LLMModelConfig` has only `ID` and `Label` fields | Code: `pkg/secrets/types.go:166–170` |
| V5 | `provider_credentials` table has `model_allowlist TEXT[]` but no structured per-model metadata | Code: migration `000015_unified_credential_model.up.sql:13–25` |
| V6 | `relay_injector.go` already writes `limit.context` / `limit.output` into `agent-config.json` for relay models | Code: `cmd/workspace-agentd/relay_injector.go:120–134` — the pipeline already supports context limits; it just lacks the data source for BYOK models |
| V7 | `ModelContextLimit()` reads `limit.context` from opencode's `/config/providers` | Code: `cmd/workspace-agentd/main.go:120–157` — returns 0 if not found |
| V8 | `FormatOpenCodeConfig` does NOT currently write `limit.context` — only model name | Code: `pkg/agent/opencode/format.go:73–82`. The `opencodeModel` struct (line 126) has only `Name string`. `limit.context` is written exclusively by `buildRelayConfig` in `relay_injector.go` using its own local types. US-39.5 must add `Limit` to `opencodeModel` and populate it from `LLMModelConfig.ContextLimit`. |
| V9 | The `UNIQUE(owner_type, owner_id, provider)` constraint means one credential row per provider name per owner | Code: migration `000015_unified_credential_model.up.sql:24`. The `provider` column is a free-text string. Two endpoints labeled `"openai"` cannot coexist for the same user. |
| V10 | Admin credentials use server-side KEK (decryptable server-side); user credentials use session-derived DEK (zero-knowledge) | Code: `pkg/secrets/credential_store.go:62`, `pkg/secrets/pg_credential_store.go:410–440` |
| V11 | Empty `model_allowlist` means "all models allowed" in injection.go | Code: `pkg/secrets/injection.go:91` — the allowlist filter block is only entered when `len(b.ModelAllowlist) > 0`. Empty = no filtering. |
| V12 | `GET /v1/models` response may be paginated; `model_enricher.go` does not handle pagination | Code: `cmd/workspace-agentd/model_enricher.go:43–47` — response struct has only `Data []struct{ID string}`. No `has_more`, `next_cursor`, or `after` field. Single-call only. |
| V13 | opencode's internal models.dev catalog is the primary source for `limit.context` on `/config/providers`; values written in agent-config.json override the catalog IF opencode honours the config-file limit field | Validated by `opencode_integration_test.go:260–288` — tests call `GET /config/providers` and assert `limit.context > 0` using a config file with NO model entries, proving the catalog provides the values. The relay injector's written `limit.context` is assumed to override the catalog but this override behaviour is untested. US-39.5 must add a test that writes a known `limit.context` value and confirms it appears in `/config/providers` rather than the catalog value. |
| V14 | No SSRF protection exists in the API server for outbound HTTP calls | Code: `api/internal/middleware/` — no URL validation, no RFC1918 blocklist. `model_enricher.go` uses a plain `http.Client` with no dial hooks. The probe endpoint makes server-side HTTP requests to user-supplied URLs. |

### Critical implication of V10

**Admin credential probing is straightforward:** the server can decrypt the credential to call `/v1/models` at any time (on save, on refresh, on schedule).

**User credential probing requires the user to be present:** the session-derived DEK is only available during an active session. The probe must happen synchronously within the credential create/update HTTP request while the plaintext key is still in memory — before it is encrypted and stored. The stored ciphertext cannot be decrypted server-side for re-probing.

This means the architecture differs:
- **Admin:** probe on save → store results → re-probe on manual refresh (server can decrypt)
- **User:** probe inline during the HTTP create/update request (key is in plaintext in-flight) → store results with ciphertext; re-probe only when user explicitly triggers refresh (requires key re-entry or a session-local decrypt path)

### Critical implication of V11 — allowlist semantic change

After this epic, auto-probe populates `model_configs` with discovered model IDs. The `model_allowlist` column is then derived from `model_configs` where `enabled == true`. For any credential that previously had `model_allowlist: []` (meaning "all models allowed"), populating `model_configs` with a subset of models and deriving `model_allowlist` from them would **silently restrict access** to only those models — a breaking semantic change.

The fix: `model_allowlist` is derived from `model_configs` **only when the admin/user has explicitly saved a model config** (i.e. called PUT/PATCH with `model_configs`). Auto-probe results are stored in `model_configs` for UI display purposes but do NOT update `model_allowlist` unless the user saves. The `model_allowlist` remains as the access-control source of truth; `model_configs` is purely metadata/display. See US-39.1 and US-39.3 for enforcement.

---

## Non-Goals

- Fetching context limits from the upstream provider API automatically — this is impossible cross-provider (V2 above). Context limits are operator-supplied.
- Replacing `model_enricher.go` — it remains as a workspace-start fallback for models not in the stored metadata.
- Pricing or cost data per model.
- Model capability auto-detection (vision, function calling) — this is provider-specific and out of scope. Capability flags can be manually set but are not auto-populated.
- Supporting non-OpenAI-compatible endpoints (e.g. Anthropic native API, Cohere) — the probe always calls `GET /v1/models`. Providers that don't implement it gracefully degrade to an empty model list.

---

## Data Model Changes

### `LLMModelConfig` — add metadata fields

**File:** `pkg/secrets/types.go`

```go
// Before:
type LLMModelConfig struct {
    ID    string `json:"id"`
    Label string `json:"label,omitempty"`
}

// After:
type LLMModelConfig struct {
    ID           string `json:"id"`
    Label        string `json:"label,omitempty"`        // human display name (defaults to ID)
    ContextLimit int64  `json:"contextLimit,omitempty"` // max input tokens (context window)
    OutputLimit  int64  `json:"outputLimit,omitempty"`  // max output tokens
    Enabled      bool   `json:"enabled"`                // included in model allowlist
}
```

`Enabled` replaces the semantics of the existing `model_allowlist TEXT[]` column. On read, the allowlist is derived from `model_configs` where `enabled == true`. On write, both are kept in sync for backward compat with existing consumers that read `model_allowlist` directly.

### DB migration — `provider_credentials.model_configs`

```sql
-- Migration 000024_provider_model_configs.up.sql
ALTER TABLE provider_credentials
    ADD COLUMN IF NOT EXISTS model_configs JSONB NOT NULL DEFAULT '[]';

-- Backfill: convert existing model_allowlist entries to model_configs with enabled=true, no limits.
UPDATE provider_credentials
SET model_configs = (
    SELECT jsonb_agg(jsonb_build_object('id', m, 'enabled', true))
    FROM unnest(model_allowlist) AS m
)
WHERE array_length(model_allowlist, 1) > 0;

-- model_allowlist is retained for backward compat (read-path consumers).
-- It is derived from model_configs on every write going forward.
```

### `CredentialRow` structs

Both `AdminCredentialRow` and `UserCredentialRow` in `pkg/secrets/pg_credential_store.go` gain:

```go
ModelConfigs []LLMModelConfig // serialised as JSONB
```

`ModelAllowlist []string` is retained and derived at write time:
```go
row.ModelAllowlist = enabledModelIDs(row.ModelConfigs)
```

---

## New API Endpoints

### Probe endpoint (fetch model list from provider)

```
POST /api/v1/provider-credentials/probe
POST /api/v1/admin/provider-credentials/:id/probe
```

**User probe** — called during credential create/update while key is in plaintext in the request body. Takes the same fields as the create request (key, baseURL, provider) and returns the model list. Does NOT persist anything — the caller includes the results in the subsequent create/update call.

**Admin probe** — called after credential creation, when the server can decrypt the stored credential. Takes only `:id`. Returns the current model list from the provider. The frontend uses this for the manual refresh button. The server also calls this probe automatically on create/update (see S39.2).

**Response** (both):
```json
{
  "models": [
    { "id": "gpt-4o", "label": "gpt-4o" },
    { "id": "gpt-4o-mini", "label": "gpt-4o-mini" }
  ],
  "probed_at": "2026-06-12T17:00:00Z",
  "error": null
}
```

On provider error (non-200, timeout, DNS failure): returns `200 OK` with `"models": []` and `"error": "..."`. Probe failure is never a fatal error — the credential is still saved.

### Update endpoint — accept model configs

Existing `PUT /api/v1/admin/provider-credentials/:id` gains an optional `model_configs` body field:

```json
{
  "model_configs": [
    { "id": "gpt-4o", "label": "GPT-4o", "contextLimit": 128000, "outputLimit": 16384, "enabled": true },
    { "id": "gpt-4o-mini", "label": "GPT-4o Mini", "contextLimit": 128000, "outputLimit": 16384, "enabled": false }
  ]
}
```

For users, the create endpoint `POST /api/v1/provider-credentials` also accepts `model_configs` in the request body.

---

## Architecture

### Probe flow — user credential

```
User fills in provider form (name, provider, apiKey, baseURL)
  │
  ├──[on apiKey blur / baseURL change]──► POST /api/v1/provider-credentials/probe
  │                                         { provider, apiKey, baseURL }
  │                                         ↓ server calls GET {baseURL}/v1/models
  │                                         ↓ returns { models: [...], error: ... }
  │
  ├── UI renders model list with editable fields
  │     (contextLimit, outputLimit, label, enabled toggle)
  │
  └──[on form submit]──► POST /api/v1/provider-credentials
                           { name, provider, apiKey, baseURL, model_configs: [...] }
```

The key is in plaintext only during the form interaction. The probe happens client-side-initiated, server-side-executed, before encryption. No second network call to the provider is needed after save.

### Probe flow — admin credential

```
Admin saves credential
  │
  ├──[synchronous, in Create handler]──► server decrypts stored credential
  │                                       server calls GET {baseURL}/v1/models
  │                                       server stores probe results in model_configs
  │
  └──[manual refresh button]──► POST /api/v1/admin/provider-credentials/:id/probe
                                  server decrypts, re-calls GET /v1/models
                                  updates model_configs in DB
                                  returns updated model list
```

Admin probe can be repeated at any time without re-entering the key.

### Context limit flow — from credential to context bar

```
User sets contextLimit=128000 for model "gpt-4o" in the UI
  │
  ├── stored in provider_credentials.model_configs JSONB
  │
  └── workspace starts
        │
        ├── credential-setup init: PrepareSecretsForInjection decrypts credential
        │     → LLMProviderData.Models[i].ContextLimit = 128000
        │
        ├── runMaterializeCommand → EnrichProviders → FlushProviders
        │     → FormatOpenCodeConfig writes:
        │         "gpt-4o": { "limit": { "context": 128000, "output": 16384 } }
        │       into agent-config.json
        │
        └── agentd ModelContextLimit("gpt-4o") reads from opencode /config/providers
              → contextTotal = 128000 → context bar shows correctly
```

This reuses the existing relay injector write path (`relay_injector.go:120–134`) with no structural changes. The only change is that `LLMModelConfig.ContextLimit` is now populated from DB-stored metadata rather than being zero.

---

## Stories

| Story | Title | Effort | Depends On |
|---|---|---|---|
| US-39.1 | DB migration + `LLMModelConfig` metadata fields | Small (0.5d) | None |
| US-39.2 | Server-side probe service (calls `GET /v1/models`, stores results) | Small (1d) | US-39.1 |
| US-39.3 | Admin probe endpoint + auto-probe on create/update | Small (0.5d) | US-39.2 |
| US-39.4 | User probe endpoint (inline, key in plaintext) | Small (0.5d) | US-39.2 |
| US-39.5 | Wire `ContextLimit`/`OutputLimit` through `FormatOpenCodeConfig` | Small (0.5d) | US-39.1 |
| US-39.6 | Admin UI — model list panel with auto-load and refresh | Medium (1.5d) | US-39.3 |
| US-39.7 | User UI — model list panel with auto-load and refresh | Medium (1.5d) | US-39.4 |
| US-39.8 | `model_enricher.go` — use stored metadata as priority source | Small (0.5d) | US-39.1, US-39.5 |

Total estimated effort: ~6.5d

---

## Dependency Graph

```
US-39.1 (DB migration + LLMModelConfig fields)
  ├── US-39.2 (probe service)
  │     ├── US-39.3 (admin probe endpoint + auto-probe)
  │     │     └── US-39.6 (admin UI)
  │     └── US-39.4 (user probe endpoint)
  │           └── US-39.7 (user UI)
  └── US-39.5 (FormatOpenCodeConfig context limit write)
        └── US-39.8 (enricher uses stored metadata)
```

US-39.5 can be implemented in parallel with US-39.2 — it only depends on the type change in US-39.1.

---

## Story Detail

### US-39.1 — DB migration + `LLMModelConfig` metadata fields

**Files:** `api/migrations/000024_provider_model_configs.up.sql` (new), `api/migrations/000024_provider_model_configs.down.sql` (new), `charts/llmsafespace/migrations/` (mirror), `pkg/secrets/types.go`, `pkg/secrets/pg_credential_store.go`

**Changes:**

1. Add `model_configs JSONB NOT NULL DEFAULT '[]'` to `provider_credentials`.
2. Backfill: convert existing `model_allowlist` rows to `model_configs` entries with `enabled: true`, no limits.
3. Add `ContextLimit int64`, `OutputLimit int64`, `Enabled bool` to `LLMModelConfig`.
4. Update `AdminCredentialRow` and `UserCredentialRow` to include `ModelConfigs []LLMModelConfig`.
5. Update `pg_credential_store.go` `List`, `Get`, `Create`, `Update` methods to read/write `model_configs`. On write, derive `model_allowlist` from `model_configs` where `enabled == true` for backward compat.
6. Update the `CredentialBinding` type and its read path to also carry `ModelConfigs`.

**Backward compat:** existing `model_allowlist` is still written on every update so any consumer of the raw column continues to work.

**TDD cycle:**
1. `TestLLMModelConfig_Serialization` — JSON round-trip with all fields
2. `TestEnableModelIDs_DeriveAllowlist` — `enabledModelIDs` helper returns only enabled entries
3. `TestAdminCredentialStore_CreateWithModelConfigs` — stores and retrieves model_configs
4. `TestAdminCredentialStore_BackfilledAllowlistMatchesModelConfigs` — backfill consistency
5. Implement migration and store changes, verify all pass

---

### US-39.2 — Server-side probe service

**Files:** `api/internal/services/providers/probe.go` (new), `api/internal/services/providers/probe_test.go` (new)

A standalone service (not a handler) that given `(provider, apiKey, baseURL string)` calls `GET {baseURL}/v1/models`, parses the response, and returns `[]LLMModelConfig` (IDs only — context limits are not auto-fetched, they are operator-supplied).

```go
type ProbeResult struct {
    Models   []sec.LLMModelConfig
    ProbedAt time.Time
    Err      error // non-nil = provider unreachable or returned error; caller treats as soft failure
}

type ProviderProber interface {
    Probe(ctx context.Context, provider, apiKey, baseURL string) ProbeResult
}
```

**Behaviour:**
- 10s timeout on the outbound request.
- Follows redirects once (no infinite redirect chains).
- On HTTP 4xx/5xx: returns empty models + error string. Not a fatal error for callers.
- On DNS failure / connection refused: same.
- On empty response / `data: []`: returns empty models, no error.
- Respects provider-specific base URL normalisation: if `baseURL` is empty, uses the provider's known default (e.g. `https://api.openai.com/v1` for `openai`).
- **Does not call `/v1/model/info`** — probe is intentionally cross-provider compatible.
- **Pagination:** follows `has_more` + `after` cursor if present in the response (OpenAI list pagination), up to a hard cap of 200 models. Most providers return all models in one response; this handles the minority that paginate. Cap is enforced before the cursor loop — if 200 models are seen on page N, no further pages are fetched.
- **SSRF protection** (see gap #1): the probe HTTP client uses a custom `DialContext` that resolves the hostname and rejects connections to RFC1918, loopback, link-local, and other non-routable IP ranges before the connection is established. Accepted ranges: public IPv4, public IPv6. Rejected: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `169.254.0.0/16`, `::1/128`, `fc00::/7`, `fe80::/10`. URL scheme: `https` only; `http` rejected unless `allowInsecureProbe` feature flag is set (default off, for local dev only). The SSRF dial hook is shared with `model_enricher.go` (a separate PR can apply it there too).

**Lifecycle:** the probe service receives the server-lifecycle context (`app.ctx`) at construction time. The auto-probe goroutines spawned by US-39.3 use a child of this context with a 15s deadline — not the request context (which is cancelled immediately when the HTTP response is returned). On server shutdown, `app.ctx` cancellation propagates to all in-flight probe goroutines within 15s. The `App.Shutdown()` flow already cancels `app.ctx` as its first step (verified: `app.go:489`).

**Security:** the API key passed to the probe is either (a) from the in-flight plaintext request body (user path) or (b) decrypted server-side from the admin credential. It is never logged. The outbound HTTP client has no logging middleware.

**TDD cycle:**
1. `TestProbe_HappyPath_ReturnModelIDs` — mock server returns valid `/v1/models` response
2. `TestProbe_EmptyList_NoError` — `data: []` is not an error
3. `TestProbe_ProviderError_SoftFailure` — HTTP 401 returns empty + error string, does not panic
4. `TestProbe_Timeout_SoftFailure` — slow server exceeds 10s, returns empty + error
5. `TestProbe_DNSFailure_SoftFailure` — bad hostname, returns empty + error
6. `TestProbe_BaseURLNormalisation_OpenAI` — empty baseURL uses OpenAI default
7. `TestProbe_BaseURLNormalisation_Custom` — explicit baseURL used verbatim
8. `TestProbe_SSRF_RFC1918_Rejected` — `baseURL: "http://192.168.1.1"` returns error, no connection made
9. `TestProbe_SSRF_Loopback_Rejected` — `baseURL: "http://127.0.0.1"` rejected
10. `TestProbe_SSRF_LinkLocal_Rejected` — `baseURL: "http://169.254.169.254"` rejected
11. `TestProbe_HTTP_Rejected` — `http://` URL rejected when `allowInsecureProbe` is false
12. `TestProbe_Pagination_FollowsCursor` — provider returns `has_more:true`, probe follows until exhausted
13. `TestProbe_Pagination_Cap200` — more than 200 models returned, probe stops at 200
14. Implement, verify all pass

---

### US-39.3 — Admin probe endpoint + auto-probe on create/update

**Files:** `api/internal/handlers/admin_provider_credentials.go`, `api/internal/handlers/admin_provider_credentials_test.go`

1. **New endpoint:** `POST /api/v1/admin/provider-credentials/:id/probe`
   - Decrypts the stored credential (server has KEK).
   - Calls `ProviderProber.Probe(ctx, provider, apiKey, baseURL)`.
   - Merges result into existing `model_configs` (adds new model IDs, preserves existing metadata — see merge semantics below).
   - Persists updated `model_configs` to DB.
   - **Does NOT update `model_allowlist`** — probe results are metadata only. `model_allowlist` is only updated when the admin explicitly saves model configs via `PUT /api/v1/admin/provider-credentials/:id` with a `model_configs` payload (see US-39.1 for the split between display metadata and access-control allowlist).
   - Returns `ProbeResult` (model list + `probed_at` + optional `error` string).
   - **Rate limit:** the probe endpoint has a separate, tighter rate limit: 10 requests per minute per credential ID per user, enforced via the existing rate-limit middleware with a custom `probe` limit key. This prevents the endpoint from being used as a bulk API-key validator.

2. **Auto-probe on `Create` and `Update`:** after persisting the credential, launch a goroutine using a child of `app.ctx` (the server-lifecycle context) with a 15s deadline — **not the request context** (which is cancelled when the response is sent). The goroutine updates `model_configs` in the DB on completion. The HTTP response returns immediately; the frontend polls or uses the manual refresh button.

   Probe goroutines are fire-and-forget within the server lifecycle. On server shutdown, `app.ctx` cancellation (fired at `app.go:489`) propagates to all in-flight probes; the 15s deadline ensures they drain within the shutdown timeout.

**Merge semantics for auto-probe:**
- New model IDs from probe: added to `model_configs` with `enabled: true`, no limits, label = ID.
- Existing model IDs already in `model_configs`: all metadata (contextLimit, outputLimit, label, enabled) is **preserved**. Operator-set values are never overwritten.
- Model IDs present in `model_configs` but absent from probe result: **retained** (provider may be incomplete or temporarily unavailable).
- **`model_allowlist` is NOT touched by any probe.** It is only derived from `model_configs` when the admin explicitly saves via `PUT`. This preserves the "empty allowlist = all models allowed" semantic for credentials that have never had an explicit allowlist.

**TDD cycle:**
1. `TestAdminProbeEndpoint_ReturnsModelList` — happy path
2. `TestAdminProbeEndpoint_MergesNewModels_PreservesExistingMetadata` — existing contextLimit not overwritten
3. `TestAdminProbeEndpoint_DoesNotUpdateModelAllowlist` — model_allowlist unchanged after probe
4. `TestAdminProbeEndpoint_ProviderError_Returns200WithErrorField` — soft failure
5. `TestAdminProbeEndpoint_RateLimit_10PerMinute` — 11th request within 60s returns 429
6. `TestAdminCreateCredential_AutoProbeUsesServerContext` — goroutine uses app.ctx child, not request context
7. `TestAdminUpdateCredential_AutoProbeTriggered` — probe fires after key rotation
8. Implement, verify all pass

---

### US-39.4 — User probe endpoint (inline, key in plaintext)

**Files:** `api/internal/handlers/user_provider_credentials.go`, `api/internal/handlers/user_provider_credentials_test.go`

**New endpoint:** `POST /api/v1/provider-credentials/probe`

Request body:
```json
{ "provider": "openai", "apiKey": "sk-...", "baseURL": "" }
```

- Called by the frontend synchronously during credential creation UI flow (on apiKey field blur or base URL change).
- The key is never stored by this endpoint — it is only used for the outbound probe call.
- Returns the same `ProbeResult` as the admin probe.
- The frontend includes the returned model list in the subsequent `POST /api/v1/provider-credentials` create request.

**User credential re-probe (manual refresh):** User credentials cannot be re-probed server-side because the key is zero-knowledge (encrypted with session DEK, server cannot decrypt). The manual refresh flow for users requires the user to re-enter their API key in the refresh form, which then calls `POST /api/v1/provider-credentials/probe` with the plaintext key and updates the stored `model_configs` via a new `PATCH /api/v1/provider-credentials/:id/models` endpoint.

**New endpoint:** `PATCH /api/v1/provider-credentials/:id/models`
- Accepts `{ model_configs: [...] }`.
- Does not accept `apiKey` — only updates the metadata portion of the credential. The ciphertext is unchanged.
- Authenticated as the credential owner.
- Used by the frontend after a manual refresh (user re-entered key → probe returned new list → user clicks "Save models").

**TDD cycle:**
1. `TestUserProbeEndpoint_HappyPath` — returns model list, no persistence side effect
2. `TestUserProbeEndpoint_NoAPIKey_Returns400` — validates required field
3. `TestUserProbeEndpoint_ProviderError_Returns200WithErrorField`
4. `TestUserPatchModels_UpdatesModelConfigs` — PATCH updates model_configs, ciphertext unchanged
5. `TestUserPatchModels_WrongOwner_Returns403`
6. Implement, verify all pass

---

### US-39.5 — Wire `ContextLimit`/`OutputLimit` through `FormatOpenCodeConfig`

**Files:** `pkg/agent/opencode/format.go`, `pkg/agent/opencode/format_test.go`, `cmd/workspace-agentd/model_enricher.go`

**Critical finding from code audit (V8):** `FormatOpenCodeConfig` does NOT currently write `limit.context`. The `opencodeModel` struct (`format.go:126`) has only `Name string`. Limit fields are written exclusively by `buildRelayConfig` in `relay_injector.go` using its own local `modelEntry`/`modelLimit` types. This story must add `Limit` to the shared `opencodeModel` struct, populate it from `LLMModelConfig.ContextLimit`/`OutputLimit`, and verify it reaches opencode's `/config/providers` endpoint.

**Changes to `opencodeModel`:**
```go
// Before:
type opencodeModel struct {
    Name string `json:"name,omitempty"`
}

// After:
type opencodeModel struct {
    Name  string           `json:"name,omitempty"`
    Limit *opencodeLimit   `json:"limit,omitempty"`
}

type opencodeLimit struct {
    Context int64 `json:"context,omitempty"`
    Output  int64 `json:"output,omitempty"`
}
```

`Limit` is a pointer so it is omitted from JSON when nil (no limit set). Written when `LLMModelConfig.ContextLimit > 0`.

**Relay injector alignment:** `relay_injector.go` currently uses its own local `modelEntry`/`modelLimit` types. After this story, it should be updated to use the shared `opencodeModel`/`opencodeLimit` types from `pkg/agent/opencode/` to avoid divergence.

**Catalog override validation (gap #8):** integration tests confirm that opencode's internal catalog provides `limit.context` values when no model entries exist in `agent-config.json`. The assumption that a written `limit.context` *overrides* the catalog is untested. This story must add an integration test:
- Write an `agent-config.json` with a known model ID and an explicit `limit.context` value that is different from opencode's catalog value for that model.
- Call `GET /config/providers` and assert the written value is returned, not the catalog value.
- If the catalog takes precedence: the design's context limit flow silently breaks for models opencode knows about. Document the precedence rule explicitly based on the test result.

**TDD cycle:**
1. `TestFormatOpenCodeConfig_IncludesContextLimit_WhenNonZero`
2. `TestFormatOpenCodeConfig_OmitsLimitBlock_WhenZero` — no regression for existing models without limits
3. `TestFormatOpenCodeConfig_OutputLimit_WhenNonZero`
4. `TestFormatOpenCodeConfig_LimitOmitted_WhenBothZero` — pointer is nil, `"limit"` key absent from JSON
5. `TestOpencode_WrittenContextLimit_OverridesCatalog` — integration test validating precedence (V13)
6. Implement, verify all pass; document catalog precedence finding in a code comment

---

### US-39.6 — Admin UI — model list panel with auto-load and refresh

**Files:** `frontend/src/components/settings/AdminProviderCredentialsTab.tsx`, `frontend/src/api/providerCredentials.ts`

**Changes to the admin credential row (`CredentialRow`):**

After the existing "Rotate API key" panel, add a **"Models" panel** section:

```
┌─ Models ─────────────────────────────────────── [↻ Refresh] ─┐
│                                                               │
│  Loading...  (spinner shown during auto-probe or refresh)     │
│  OR                                                           │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ ✓  gpt-4o          GPT-4o      ctx: 128K  out: 16K  [✎] │ │
│  │ ✓  gpt-4o-mini     GPT-4o Mini ctx: 128K  out: 16K  [✎] │ │
│  │ ✗  o1-preview      o1 Preview  ctx: —     out: —    [✎] │ │
│  └─────────────────────────────────────────────────────────┘ │
│  Probe error: "401 Unauthorized" (shown if probe failed)      │
└───────────────────────────────────────────────────────────────┘
```

**Model row [✎] expand:** click to reveal inline edit fields:
- Display name (text input, defaults to ID)
- Context window — `max_input_tokens` (number input, placeholder "e.g. 128000")
- Max output tokens (number input, placeholder "e.g. 16384")
- Enabled toggle (checkbox — whether model appears in model selector)

**Auto-load:** When the credential row is expanded for the first time, if `model_configs` is empty, the UI calls `POST /api/v1/admin/provider-credentials/:id/probe` automatically with a loading spinner. This is a single background call — not a polling loop.

**Refresh button:** `↻ Refresh` calls the same probe endpoint. Shows a spinner during the call. On completion, merges the returned model list with any existing metadata the admin has set (new IDs added, existing metadata preserved — mirrors server-side merge semantics).

**Save:** Changes to model metadata (context limits, labels, enabled) are saved via the existing `PUT /api/v1/admin/provider-credentials/:id` endpoint with the updated `model_configs` payload.

**New API call:** `adminProviderCredentialsApi.probe(id: string): Promise<ProbeResult>` → `POST /api/v1/admin/provider-credentials/:id/probe`

**TDD cycle:**
1. `TestAdminCredentialRow_ModelsPanel_ShownWhenExpanded`
2. `TestAdminCredentialRow_AutoProbe_TriggeredOnFirstExpand`
3. `TestAdminCredentialRow_AutoProbe_NotTriggeredWhenModelsPresent`
4. `TestAdminCredentialRow_RefreshButton_CallsProbeEndpoint`
5. `TestAdminCredentialRow_ModelRow_Edit_SavesContextLimit`
6. `TestAdminCredentialRow_ModelRow_EnabledToggle_UpdatesAllowlist`
7. `TestAdminCredentialRow_ProbeError_ShowsErrorMessage`
8. Implement, verify all pass

---

### US-39.7 — User UI — model list panel with auto-load and refresh

**Files:** `frontend/src/components/settings/UserProviderCredentialsTab.tsx`, `frontend/src/api/providerCredentials.ts`

**Changes to the create form (`CreateUserCredentialForm`):**

After the API Key field, add a **"Models" section** that auto-populates:

```
┌─ API Key ──────────────────────────────────────────────────┐
│  sk-... [👁]                                                │
└─────────────────────────────────────────────────────────────┘
                    [checking models...]  ← shown on apiKey blur
┌─ Models ───────────────────────────────── [↻ Re-check] ────┐
│  ✓  gpt-4o          ctx: —  out: —   [Set limits]          │
│  ✓  gpt-4o-mini     ctx: —  out: —   [Set limits]          │
└─────────────────────────────────────────────────────────────┘
```

**Auto-probe trigger:** when the API Key field loses focus (blur) AND the field is non-empty, call `POST /api/v1/provider-credentials/probe` with the current form values. Show an inline loading indicator. No user action required.

**Re-probe trigger (Re-check button):** if the user changes the key or base URL after the initial probe, a `↻ Re-check` button appears. Clicking it repeats the probe.

**"Set limits" expand:** each model row can be expanded to set `contextLimit`, `outputLimit`, and display name inline in the create form. These values are included in the create request payload.

**Refresh flow for existing credentials (credential row view):**

In the existing `CredentialRow` component, add a **"Models" collapsible section** showing the stored model list. Since re-probing requires re-entering the API key (zero-knowledge constraint), the refresh flow is:

```
[↻ Refresh models]
  → reveals: "Enter API key to refresh model list"
  → password input + [Check] button
  → calls POST /api/v1/provider-credentials/probe with entered key
  → shows updated model list with editable limits
  → [Save models] calls PATCH /api/v1/provider-credentials/:id/models
```

The API key entered here is used only for the probe — it is not re-encrypted or stored. The existing ciphertext is unchanged.

**New API calls:**
- `userProviderCredentialsApi.probe(req: ProbeRequest): Promise<ProbeResult>` → `POST /api/v1/provider-credentials/probe`
- `userProviderCredentialsApi.updateModels(id: string, models: LLMModelConfig[]): Promise<void>` → `PATCH /api/v1/provider-credentials/:id/models`

**TDD cycle:**
1. `TestCreateCredentialForm_AutoProbe_TriggeredOnAPIKeyBlur`
2. `TestCreateCredentialForm_AutoProbe_NotTriggeredOnEmptyKey`
3. `TestCreateCredentialForm_ReCheckButton_ShownAfterKeyChange`
4. `TestCreateCredentialForm_ModelList_PopulatedFromProbeResult`
5. `TestCreateCredentialForm_SetLimits_IncludedInCreatePayload`
6. `TestCredentialRow_ModelsPanel_ShowsStoredModels`
7. `TestCredentialRow_RefreshFlow_RequiresKeyReEntry`
8. `TestCredentialRow_RefreshFlow_CallsPatchEndpoint`
9. Implement, verify all pass

---

### US-39.8 — `model_enricher.go` — use stored metadata as priority source

**Files:** `cmd/workspace-agentd/model_enricher.go`

Currently `model_enricher.go` calls `GET {baseURL}/v1/models` when a provider has a custom base URL and no models stored. After US-39.1, providers may already have `model_configs` with IDs, limits, and labels.

**Change:** skip the `/v1/models` network call if `len(p.Models) > 0`. The stored metadata is the authoritative source. The enricher's `/v1/models` call is only for **discovery** (when no models are known yet), not for refreshing metadata.

Additionally, when `p.Models` has entries with `ContextLimit > 0`, propagate them through the `LLMProviderData` as-is — no transformation needed since `FormatOpenCodeConfig` (US-39.5) now writes them correctly.

This means: once an admin or user has set context limits via the UI, the limits are guaranteed to be in `agent-config.json` at workspace start without any network calls.

**No change to the enricher's caching logic** — the 24h TTL cache remains for the discovery path.

**TDD cycle:**
1. `TestEnrichProviders_SkipsNetworkCall_WhenModelsPresent` — `len(p.Models) > 0` → no HTTP call
2. `TestEnrichProviders_CallsNetwork_WhenNoModels` — existing behavior unchanged
3. `TestEnrichProviders_ContextLimitsPreserved_FromStoredMetadata`
4. Implement, verify all pass

---

## Failure Modes

### FM1 — Probe fails at admin credential create time (MEDIUM)

**Scenario:** Admin creates a credential for a provider that is temporarily unreachable. Probe returns empty model list.

**Behaviour:** Credential is saved successfully. `model_configs` is `[]`. UI shows empty model list with error message from probe. Admin can use the refresh button once the provider is reachable.

**Risk:** None. Credential is fully usable; model enricher at workspace start handles discovery.

### FM2 — User's provider returns a very large model list (LOW)

**Scenario:** An OpenRouter key returns 400+ models. UI becomes unwieldy.

**Mitigation:** Frontend virtualises the model list (only render visible rows). Probe service caps stored `model_configs` at 200 entries with a warning if truncated. Pagination is followed up to the cap (see V12).

### FM3 — Stale model metadata after provider updates their model lineup (LOW)

**Scenario:** Provider adds a new model. Admin's stored `model_configs` doesn't include it. Workspace model selector doesn't show it.

**Behaviour:** The model enricher at workspace start calls `/v1/models` and discovers it with no limits. Context bar shows "Unknown" for the new model until the admin refreshes via the UI and sets limits.

**Risk:** Low — the model is usable, just without context limits until manually configured.

### FM4 — Zero-knowledge constraint blocks user re-probe (LOW)

**Scenario:** User saved a credential. Provider changes the available models. User can't refresh without re-entering their key.

**Mitigation:** The `↻ Refresh models` flow in the user UI prompts for key re-entry. Clearly labelled: "Re-enter your API key to refresh the model list. The key is used only for this check and is not re-stored."

**Risk:** Minor UX friction. Acceptable — it is the correct security posture for a zero-knowledge system.

### FM5 — Admin auto-probe goroutine races with subsequent update (LOW)

**Scenario:** Admin creates a credential. Auto-probe goroutine fires. Before it completes, the admin immediately edits the credential. The goroutine's result arrives and overwrites the edited `model_configs`.

**Mitigation:** The goroutine uses merge semantics — it only adds new model IDs, never overwrites existing metadata or `model_allowlist`. An admin edit that sets `contextLimit` for a model will not be overwritten by the probe result.

### FM6 — SSRF via user-supplied `baseURL` (HIGH — mitigated)

**Scenario:** A user supplies `baseURL: "http://169.254.169.254/latest/metadata"` or `baseURL: "http://postgres:5432"`. The API server makes an outbound HTTP request to that address.

**Mitigation:** The probe service uses a custom `DialContext` that rejects RFC1918, loopback, and link-local IP ranges at the TCP dial layer — before any HTTP bytes are sent. URL scheme is restricted to `https` (configurable for dev). See US-39.2 for the full SSRF blocklist.

**Residual risk:** The SSRF blocklist does not cover all cases — e.g. DNS rebinding attacks (hostname resolves to public IP at DNS time, then to private IP at connect time). For production deployments a network-level egress policy (existing `NetworkPolicy` pattern) is the belt-and-suspenders. The blocklist is the application-level guard.

### FM7 — Auto-probe silently changes allowlist semantics (HIGH — mitigated by design)

**Scenario:** A credential has `model_allowlist: []` (all models allowed). Auto-probe runs and populates `model_configs`. If `model_allowlist` were derived from `model_configs`, it would change from "all allowed" to "only these N models allowed."

**Mitigation:** `model_allowlist` is **never updated by a probe**. Probe results write only to `model_configs` (display metadata). `model_allowlist` is derived from `model_configs` only when the admin/user explicitly saves via PUT/PATCH. The access control semantic is preserved.

### FM8 — opencode catalog takes precedence over written `limit.context` (MEDIUM — to be validated)

**Scenario:** For a model opencode knows about (e.g. `gpt-4o`), an operator sets `contextLimit: 32000` to enforce a tighter budget limit. opencode's internal catalog says 128000. If the catalog takes precedence, the operator-set value is silently ignored and the context bar shows the wrong limit.

**Mitigation:** US-39.5 includes an integration test (`TestOpencode_WrittenContextLimit_OverridesCatalog`) that validates the precedence order. If the catalog wins, the design must be revised to inject limits via a different mechanism (e.g. provider-level `maxTokens` override or a separate opencode config option). Until the test confirms the outcome, the override behaviour is an unvalidated assumption.

### FM9 — Context limits don't take effect for running workspaces (MEDIUM)

**Scenario:** Admin sets a context limit for a model. A workspace is already running. The context bar continues showing "Unknown" until the workspace pod restarts.

**Behaviour:** This is consistent with how all credential changes work — `agentNeedsRefresh` triggers a credential reload, which re-runs `runMaterializeCommand` and rewrites `agent-config.json`. If the credential reload path correctly propagates `model_configs` → `LLMModelConfig.ContextLimit` → `FormatOpenCodeConfig`, the context bar will update on the next reload without a pod restart.

**Action:** Confirm the credential reload path (`runMaterializeCommand` → `EnrichProviders` → `FlushProviders`) carries `ContextLimit` through after US-39.5. Add a note in the UI: "Context limits are applied on the next workspace credential reload or restart."

### FM10 — Probe endpoint used as bulk API key validator (LOW — rate limited)

**Scenario:** An authenticated attacker uses `POST /api/v1/provider-credentials/probe` to validate stolen API keys at scale. Each probe call attempts authentication with the supplied key against the target provider. No credential is created, so no audit trail exists.

**Mitigation:** Probe endpoints have a separate, tighter rate limit: 10 requests per minute per user (see US-39.3). Probe attempts are logged (provider + baseURL domain, never the key itself). The rate limit degrades bulk validation to 600/hour — not zero, but economically unattractive at scale.

---

## Acceptance Criteria

- [ ] `LLMModelConfig` has `ContextLimit`, `OutputLimit`, `Enabled`, `Label` fields
- [ ] Migration 000024 adds `model_configs JSONB` to `provider_credentials`; backfills existing `model_allowlist` rows
- [ ] `POST /api/v1/provider-credentials/probe` exists and calls `GET /v1/models` on the supplied endpoint
- [ ] `POST /api/v1/admin/provider-credentials/:id/probe` exists; decrypts stored credential and probes
- [ ] Admin `Create` auto-probes asynchronously; does not block the HTTP response
- [ ] Admin `Update` (key rotation) re-probes asynchronously
- [ ] Probe merge semantics: new model IDs added with `enabled: true`; existing metadata never overwritten
- [ ] `PATCH /api/v1/provider-credentials/:id/models` allows updating model metadata without re-encrypting the key
- [ ] `FormatOpenCodeConfig` writes `limit.context` and `limit.output` when `ContextLimit > 0`
- [ ] `model_enricher.go` skips network call when `len(p.Models) > 0`
- [ ] Admin UI credential row shows model list panel; auto-probes on first expand when list is empty
- [ ] Admin UI refresh button re-probes and merges results
- [ ] Admin UI model row inline edit: label, context limit, output limit, enabled toggle; saved via `PUT`
- [ ] User create form auto-probes on API key field blur; no action required from user
- [ ] User credential row shows stored model list with refresh flow (key re-entry required)
- [ ] Probe failure is never a fatal error — credential always saves; UI shows error inline
- [ ] Context bar shows correct values for models with `ContextLimit > 0` set via the UI
- [ ] All existing credential CRUD tests pass unchanged
- [ ] TDD discipline followed throughout — tests written before implementation

---

## Out of Scope

| Item | Why |
|---|---|
| `/v1/model/info` | Not cross-provider compatible. Intentionally excluded. |
| Auto-detecting `ContextLimit` from provider API | No standard endpoint for this. Operator-supplied only. |
| Capability flags (vision, function calling) | Provider-specific, no standard probe. Manual-only in future iteration. |
| Pricing data | Out of scope for this epic. |
| Model ordering / pinning | UI improvement, deferred. |
| Removing `model_allowlist` column | Backward compat required; defer until all consumers of the raw column are audited. |
| Re-probing on a schedule | Credentials are long-lived; on-demand probe is sufficient. |

---

## Design Debt

- **`model_allowlist TEXT[]` column is redundant** once all consumers use `model_configs`. Tracked as cleanup debt — remove in a future migration once the backfill is confirmed stable in production.
- **User re-probe UX is friction-heavy.** The zero-knowledge constraint requires key re-entry. A future Epic 35 (Secretless Credential Injection) would change the key management model and may enable server-side re-probe for user credentials. Revisit then.
- **No model-level access control enforcement.** `Enabled` controls visibility in the model selector UI, but it is not enforced server-side (a user could still pass any model ID in a workspace request). Server-side enforcement is deferred — the existing `model_allowlist` validation in `injection.go` handles hard enforcement; `Enabled` is a soft UI gate.
- **One credential per `(owner, provider-string)` pair.** The `UNIQUE(owner_type, owner_id, provider)` constraint prevents users from having two credentials for the same provider-name string — e.g. two separate OpenAI-compatible endpoints both labelled `"openai"`. This is a pre-existing architectural constraint that this epic inherits. Users who need two endpoints with the same provider type must use distinct provider names (e.g. `"openai-work"` and `"openai-personal"`). The probe's base URL normalisation uses the provider string as a routing key and degrades gracefully for unrecognised strings (passes through the URL as-is). This limitation is documented in the UI via a hint in the provider field.
- **Catalog override behaviour unvalidated.** US-39.5 adds a test to determine whether `agent-config.json` model limits override opencode's internal catalog. Until that test runs, the end-to-end context limit flow for models opencode knows about is an assumption.

---

## Open Questions

| # | Question | Recommended Resolution |
|---|---|---|
| Q1 | Should the probe result include models from `GET /v1/models/{id}` (individual model lookup) for richer metadata? | No — individual model lookup is also not standardized for metadata. Keep probe to list endpoint only. |
| Q2 | Should `model_configs` have a `last_probed_at` timestamp per-credential to show staleness? | Yes — add `last_probed_at TIMESTAMPTZ` column in the same migration (US-39.1). |
| Q3 | What is the max size of `model_configs`? OpenRouter returns 400+ models. | Cap at 200 entries. Probe follows pagination up to the cap. Log a warning if truncated. Future: search/filter in the model list UI. |
| Q4 | For the user refresh flow, should the key re-entry be a separate drawer/modal or inline in the credential row? | Inline is sufficient for V1. A separate drawer is cleaner for V2 if the model list grows large. |
| Q5 | Does a written `limit.context` in `agent-config.json` override opencode's internal catalog value, or does the catalog take precedence? | Must be answered by `TestOpencode_WrittenContextLimit_OverridesCatalog` in US-39.5. The entire context limit flow depends on the answer. If the catalog wins, a different injection mechanism is needed. |
| Q6 | Should the `allowInsecureProbe` flag (allowing `http://` URLs) be an instance setting (admin-configurable) or a build-time flag? | Instance setting (Tier 2, default `false`). Allows self-hosted operators with internal HTTP-only LiteLLM deployments to enable it without rebuilding. |
