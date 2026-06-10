# Worklog: Epic 10 — OpenCode Auth-Store Credential Injection

**Date:** 2026-06-02
**Session:** Validated opencode internals, rewrote PRR, implemented auth-store credential injection client
**Status:** Complete (unit tested, not e2e tested)

---

## Objective

Replace the `PATCH /global/config` credential injection mechanism (which disposes ALL opencode instances) with targeted `PUT /auth/:providerID` + `POST /instance/dispose` (disposes only the current instance). Validate all assumptions in the PRR against actual opencode source code.

---

## Work Completed

### PRR Validation & Rewrite

Cloned opencode upstream (anomalyco/opencode, v1.15.12 HEAD) and read source to validate every assumption in the original PRR. Found 4 critical false assumptions:

1. **`Auth.create` does not exist** — Auth.Service has `get`, `all`, `set`, `remove` only
2. **`Event.Switched` does not exist** in the auth/provider system — only session-level AgentSwitched/ModelSwitched events
3. **No `AccountPlugin` or `catalog.transform`** — these were fabricated
4. **`PUT /auth/:providerID` already exists** in the Control API group — the PRR claimed no endpoint existed

Discovered that the TUI itself calls `instance.dispose()` after `auth.set()` (dialog-provider.tsx:400), proving there is no automatic hot-reload mechanism.

Identified the correct hot-reload path: `InstanceState.invalidate(state)` on the Provider service's cache. This is selective (doesn't touch sessions, tools, MCP, LSP) and in-flight LLM calls survive because `session/llm.ts:101` resolves the LanguageModelV3 once at prompt start.

Also identified a layer scoping issue: Control API is root-scoped (no instance context), Provider.Service is instance-scoped. The upstream PR should add `POST /provider/refresh` to the instance-scoped ProviderApi group.

### Implementation

- **`pkg/agent/opencode/client.go`** — New `Client` struct with `PushCredentials`, `DisposeInstance`, `RefreshCredentials` methods
- **`pkg/agent/opencode/client_test.go`** — 14 TDD tests (httptest mock servers)
- **`cmd/workspace-agentd/secrets.go`** — Replaced `patchOpenCodeConfig` with `opencode.Client.RefreshCredentials`; removed dead function
- **`pkg/agentd/secrets/secrets.go`** — Added `StagedProviders()` public accessor
- **`pkg/agentd/secrets/secrets_test.go`** — 2 new tests for accessor
- **`design/stories/epic-10-multi-tenant-trust/opencode-auth-create-prr.md`** — Full rewrite with 15 validated assumptions
- **`design/stories/epic-10-multi-tenant-trust/llm-provider-step2-design.md`** — Updated Step 3 reference

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Use auth-store path (`PUT /auth/:providerID`) instead of config-file path (`PATCH /global/config`) | Targeted disposal (one instance, not all); no config file to manage; matches future hot-reload path |
| Two-call pattern (push + dispose) instead of single atomic call | Matches opencode's existing architecture; Control API is root-scoped, dispose is instance-scoped |
| Fail-fast on push error, skip dispose | If credentials didn't write, no reason to disrupt the running instance |
| Keep `FlushProviders` for config file write | Still needed for initial boot (entrypoint reads config before opencode starts) |
| `StagedProviders()` accessor on Materializer | Allows reload handler to access structured data for API injection without re-parsing |

---

## Blockers

None. Implementation complete pending e2e validation on cluster.

---

## Tests Run

```bash
# All new + existing tests pass with race detection:
go test -timeout 60s -short -race ./pkg/agent/opencode/    # 28 tests PASS
go test -timeout 60s -short -race ./pkg/agentd/secrets/    # 42 tests PASS  
go test -timeout 60s -short -race ./cmd/workspace-agentd/  # 15 tests PASS (subprocess tests skipped in -short)
```

---

## Next Steps

1. **E2e test on cluster**: Deploy updated agentd image, call `/v1/reload-secrets` with llm-provider batch, verify provider connects and prompt works
2. **Verify fallback**: Kill opencode, send reload-secrets, confirm `proc.restart()` fallback triggers
3. **Submit upstream PR**: `POST /provider/refresh` endpoint for selective invalidation (preserves in-flight calls)

---

## Files Modified

- `pkg/agent/opencode/client.go` (NEW)
- `pkg/agent/opencode/client_test.go` (NEW)
- `cmd/workspace-agentd/secrets.go` (MODIFIED — replaced patchOpenCodeConfig, removed dead code)
- `pkg/agentd/secrets/secrets.go` (MODIFIED — added StagedProviders accessor)
- `pkg/agentd/secrets/secrets_test.go` (MODIFIED — 2 new tests)
- `design/stories/epic-10-multi-tenant-trust/opencode-auth-create-prr.md` (REWRITTEN)
- `design/stories/epic-10-multi-tenant-trust/llm-provider-step2-design.md` (MODIFIED — line 41)
