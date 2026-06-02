# LLM Provider Credential Management — Step 2 Design

**Date:** 2026-06-02
**Status:** Draft
**Depends on:** af30edc (Step 1: type definitions, materializer staging, restart trigger)

---

## Validated Assumptions

These are facts confirmed by reading the opencode source code (packages/core/src/config/provider.ts, plugin/provider/*.ts, plugin/account.ts, config/plugin/provider.ts):

| # | Assumption | Evidence |
|---|-----------|----------|
| A1 | opencode config file uses `providers` (plural) as the top-level key containing a record of provider configs | `Config.Info` schema: `providers: Schema.Record(Schema.String, ConfigProvider.Info)` |
| A2 | Each provider entry has: `name?`, `env?`, `endpoint?`, `options?`, `models?` | `ConfigProvider.Info` class definition |
| A3 | API keys flow through `options.aisdk.provider.apiKey` at runtime (set by AccountPlugin and ConfigProviderPlugin) | `plugin/account.ts:35`: `provider.options.aisdk.provider.apiKey = account.credential.key` |
| A4 | A config-file provider is enabled via `{ via: "custom", data: {} }` implicitly when `ConfigProviderPlugin` processes it | `config/plugin/provider.ts:26`: `provider.enabled = { via: "custom", data: {} }` |
| A5 | The `endpoint` field determines which AI SDK package to load. Known types: `openai/responses`, `openai/completions`, `anthropic/messages`, `aisdk` (generic) | `ProviderV2.Endpoint` union type |
| A6 | opencode reads config at startup from `OPENCODE_CONFIG` env var path. No file-watch/hot-reload exists | Design doc `0024`: "A15 — opencode does not currently support config reload at runtime" |
| A7 | The `model` field in top-level config sets the default model as `"{providerID}/{modelID}"` | `Config.Info` schema: `model: Schema.String.pipe(Schema.optional)` |
| A8 | `models` within a provider config is a `Record<string, Model>` — keys are model IDs, values have optional `name`, `disabled`, `api_id`, etc. | `ConfigProvider.Info.models` schema |

**Unvalidated (explicitly deferred):**

| # | Assumption | Why deferred |
|---|-----------|-------------|
| ~~U1~~ | ~~opencode `PATCH /config` could enable hot-reload without restart~~ | **NOW VALIDATED** — see below |

**Newly validated (from source reading):**

| # | Finding | Evidence |
|---|---------|----------|
| A9 | `PATCH /global/config` writes merged config to disk, invalidates cache, then calls `disposeAllInstancesAndEmitGlobalDisposed` | `handlers/global.ts:89` + `config.ts:861` |
| A10 | `disposeAll` kills all active opencode instances/sessions — users lose in-flight conversation state | `instance-store.ts:164-178` disposes every entry |
| A11 | The opencode *process* stays alive — no cold boot, no port rebind, no race condition | `disposeAll` only affects instances within the running process |
| A12 | After dispose, opencode re-reads config on next instance creation (config is cached with invalidation) | `cachedInvalidateWithTTL` at `config.ts:482` |

**Implication for Step 2:** Use `PATCH /global/config` to push credentials into opencode. This disposes all instances (sessions lost) but avoids process restart, eliminates the restart race, and is the only HTTP API available for API key injection today.

**Future (Step 3): Contribute `POST /provider/:providerID/auth/apikey` to opencode** that calls `Auth.create({type: "api", key})` internally. This triggers `catalog.transform` via `Event.Switched` without disposing instances — true hot-reload with sessions preserved. See `design/stories/epic-10-multi-tenant-trust/opencode-auth-create-prr.md`.

---

## Problem

Step 1 built the staging pipeline: `Materialize()` validates `llm-provider` secrets and stages them in memory as `[]LLMProviderData`. But nothing converts that data into a file opencode can read. The `FlushProviders(formatter)` method exists but has no formatter to call, and the reload handler doesn't invoke it.

The legacy `api-key` type writes opaque JSON directly to `AgentConfigPath`. This is not in production and has no users. Step 2 replaces it entirely with the structured `llm-provider` path.

---

## Product Context (Full Arc)

Step 2 is one piece of a larger credential management UX:

1. **Credential creation** — User creates an `llm-provider` credential (or credential set). In the future, this can be org-level or individual-level.
2. **Model discovery** — For major providers (Anthropic, OpenAI, Google), models are discovered dynamically from provider APIs. For OpenAI-compatible endpoints, the `/models` endpoint is queried. User can hide/show specific models via the `Models` allowlist.
3. **Propagation** — When a credential is created or updated, the API server pushes the new secret batch to ALL active workspace pods via `/v1/reload-secrets`. The materializer handles one pod; propagation is the controller's job.
4. **Agent config pickup** — Today: restart. Future: hot-reload via agent-specific APIs (opencode `PATCH /config`, Claude Code's config mechanism, etc.). The design must not couple flush and restart permanently.
5. **Model selection UX** — Users see a populated model dropdown. Agent reads available models from its config. The formatter controls which models are visible.

**Multi-agent future:** The same `[]LLMProviderData` will be rendered differently for each agent:
- opencode: `providers` map with `options.aisdk.provider.apiKey`
- Claude Code: `~/.claude/settings.json` or equivalent
- Codex: environment variables or codex config file
- Aider: `.aider.conf.yml` or env vars

Each agent gets its own formatter function. The materializer is agent-agnostic.

---

## Design Goals

1. **Single responsibility**: The materializer stages data. The formatter renders it. The reload handler orchestrates.
2. **No two writers to the same file**: `applyAPIKey()` stops writing to `AgentConfigPath`. Only `FlushProviders()` writes there.
3. **Deterministic output**: Given the same `[]LLMProviderData` input, produce byte-identical config. No maps iterated in random order.
4. **Fail-safe ordering**: Config file written before restart signal. Never restart with stale/missing config.
5. **Testable in isolation**: Formatter is a pure function (data in, bytes out). No filesystem, no process management.
6. **Restart is temporary**: The flush/restart separation must allow future removal of restart without restructuring. When hot-reload is validated, the reload handler simply stops calling `proc.restart()`.

---

## Architecture

### Control Flow (reload handler)

```
POST /v1/reload-secrets
  │
  ▼
json.Decode(batch []Secret)
  │
  ▼
m.Materialize(batch)              // stages llm-provider, writes ssh/git/env/files
  │
  ▼
m.FlushProviders(formatter)       // writes AgentConfigPath (the ONLY writer)
  │                                // returns error if formatter fails
  ▼
if err != nil → respond 500, do NOT notify agent
  │
  ▼
if hasLLMProviders(batch):
    PATCH http://localhost:{port}/global/config   // push config into running opencode
    │                                             // disposes instances, re-reads config
    ▼
    (opencode reinitializes with new providers — sessions lost, process stays alive)
  │
  ▼
respond 200
```

**Invariant:** The PATCH is NEVER sent unless `FlushProviders` returned nil (config file is on disk and valid).

**Why PATCH instead of proc.restart():**
- No process spawn/death race condition
- No health-check polling loop
- No port rebind
- opencode stays alive and re-reads its config file immediately
- Simpler error handling (HTTP 4xx/5xx vs process exit codes)

### Formatter: `FormatOpenCodeConfig`

Pure function. Lives in `pkg/agent/opencode/format.go`.

```go
func FormatOpenCodeConfig(providers []secrets.LLMProviderData) ([]byte, error)
```

**Input:** Ordered slice of validated `LLMProviderData` (guaranteed non-empty by `FlushProviders` guard).

**Output:** JSON bytes representing a valid opencode config file.

**Resolution rules for multi-provider ambiguity:**

| Field | Rule | Rationale |
|-------|------|-----------|
| `model` (default) | First provider (in batch order) with non-empty `Default` wins | Batch order is deterministic (JSON array order from API). First = highest priority. |
| Top-level `model` absent | Omit field entirely. opencode will prompt user to select. | Safe degradation — no broken default. |
| `SmallModel` | Not rendered. opencode has no `small_model` config field. | Validated by reading `Config.Info` schema — no such field exists. |

**SmallModel note:** The original design assumed a `small_model` top-level field. This does not exist in opencode's schema. `SmallModel` in `LLMProviderData` is reserved for future use (e.g., Codex formatter) but has no effect in the opencode formatter. Document this; don't invent config fields.

### Output Format

Given input:
```json
[
  {"provider": "anthropic", "apiKey": "sk-ant-123", "baseURL": "", "models": [], "default": "anthropic/claude-sonnet-4-5"},
  {"provider": "openai", "apiKey": "sk-openai-456", "baseURL": "https://custom.endpoint/v1", "models": [{"id": "gpt-4o", "label": "GPT-4o"}]}
]
```

Produces:
```json
{
  "$schema": "https://opencode.ai/config.json",
  "providers": {
    "anthropic": {
      "options": {
        "aisdk": {
          "provider": {
            "apiKey": "sk-ant-123"
          }
        }
      }
    },
    "openai": {
      "endpoint": {
        "type": "openai/responses",
        "url": "https://custom.endpoint/v1"
      },
      "options": {
        "aisdk": {
          "provider": {
            "apiKey": "sk-openai-456"
          }
        }
      },
      "models": {
        "gpt-4o": {
          "name": "GPT-4o"
        }
      }
    }
  },
  "model": "anthropic/claude-sonnet-4-5"
}
```

**Mapping logic:**

| LLMProviderData field | opencode config location | Notes |
|----------------------|-------------------------|-------|
| `Provider` | key in `providers` map | Used as provider ID |
| `APIKey` | `providers.{id}.options.aisdk.provider.apiKey` | Always set |
| `BaseURL` (non-empty) | `providers.{id}.endpoint` | Endpoint type depends on provider (see below) |
| `BaseURL` (empty) | Omit `endpoint` entirely | opencode uses built-in default for known providers |
| `Models` (non-empty) | `providers.{id}.models` | Key = model ID, value = `{name: label}` if label set |
| `Models` (empty) | Omit `models` entirely | All provider models visible (opencode default) |
| `Default` | top-level `model` | First non-empty wins |
| `SmallModel` | Not rendered | No opencode equivalent |

**Endpoint type resolution for BaseURL:**

When `BaseURL` is set, we need to specify an endpoint type. Resolution:

| Provider | Endpoint type |
|----------|--------------|
| `anthropic` | `{"type": "anthropic/messages", "url": "<baseURL>"}` |
| `openai` | `{"type": "openai/responses", "url": "<baseURL>"}` |
| Any other | `{"type": "aisdk", "package": "@ai-sdk/openai-compatible", "url": "<baseURL>"}` |

This matches opencode's endpoint dispatch logic. Known providers have typed endpoints; unknown providers use the generic `aisdk` + `@ai-sdk/openai-compatible` package (same as the existing litellm config in README.md).

---

## Changes Required

### 1. Neutralize `applyAPIKey()` — stop writing to `AgentConfigPath`

`applyAPIKey()` currently writes raw JSON to `AgentConfigPath`, conflicting with `FlushProviders()`. Since `api-key` is not in production for LLM use and `llm-provider` replaces it for that purpose:

- Change `applyAPIKey()` to write to `SecretsEnvPath` as an env var (e.g., `export API_KEY_<name>=<value>`) instead of `AgentConfigPath`
- This preserves `api-key` for future non-LLM uses (MCP server tokens, webhook secrets) without conflicting with the agent config file
- Remove `"api-key"` from `shouldRestart()` — env-file changes are picked up by the existing env-reload mechanism

**Alternative considered:** Delete `api-key` entirely. Rejected because it may have future non-LLM uses, and the cost of keeping it alive with a non-conflicting write path is near zero.

### 2. New file: `pkg/agent/opencode/format.go`

```go
// FormatOpenCodeConfig renders []LLMProviderData into opencode's config JSON.
// Pure function — no side effects.
func FormatOpenCodeConfig(providers []secrets.LLMProviderData) ([]byte, error)
```

Implementation details:
- Use a struct type for serialization (not `map[string]interface{}`) to guarantee field ordering
- Sort provider keys alphabetically for deterministic output (JSON maps have no order guarantee but sorted keys aid debugging and testing)
- Use `json.Marshal` with struct tags, not manual string building
- Validate: at least one provider must be present (caller should guarantee this, but defensive check)

### 3. New file: `pkg/agent/opencode/format_test.go`

Test cases:
- Single provider, no optional fields → minimal config
- Single provider, all fields populated → full config with endpoint + models + default
- Multiple providers, first has Default → model field set from first
- Multiple providers, none has Default → model field omitted
- Multiple providers, second has Default → model field set from second
- Provider with BaseURL → correct endpoint type mapping per provider name
- Unknown provider with BaseURL → aisdk/openai-compatible endpoint
- Models with labels → `name` field set
- Models without labels → `name` field omitted
- Deterministic: same input always produces same bytes (sort verification)
- Empty input → error (defensive)

### 4. Evolve `AgentRuntime` interface

The current interface:
```go
type AgentRuntime interface {
    Type() AgentType
    ValidateCredentials(rawConfig []byte) (*CredentialCheckResult, error)
    FormatCredentials(rawConfig []byte) ([]byte, error)
}
```

Change `FormatCredentials` to accept structured provider data:

```go
type AgentRuntime interface {
    Type() AgentType
    ValidateCredentials(rawConfig []byte) (*CredentialCheckResult, error)
    FormatProviderConfig(providers []secrets.LLMProviderData) ([]byte, error)
}
```

This enables the future multi-agent dispatch pattern:
```go
// Future: reload handler selects formatter from agent registry
agent, _ := agent.Get(agentType)
formatter := agent.FormatProviderConfig
m.FlushProviders(formatter)
```

For Step 2, the reload handler still directly passes `opencode.FormatOpenCodeConfig` (the hardcoded opencode path). The interface change is forward-looking — when Claude Code support is added, its `AgentRuntime` implementation provides its own `FormatProviderConfig`, and the handler switches to the registry-based dispatch.

**Why not just use the callback directly?** For Step 2, the callback is simpler. But the interface gives us:
- Discoverability: `agent.Get("claude-code").FormatProviderConfig` is self-documenting
- Testing: Mock the interface in reload handler tests
- Consistency: All agent-specific logic lives behind one interface

### 5. Wire into reload handler

In `cmd/workspace-agentd/secrets.go`, the `reloadSecretsHandler`:

```go
// After Materialize succeeds:
if err := m.FlushProviders(opencode.FormatOpenCodeConfig); err != nil {
    // Respond 500. Do NOT restart.
    log.Error("reload-secrets: flush providers failed", zap.Error(err))
    w.WriteHeader(http.StatusInternalServerError)
    _ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
    return
}

// Only now is it safe to restart.
if proc != nil && shouldRestart(batch) {
    proc.restart()
    restarted = true
}
```

### 6. Update `shouldRestart()`

Remove `"api-key"` (no longer triggers restart since it writes to env file). Keep `"env-secret"` and `"llm-provider"`.

**Future:** When hot-reload is validated, `"llm-provider"` is removed from this list too — the handler will call the agent's reload API instead of restarting.

---

## Failure Modes & Mitigations

| Failure | Impact | Mitigation |
|---------|--------|-----------|
| Formatter produces invalid JSON | opencode fails to start after restart | Impossible: `json.Marshal` on typed structs cannot produce invalid JSON. Unit test confirms round-trip parse. |
| Formatter produces valid JSON with wrong schema | opencode starts but ignores providers | Integration test: boot opencode with generated config, verify providers appear in `/config/providers` response. (Deferred to integration test phase.) |
| FlushProviders fails (disk full, permissions) | Config file not updated, agent not restarted | Handler returns 500. API server retries later. Agent continues with previous config. |
| Batch contains only non-llm-provider secrets | FlushProviders is a no-op (no staged providers) | Correct: no config write needed, restart still triggered by env-secret if present. Previous config file remains from last successful flush. |
| opencode config format changes upstream | Silent breakage | Pin opencode version. Add schema version assertion in integration tests. Track upstream releases. |

---

## What This Design Does NOT Do (Step 2 Scope)

1. **No hot-reload.** Restart is the current mechanism. Hot-reload is a future optimization — the architecture supports dropping the restart call with zero structural changes when validated.
2. **No `SmallModel` rendering.** opencode has no equivalent field. Reserved for future agent formatters (e.g., Aider supports `--weak-model`).
3. **No multi-agent dispatch in the handler.** The reload handler hardcodes the opencode formatter. Future: read agent type from pod config, dispatch via `AgentRuntime` interface.
4. **No model discovery.** Model discovery from provider APIs (point 1b in product arc) is a separate service. This design only renders the allowlist that results from discovery.
5. **No multi-workspace propagation.** That's the controller/API server pushing to `/v1/reload-secrets` on each pod. Out of scope for the materializer.
6. **No org-level credentials.** The `LLMProviderData` struct works identically for org and individual credentials — the materializer doesn't care about ownership. Org support is an API/binding layer concern.

---

## Future Work (Informed by Product Arc)

### Hot-Reload Path (replaces restart)

When validated against a running opencode instance:
1. After `FlushProviders()` writes the config file, instead of `proc.restart()`:
   - Call opencode's `PATCH /config` or equivalent reload endpoint
   - Verify providers appear in `/config/providers` response
   - Only restart as fallback if reload fails
2. The handler code change is ~5 lines. No materializer changes needed.

### Claude Code / Codex / Aider Formatters

Each agent needs:
1. A `FormatProviderConfig([]LLMProviderData) ([]byte, error)` implementation
2. Registered on `AgentRuntime`
3. Possibly a different output path (Claude Code writes to `~/.claude/settings.json`, not `/tmp/agent-config.json`)

The `Paths` struct already has `AgentConfigPath` — for non-opencode agents, this would be set to their respective config path. Or: `FormatProviderConfig` returns bytes + path (consider extending return type if needed).

### Model Discovery Service

Separate from the materializer. Flow:
1. User creates credential → API server calls provider's `/models` endpoint
2. API returns discovered models → user hides/shows models → stored in `LLMProviderData.Models`
3. On next propagation, formatter renders only visible models

The materializer doesn't participate in discovery — it just renders what it receives.

---

## Complexity Assessment

| Dimension | Rating | Notes |
|-----------|--------|-------|
| Lines of code | ~120 (format.go) + ~200 (format_test.go) + ~20 (handler wiring) + ~30 (deletions) | Net addition small |
| New abstractions | 0 | Uses existing `LLMProviderFormatter` callback type |
| New dependencies | 0 | Standard library only |
| Interface changes | 1 (remove dead `FormatCredentials` from `AgentRuntime`) | Breaking change is safe — method has zero callers |
| Config format coupling | Moderate | Tied to opencode's `ConfigProvider.Info` schema. Mitigated by pinning version + integration test. |

---

## Implementation Order

1. Delete `applyAPIKey()` and `SecretTypeAPIKey` references (cleanup)
2. Implement `FormatOpenCodeConfig` + tests (pure function, no dependencies)
3. Remove `FormatCredentials` from `AgentRuntime` interface + implementations
4. Wire `FlushProviders(opencode.FormatOpenCodeConfig)` into reload handler
5. Add reload handler test verifying flush-before-restart ordering
6. Update worklog
