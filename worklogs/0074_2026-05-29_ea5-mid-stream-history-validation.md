# Worklog: EA5 Mid-Stream History Validation

**Date:** 2026-05-29
**Session:** Validate whether `GET /session/:id/message` returns partial assistant messages while streaming is in progress.
**Status:** Complete

---

## Objective

Determine if opencode v1.15.12's history endpoint returns incomplete/partial messages when queried during an active streaming response. This has implications for frontend polling behavior and the proxy's `GetHistory` handler.

---

## Test Setup

- Created workspace `f8d311a1-2ccf-4adc-82ec-b12afb070393` via API (`upgrade-test` user)
- Pod confirmed running opencode v1.15.12
- Session: `ses_18ad86224ffeX42nm0KR5sLpj3`
- Sent a long prompt via `POST /session/:id/prompt_async` designed to take 30-60s with multiple tool calls:
  > Write a detailed comparison of three container orchestration platforms: Kubernetes, Docker Swarm, and Nomad. For each platform, first create a file called /workspace/comparison-{name}.md with a brief summary, then continue with your analysis.

- Polled `GET /session/:id/message` at T+5s, T+10s, and T+18s via direct pod port-forward

---

## Results

### T+5s (mid-stream)

```
Total messages: 2
  user: 1 parts
    [text] id=prt_e752822cf001iMKMB7rKG text=Write a detailed comparison...
  assistant: 4 parts
    [step-start] id=prt_e7528261c001Wke8S0Dlu
    [reasoning] id=prt_e7528293e001xdgqCJKJB text=The user wants a detailed comparison...
    [tool]      id=prt_e75282c9c001MEYSYLIa3
    [tool]      id=prt_e75283615001zg1CF5RnU
```

### T+10s (still streaming)

```
Total messages: 3
  user: 1 parts
    [text] id=prt_e752822cf001iMKMB7rKG
  assistant: 6 parts          ← grew from 4 to 6 (added tool + step-finish)
    [step-start]  [reasoning]  [tool]  [tool]  [tool]  [step-finish]
  assistant: 1 parts          ← second assistant turn started
    [step-start] id=prt_e752849e9001ZqEchcEHa
```

### Final (after completion)

```
Total messages: 3
  user: 1 parts
  assistant: 6 parts (step-start, reasoning, tool×3, step-finish)
  assistant: 3 parts (step-start, reasoning, text)
```

---

## Conclusion

**EA5 CONFIRMED:** `GET /session/:id/message` returns partial/incomplete assistant messages while streaming is in progress.

Key observations:

1. **Parts accumulate in real-time** — an assistant message returned at T+5s had 4 parts; the same message at T+10s had 6 parts
2. **Multiple assistant turns visible mid-stream** — by T+10s, a second assistant message had started appearing
3. **Every part has an `id` field** — format `prt_<ascending-id>`, confirming v1.15 part IDs flow through
4. **Multiple part types observed** — `step-start`, `reasoning`, `tool`, `text`, `step-finish`

**Implications for the proxy:** The `GetHistory` handler (`proxy.go:179`) streams this response through unmodified via `doProxy` with `stripPatch=false`. Frontend consumers that poll for history during an active session will see partial assistant messages. This is not a regression from v1.2.27 (same behavior) but is worth documenting for frontend developers.

---

## Clean Up

- Test workspace deleted via `DELETE /api/v1/workspaces/f8d311a1-2ccf-4adc-82ec-b12afb070393`
- Port-forward torn down

---

## Files Modified

- `worklogs/0074_2026-05-29_ea5-mid-stream-history-validation.md` (this file)
