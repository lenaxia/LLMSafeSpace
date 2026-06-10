# Worklog: Credential Flow — Bug 1 + Bug 2 Fixes + CI Parity

**Date:** 2026-06-02
**Session:** Implement fixes for the two bugs surfaced in worklog 0125, including CI test fixtures that match production behaviour
**Status:** Complete — all unit/integration tests green with `-race`; live cluster validation pending CI image build

---

## Objective

Fix the two production bugs found by the live credential-flow exercise
in worklog 0125, and update the CI test fixtures to faithfully model
the production behaviour that hid both bugs through merge.

| Bug | Symptom | Root cause |
|---|---|---|
| 1 | `PUT /auth/openai returned 401` on credential push | New `pkg/agent/opencode/client.go` did not set HTTP Basic auth |
| 2 | opencode crash loop after Bug 1's fallback `proc.restart()` | `restart()` and the supervisor goroutine both called `cmd.Wait()`; the kernel had not reaped the old PID before the new opencode tried to bind port 4096 |

---

## Bug 1 — Basic auth on opencode credential push

### Fix

`pkg/agent/opencode/client.go`:

- Added `password string` field to `Client`.
- `NewClient(baseURL, password string)` — the password is now a
  required constructor parameter. Empty string is allowed (so unit
  tests that don't gate on auth still work) but produces 401 against
  real opencode.
- Both `setAuth` (PUT /auth/:providerID) and `DisposeInstance`
  (POST /instance/dispose) now call
  `req.SetBasicAuth(agentd.AuthUsername, c.password)` before sending.

`cmd/workspace-agentd/secrets.go`:

- `reloadSecretsHandler` gained a third parameter, `opencodePassword string`.
- Passed through to the new `opencode.NewClient(url, opencodePassword)` call site.

`cmd/workspace-agentd/main.go`:

- The package-local `password` variable (read from
  `/sandbox-cfg/password` at startup) is now passed to
  `reloadSecretsHandler`. No new file I/O; the existing read at
  `main.go:402-406` already had the value.

`cmd/workspace-agentd/secrets_test.go`:

- All 7 `reloadSecretsHandler(cfg, nil)` test invocations updated to
  `reloadSecretsHandler(cfg, nil, "")` for the new signature. `""` is
  fine for these tests because they don't push to opencode (no
  llm-provider in the batch) or use a stub URL that doesn't enforce auth.

### CI parity

The original `pkg/agent/opencode/client_test.go` (worklog 0121) used
`httptest.NewServer` with no auth gate. That's why the missing
`SetBasicAuth` call passed CI — every mock server returned 200
regardless of `Authorization` header. Real opencode returns 401.

