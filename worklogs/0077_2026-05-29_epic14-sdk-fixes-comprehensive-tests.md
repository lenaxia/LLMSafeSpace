# Worklog: Epic 14 — SDK Bug Fixes + Comprehensive Live Tests + VS Code Extension Wiring

**Date:** 2026-05-29
**Session:** Fix all bugs from worklog 0075 live validation, rewrite live integration tests for full coverage, wire VS Code extension features
**Status:** Complete

---

## Objective

Address all bugs discovered during worklog 0075's live cluster validation: fix SDK sendMessage format, TS SDK 202 handling, wire VS Code extension terminal/chat features, and upgrade all 3 live integration tests to comprehensive coverage using actual SDK methods.

---

## Work Completed

### Bug Fixes (from worklog 0075)

| Bug ID | Fix | Verified |
|--------|-----|----------|
| SDK-1 | All 3 SDKs now send `{content, parts: [{type:"text", text:content}]}` in sendMessage | ✅ Unit tests pass |
| SDK-2 | TS SDK `request()` handles `res.status === 202` alongside 204 (no JSON parse on empty body) | ✅ Unit tests pass |
| EXT-1 | `registerTerminalCommand()` imported and called in `extension.ts` | ✅ Build passes |
| EXT-2 | `registerChatParticipant()` imported and called in `extension.ts` | ✅ Build passes |
| EXT-3 | `terminal-provider.ts` uses `import WebSocket from "ws"` with `.on()` event API | ✅ Build passes |
| EXT-4 | `resources/icon.svg` created (terminal icon) | ✅ File exists |

### Comprehensive Live Integration Tests

All 3 SDKs now have parity in live test coverage (~45 assertions each):

| Section | TypeScript | Python | Go |
|---------|-----------|--------|-----|
| Auth: me, create/delete API key | ✅ | ✅ | ✅ |
| Workspace: create, get, list, pagination, rename | ✅ | ✅ | ✅ |
| Workspace: getStatus, activeSessions | ✅ | ✅ | ✅ |
| Wait for agent healthy | ✅ | ✅ | ✅ |
| Sessions: ensure, getActive, rename | ✅ | ✅ | ✅ |
| Sessions: sendMessage via SDK (with parts fix) | ✅ | ✅ | ✅ |
| Sessions: getHistory, abort | ✅ | ✅ | ✅ |
| Terminal: getTicket, uniqueness | ✅ | ✅ | ✅ |
| Secrets: create, list, get, reveal, delete | ✅ | ✅ | ✅ |
| Suspend / Resume + session after resume | ✅ | ✅ | ✅ |
| Activate (suspend → activate → healthy) | ✅ | ✅ | ✅ |
| Error: NotFoundError on nonexistent workspace | ✅ | ✅ | ✅ |
| Error: AuthError on invalid API key | ✅ | ✅ | ✅ |
| Error: terminal ticket on nonexistent workspace | ✅ | ✅ | ✅ |
| Cleanup: delete + verify deletion | ✅ | ✅ | ✅ |

### Key Improvement: Go Live Test Uses Actual SDK

Previous Go live test used a raw inline HTTP client — it wasn't testing the SDK at all. Now imports `llm "github.com/lenaxia/llmsafespace/sdk/go"` and exercises `c.Workspaces.Create()`, `c.Sessions.SendMessage()`, `c.Terminal.GetTicket()`, etc.

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Send both `content` and `parts` in sendMessage | Backwards-compatible: old API ignores `parts`, new opencode requires it |
| Use `ws` npm package in VS Code extension | Node.js < 21 has no global `WebSocket`; VS Code extension host is Node |
| Live tests use try/catch for secrets | Secrets API may not be deployed in all environments |
| Go live test is `cmd/live-test/main.go` not `_test.go` | Avoids import cycle with the SDK package; runs as standalone binary |

---

## Blockers

- Git push requires GitHub credentials not available in this environment. Commit `c16fb6f` is local.

---

## Tests Run

```
Unit tests (all pass):
  TypeScript SDK:  12/12
  Python SDK:     10/10
  Go SDK:          7/7
  OpenAPI:         9/9
  Terminal router:  8/8 (integration)
  Terminal handler: 9/9

VS Code extension: builds successfully (145.7kb, 7ms)

Live tests (ready for cluster — not run in this session):
  TypeScript: ~45 assertions
  Python:     ~45 assertions
  Go:         ~40 assertions
```

---

## Next Steps

1. Push commit `c16fb6f` from a machine with GitHub access
2. Deploy updated images and run live tests: `API_URL=... API_KEY=... npx tsx tests/live-integration.test.ts`
3. Verify sendMessage works without raw HTTP workaround (SDK-1 fix)
4. Verify suspend/resume doesn't crash TS SDK (SDK-2 fix)
5. Test VS Code extension in actual VS Code (terminal + chat participant)

---

## Files Modified

```
.gitignore                                          (added sdks/go/live-test)
sdks/go/cmd/live-test/main.go                       (rewritten — uses SDK package)
sdks/go/live-test                                   (deleted — accidentally committed binary)
sdks/python/tests/test_live_integration.py          (rewritten — comprehensive)
sdks/typescript/tests/live-integration.test.ts      (rewritten — comprehensive)
```

Note: SDK source fixes (client.ts, client.py, services.go) and VS Code fixes (extension.ts, terminal-provider.ts, package.json, icon.svg) were already committed in `cdf2ddc` by a parallel agent session before this session's pull.
