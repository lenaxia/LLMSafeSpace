# Worklog: US-38.10: Add PushCredentials Retry with Exponential Backoff

**Date:** 2026-06-13
**Session:** Implement bounded retry with exponential backoff for opencode credential injection (PUT /auth/:providerID) so transient failures self-heal instead of aborting the credential-push loop.
**Status:** Complete — PR #143

---

## Objective

Credential injection via `PUT /auth/:providerID` (opencode Control API) failed
transiently whenever the sandbox pod restarted, the opencode process was briefly
unavailable, or an upstream returned a flaky 5xx. A single failure aborted the
entire credential-push loop, leaving the workspace with missing providers until
the next reload. Add a bounded retry with exponential backoff so transient errors
heal themselves instead of surfacing to the user.

---

## Work Completed

### Design

* `Client` gains a `*zap.Logger` field; `NewClient` accepts an optional logger
  (defaults to a no-op logger so existing unit tests keep working).
* New `nonRetryableError` sentinel type marks client errors (HTTP 4xx) that must
  NOT be retried — retrying a 401/403/404 would only waste time and mask a real
  config bug.
* New `retryWithBackoff` helper runs a closure up to `maxAttempts` times with
  exponential delay + jitter:
  `delay = initialDelay * 2^(attempt-1) + rand(0–500ms)`.
  It honours `ctx.Done()` between attempts so cancellation/timeout is prompt.
* `setAuth` is rewritten to:
  * wrap each request in a 5 s per-attempt timeout (so a hung connection does
    not block the whole budget);
  * return `nonRetryableError` for `4xx`, a plain `error` for `5xx`/network
    failures (retryable), and `nil` on success;
  * run through `retryWithBackoff(ctx, 3, 1s, …)`.
* 4xx → stop immediately; 5xx and network errors → retry; context cancel →
  return `ctx.Err()` promptly.

All six `NewClient` call sites updated to pass a logger where one is available.

### Review Remediation (round 1)

Reviewer feedback addressed in the review-fix commit:

1. **Missing worklog** — this file (0251).
2. **Missing copyright headers** — added to `client_test.go` (rewritten) and
   `helpers_test.go` (new).
3. **`nonRetryableError` fields unexported** — the type is unexported, so its
   fields (`provider`, `statusCode`, `attempt`) are now lowercase; `Error()` and
   the constructor updated accordingly.
4. **Unrelated `proxy.go` change** — the stray `workspaceID` field on
   `workspaceConfig` (and a stale worklog renumber) introduced in the feature
   commit were removed.

### Review Remediation (round 2)

1. **Missing doc comments** — added one-line doc comments to
   `retryWithBackoff` (describing its bounded-retry-with-backoff behaviour) and
   `ZapLogger()` (describing its return value). The misplaced `setAuth` doc
   comment that previously sat above `retryWithBackoff` was moved to the
   `setAuth` function where it belongs.
2. **Worklog completed to template** — this file now follows the README-LLM.md
   worklog template (Key Decisions, Blockers, Tests Run, Next Steps, Files
   Modified).

---

## Key Decisions

* **Retry only 5xx/network errors, never 4xx.** A 4xx (e.g. 401/403/404)
  indicates a configuration or auth problem that retrying will not fix;
  retrying would mask the root cause and waste up to ~7s of backoff. The
  `nonRetryableError` sentinel type short-circuits the loop. Rationale: surface
  config bugs immediately, heal transient infra noise silently.
* **Three attempts max, 1s initial delay.** Bounds total worst-case latency to
  ~7s (1s + 2s + jitter), which is well inside the controller's pod-ready
  timeout. More attempts would hold the credential-push loop open too long.
* **5s per-attempt timeout inside the closure.** A hung TCP connection must not
  consume the entire retry budget. The timeout is applied inside `setAuth`
  rather than in `retryWithBackoff` so the budget covers the backoff sleep too.
* **Logger is optional (defaults to no-op).** Keeps existing unit tests (which
  construct a `Client` directly) compiling without forcing every test to pass a
  logger.
* **Decision made with sufficient information** — no follow-up needed.

---

## Blockers

None.

---

## Tests Run

Rewrote `client_test.go` around a shared `requireAuth` test helper
(`helpers_test.go`) and added focused unit tests:

1. `success` — single 200 response, no retry.
2. `retryThenSuccess` — first attempt 503, second 200; asserts exactly two calls.
3. `noRetryOn4xx` — 404 returns immediately with a single attempt.
4. `contextCancellation` — cancelled context surfaces `ctx.Err()`.
5. `maxRetries` — persistent 500 exhausts all three attempts.
6. `partialFailure` — first provider fails (non-retryable) so second provider is
   never attempted.

Integration tests in `client_integration_test.go` updated for the new
`NewClient` signature.

Commands run and outcomes:

```
$ go build ./...
  → PASS (exit 0)

$ go test ./pkg/agent/opencode/... -timeout 60s
  → PASS — all unit tests green
```

---

## Next Steps

* Monitor production for the ratio of retryable vs. non-retryable auth errors
  after deploy; if non-retryable (4xx) dominates, investigate the credential
  materialization path rather than the retry policy.
* Consider exporting a small structured log field (`attempt=N`) from
  `retryWithBackoff` if ops needs per-attempt visibility (currently logs only on
  final failure).
* No code action required for merge — PR #143 is ready.

---

## Files Modified

* `pkg/agent/opencode/client.go` — added `*zap.Logger` field to `Client`,
  updated `NewClient` signature, added `retryWithBackoff` + `nonRetryableError`,
  rewrote `setAuth` to use retry; added doc comments to `retryWithBackoff` and
  moved the `setAuth` doc comment to its function.
* `api/internal/logger/logger.go` — added `ZapLogger()` accessor with doc
  comment.
* `api/internal/handlers/agent_reload.go` — pass logger to opencode client.
* `cmd/workspace-agentd/agent_reload.go` — pass logger to opencode client.
* `cmd/workspace-agentd/secrets.go` — pass logger to opencode client.
* `api/internal/handlers/agent_drain_test.go` — update for new `NewClient`.
* `api/internal/handlers/agent_reload_e2e_test.go` — update for new `NewClient`.
* `pkg/agent/opencode/client_test.go` — rewritten retry unit tests (with
  copyright header).
* `pkg/agent/opencode/helpers_test.go` — new shared `requireAuth` helper (with
  copyright header).
* `pkg/agent/opencode/client_integration_test.go` — updated for new `NewClient`.
* `worklogs/0251_2026-06-13_epic38-pushcredentials-retry.md` — this worklog.
