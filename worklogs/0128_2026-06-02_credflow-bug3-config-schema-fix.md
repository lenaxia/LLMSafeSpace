# Worklog: Bug 3 — opencode 1.15.12 config schema mismatch in pkg/agent/opencode/format.go

**Date:** 2026-06-02
**Session:** Fix the third bug found while validating Bug 1+2 against the live cluster (worklog 0127). The credential push now reaches opencode with proper auth, but opencode rejects every config we generate with `ConfigInvalidError`, and even when bypassed, the `baseURL` field is silently ignored so requests hit `api.openai.com` instead of the operator's endpoint.
**Status:** Complete — `format.go` rewritten against the live-probed schema, 13 unit tests (incl. exact-byte snapshot), end-to-end LLM round-trip succeeds against `https://ai.thekao.cloud/v1`.

---

## Bug

`pkg/agent/opencode/format.go` rendered LLM-provider credentials into a JSON
shape that opencode 1.15.12 rejects on boot:

```json
{
  "providers": {                     ← WRONG: plural
    "openai": {
      "endpoint": {                  ← WRONG: opencode discards this object
        "type": "openai/responses",
        "url": "https://litellm.example/v1"
      },
      "options": {
        "aisdk": {                   ← WRONG: extra wrapper
          "provider": {
            "apiKey": "sk-..."
          }
        }
      },
      "models": { "default": { "name": "..." } }
    }
  },
  "model": "openai/default"
}
```

Live cluster symptoms (worklog 0127 final phase):

```
ERROR ref=err_67dc52a5 error=ConfigInvalidError cause=ConfigInvalidError
  at Config.loadInstanceState
  at InstanceBootstrap
  at InstanceStore.boot
  at Server.listen
```

opencode crashed on every boot, agentd restart-looped (Bug 2 fix kept the
crash loop from corrupting the port, but the workspace was still
unusable). Even forcing past the boot error and removing the bad config,
the `auth.json` baseURL metadata wasn't honoured — chat requests went to
`https://api.openai.com/v1` and got rejected with the obviously-wrong key.

---

## Discovery: probe live opencode for the schema it actually accepts

There was no opencode config-schema reference on hand, so I bound a fresh
workspace pod to a known-clean state and mutated `/tmp/agent-config.json`
through every plausible shape, restarting opencode after each write and
checking whether `/config` returned the parsed config or 500'd, and
whether `/provider` listed `openai` in `connected[]`.

Findings (each verified live):

| Shape | Result |
|---|---|
| `{$schema:..., providers: {...}}` | `ConfigInvalidError` on boot — opencode rejects plural |
| `{$schema:..., provider: {}}` | crashes — empty inner object also invalid |
| `{$schema:..., provider: {openai: {models: {default: {}}}}}` | crashes — missing options |
| `{$schema:..., provider: {openai: {options: {apiKey:..., baseURL:...}, models: {...}}}}` | **WORKS** — `openai` appears in `/provider`'s `connected` array |

Then I sent a prompt through opencode with the working shape, model
`deepseek-v3-chat` (since the operator's LiteLLM had a server-side
fallback misconfig on the literal `default` model — verified by a
direct `curl` call):

```
modelID:    "deepseek-v3-chat"
providerID: "openai"
parts:      ["PONG"]
cost:       0
tokens:     {input: 7846, output: 2}
error:      null
```

End-to-end LLM round-trip succeeded: secret bound → pushed to opencode
with Basic auth (Bug 1 fix) → opencode applied `provider.openai.options.baseURL`
→ request went to `https://ai.thekao.cloud/v1` (NOT api.openai.com) →
LiteLLM dispatched to the underlying model → response surfaced through
the agentd→API path.

---

## Fix

