# 0137 — SDK and MCP canary test suite

**Date:** 2026-06-03  
**Author:** mikekao  

## Summary

Implemented a comprehensive canary test suite for all three SDKs (Go, Python, TypeScript) and the MCP server. Canaries are structured as two-tier Fission functions (shallow every 1 minute, deep every 5–15 minutes) and also run as a CI job (`sdk-canary`) against a local API server on every PR.

## What was built

### `sdks/canary/TESTPLAN.md` (v3.0)

38 named scenarios across two tiers. Three major revision cycles during design:
- v1: initial 33 scenarios (obvious gaps)
- v2: added 18 missing workflows (logout/JWT revocation, `GET /auth/config`, secrets audit, key rotation, account recovery, question/permission input flow, verbose flag, SSE events, user settings, rate limiting, session limit, env injection)
- v3: added 20 more gaps found by reading the actual handler/service code: proxy error shapes (`workspace not ready` 503, path traversal 400), storage size validation, `imageTag`/`agentVersion` in workspace list, `activate` auto-eviction, `ensure-session` auto-resume assertion, status resource fields (`imageTag`, disk/memory/context, `conditions[]`, `lastActivityAt`), session `parentId` backfill, schema version drift detection, idempotency cases (re-create after delete, double bind, reload-empty), suspend-already-suspended as 409 assertion, `GET /sessions/:sessionId` individual endpoint, `prompt_async`+SSE as first-class scenario, connection limit 429, `D-MCP-PROMPT-ASYNC` to catch MCP-specific breakage

### Two-tier model

The key design decision: shallow and deep canaries have fundamentally different failure signatures and acceptable flakiness:
- **Shallow (Tier 1, 1 min):** "Is the API broken?" — deterministic, < 30s, near-zero flakiness. Alert on 1st failure.
- **Deep (Tier 2, 5–15 min):** "Is the product broken?" — spans K8s CRD reconciliation, agent startup, credential injection. Alert on 2nd consecutive failure.

### Go SDK additions (`sdks/go/services.go`, `types.go`, `client.go`)

Added ~30 missing methods to bring the Go SDK to feature parity with TypeScript and Python:
- `Auth`: `CreateAPIKey`, `ListAPIKeys`, `DeleteAPIKey`
- `Workspaces`: `Restart`, `Rename`, `Activate`, `GetStatus`, `SetBindings`, `GetBindings`, `ReloadSecrets`, `SetModel`, `GetModels`, `SetEnv`, `GetEnv`, `DeleteEnv`
- `Sessions`: `List`, `GetActive`, `Rename`, `Get`, `SendPromptAsync`
- `Secrets`: `Get`, `Update`, `Reveal` (with password param), `GetAuditLog`, `GetBindingsForSecret` — also fixed `List` to handle `{"secrets": [...]}` wrapper
- New types: `APIKey`, `WorkspaceStatus`, `WorkspaceCondition`, `CredentialState`, `AgentHealth`, `ActivateWorkspaceResponse`, `SessionListItem`, `ActiveSessionsResponse`, `BindingsResponse`, `ReloadResult`, `ModelListResponse`, `ModelItem`, `AuditEntry`, `UserSettings`
- New services: `UserSettingsService`, `AccountService` (rotate-key, change-password, recover)

### Python SDK additions (`sdks/python/llmsafespace/client.py`)

- `workspaces`: `restart`, `set_bindings`, `get_bindings`, `reload_secrets`, `set_model`, `get_models`, `set_env`, `get_env`, `delete_env`
- `sessions`: `rename`, `get`, `get_active`, `send_prompt_async`
- `secrets`: `update`, `reveal` (now takes password param), `get_audit_log`, `get_bindings_for_secret` — fixed `list` to handle `{"secrets": [...]}` wrapper

### Canary implementations

**Go SDK canaries (`sdks/canary/go/`):**
- Shared framework: `result.go` (Runner, Result, Check, HTTP helpers), `config.go` (Config, wait helpers, SDK factories)
- Shallow: S-HEALTH, S-AUTH, S-AUTH-CONFIG, S-LOGOUT, S-APIKEY, S-USER-SETTINGS, S-WS-CRUD, S-WS-STATUS, S-SECRET-CRUD, S-SECRET-REVEAL, S-SECRET-AUDIT, S-SECRET-BINDINGS, S-ENV-VARS, S-CRED-CRUD, S-ERROR-FORMAT
- Deep: D-WS-LIFECYCLE, D-SESSION-ENSURE, D-SESSION-MSG, D-PROMPT-ASYNC, D-CRED-BIND, D-CRED-MODEL-FLOW, D-SUSPEND-RESUME-SESSION, D-SSE-EVENTS

**Python SDK canaries (`sdks/canary/python/`):**
- Shared framework: `canary.py`
- Shallow: s_auth, s_ws_crud, s_secret_crud, s_error_format
- Deep: d_cred_model_flow, d_suspend_resume_session

**TypeScript SDK canaries (`sdks/canary/typescript/`):**
- Shared framework: `canary.ts`
- Shallow: s-auth, s-ws-crud, s-error-format
- Deep: d-cred-model-flow

**MCP server canary (`sdks/canary/mcp/main.go`):**
- Communicates via stdio transport (spawns the mcp binary)
- Covers S-MCP-TOOLS (tool registry completeness), S-MCP-AUTH-NEG (bad key → isError, not JSON-RPC error), S-MCP-CRED (credential CRUD), S-MCP-INPUT-NEG (missing args, oversized message)

### Fission manifests (`sdks/canary/fission/canary-functions.yaml`)

Fission `Function` and `TimeTrigger` resources for all deployed scenarios. Functions reference a `canary-go-shallow` package and mount a `llmsafespace-canary-secrets` K8s Secret for env vars.

### CI job (`.github/workflows/ci.yml`)

Added `sdk-canary` job after `sdk-contract`, running all `ci:fast` scenarios:
- Starts PostgreSQL and Valkey services
- Builds and starts the API server locally
- Runs migrations
- Seeds test accounts via a bootstrap script
- Runs all shallow canaries for Go, Python, and TypeScript
- Runs MCP canaries

### `sdks/canary/README.md`

Documents structure, how to run locally, two-tier model, test accounts, Fission deployment, and result format.

## Most important scenario: D-PROMPT-ASYNC

The `prompt_async` + SSE flow had **zero canary coverage** before this work despite being the exact code path the MCP server uses internally for `session_message`. If `prompt_async` silently breaks while the synchronous `sendMessage` (which the SDK canaries use) still works, all SDK tests pass but every MCP `session_message` call starts timing out. The `D-PROMPT-ASYNC` scenario closes this gap by directly testing `POST /sessions/:id/prompt_async` + subscribing to `GET /events` and asserting a `session.status{status:idle}` event is received.

## Not yet implemented

The following scenarios from TESTPLAN.md are designed but not yet implemented as code:
- S-OWNERSHIP, S-SECRET-BINDINGS, S-ENV-VARS, S-WS-QUOTA, S-RATE-LIMIT (shallow but need setup)
- D-ACTIVATE-EVICTION, D-SESSION-TITLE, D-SESSION-LIMIT, D-SESSION-GET, D-AGENT-INPUT, D-SESSION-SUBTASK, D-TERMINAL, D-MODEL-LIST-ANNOTATED, D-MODEL-SET, D-ENV-INJECTION, D-KEY-ROTATE, D-CHANGE-PASSWORD, D-ACCOUNT-RECOVER
- MCP: D-MCP-WORKSPACE, D-MCP-SESSION, D-MCP-PROMPT-ASYNC, D-MCP-MODEL

These are the right next step after the CI integration is validated.