Rewrote the test file to add a `requireAuth(t, h)` helper that wraps
every test handler and rejects requests lacking
`Basic <agentd.AuthUsername>:<testPassword>` with the exact response
opencode produces:

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Basic realm="Secure Area"
```

Every test now uses `newClientForTest(srv.URL)` which constructs
`NewClient(srv.URL, testPassword)`. Two new regression-guard tests
explicitly assert that an unauthenticated client surfaces 401:

- `TestPushCredentials_UnauthenticatedClient_Returns401`
- `TestDisposeInstance_UnauthenticatedClient_Returns401`

`pkg/agent/opencode/client_integration_test.go` got the same treatment:
all 16 mock servers now wrapped in `requireAuth`, all 17 `NewClient`
call sites updated to pass `testPassword`.

If a future change drops `SetBasicAuth` from any code path, both test
files now fail loudly. Worklog 0125's "test fixture is unfaithful to
the real server" gap is closed.

### Verification

```
$ go test -timeout 60s -count=1 -race ./pkg/agent/opencode/
ok  github.com/lenaxia/llmsafespace/pkg/agent/opencode  1.694s
```

All 28 tests pass with `-race`.

---

## Bug 2 — `managedProcess.restart()` double-`Wait()` race

### Root cause recap

Pre-fix `managedProcess` had two goroutines that called
`cmd.Wait()` on the same `*exec.Cmd`:

1. The supervisor spawned by `start()` (`main.go:581-604`) — its loop
   waited on the child and triggered auto-restart on crash.
2. `restart()` itself (`main.go:617`) — explicitly waited after
   sending SIGTERM, falling back to SIGKILL after 5s.

Concurrent `Wait()` on the same Cmd is undefined. In practice the
first one to return won the kernel reap, the second got an error
immediately, and `restart()` then called `start()` to spawn the next
opencode — but the kernel had not yet released port 4096 because
SIGTERM is asynchronous and the actual exit raced with the new
`Start()`. New opencode crashed with `Failed to start server. Is port
4096 in use?`. The supervisor's auto-restart loop kept retrying,
crashing each time. PID 82 (the original opencode) was still running
the entire time.

### Fix

Refactored `managedProcess` to a **single supervisor goroutine** that
is the SOLE caller of `cmd.Wait()`. New design (`main.go:560-770` in
the new layout):

```go
type managedProcess struct {
    mu sync.Mutex
    cmd *exec.Cmd
    restartCount int
    lastRestartAt time.Time

    cmdFactory func() *exec.Cmd      // injectable for tests
    healthCheckURL string            // injectable for tests

    upCh chan struct{}               // closed on every successful (re)start
    doneCh chan struct{}             // closed when supervisor exits
    stopRequested bool
    restartRequested bool

    probeWg sync.WaitGroup           // tracks healthProbeAfterRestart goroutines
}
```

`supervise()` is the loop:

1. Build cmd via `p.cmdFactory()`.
2. `cmd.Start()`. On failure, treat as crash (backoff + retry) unless
   `stopRequested`.
3. Replace `p.upCh` with a fresh channel; close the captured one
   (announces "child is up").
4. `cmd.Wait()` — exactly once per child, in this goroutine, period.
5. Inspect intent flags:
   - `stopRequested` → close `doneCh`, return.
   - `restartRequested` → reset counters, loop.
   - else (crash) → backoff, loop.

`restart()` becomes:

```go
p.restartRequested = true
signal SIGTERM (5s SIGKILL fallback via time.AfterFunc)
<-upCh    // blocks until supervisor announces the NEW child is up
go healthProbeAfterRestart()
```

Critically, `restart()` does NOT call `Wait()`. The supervisor's loop
iteration calls `Wait()`, which reaps the kernel side, and only THEN
proceeds to `Start()` for the new child. By the time the supervisor
closes `upCh`, the old PID is reaped AND the new one is up. No port
race possible.

`stop()` is added (was implicit in pre-fix code via the crash path):

```go
p.stopRequested = true
signal SIGTERM (5s SIGKILL fallback)
<-doneCh
p.probeWg.Wait()    // drain any in-flight health probes
```

The `probeWg.Wait()` was added when the test suite caught a data race
under `-race`: a leaked `healthProbeAfterRestart` goroutine outlived
the test's `t.Cleanup` that restored the package-global `log`,
producing a write/read race on the logger pointer. `probeWg` ties
the probe goroutine's lifetime to `stop()` so test teardown is
deterministic.

### Production wiring change

`main.go:401-412`:

- `proc = &managedProcess{}` then `proc.start()` — unchanged signature,
  but now spawns the new supervisor goroutine (not the old start+monitor
  pattern).
- `defaultOpencodeCmdFactory()` is the production cmdFactory — same
  argv as before (`opencode serve --hostname 0.0.0.0 --port AGENT_PORT`),
  same env (`buildEnvFrom(SecretsEnvPath)`), pulled out of `start()`
  into a free function so it has a stable name for test diffing.

### CI parity

Pre-fix the test suite had **zero tests for `managedProcess`** — this
is why the bug shipped. Real subprocess timing was never exercised.

Added `cmd/workspace-agentd/managed_process_test.go` (290 lines, 6 tests):

| Test | Asserts |
|---|---|
| `TestManagedProcess_StartLaunchesSubprocess` | basic happy path: subprocess runs and accepts connections |
| `TestManagedProcess_Restart_FreesPortBeforeNewBind` | **Bug 2 regression guard.** Fake holds port 300ms past SIGTERM; after `restart()` returns, the new process must already own the port. Pre-fix this would flap with "address already in use." |
| `TestManagedProcess_Restart_OldProcessIsReaped` | the original `cmd.ProcessState` is non-nil after `restart()` returns — proves Wait() completed |
| `TestManagedProcess_AutoRestartOnCrash` | killing the child triggers supervisor auto-restart; new PID differs |
| `TestManagedProcess_StopPreventsAutoRestart` | after `stop()`, no auto-restart fires; port stays free |
| `TestManagedProcess_RapidRestarts` | 5 back-to-back `restart()` calls don't leak port holders |

Test infrastructure uses the standard `TestHelperProcess` re-exec
pattern (cf. `os/exec/exec_test.go`):

- A test's `cmdFactory` re-execs the test binary with
  `GO_TEST_FAKE_OPENCODE=1`, plus per-test env vars (`FAKE_PORT`,
  `SIGTERM_DELAY_MS`, `FAKE_EXIT`).
- `TestHelperProcess` is a real `Test*` function but short-circuits
  to `runFakeOpencode()` when the marker env is set.
- The fake binds `127.0.0.1:FAKE_PORT`, serves a trivial `/v1/readyz`
  (so the post-restart health probe can succeed), and on
  `SIGTERM_DELAY_MS > 0` catches SIGTERM and sleeps for the
  configured duration before exiting — faithfully reproducing
  opencode's slow shutdown.
- `freeTCPPort(t)` allocates a unique 127.0.0.1:0 port per test so
  the suite is parallel-safe.

The 300ms SIGTERM delay in `TestManagedProcess_Restart_FreesPortBeforeNewBind`
is the exact regression case: pre-fix `restart()` returned in <50ms
(its own `Wait()` errored out immediately on the contended Cmd) and
the new opencode tried to bind a port still held by the original.

### Verification

```
$ go test -timeout 120s -count=1 -race -run TestManagedProcess -v ./cmd/workspace-agentd/
=== RUN   TestManagedProcess_StartLaunchesSubprocess          0.11s PASS
=== RUN   TestManagedProcess_Restart_FreesPortBeforeNewBind   2.82s PASS
=== RUN   TestManagedProcess_Restart_OldProcessIsReaped       1.21s PASS
=== RUN   TestManagedProcess_AutoRestartOnCrash               2.23s PASS
=== RUN   TestManagedProcess_StopPreventsAutoRestart          2.11s PASS
=== RUN   TestManagedProcess_RapidRestarts                    6.97s PASS
PASS
```

All 6 new tests pass with `-race`.

---

## Full suite verification

```
$ go test -timeout 300s -count=1 -short -race \
    ./cmd/workspace-agentd/... ./pkg/agent/... ./pkg/agentd/...
