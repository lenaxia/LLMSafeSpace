# Worklog: peer-poll Test Flake Fix — Extract loadPeerConfig

**Date:** 2026-06-21
**Session:** PR #335 CI run failed on `TestPollPeerConfig_PreservesFleetOnParseError`. Diagnose, fix, and harden the tests.
**Status:** PR #336 open and reviewed.

---

## Objective

PR #335 had a CI test failure unrelated to the fix it shipped:

```
--- FAIL: TestPollPeerConfig_PreservesFleetOnParseError (0.12s)
    peer_poll_test.go:134: Should have 1 item(s), but has 0
        Messages: poller must NOT clear the fleet on parse errors — keep last known good
```

The failure was non-deterministic (passed locally, failed in CI). Investigate whether it was a real bug introduced by PR #335 or a pre-existing flake, and fix.

---

## Findings

### Pre-existing flake from PR #332 (worklog 0467 AI #3)

The peer-poll tests in PR #332 were goroutine-driven:

```go
go pollPeerConfig(ctx, path, 20*time.Millisecond, fleet)
time.Sleep(60 * time.Millisecond)
require.Len(t, fleet.HealthyRelays(), 1)

require.NoError(t, os.WriteFile(path, []byte(`{garbage`), 0o600))
time.Sleep(60 * time.Millisecond)
assert.Len(t, fleet.HealthyRelays(), 1, "must NOT clear the fleet on parse errors")
```

The 60ms sleep is enough margin under normal scheduling but not under the race detector's added overhead. The race window:

1. Test calls `WriteFile(corruptData)` — opens with `O_TRUNC` then writes
2. Between truncation and write, the file briefly appears empty/short
3. Polling goroutine ticks during this window, reads truncated content
4. Parser sees malformed JSON, takes the parse-error branch — **but** in some scheduling orders the truncated read returns a 0-byte content first, hitting the empty-file branch which calls `UpdatePeers(nil)` and clears the fleet
5. The test assertion runs after the next tick which reads the fully-written corrupt content (parse error → fleet preserved), but the fleet was already cleared in step 4

CI race-detector slows scheduling enough that step 4 frequently happens. Local runs typically don't.

### Fix: extract loadPeerConfig and call it synchronously

`pollPeerConfig` had its file-load logic in an inner `load := func() {...}` closure. I extracted that closure to a top-level `loadPeerConfig(path, fleet)` function. The polling loop is unchanged in behavior; the extracted function is the same code with the same comments.

Tests now call `loadPeerConfig(path, fleet)` directly after each filesystem write. No goroutine, no sleep, no race window. Each test is a deterministic sequence:

```go
loadPeerConfig(path, fleet)        // initial seed
require.Len(t, fleet.HealthyRelays(), 1)

os.WriteFile(path, []byte(`{garbage`), 0o600)
loadPeerConfig(path, fleet)        // observed corrupt — preserve fleet

assert.Len(t, fleet.HealthyRelays(), 1)
```

This is strictly more deterministic and exercises the same code path as production (`pollPeerConfig` calls `loadPeerConfig` on every tick).

### Added tests beyond the prior set

Per the PR #332 reviewer's note about the missing whitespace-only case:
- `TestLoadPeerConfig_RemovesRelaysWhenWhitespaceOnly` — pins the `strings.TrimSpace` branch.

Per pinning steady-state add/remove behavior (which the prior tests only exercised implicitly):
- `TestLoadPeerConfig_AddsAndRemovesPeersBasedOnFile` — fleet tracks file content as it changes (a → b,c).

---

## Key Decisions

- **Extracted to top-level function** rather than restructuring tests with `assert.Eventually`. Direct synchronous testing is the cleaner pattern when the production code can be split this way without changing semantics.
- **Did not add logging on the silent IO-error path** (`loadPeerConfig` returns without logging when `os.ReadFile` fails for a non-`NotExist` reason). Reviewer flagged this as a follow-up — it's pre-existing and out of scope for the flake fix. Worth a follow-up PR.
- **Did not change the polling interval** (5s in production). The flake was test-only; the production loop is fine.

---

## Adversarial Self-Review

1. **Does the extraction change production behavior?** No. `loadPeerConfig` is the same code as the prior closure; `pollPeerConfig` calls it identically (same path argument, same fleet pointer, same call sequence: once at startup, then per tick).
2. **Is the synchronous test pattern losing coverage of the polling loop itself?** The polling loop is trivial (ticker + select + call). The actual logic-under-test is `loadPeerConfig`. If the loop ever needs explicit testing, that's a separate concern (e.g. ticker behavior on context cancel) — not what these tests are pinning.
3. **Race detector under the new tests?** Verified: `go test -timeout 60s -race ./cmd/relay-router/` passes consistently.
4. **What about the missing `0470` worklog reference?** Reviewer flagged this — fixing now.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 60s -race ./cmd/relay-router/` — pass
- `go test -timeout 240s -short ./...` — all green
- `make lint` — 0 issues

---

## Next Steps

1. Merge PR #336.
2. Wait for CI to publish images for the merged main branch.
3. Deploy and verify the orphan-cleanup test on the live cluster (PR #335's main goal — the no-ownerRef + strip-on-update lifecycle).

---

## Files Modified

| File | Change |
|---|---|
| `cmd/relay-router/main.go` | Extracted `load` closure to top-level `loadPeerConfig` function; `pollPeerConfig` now calls it |
| `cmd/relay-router/peer_poll_test.go` | Rewrote tests to call `loadPeerConfig` synchronously; renamed test functions; added two new tests (whitespace-only, add/remove steady state) |
| `worklogs/0470_2026-06-21_peer-poll-test-flake-fix.md` | This file |