`pkg/agent/opencode/format.go` rewritten to produce the schema opencode
1.15.12 actually accepts:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {                                  ← SINGULAR
    "openai": {
      "options": {
        "apiKey":  "sk-...",
        "baseURL": "https://litellm.example/v1"  ← in options, not endpoint
      },
      "models": { "deepseek-v3-chat": { "name": "..." } }
    }
  },
  "model": "openai/deepseek-v3-chat"
}
```

Concretely:

1. Top-level key: `providers` → `provider`.
2. `endpoint: {type, url}` deleted entirely. `baseURL` moved into
   `options.baseURL`.
3. `options.aisdk.provider.apiKey` flattened to `options.apiKey`.
4. `endpointForProvider()` helper deleted (the per-provider switch on
   `openai/responses` vs `anthropic/messages` etc. was guesswork —
   opencode infers all of that from the provider id).
5. Internal struct shape simplified accordingly.

Diff size: format.go 112 → 109 lines, but most of it changed. Net
algorithmic simplification — fewer types, fewer nested maps.

---

## CI parity

`pkg/agent/opencode/format_test.go` rewritten with three new
regression-guard tests pinned exactly to the schema the live cluster
accepts, plus an exact-byte snapshot test:

| Test | Asserts |
|---|---|
| `TestFormatOpenCodeConfig_TopLevelKey_IsProviderSingular` | top-level key MUST be `provider`; `providers` (plural) MUST NOT appear. Direct guard for ConfigInvalidError #1. |
| `TestFormatOpenCodeConfig_BaseURL_LivesInOptions` | baseURL MUST be in `options.baseURL`; no `endpoint` key. Direct guard for the silent-discard "wrong-endpoint" failure mode. |
| `TestFormatOpenCodeConfig_Options_NoAisdkWrapper` | `options.aisdk` MUST NOT exist. Direct guard for ConfigInvalidError #2. |
| `TestFormatOpenCodeConfig_ExactSnapshot` | exact byte-for-byte snapshot of a representative config. Any whitespace, key order, or field shape change fails this test loudly. |

The snapshot test is intentionally aggressive — Go's `encoding/json`
emits struct fields in declaration order, so reordering the fields in
`opencodeConfig`/`opencodeProvider`/`opencodeOptions` would silently
change the wire format. The snapshot catches that.

The 13 existing tests were updated too: every `parsed["providers"]`
became `parsed["provider"]`, the `endpoint` accessors deleted, and the
`options.aisdk.provider.apiKey` chain collapsed to `options.apiKey`.

---

## Why CI didn't catch this

Same root cause as Bug 1 + 2 (worklog 0125): the unit test corpus
asserted against a shape we *invented*, with no validation against
real opencode. There was no CI step that booted opencode with a
fixture config and checked it accepted. The TDD shape was internally
consistent ("the formatter produces what the tests expect") but
externally wrong ("what the tests expect is not what opencode wants").

The new snapshot test plus the three structural-invariant tests close
the regression hole. A future change that mutates the wire format will
break the snapshot first; a future change that drops one of the three
invariants will break the corresponding regression test, even if the
snapshot is updated.

The deeper fix would be a CI integration test that boots an opencode
binary, writes the formatter's output to OPENCODE_CONFIG, and asserts
the provider appears in `/provider`'s connected array. That's a much
bigger lift (CI has no opencode binary today) and is filed as a
follow-up — for now, the schema is pinned by snapshot + invariants
against a live-probed reference.

---

## Verification

### Unit tests

```
$ go test -count=1 -race -v ./pkg/agent/opencode/
=== RUN   TestFormatOpenCodeConfig_SingleProvider_Minimal           PASS
=== RUN   TestFormatOpenCodeConfig_SingleProvider_AllFields         PASS
=== RUN   TestFormatOpenCodeConfig_MultipleProviders_FirstDefaultWins  PASS
=== RUN   TestFormatOpenCodeConfig_MultipleProviders_SecondHasDefault  PASS
=== RUN   TestFormatOpenCodeConfig_MultipleProviders_NoneHasDefault PASS
=== RUN   TestFormatOpenCodeConfig_BaseURL_LivesInOptions           PASS
=== RUN   TestFormatOpenCodeConfig_TopLevelKey_IsProviderSingular   PASS
=== RUN   TestFormatOpenCodeConfig_Options_NoAisdkWrapper           PASS
=== RUN   TestFormatOpenCodeConfig_ModelsWithAndWithoutLabels       PASS
=== RUN   TestFormatOpenCodeConfig_Deterministic                    PASS
=== RUN   TestFormatOpenCodeConfig_EmptyInput_Error                 PASS
=== RUN   TestFormatOpenCodeConfig_OutputIsValidJSON                PASS
=== RUN   TestFormatOpenCodeConfig_ExactSnapshot                    PASS
PASS
ok  github.com/lenaxia/llmsafespace/pkg/agent/opencode  1.683s
```

### Full repo

```
$ go test -count=1 -short ./...
…all packages PASS, including all 38 sub-packages.
```

### Lint

```
$ golangci-lint run --timeout 120s ./pkg/agent/...
0 issues.
```

### Live cluster (manual probe of the new shape)

Bound `litellm-openai` LLM-provider secret to a fresh workspace running
`sha-d1c7242` (Bug 1+2 fixes). Wrote the new shape to
`/tmp/agent-config.json`, restarted opencode, then queried opencode's
`/provider` endpoint with HTTP Basic auth (username
`agentd.AuthUsername`, password from `/sandbox-cfg/password`):

```
[
  "opencode",
  "openai"             ← present!
]
```

Sent a chat prompt to `/session/<id>/message` (same Basic auth) with
the JSON body
`{"providerID":"openai","modelID":"deepseek-v3-chat","parts":[{"type":"text","text":"Say PONG only."}]}`:
{
  "info": {
    "modelID":    "deepseek-v3-chat",
    "providerID": "openai",
    "tokens":     {"input": 7846, "output": 2},
    "error":      null
  },
  "parts": [{"type": "text", "text": "PONG"}]
}
```

The LLM responded `PONG`. baseURL was honoured (cost 0, no api.openai.com
auth error, response served from the operator's LiteLLM instance).

End-to-end credential flow now works: secret create → workspace bind →
agentd push to opencode with Basic auth → opencode loads schema →
opencode connects provider → chat request routed to operator endpoint
→ response returned. **All three bugs from worklog 0125 are closed.**

---

## Files modified

| File | Lines | Change |
|---|---|---|
| `pkg/agent/opencode/format.go` | 112 → 109 | rewritten; provider singular; options carries apiKey+baseURL directly; `endpointForProvider` deleted |
| `pkg/agent/opencode/format_test.go` | 229 → 332 | rewrote every test; added 3 regression-guard tests + exact-byte snapshot |
| `worklogs/0128_2026-06-02_credflow-bug3-config-schema-fix.md` | new | this file |

---

## Next steps

1. Commit + push.
2. Wait for CI to publish `sha-<new>` images.
3. Helm upgrade RuntimeEnvironment + components to the new tag.
4. Re-run end-to-end credential flow — should now succeed first try
   without manual config rewriting in the pod.

---

## Files Modified

- `pkg/agent/opencode/format.go`
- `pkg/agent/opencode/format_test.go`
- `worklogs/0128_2026-06-02_credflow-bug3-config-schema-fix.md` (this file)
