# 0049 — Context Limit UX & Error Surfacing

**Date:** 2026-06-09  
**Triggered by:** Session `ses_1571ba63affeGzSAx75T4tE9oc` (workspace `a3a1c914`, "Greeting") halting silently after the agent called `grep` with missing `pattern` field.

---

## Root Cause Investigation

### What actually happened

The chat halted because `glm-5.1` (via `thekao cloud` / LiteLLM / Z.AI) returned `finish_reason=length` after only 30 output tokens. The model was mid-generation of a malformed `grep` tool call (missing required `pattern` field). After the tool error result was sent back, the next step returned `reason=unknown` with 0 output tokens — the model silently refused to continue.

The session had `~196,806` input tokens against a model limit of 200,000. Each subsequent message (including a "continue" sent after the halt) triggered the same empty response because the context was effectively full.

### Why auto-compaction did not fire

opencode's `isOverflow()` (`overflow.ts:29`) returns `false` when `model.limit.context === 0`. The context limit for `glm-5.1` was 0 in opencode because:

1. LiteLLM's model config had no `max_input_tokens` field — only `max_tokens` (output limit).
2. `model_enricher.go` in agentd only populates `LLMModelConfig{ID: m.ID}` with no context window.
3. `ModelContextLimit()` returned 0.
4. Fallback in `statusz` handler: `128000 * max(len(sessions), 1)` = `5 × 128K = 640K` — a fabricated number shown in the UI as if it were real.

### Why the UI showed 640K

The `640,000` shown in the context bar had nothing to do with the model. It was `5 sessions × 128,000` — a pure heuristic fallback with no basis in the provider's actual limit.

---

## Fixes Made

### 1. LiteLLM model config (`talos-ops-prod`)

Added `max_input_tokens` to all models so LiteLLM surfaces context windows via `/v1/models`. Also corrected `max_tokens` (output limits), removed deprecated `deepseek-chat`/`deepseek-reasoner` aliases, removed legacy `o3-mini-reasoning` and `gpt-5.4`, added `supports_function_calling`, `supports_response_schema`, `supports_vision` to all models.

Key values (sourced from official provider docs):
- `glm-5.1`, `glm-4.7`, `glm-4.6`: `max_input_tokens=200000`, `max_tokens=131072`
- `deepseek-v4-flash`, `deepseek-v4-pro`: `max_input_tokens=1000000`, `max_tokens=384000`
- `gpt-5.5`: `max_input_tokens=1000000`, `max_tokens=128000`
- `gpt-5.4-mini`: `max_input_tokens=400000` (not 1M — aggregator sites were wrong)
- `bedrock-claude-sonnet-4.6`: `max_input_tokens=1000000`, `max_tokens=64000`

**Committed to `talos-ops-prod` main as `6a17ce53`.**

### 2. agentd — remove fabricated fallback (`cmd/workspace-agentd/main.go`)

Removed the `128K × sessions` fallback. `TotalTokens=0` is now passed through when `ModelContextLimit()` returns 0. This is the honest signal — "we don't know the limit."

### 3. Frontend — context bar "Unknown" with tooltip (`DiskUsageBar.tsx`)

When `contextTotal=0` (or missing), the context bar now shows:
- Used token count
- "Unknown" badge (yellow, underlined, cursor-help)
- On hover or click: tooltip explaining that the provider did not report a context window size and that auto-compaction is disabled, with a hint to set `max_input_tokens` in LiteLLM.

No progress bar is shown when the limit is unknown (a progress bar at 0% or at a fabricated total is misleading).

### 4. Frontend — stream interrupted banner (`useChatStream.ts`, `ChatPage.tsx`)

Added `streamTimedOut` flag to `useChatStream`. When the 60s `IDLE_WAIT_TIMEOUT_MS` timeout fires without an `session.status=idle` SSE event **and** the server is not still actively busy (slow response guard), a red banner appears:

> Response interrupted — the connection timed out  [Dismiss]

**Validation finding:** `serverBusyRef` (a live ref, not a stale closure value) is checked at timeout-fire time to suppress false positives for slow-but-healthy responses. The canary SDK uses a 90s timeout for the same wait, confirming 60s is not a safe upper bound for normal operation.

