# Worklog: Fix Context Usage Bar — 0/Unknown for All Sessions

**Date:** 2026-06-14
**Session:** Root-cause and fix the context usage bar showing "0/Unknown" for all sessions. Prove fixes by live cluster experiment before writing code.
**Status:** Complete

---

## Objective

The frontend context usage bar displayed "0/Unknown" for every session regardless of how much LLM
history the session had. Identify the root cause with evidence, fix it, and ship tests.

---

## Investigation

### Validated facts (all confirmed by direct cluster observation)

**session_index.context_used is NULL for all rows:**
Direct PostgreSQL query — 613 rows, 0 with `context_used` set. `session_index` is what the Sidebar
reads via the `/sessions` API endpoint. This is why the numerator is always 0.

**SSE tracker IS connected:**
Checked `/proc/net/tcp` (IPv4 table, not tcp6) on workspace pods — both API pod IPs have established
connections to port 4096 on every Active workspace pod. Earlier investigation incorrectly checked
`/proc/net/tcp6`; pod-to-pod traffic in this cluster uses IPv4.

**`session.next.step.ended` is never emitted to `/event`:**
90-second live SSE monitor across 4 simultaneously busy workspaces. Zero `step.ended` events.
Hundreds of `message.part.delta`, `message.updated`, `session.status` events arrived fine.
Sessions completed (last_message_at updated in DB) but `context_used` stayed NULL.

**Root cause of missing event — `OPENCODE_EXPERIMENTAL_EVENT_SYSTEM` not set:**
In `opencode/src/session/processor.ts`, `Step.Ended` is published only when
`flags.experimentalEventSystem = true`. This flag reads `OPENCODE_EXPERIMENTAL_EVENT_SYSTEM`.
The env var was not set in workspace pods. Confirmed via `strings` on the running binary — the flag
exists in opencode 1.15.12.

**`contextTotal` is always 0 because `/v1/models` returns no context window data:**
`thekao cloud` provider model entries in `agent-config.json` were written as `{}` — no `limit` object.
opencode served `limit.context=0` from `/config/providers`. `ModelContextLimit()` returned 0.
The `/v1/models` API returns only `{id, object, created, owned_by}`. `/v1/model/info` requires
elevated key permissions that standard LLM keys don't have. Limits cannot be auto-discovered.

---

## Live Experiments (proof before code)

### Experiment 1 — contextUsed fix

1. Added `OPENCODE_EXPERIMENTAL_EVENT_SYSTEM=true` to `/tmp/secrets-env` on workspace `3c600987`.
2. Killed opencode. agentd restarted it. Verified flag in process env via `/proc/{pid}/environ`.
3. Sent a real LLM prompt via `POST /session/{id}/message`.
4. Captured SSE stream simultaneously. Event appeared:
   ```json
   {"type":"session.next.step.ended","properties":{"sessionID":"ses_13b6fbddffferdxZvCw7Ui5JX7",
   "tokens":{"input":54,"output":3,"reasoning":0,"cache":{"write":0,"read":114368}}}}
   ```
5. DB write happened at the same second: `context_used=114422`. Math exact:
   `input(54) + cache.read(114368) + cache.write(0) = 114422`.

### Experiment 2 — contextTotal fix

1. Used `jq` to write `limit.context=200000` into all `thekao cloud` model entries in `/tmp/agent-config.json`.
2. Restarted opencode. `/config/providers` immediately returned `ctx=200000` for all thekao cloud models.
3. `agentd /v1/statusz` reported `context.total_tokens=200000`.
4. CRD `status.contextTotal` updated to `200000` within 35 seconds (one controller poll cycle).
5. Frontend showed `114k/200k` correctly.

---

## Fixes

### Fix 1 — `controller/internal/workspace/pod_builder.go`

Added `OPENCODE_EXPERIMENTAL_EVENT_SYSTEM=true` to the workspace container env. One line, unconditional.
This enables the opencode v2 event system which emits `session.next.step.ended` to the `/event` SSE stream,
allowing the API proxy's `persistContextFromEvent` to write `context_used` to `session_index`.

### Fix 2 — `pkg/secrets/types.go` + `pkg/agent/opencode/format.go`

Added `ContextLimit int` field to `LLMModelConfig`. When non-zero, `FormatOpenCodeConfig` writes it as
`limit.context` in the model entry in `agent-config.json`. Users configure this explicitly in their
credential secret because the `/v1/models` endpoint does not expose context window sizes.

The model enricher (`model_enricher.go`) is unchanged. It only runs when `Models` is empty, so it never
overwrites user-configured `ContextLimit` values. Models discovered by the enricher have `ContextLimit=0`
by design — documented in a new test.

---

## Tests Added

All tests are named to document _why_ the assertion exists:

- `TestPodBuilder_ContainerEnv_OpenCodeExperimentalEventSystem` — env var present in pod spec
- `TestPodBuilder_ContainerEnv_RequiredVars` — baseline env vars
- `TestFormatOpenCodeConfig_ContextLimit_WrittenAsLimitContext` — ContextLimit→`limit.context`
- `TestFormatOpenCodeConfig_ContextLimit_Zero_NoLimitField` — zero ContextLimit→no limit field
- `TestFormatOpenCodeConfig_ExactSnapshot_WithContextLimit` — full wire-format snapshot
- `TestEnrichProviderModels_PreservesContextLimitOnExistingModels` — enricher preserves user values
- `TestEnrichProviderModels_FetchedModels_HaveZeroContextLimit` — enricher-fetched models have no limit

---

## What this does NOT fix

- Sessions on existing workspace pods will not see the fix until those pods are recreated (the env var
  is injected at pod creation time). Running workspaces must be stopped and restarted.
- `ContextLimit` for custom provider models must be configured manually by the workspace owner in their
  credential secret. There is no automatic source for this data.

---

## Files Modified

- `controller/internal/workspace/pod_builder.go` — add env var to container spec
- `controller/internal/workspace/pod_builder_test.go` — new file, pod builder tests
- `pkg/secrets/types.go` — add `ContextLimit` to `LLMModelConfig`
- `pkg/agent/opencode/format.go` — write `limit.context` in model entries; add `opencodeModelLimit` type
- `pkg/agent/opencode/format_test.go` — add ContextLimit tests
- `cmd/workspace-agentd/model_enricher_test.go` — add ContextLimit preservation tests
