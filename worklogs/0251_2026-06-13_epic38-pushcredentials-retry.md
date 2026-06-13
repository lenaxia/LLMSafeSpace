# Worklog: US-38.10: Add PushCredentials Retry with Exponential Backoff

**Date:** 2026-06-13
**Epic:** 38 — Workspace session/activity integrity
**Story:** US-38.10
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

## Design

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

---

## Tests

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

---

## Review Remediation (this PR)

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