ok  github.com/lenaxia/llmsafespace/cmd/workspace-agentd  49.510s
ok  github.com/lenaxia/llmsafespace/pkg/agent              1.020s
ok  github.com/lenaxia/llmsafespace/pkg/agent/opencode     1.674s
ok  github.com/lenaxia/llmsafespace/pkg/agentd             1.017s
ok  github.com/lenaxia/llmsafespace/pkg/agentd/secrets     1.129s
```

```
$ golangci-lint run --timeout 120s ./cmd/workspace-agentd/... ./pkg/agent/...
0 issues.
```

```
$ go test -timeout 300s -count=1 -short ./...   # full repo, no race
…all packages PASS, including the 38 sub-packages of api/, controller/,
pkg/, runtimes/, charts/.
```

---

## Files modified

| File | Change | Lines |
|---|---|---|
| `pkg/agent/opencode/client.go` | password field + SetBasicAuth in two paths | +18 / -3 |
| `pkg/agent/opencode/client_test.go` | requireAuth helper, all servers wrapped, two 401 regression tests | rewritten, +60 net |
| `pkg/agent/opencode/client_integration_test.go` | wrap all servers, update all NewClient call sites | +17 / -17 |
| `cmd/workspace-agentd/main.go` | managedProcess refactor + cmdFactory + healthCheckURL injection + probeWg + defaultOpencodeCmdFactory | +175 / -90 |
| `cmd/workspace-agentd/secrets.go` | reloadSecretsHandler accepts opencodePassword | +12 / -2 |
| `cmd/workspace-agentd/secrets_test.go` | update 7 call sites | +7 / -7 |
| `cmd/workspace-agentd/managed_process_test.go` | NEW: 6 tests + TestHelperProcess infra | +313 |

Total: ~620 lines changed, ~360 are tests.

---

## Why these fixes don't regress anything

- `NewClient` signature change is internal: only callers are
  `cmd/workspace-agentd/secrets.go` and the test files in the same
  package. Updated atomically.
- `reloadSecretsHandler` signature change is internal to
  `cmd/workspace-agentd`. All 7 test invocations updated.
- `managedProcess` external API surface unchanged: `start()`,
  `restart()`, `stop()` (new but optional). Production caller in
  `main.go:411` (`proc.start()`) and `secrets.go:289`
  (`proc.restart()`) work identically. The `stop()` method is
  new and only used by tests; production exits via the OS, not via
  managedProcess.stop().
- `defaultOpencodeCmdFactory` builds the same argv + env as the
  pre-fix `start()` did inline. Behaviorally identical for the
  production path.
- Health-probe semantics preserved: still 10 polls × 1s, still uses a
  fresh context. Now also early-aborts on `doneCh` close, which only
  fires when `stop()` is called — production never calls `stop()`, so
  the probe behavior is unchanged in prod.

---

## Tests run

| Command | Result |
|---|---|
| `go test -timeout 60s ./pkg/agent/opencode/` | PASS (28 tests) |
| `go test -timeout 60s -race ./pkg/agent/opencode/` | PASS |
| `go test -timeout 120s -run TestManagedProcess ./cmd/workspace-agentd/` | PASS (6 tests) |
| `go test -timeout 120s -race -run TestManagedProcess ./cmd/workspace-agentd/` | PASS |
| `go test -timeout 180s -short ./cmd/workspace-agentd/...` | PASS |
| `go test -timeout 180s -short -race ./cmd/workspace-agentd/...` | PASS |
| `go test -timeout 300s -short ./...` | PASS |
| `golangci-lint run` (changed packages) | 0 issues |

---

## Next steps

1. Commit + push to main (per operator).
2. Monitor CI workflow build.
3. Once CI publishes new images, deploy to cluster.
4. Re-run the credflow exercise from worklog 0125 — should now
   succeed end-to-end (secret bind → push to opencode with Basic auth →
   credentials live in opencode without restart-loop).
5. Triage the **tertiary observation** from worklog 0125:
   `workspace-secrets-<id>` was not created on the initial bind path.
   That's a separate bug from these two and is not addressed here.

---

## Files Modified

- `pkg/agent/opencode/client.go`
- `pkg/agent/opencode/client_test.go`
- `pkg/agent/opencode/client_integration_test.go`
- `cmd/workspace-agentd/main.go`
- `cmd/workspace-agentd/secrets.go`
- `cmd/workspace-agentd/secrets_test.go`
- `cmd/workspace-agentd/managed_process_test.go` (new)
- `worklogs/0127_2026-06-02_credflow-bug1-bug2-fixes.md` (this file)
