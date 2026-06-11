# Worklog 0221 — opencode infinite retry loop on context-window overflow

**Date:** 2026-06-10

## Problem

Session `ses_1518fd31fffeNS1r3eKxWEMtFd` on workspace `a3a1c914-c10e-4b02-afe3-00098ee159bc`
appeared blank in the UX despite having 1,106 messages in the opencode SQLite DB on the PVC.

Investigation revealed two distinct issues:

1. **opencode upstream bug:** the agent entered an infinite retry loop after hitting the
   context window limit of `glm-5.1` (210,869 tokens). The loop ran for **38 minutes**,
   producing **741 identical failed LLM calls** before the session was abandoned.

2. **LLMSafeSpace frontend bug:** the blank UX is caused by `transformHistory` silently
   dropping messages whose parts are entirely `step-start`/`step-finish`. The first page
   fetch (50 messages, all from the tail of the loop) renders nothing, and the "Load
   earlier messages" button never appears because it is rendered inside the (empty) message
   list. The conversation content is intact and safe on the PVC.

## Root Cause — opencode infinite retry (upstream bug)

The session was performing a long agentic task with `glm-5.1`. At step ~N the context
reached 210,869 tokens — the model's context limit. At that moment the agent issued a
`todowrite` tool call. The tool completed successfully and the result was appended to the
context, pushing it to or past the limit.

The next LLM call sent the full 210,869-token context. The provider (`thekao.cloud` /
`glm-5.1`) returned a response with:
- `inputTokens = 210869`
- `outputTokens = 0`
- `finish_reason = null` / absent

opencode's AI SDK adapter (`packages/opencode/src/session/llm/ai-sdk.ts`) maps an absent
or unrecognized `finishReason` to the string `"unknown"` (line ~21). The `finish-step`
event carried `usage: { inputTokens: 210869, outputTokens: 0 }` and `reason: "unknown"`.

The session loop in opencode treated this as a normal (if empty) turn and immediately
retried. The `isOverflow()` pre-flight check in `overflow.ts` is evaluated before sending
to decide whether to compact — but compaction requires actual output tokens to summarize.
With zero output and `finish=unknown` there was nothing to compact, and no post-response
guard detected the "full context + zero output + unknown finish" pattern as terminal.

The loop ran from `2026-06-10T18:53:09Z` to `2026-06-10T19:31:41Z` (~38 minutes),
firing one call every ~3 seconds:

```
input_tokens:  210,869 (every call, no cache hits)
output_tokens: 0
finish:        "unknown"
duration:      ~3,000ms per call
total calls:   741
total tokens:  ~156,000,000
```

## Evidence

```sql
-- Confirmed directly on the workspace pod:
SELECT COUNT(*), MIN(time_created), MAX(time_created),
  (MAX(time_created)-MIN(time_created))/1000/60 AS minutes
FROM message
WHERE session_id='ses_1518fd31fffeNS1r3eKxWEMtFd'
  AND json_extract(data,'$.tokens.output') = 0
  AND json_extract(data,'$.finish') = 'unknown';
-- Result: 741 | 1781117589817 | 1781119901356 | 38
```

Last good message (before the cliff):
```json
{"finish":"tool-calls","tokens":{"total":202395,"input":289,"output":156,
 "reasoning":94,"cache":{"write":0,"read":201856}},"modelID":"glm-5.1"}
```

First bad message:
```json
{"finish":"unknown","tokens":{"total":210869,"input":210869,"output":0,
 "reasoning":0,"cache":{"write":0,"read":0}},"modelID":"glm-5.1"}
```

The `tokens.input` value (210,869) came from the provider API response — opencode does
not calculate token counts itself.

## Relevant opencode code paths

| File | Role |
|---|---|
| `packages/opencode/src/session/overflow.ts` | `isOverflow()` pre-flight only; no post-response check |
| `packages/opencode/src/session/llm/ai-sdk.ts:21` | Maps missing `finishReason` → `"unknown"` silently |
| `packages/opencode/src/session/session.ts:390` | `getUsage()` — token accounting correct, but no overflow detection here |

## Impact

- ~156M tokens sent to `thekao.cloud` relay endpoint over 38 minutes.
- Workspace `contextUsed` stat inflated to 159,806,311 (mostly from this loop).
- Session history invisible in UX (separate frontend bug, tracked separately).

## Action Required

### Upstream (opencode — anomalyco/opencode)
File issue requesting a post-response guard: if `finishReason === "unknown"` AND
`outputTokens === 0` AND context is at or near the model's limit, treat as a terminal
error rather than retrying. The session should surface an error message to the user
and halt.

### LLMSafeSpace (frontend — tracked separately)
The "Load earlier messages" button must render even when the current visible message
count is zero, as long as `hasNextPage` is true. Additionally, if a fetched page
produces zero renderable messages but a next cursor exists, the frontend should
automatically fetch the next page rather than silently stopping.

## Status

- [x] Upstream opencode issue filed: https://github.com/anomalyco/opencode/issues/31757
- [ ] LLMSafeSpace frontend pagination fix
