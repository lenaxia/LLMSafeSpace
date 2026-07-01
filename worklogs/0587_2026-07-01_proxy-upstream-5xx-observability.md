# Worklog: proxy upstream-5xx observability (server-side)

**Date:** 2026-07-01
**Session:** #488 — the API proxy layer silently passed opencode 5xx responses to the client with no server-side log or metric. Diagnosing #486 (opencode ConfigInvalidError) required kubectl-exec + curl inside a workspace pod. This PR closes the observability gap so future upstream-5xx incidents are discoverable in Prometheus/logs in seconds.

**Status:** Complete (companion frontend UX in #490, separate PR #491)

---

## Objective

Turn the class of incident where "the API returns 500 wrapping an opencode 500" from a manual `kubectl exec + curl` dive into a Grafana panel + a grep of the API log. Zero user-facing behavior change; pure infrastructure signal.

---

## Work Completed

### Audit — no duplication

Inventoried every existing observability surface before proposing anything new:

- `services/metrics/Service.RecordError(errorType, endpoint, code)` — exists on the Service instance but `proxy.go` doesn't have a Service reference (uses only package-level `Record*` helpers). Reusing would require plumbing the Service through the ProxyHandler constructor; the package-level pattern is cleaner and follows the existing style of `RecordQuotaExceeded`, `RecordAuthLockout`, `RecordRequestBufferFull`, etc.
- `middleware/api_requests_total{status}` — records the API's *outbound* status, not the upstream status. They differ when the API wraps an upstream 500 as a 502/503. Not duplicative; complementary.
- `ProxyHandler.logger` — already wired at construction (used for the 401 branch). Reused directly.

Decision: new package-level counter + new package-level `RecordUpstream5xx` helper following the existing pattern in `metrics.go`. One new chokepoint helper in the handlers package. No new service-instance surfaces; no new dependencies.

### Validated assumptions

1. **Both proxy paths need instrumentation.** Validated by grep — `doProxy` handles streaming (POST prompts, /session, etc.) and `doHistoryRequest` handles the non-streaming history GET. Only 401 has an existing log at `proxy.go:472`; every other status is a silent pass-through in both paths.
2. **Body preview is safe on the history path (already buffered), unsafe on the streaming path.** Validated by reading `doProxy` — the body is streamed chunk-by-chunk downstream and cannot be peeked without consuming the stream. On the history path the body is already buffered for pagination. This means the log's `bodyPreview` field is empty on the streaming path — documented in the code + PR body.
3. **`workspaceID` is not in scope of `doHistoryRequest`'s signature.** Verified by reading the caller at `proxy_handlers.go:264` — it has `workspaceID` (from `c.Param("id")`) but doesn't pass it down. Extended the signature; updated both call sites (initial + stale-IP retry).
4. **`santhosh-tekuri/jsonschema/v6` promotion in #487 does not need a re-promotion** — different concern. This PR promotes nothing new; the metric counter is a plain Prometheus dependency already present.
5. **Path-label cardinality is a real production concern.** Session IDs are per-user, per-conversation — raw path labels would spawn one time series per session and exhaust Prometheus memory in weeks. Sanitizer collapses `ses_*` segments to `:id`. Validated by the counter's `[]string{"workspace_id", "path", "upstream_status"}` labels — path is dimensioned, must be bounded.

### Root fix

- `api/internal/services/metrics/metrics.go`: new CounterVec `upstream5xxTotal` (Prom name `api_upstream_5xx_total`), `RecordUpstream5xx` helper, `Upstream5xxCounter` accessor for tests.
- `api/internal/handlers/proxy_upstream_observability.go`: new file. `recordUpstream5xx` chokepoint (log + metric), `sanitizePathForMetric` cardinality bounder, `upstream5xxBodyPreviewCap = 512`.
- `api/internal/handlers/proxy.go`: instrumentation in `doProxy` immediately after `httpClient.Do(req)`.
- `api/internal/handlers/proxy_handlers.go`: instrumentation in `doHistoryRequest` after body is buffered, plus signature extension for `workspaceID`.

### Tests (TDD'd + adversarially validated)

Integration:
- `TestGetHistory_Upstream5xx_LogsWarnAndRecordsMetric` — mock upstream 500 through the full history handler → asserts Warn log + counter fire with correct labeled values.
- `TestGetHistory_Upstream2xx_DoesNotLogWarnOrRecordMetric` — symmetric negative case (2xx must not touch observability surface).
- `TestDoProxy_Upstream5xx_LogsWarnAndRecordsMetric` — same shape for the streaming proxy path (POST /sessions returning 502).

Unit (added in review-feedback round to address test-completeness gap):
- `TestSanitizePathForMetric_TableDriven` — 8-case table covering the history endpoint, session detail, prompt_async, unchanged non-session paths, nested session IDs, empty path, and the `ses_`-prefix false-positive edge case (documented).
- `TestRecordUpstream5xx_BodyPreviewCap` — exercises the 600-byte body → 512+3-byte truncated-preview branch that integration tests never hit (real error envelopes are <200 bytes).
- `TestRecordUpstream5xx_BodyPreviewUncappedWhenSmall` — negative case for the cap.
- `TestRecordUpstream5xx_NilBodyProducesEmptyPreview` — pins the streaming-path behavior.
- `TestRecordUpstream5xx_NilLoggerDoesNotPanic` — defensive nil-check.

Adversarial validation: neutered `recordUpstream5xx` to a no-op — all 3 integration tests fail with `got warns=[]`. Restored — green.

### Review-feedback iteration

First review returned CHANGES_REQUESTED with 4 non-blocking items:
1. Doc error in `metrics.go:599` referenced non-existent `proxy_path_sanitize.go`. Fixed — now points at the actual file `proxy_upstream_observability.go`.
2. Body-preview truncation branch untested. Fixed — added `TestRecordUpstream5xx_BodyPreviewCap` + 3 sibling unit tests.
3. `sanitizePathForMetric` lacked table-driven unit tests. Fixed — added `TestSanitizePathForMetric_TableDriven` covering 8 cases.
4. Test file header claimed 4xx coverage that didn't exist. Fixed — corrected the doc comment to state 4xx is intentionally out of scope (handled by middleware's existing `api_requests_total{status}`).
5. Missing worklog entry (this file).