The banner is also auto-dismissed when `session.status=idle` arrives late (covering the race where the idle event was buffered).

### 5. Frontend — retry banner (`SessionRetryBanner.tsx`, `ChatPage.tsx`)

New `SessionRetryBanner` component (yellow, matching `HealthBanner` style) shown when opencode is retrying after a provider error.

**Validation findings that changed the implementation:**

- The proxy's synthesized `session.status` string event for `retry` carries only `"busy"` — the rich retry payload (`attempt`, `message`, `next`, `action`) only travels inside an `opencode.event` wrapper with `event_type="session.status"`. The retry handler was moved to the `opencode.event` path accordingly. The original `typeof event.status === "object"` branch in the `session.status` handler was dead code and was removed.

- `status.next` is an **absolute epoch timestamp in milliseconds** (`Date.now() + delay`), not a relative duration. `SessionRetryBanner` now initialises remaining time as `Math.max(0, status.next - Date.now())`.

The banner shows:
- Spinning refresh icon
- `status.message` text
- Attempt number (if >1)
- Live countdown to next attempt
- Optional action link (`status.action.label` + `status.action.link`)

Disappears when `session.status=idle` or `session.status=busy` fires.

### 6. Frontend — error name mapping (`ChatPage.tsx`)

The `session.error` SSE handler previously collapsed all error names to a raw string. Now maps known names to actionable text:

| `error.name` | Before | After |
|---|---|---|
| `MessageOutputLengthError` | `⚠️ MessageOutputLengthError` | `⚠️ Response was too long for this model's output limit` |
| `ContextOverflowError` | `⚠️ Agent error` | `⚠️ Context limit reached — type /compact to summarize the conversation and continue` |
| `ProviderAuthError` | `⚠️ <raw provider text>` | `⚠️ Authentication failed for <providerID> — check your credentials` |

---

## Validation Findings (known gaps)

### Path A overflow is still silent

When `isOverflow()` fires at `step-finish` (soft overflow, token count ≥ usable limit), opencode sets `needsCompaction=true` and returns `"compact"` to the caller — **no `session.error` event is emitted**. The user gets no warning before compaction starts; the session just silently transitions. `ContextOverflowError` only fires on Path B (hard overflow — provider rejects the request), and in that path `status.set("idle")` is also never called, leaving the session stuck in `"busy"` after the error.

The glm-5.1 halt (Path A variant with `reason=length` + silent empty follow-up) is **still not surfaced** by any of these changes. The LiteLLM `max_input_tokens` fix prevents the condition from arising on future sessions, but if it happens again (e.g. a different model with an unreported limit), the user will still see a stalled chat with no explanation.

### `model_enricher.go` still doesn't read `max_input_tokens`

`agentd`'s `fetchModels` populates `LLMModelConfig{ID: m.ID}` only. Even with `max_input_tokens` now in LiteLLM, agentd never reads it from the `/models` response. `ModelContextLimit()` will still return 0 for LiteLLM-proxied models until `model_enricher.go` is updated to parse and store the context window.

### ~~`SessionRetryBanner` has no tests~~ ✓ resolved in commit `10a3f60d`

### ~~Error name mapping is untested inline code~~ ✓ resolved in commit `10a3f60d`

---

## Files Changed

### LLMSafeSpace
- `cmd/workspace-agentd/main.go` — remove 128K×sessions fallback
- `frontend/src/components/workspace/DiskUsageBar.tsx` — Unknown context tooltip
- `frontend/src/components/chat/SessionRetryBanner.tsx` — new component
- `frontend/src/hooks/useChatStream.ts` — streamTimedOut + serverBusyRef
- `frontend/src/pages/ChatPage.tsx` — retry banner, interrupted banner, error name mapping
- `frontend/src/api/types.ts` — SessionStatusEvent comment clarifying retry shape
- `docs/0049_2026-06-09_context-limit-ux-error-surfacing.md` — this file

### talos-ops-prod
- `kubernetes/apps/home/localai/litellm/helm-release.yaml` — model list overhaul
