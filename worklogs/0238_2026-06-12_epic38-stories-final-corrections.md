# Worklog: Epic 38 Stories — US-38.4 through US-38.12 Final Corrections

**Date:** 2026-06-12
**Session:** Applied all blocking and non-blocking corrections from the fourth-pass meta-critical review to the nine remaining story files
**Status:** Complete

---

## Objective

Update US-38.4 through US-38.12 to incorporate every finding from the fourth-pass review documented in worklog 0237. Both blocking (critical, would cause production regressions or build failures) and non-blocking (important quality improvements) corrections applied to each story.

---

## Changes by Story

### US-38.4 — Hash API Keys in Redis

**Blocking:**
- Corrected function name "validateAPIKeyCached" (does not exist) → `AuthenticateAPIKey` throughout all sections, headers, and test function names

**Non-blocking:**
- Test assertion count corrected to ~20 (12 in `auth_test.go` + 8 in `auth_apikey_dek_test.go`), not ~15
- Added Weaknesses section: (1) two code paths must be updated atomically; (2) DeleteAPIKey has no cache revocation — deleted keys remain valid 15 min (pre-existing)
- Added note that `auth_apikey_dek_e2e_test.go` already uses `pkgutil.HashString` and won't break
- Added Acceptance Criterion: `grep -n 'Sprintf("apikey:%s", apiKey' auth.go` must return zero results
- Added operational runbook: `echo -n "lsp_..." | sha256sum | awk '{print $1}'` for Redis key lookup

---

### US-38.5 — Fix nohtml Validator

**Blocking:**
- Added prominent "Test Package Constraint" section as primary constraint (not afterthought): `validateNoHTML` is unexported; tests must be in `package middleware` (internal), NOT `package tests` (external). The existing `api/internal/middleware/tests/` directory is the wrong location for these tests.

**Non-blocking:**
- Added Weaknesses section: (1) only checks literal ASCII `<`/`>` — entities, `javascript:` URLs, CSS expressions all pass; CSP is primary defense; (2) name "nohtml" overpromises — renaming requires updating all consumer struct tags
- Added Acceptance Criterion: `grep -rn 'nohtml' --include='*.go' .` must list all struct fields before story closes
- Clarified that production code uses the `init()`-registered global `validate` instance (validation.go:51-65)
- Added note recommending future `bluemonday` story for richer field sanitization

---

### US-38.6 — Fix Controller Gauge Drift

**Blocking:**
1. Replaced generic rollback pseudocode with exact patterns for every call site:
   - *Direct-update sites* (7 in phase_active.go): expand the one-liner `return ctrl.Result{}, r.Status().Update(...)` with explicit `if err` block and rollback `Inc()`
   - *enterRecovery sites*: capture return value, add rollback `Inc()` on error
2. Moved `health.go:118` and `health.go:145` from a separate afterthought section into the main implementation table — total is now explicitly 9 Dec() locations (7 in phase_active.go + 2 in health.go)
3. Removed the non-existent `previousState` field from the handleTerminating fix. Replacement approach: use `workspace.Status.PodIP != ""` as proxy for "was Active", with explicit warning that `PodName != ""` is wrong (phase_creating.go:101 sets PodName before Active is reached) and a Creating→Terminating false-decrement guard

**Non-blocking:**
4. `safeModeGauge` else-branch changed from `Set(0)` to `DeleteLabelValues(workspaceID)` — `Set(0)` creates spurious zero-valued label entries causing cardinality explosion
5. Added "Critical note on enterRecovery": all three Creating-phase call sites (phase_creating.go:90, 159, 171) cited; rule stated explicitly: never add Dec() inside enterRecovery
6. Added "Active→Suspending paths already handled" exclusion note with references to phase_active.go:119-122, 143-144 and phase_suspend.go:25
7. Referenced `SeedWorkspacesRunning` (metrics.go:166-168) as startup safety net
8. Added "Gauge Health Check" acceptance criterion: after 10 Creating→Active→Creating cycles, gauge equals 1 not 11
9. Added safeModeGauge cardinality failure mode to Failure Modes table

---

### US-38.7 — Remove Dead Code

**Blocking (CRITICAL):**
1. Replaced Item 4 entirely. The original claimed `utils.go:19-59` are dead condition helpers. They are NOT dead — `SetCondition`, `AddFinalizer`, `RemoveFinalizer`, `IsPodReady`, `GenerateRandomString` are all actively called by production code (`phase_pending.go:23`, `phase_terminating.go:60`, `secrets.go:78`). Item 4 is now "EVALUATED: NOT DEAD CODE — DO NOT REMOVE" with grep evidence and explanation that `health.go` has a private `setCondition` method that creates false name confusion.

