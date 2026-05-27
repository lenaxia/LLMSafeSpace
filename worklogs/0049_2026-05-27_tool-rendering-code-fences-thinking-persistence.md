# Worklog: Streaming UX Fixes — Tool Rendering, Code Fences, Thinking Persistence

**Date:** 2026-05-27
**Session:** Fix tool call visibility, code block streaming, thinking persistence after completion, scrollbars, table borders
**Status:** Complete

---

## Objective

Fix multiple streaming UX issues reported during live testing:
1. Tool calls not visible during streaming
2. Code blocks not rendering during streaming
3. Thinking blocks disappearing after streaming completes
4. Table borders not visible
5. Scrollbars too large/default style

---

## Work Completed

### Tool call rendering during streaming (commit `4a7bc58`)
- `MessagePart` required `part.text || part.name` to render tool_use — but streaming tool parts have empty text
- Added `isStreaming` to the condition so tool parts render as "Tool call: tool" during streaming
- 2 new tests

### Scrollbars + table borders (commit `49b51d9`)
- Fixed scrollbar CSS: previous `hsl(var(...) / opacity)` syntax was broken (variable already contains `hsl()`)
- Replaced with `rgba()` colors, 4px width, no arrow buttons, grows on hover
- Added `.prose table` border styles: `border: 1px solid` on th/td with collapse

### Code blocks during streaming (commit `ac1c346`)
- ReactMarkdown can't render incomplete fenced code blocks (open ` ``` ` without closing)
- During streaming, count fence markers and append closing fence if odd count
- Code blocks now render progressively as they stream in

### Thinking persistence after streaming (commit `479d095`)
- Root cause: `useChatStream` fetches history on completion; `transformHistory` strips tool parts and may not include thinking
- Fix: completion callback uses `sseStreamPartsRef.current` (accumulated streaming parts) as the final assistant message
- Preserves full interleaved structure (thinking → tool → thinking → text) in the rendered message

### Tool part diagnostic logging (commit `479d095`)
- Added `console.log` dumping tool part field names, `name`, `toolName`, `id` to diagnose what data opencode sends for tool calls

---

## Key Decisions

1. **Streaming parts as source of truth for completed messages** — History API strips tool parts and may not include thinking. The streaming parts array IS the complete message structure.
2. **Auto-close code fences** — Simple regex count of ` ``` ` markers; if odd, append closing. Works for nested fences too (each level is a separate marker).
3. **Ref for completion callback** — React state is stale in async callbacks; ref synced via useEffect ensures the callback sees the latest parts.

---

## Blockers

- **Tool name/args not available** — `part.updated(tool)` events have empty text and unknown field structure. Diagnostic logging added; need user to provide console output showing `[SSE] tool part fields:` to determine what data is available.

---

## Tests Run

- `npx vitest run` — 393 tests passing
- `npx vite build` — production bundle successful

---

## Next Steps

1. Check `[SSE] tool part fields:` console output to determine tool name/args field names
2. Display meaningful tool names in the streaming bubble (e.g., "bash", "webfetch", "write")
3. Consider showing tool input/output in expandable sections

---

## Files Modified

- `frontend/src/components/chat/MessagePart.tsx` — tool rendering condition, auto-close code fences
- `frontend/src/components/chat/MessagePart.test.tsx` — 2 new tool streaming tests
- `frontend/src/pages/ChatPage.tsx` — sseStreamPartsRef, completion callback using ref, tool field logging
- `frontend/src/styles/index.css` — scrollbar CSS fix, table border styles