---

## Key Decisions

- **Package-level counter + helper over service-instance method.** The proxy handlers already use package-level metrics helpers exclusively (`RecordRequestBufferFull`, `RecordQuotaExceeded`, etc.). Plumbing the Service instance through the ProxyHandler constructor for a single counter would be inconsistent with the file's established style. Package-level is idiomatic here.
- **Single chokepoint (`recordUpstream5xx`) over two duplicated log-and-record blocks.** Both proxy paths call the same function so future changes (log message wording, metric label additions) happen once. Path sanitization also lives in the chokepoint so both callers get it for free.
- **Body preview at 512 bytes + `...` suffix.** Opencode error envelopes are typically <200 bytes; 512 covers real errors verbatim without inflating log volume. The `...` suffix is a signal to operators that the log was cut.
- **Sanitize `ses_`-prefixed segments only.** Documented in the sanitizer's doc comment and pinned by the "any ses_ prefixed segment" table case. Opencode has no legitimate non-session path segment starting with `ses_`, so the false-positive risk is nil in the current wire protocol. If opencode adds one, the table case surfaces the regression at test time.
- **Do NOT sanitize the `path` in the log line.** Operators debugging need to grep for a specific session's failure history — collapsing to `:id` would defeat that. Log carries the raw path; metric label uses the sanitized path. Documented in the code + a test asserts both.
- **Skip 4xx observability entirely.** Middleware's `api_requests_total{status}` already tracks all statuses at the API's *outbound* layer. Adding a per-status upstream signal for 4xx would be duplication + cardinality inflation. The signal that matters for upstream 5xx is that opencode is failing catastrophically — a 4xx is a bug in the client, which the middleware already surfaces.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 60s -run "TestGetHistory_Upstream|TestDoProxy_Upstream" ./api/internal/handlers/` — initial RED (`expected a Warn log line ... got warns=[]`), GREEN post-fix.
- `go test -timeout 30s -run "TestSanitize|TestRecordUpstream5xx_" ./api/internal/handlers/` — 8 sanitizer cases + 4 recorder unit tests, all green.
- Adversarial: neutered `recordUpstream5xx` — all 3 integration tests fail. Restored — green.
- `go test -timeout 300s -short ./api/internal/handlers/ ./api/internal/services/metrics/` — full packages green.
- Pre-existing `TestGetHistory_UpstreamError_DoesNotMaskAsEmptyPage` still passes — client contract unchanged.

---

## Next Steps

- Deploy Workstream A merged (#487); this PR (#489) is Workstream B1, deploys separately.
- **Grafana dashboard update** (deferred, not in scope of this PR): add a panel for `sum(rate(api_upstream_5xx_total[5m])) by (workspace_id, upstream_status)`. Alert rule: `alert if > 0.1/s for 5m` — every workspace that starts 5xx'ing during a deploy would page immediately.
- **Frontend UX** (#491) — companion PR that makes the same error visible to end users via a diagnostic banner. Independent of this PR; the two together give operators a complete visibility path from user report → Prometheus → API log grep → opencode stack trace.

---

## Files Modified

- `api/internal/handlers/proxy.go` — instrumentation call in `doProxy`.
- `api/internal/handlers/proxy_handlers.go` — instrumentation call in `doHistoryRequest`; signature extension for `workspaceID`; both call sites updated.
- `api/internal/handlers/proxy_upstream_observability.go` — new file. Chokepoint helper + path sanitizer + preview cap constant.
- `api/internal/handlers/proxy_upstream_5xx_observability_test.go` — new file. 3 integration tests + capturing logger + counter helper.
- `api/internal/handlers/proxy_upstream_observability_test.go` — new file (added in review round). Unit tests for sanitizer + preview cap + defensive nil checks.
- `api/internal/services/metrics/metrics.go` — new CounterVec + `RecordUpstream5xx` + `Upstream5xxCounter` accessor. Fixed a doc comment referencing a non-existent filename.
- `worklogs/0587_2026-07-01_proxy-upstream-5xx-observability.md` (this file).