2. Expanded Item 5 scope. Removing only `stripPatchParts` leaves the call at `proxy.go:671` — immediate compile error. Item 5 now lists all 6 required sub-tasks atomically: (a) remove helper functions; (b) remove shouldFilter block including call site at line 671; (c) remove `stripPatch bool` parameter from doProxy signature; (d) update both doProxy callers; (e) remove both G1 tests; (f) evaluate `maxNonStreamingResponseBytes` constant. Commit message guidance includes worklog 0070 gzip caveat.

**Non-blocking:**
3. Items 7 and 8: added notes that grep hits inside `request_id_test.go` and `cors_test.go` are the test files scheduled for concurrent deletion and do not block removal
4. After Item 8: added follow-up note documenting SecurityMiddleware OPTIONS preflight bug (security.go:114-117) as a required follow-up task
5. Item 9: confirmed "Remove ONLY lines 15-16, NOT line 14" guidance was already correct; preserved with 8+ external reference note for AgentTypeOpenCode
6. Added Failure Modes section: HIGH risk of executing old Item 4 description; HIGH risk of orphaned stripPatchParts call site

---

### US-38.8 — Consolidate Dual Patterns

**Blocking (CRITICAL — both are silent production regressions):**

1. Pattern 1 migration template fixed. The actual `extractAuth` at `secrets.go:855-867` does NOT write HTTP responses. The old template `if userID == "" { return }` would return HTTP 200 with empty body for unauthenticated requests. Added prominent WARNING block and correct two-line pattern that always includes `c.JSON(http.StatusUnauthorized, ...)` before `return`. Every migrated call site must preserve 401 behavior.

2. `ClassifyPostgresError` sample corrected. Project uses `pgx/v5` (`go.mod:15`). Old sample used `*pq.Error` from `github.com/lib/pq` — type assertion would silently fail, all duplicate-key 409s become 500s. Corrected to `*pgconn.PgError` from `github.com/jackc/pgconn` with correct SQLSTATE codes.

**Non-blocking:**
3. `terminal.go:139` added to Pattern 1 call site list — total is now 11, not 10
4. `isNotFound` function (same `strings.Contains` anti-pattern) added to Pattern 2 scope
5. All isDuplicateErr/isNotFound call sites enumerated: admin_provider_credentials.go:137, 305, 338 and user_provider_credentials.go:139
6. Pattern 4: added mandatory test-code audit step — grep for `NewSecretService` in test files and confirm `SetAdminKeyDeriver` wiring before removing legacy path
7. Pattern 3: added "Behavioral Changes" table calling out buffer size 16→128 and subscriber limit 0→20
8. Added note about `user_provider_credentials.go:75-76` — both `userID` and `sessionID` come from `extractAuth(c)` second return value

---

### US-38.9 — Move Services Out of Handlers

**Blocking (CRITICAL — guaranteed compile failure):**

1. `EventBroker` interface replaced entirely. Old interface had `Publish(eventType string, payload interface{})` which: (a) does not match what `WorkspaceWatcher` actually calls, and (b) does not exist on `UserEventBroker`. The correct interface is `WorkspaceOwnerTracker` with `RecordWorkspaceOwner(workspaceID, userID string)` and `CleanupWorkspace(workspaceID string)`. Every reference to EventBroker updated throughout.

2. `proxy.go` field names corrected: `workspaceWatcher` → `watcher` (proxy.go:81), `workspaceEventBroker` → `broker` (proxy.go:84)

**Non-blocking:**
3. Added pre-move step: move `WorkspaceSSEEvent` to `api/internal/types/sse_event.go` first to prevent hub dependency
4. Added callback/interface types note for SSETracker: `SessionIdleCallback`, `RawEventCallback`, `InferenceCallback`, `SessionMetricsRecorder` must travel with `session_tracker.go` to `services/sse/`; import cycle guard added
5. Added import cycle analysis: `crd_watcher.go:14` imports `services/metrics`; verify no reverse dependency before move
6. Added test file unexported-symbol note to general procedure
7. US-38.8 conditional made explicit: if WorkspaceEventBroker already removed, skip Items 3-4 entirely
8. Execution order corrected: event brokers (Items 3+4) explicitly precede WorkspaceWatcher (Item 6) with explanation of import cycle reason

---

### US-38.10 — Add PushCredentials Retry

**Blocking:**
- `log.Printf` replaced with zap throughout. `Client` gains `logger *zap.Logger` field; `NewClient` accepts `*zap.Logger` (nil → `zap.NewNop()`); retry helper uses `c.logger.Warn(...)` with structured fields; tests use `zaptest.NewLogger(t)`; `secrets.go:400` call updated to pass `log`

**Non-blocking:**
- Corrected reloadMu claim: `StageCredentials` is called at `secrets.go:401` which is AFTER `reloadMu.Unlock()` at line 385 — mutex is NOT held during retry
- Worst-case latency corrected: 10+1+10+2+10 = 33s per provider (not 3s); per-attempt context timeout of 5s reduces to ~18s
- Added jitter: `delay += time.Duration(rand.Intn(500)) * time.Millisecond`
- Added partial failure recovery documentation: PUT is idempotent; re-call `/v1/reload-secrets` to recover
- Added Prometheus counter recommendation: `agent_setauth_retry_total{provider, attempt}`

---

### US-38.11 — Fix Kubernetes Client

**Blocking (CRITICAL — guaranteed build break):**
- After Fix 3 adds `ctx` to List/Watch, `cache.ListWatch` closures in `informers.go` cannot accept context (API limitation). Fix: add `ctx context.Context` field to `InformerFactory`, initialized to `context.Background()` in constructor. Closures use `f.ctx`. Explicit note: this is the only acceptable remaining `context.Background()` use in `pkg/kubernetes/` after Fix 3.

**Non-blocking:**
- `namespace string` parameter removed from `RuntimeEnvironments()` after Fix 4 makes it meaningless; interface, implementation, informers.go, and mocks all updated; all 4 affected locations listed
- `StartInformers` nil-pointer risk documented and fixed: initialize informers inline within the mutex, or via a `newRuntimeEnvInformerLocked()` helper
- `sync.Once` failure caching behavior documented with explicit code comment
- CI acceptance check added: `grep -rn 'context.TODO()' pkg/kubernetes/` must return zero results after Fix 3 (informer closures use `f.ctx`)
- Blast radius for `RuntimeEnvironments()` change fully listed

---

### US-38.12 — Add Agentd Graceful Shutdown

**Blocking:**
- Server shutdowns changed from sequential to CONCURRENT using `sync.WaitGroup` — both drain within the same 25s budget instead of consuming it sequentially

**Non-blocking:**
- `terminationGracePeriodSeconds: 35` documented as required pod manifest change (25s shutdown + 5s SIGKILL buffer + margin); added as implementation step, acceptance criterion, and Files to Change entry
- Relay injector goroutine: `startRelayInjector` now receives `rootCtx`; health-wait loop checks `ctx.Done()`; `fetchFreeModels` threaded with context; file-write non-cancellable caveat documented; one-shot flag behavior noted
- `defer bgCancel()` removed from all code blocks — explicit `bgCancel()` in shutdown sequence is authoritative; having both is misleading
- `startRelayInjector` placement shown in restructured `main()` skeleton (between Phase 4 and Phase 5)
- Added `sync.WaitGroup` for three background goroutines (SSE tracker, fillGaps, refreshIsHealthyLoop) with bounded 5s wait after `bgCancel()`
- `secrets.go:163` and `main.go:1235` documented as intentionally out of scope (materialize subcommand and healthProbeAfterRestart respectively)
- Added `TestConcurrentServerShutdown` test to verify concurrent draining

---

## Files Modified

- `design/stories/epic-38-architectural-remediation/US-38.4-hash-api-keys-in-redis.md`
- `design/stories/epic-38-architectural-remediation/US-38.5-fix-nohtml-validator.md`
- `design/stories/epic-38-architectural-remediation/US-38.6-fix-controller-gauge-drift.md`
- `design/stories/epic-38-architectural-remediation/US-38.7-remove-dead-code.md`
- `design/stories/epic-38-architectural-remediation/US-38.8-consolidate-dual-patterns.md`
- `design/stories/epic-38-architectural-remediation/US-38.9-move-services-out-of-handlers.md`
- `design/stories/epic-38-architectural-remediation/US-38.10-add-pushcredentials-retry.md`
- `design/stories/epic-38-architectural-remediation/US-38.11-fix-kubernetes-client.md`
- `design/stories/epic-38-architectural-remediation/US-38.12-add-agentd-graceful-shutdown.md`

---

## Next Steps

US-38.1, US-38.2, and US-38.3 received their blocking and non-blocking corrections in the previous session (worklog 0237 reference). All 12 stories in epic 38 have now completed four passes of adversarial review and correction. The stories are ready for implementation.

Recommended implementation order per the epic README:
- Phase 1 (immediate, one-line fixes): US-38.4, US-38.5
- Phase 2 (security critical, 1-3 days): US-38.3, US-38.6, US-38.10
- Phase 3 (infrastructure, 3-7 days): US-38.1, US-38.11, US-38.12
- Phase 4 (structural, 7-14 days): US-38.2, US-38.7, US-38.8, US-38.9
