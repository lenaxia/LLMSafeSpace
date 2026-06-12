# Worklog: Epic 38 Stories — Fifth-Pass Review and Corrections

**Date:** 2026-06-12
**Session:** Fifth-pass adversarial review of all 12 epic-38 stories against actual source code; applied all remaining blocking and non-blocking corrections
**Status:** Complete

---

## Objective

Perform a final adversarial review pass with the bar: "Could a competent junior engineer pick this story up cold and implement it correctly without asking any questions?" For each story, validate every claim, interface definition, code sample, line number, and test against the actual code. Apply all remaining corrections.

---

## Findings and Corrections by Story

### US-38.1 — Fix Rate Limiter

**3 blocking, 1 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `cfg.Redis.Addr` does not exist — config has `Host`/`Port` separately; also missing `Password`, `DB`, `PoolSize` | Replaced with `fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port)` + full Redis options in all wiring snippets |
| B2 | BLOCKING | `s.rateLimiter` is wrong (exported field is `s.RateLimiter`); `s.logger.Error(...)` in Stop() references non-existent struct field | Fixed field name; replaced logger call with `fmt.Fprintf(os.Stderr, ...)` with note |
| B3 | BLOCKING | Step 9 wiring eliminates the Redis startup health check that `cache.New()` currently performs | Added explicit `client.Ping(context.Background()).Err()` check after client construction |
| N1 | NON-BLOCKING | `RemoveFromWindow` exclusive boundary `"(%d"` format unexplained | Added comment: "The '(' prefix means strictly less than cutoff — entries at exactly cutoff are kept" |

---

### US-38.2 — Decompose ProxyHandler

**6 blocking, 3 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `stream_user_events.go` (296 lines) entirely absent — true total is 2323 lines, not 2027; file contains 6 `h.userBroker` call sites that break after broker removal | Added to Summary table; added migration step for 6 call sites; updated AC#1 |
| B2 | BLOCKING | `EventRouterService` interface uses unexported types `*subscriber` and `[]replayEntry` — cross-package compile error | Replaced with channel-based: `(<-chan types.WorkspaceSSEEvent, func(), error)` |
| B3 | BLOCKING | `WorkspaceSSEEvent` creates circular import: interfaces imports event, event imports interfaces | Added prerequisite step to move to `pkg/types/events.go`; interfaces uses `types.WorkspaceSSEEvent` |
| B4 | BLOCKING | Step 3 maps `checkAndAddActiveSession → ConnectionLimiter.Acquire` — wrong; these are different maps (`activeSess` vs `connCount`) | Fixed: `acquireConnection → Limiter.Acquire`, `releaseConnection → Limiter.Release`; `checkAndAddActiveSession` stays in Step 4 |
| B5 | BLOCKING | `GetSessionIndex()` added to Services interface but `services.Services` struct has no SessionIndex field and no construction path | Added explicit step: add field to struct, add `sessionindex.New()` in `services.New()`, add accessor |
| B6 | BLOCKING | Proposed final `ProxyHandler` struct omits `agentStateChecker` still needed by `proxy_chat_enrichment.go:60-62` | Added field with cross-reference to `app.go:467` |
| N1 | NON-BLOCKING | "2027-line" should be "2323-line (spanning five files)" | Updated everywhere |
| N2 | NON-BLOCKING | Story says "extend SessionIndexService with PersistTitle/PersistParent/PersistContextUsed" but these methods already exist as `UpsertTitle`/`UpsertParent`/`UpsertContextUsed` at `interfaces.go:147-149` | Corrected: SessionManager wraps existing methods with error logging; no new interface methods needed |
| N3 | NON-BLOCKING | `startOnce`/`stopOnce` sync.Once fields absent from proposed final struct | Added with note on concurrent Start()/Stop() protection |

---

### US-38.3 — Replace HKDF with Argon2id

**6 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `key_service.go:147` classified as password path — actual code derives `recoveryKEK` from `recoveryKey` (16 random bytes) — high entropy, must use DeriveKEKFromKey | Moved to recovery key group; password sites = 6, recovery key sites = 4 |
| B2 | BLOCKING | "10 high-entropy callers" count wrong — after B1 fix the correct count is 9 | Changed everywhere including AC#7 |
| B3 | BLOCKING | Sealed root key backward-compat uses version-byte heuristic — 0.78% of legacy files would be corrupted | Replaced with length-based detection: `if len(data) == 92` (legacy) vs `>= 33` (versioned) |
| B4 | BLOCKING | Login-time upgrade has no concrete interface changes — `KeyStore` and `PgKeyStore` missing `UpgradePasswordKDF` method with `WHERE kdf_version=0` conditional UPDATE | Added method to KeyStore interface, PgKeyStore implementation, and UnlockDEK orchestration |
| B5 | BLOCKING | "Existing tests that break" lists only 2 tests — actually 10 tests across 3 files call `DeriveKEK` directly and will fail to compile | Expanded to all 10 with file:line and per-test disposition |
| B6 | BLOCKING | New test code uses `secrets.` prefix (external package) but test files are `package secrets` (internal); uses testify `require` not used in existing test files | Fixed: new tests in `crypto_argon2_test.go` with `package secrets_test` declaration and explicit testify import |
| N1 | NON-BLOCKING | `// 64 MB` comment wrong unit — argon2 takes KiB | Changed to `// 64 MiB (65536 KiB)` |
| N2 | NON-BLOCKING | `pg_key_store.go` missing from Files Modified table | Added with: "Add kdf_version to SELECT/INSERT queries. Add UpgradePasswordKDF method." |

---

### US-38.4 — Hash API Keys in Redis

**1 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| N1 | NON-BLOCKING | `auth_apikey_dek_test.go` has ~11 assertions, not ~8; total is ~23, not ~20 | Updated counts in 3 locations |

---

### US-38.5 — Fix nohtml Validator

**4 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| N1 | NON-BLOCKING | Code snippets omit the `reflect.Kind()` guard — full function body not shown | Updated both Buggy Code and Proposed Fix blocks to show complete function |
| N2 | NON-BLOCKING | No warning about mixing test approaches with shared global `validate` instance | Added warning: prefer `setupValidator` to avoid test pollution |
| N3 | NON-BLOCKING | No explanation that `init()` runs automatically — junior may try to call it manually | Added note on Go's automatic `init()` invocation |
| N4 | NON-BLOCKING | AC#8 doesn't say what to do if affected fields are live API endpoints | Added: document as tightened validation in PR description if breaking change |

---

### US-38.6 — Fix Controller Gauge Drift

**3 blocking, 5 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `metrics_wiring_test.go` update instructions missing the critical `newTestGaugeMetric → newTestGaugeVec` constructor replacement — the type change makes all 4 test call sites fail to compile | Added explicit constructor replacement for all 4 test sites with `ws.UID` requirement |
| B2 | BLOCKING | No sequencing note for Fix 4a/4b vs Fix 7 — applying wiring.go signature change before metrics.go type change breaks compilation | Added: "Change metrics.go FIRST, then metrics_wiring.go, then tests — type mismatch blocks intermediate compilation" |
| B3 | BLOCKING | Line-66 rollback snippet uses undefined variable `class` — not in scope at that call site | Replaced with concrete `FailureClassInfrastructure` literal; complete snippet shown |
| N1 | NON-BLOCKING | `phase_active.go:73-79` path incorrectly implied to include RestartCount++ | Added note: "this path does NOT increment RestartCount" |
| N2 | NON-BLOCKING | `SeedWorkspacesRunning` line reference off by 5 (166 vs 161) | Corrected to `metrics.go:161-167` |
| N3 | NON-BLOCKING | Test 8 uses `prometheus.DefaultGatherer` which won't contain the test metric | Replaced with `GetMetricWith` + `testutil.ToFloat64` verification |
| N4 | NON-BLOCKING | Story implies rollback covers all metric mutations — only the gauge rollback is needed | Added clarifying note: other metrics are additive and self-correct |
| N5 | NON-BLOCKING | `enterRecovery` call at line 84 uses generic `class` — same issue as B3 but for the CrashLoopBackOff path | Applied same concrete-literal fix pattern |

---

### US-38.7 — Remove Dead Code

**3 blocking, 1 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | Item 3 scope too narrow — lines 7-19 (ControllerName, AnnotationCreatedBy, LabelApp, etc.) are also dead; all have zero external references | Expanded Item 3 to full lines 7-30; file-deletion instruction added |
| B2 | BLOCKING | Item 3 example constants "ConditionTypeAvailable, ConditionTypeProgressing" don't exist — actual names are ConditionReady, ConditionPodCreated, etc. | Replaced with all actual constant names; grep commands pre-filled |
| B3 | BLOCKING | Item 5 missing sub-task (g) — stale doc-comments at `proxy.go:604-608` and `proxy.go:458-459` not updated | Added (g) to atomic commit scope |
| N1 | NON-BLOCKING | Item 5(a) range starts at 777, missing doc-comment block at 771 | Changed to `proxy.go:771-846` |

---

### US-38.8 — Consolidate Dual Patterns

**4 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `"github.com/jackc/pgconn"` (pgx/v4 standalone module) used throughout — project uses `pgx/v5` where pgconn is at `"github.com/jackc/pgx/v5/pgconn"` | All occurrences replaced; Driver note updated |
| B2 | BLOCKING | "11 call sites" but list has only 10 `GetString("userID")` entries — 11th is `GetString("sessionID")` (different key) | Changed to "10 userID + 1 sessionID call sites" everywhere |
| B3 | BLOCKING | Single migration template for 10 sites — but 6 have no existing guard (behavior change); 4 have existing guard (preserve) | Split into Template A (4 guarded sites) and Template B (6 unguarded sites) with explicit before/after for each |
| B4 | BLOCKING | `isNotFound` line range `23-25` missing closing brace at line 26 | Corrected to `23-26` |
| N1 | NON-BLOCKING | Pattern 1, line 75-76: removing `|| sessionID == ""` changes HTTP 401 → 503 without documentation | Added behavior-change callout with decision guidance |
| N2 | NON-BLOCKING | Pattern 4 Step 4 suggests unnecessary empty-bindings guard | Replaced with note that new path already handles zero bindings correctly |

---

### US-38.9 — Move Services Out of Handlers

**2 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | WorkspaceWatcher uses chained `LlmsafespaceV1()` calls that break after US-38.11 changes the return type to `(interface, error)` | Added cross-story compatibility note with corrected error-check pattern |
| B2 | BLOCKING | `session_tracker.go:249` references `opencodePort` constant from `proxy.go` — compile error after move | Added note to replace with `agentd.AgentPort` directly |
| N1 | NON-BLOCKING | `WorkspaceOwnerTracker` interface location not specified — junior may put it in interfaces.go | Added explicit: "define in `watcher.go`, NOT in `interfaces/interfaces.go`" |
| N2 | NON-BLOCKING | Test file inventory missing before Step D | Added `ls api/internal/handlers/*_test.go` step |

---

### US-38.10 — Add PushCredentials Retry

**2 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | Only `secrets.go:400` listed as caller needing NewClient update — 5 additional call sites missed: `cmd/workspace-agentd/agent_reload.go:37`, `api/internal/handlers/agent_reload.go:158`, `api/internal/handlers/agent_reload.go:417`, plus 2 test files | All 6 call sites added to Files to Change table and Step 1 |
| B2 | BLOCKING | `retryWithBackoff` logs `zap.String("provider", "")` but has no access to provider name — `fn` parameter is `func(attempt int) error` | Fixed: move retry logging INTO the `setAuth` closure where `p.Provider` is in scope |
| N1 | NON-BLOCKING | `secrets.go:400` reference imprecise (StageCredentials is at 400-401, 15 lines after unlock at 385) | Updated with precise line numbers and distance |
| N2 | NON-BLOCKING | Tests with real 3s backoff delays will slow CI | Added note and suggestion to make `initialDelay` a parameter for tests |

---

### US-38.11 — Fix Kubernetes Client

**3 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | `StartInformers` calls `newRuntimeEnvInformerLocked()` and `newWorkspaceInformerLocked()` but never defines them — junior cannot implement without the function bodies | Added full bodies of both locked helpers |
| B2 | BLOCKING | Fix 3 and Fix 5 show different (contradictory) `InformerFactory` struct layouts | Added note: "Apply only Fix 5's complete struct; Fix 3's abbreviated struct is illustrative only" |
| B3 | BLOCKING | AC for StartInformers says "call accessor methods" which would deadlock under `f.mu` | Updated AC to name the locked helpers and explain the deadlock risk |
| N1 | NON-BLOCKING | Fix 4 says "1 call site in informers.go" — actually 2 lines (ListFunc at 36 and WatchFunc at 39) | Corrected to "2 lines" |
| N2 | NON-BLOCKING | Acceptance criterion contradicts implementation (public accessors vs locked helpers) | Aligned AC language with implementation |

---

### US-38.12 — Add Agentd Graceful Shutdown

**2 blocking, 2 non-blocking**

| ID | Type | Issue | Fix Applied |
|----|------|-------|-------------|
| B1 | BLOCKING | Fix 7 shows health-wait loop context cancellation but misses `time.Sleep(5s)` at `relay_injector.go:428` (inside model-fetch retry loop) | Added `relay_injector.go:428` fix to Fix 7 |
| B2 | BLOCKING | Files to Change says "Ensure X accepts context.Context (verify)" for healthz_cache.go and sse_tracker.go — both already do | Changed to "No change needed — already accept context.Context; verify with grep" |
| N1 | NON-BLOCKING | bgCtx/rootCtx relationship not explained — goroutines may already be stopped when bgCancel() fires | Added explanatory note about belt-and-suspenders pattern |
| N2 | NON-BLOCKING | terminationGracePeriodSeconds has no file path guidance | Added `grep -rn 'terminationGracePeriodSeconds' charts/` command |

---

## Summary

| Story | Blocking Fixed | Non-Blocking Fixed | Total |
|-------|---------------|-------------------|-------|
| US-38.1 | 3 | 1 | 4 |
| US-38.2 | 6 | 3 | 9 |
| US-38.3 | 6 | 2 | 8 |
| US-38.4 | 0 | 1 | 1 |
| US-38.5 | 0 | 4 | 4 |
| US-38.6 | 3 | 5 | 8 |
| US-38.7 | 3 | 1 | 4 |
| US-38.8 | 4 | 2 | 6 |
| US-38.9 | 2 | 2 | 4 |
| US-38.10 | 2 | 2 | 4 |
| US-38.11 | 3 | 2 | 5 |
| US-38.12 | 2 | 2 | 4 |
| **Total** | **34** | **27** | **61** |

---

## Files Modified

All 12 story files in `design/stories/epic-38-architectural-remediation/`:
- `US-38.1-fix-rate-limiter.md`
- `US-38.2-decompose-proxy-handler.md`
- `US-38.3-replace-hkdf-with-argon2id.md`
- `US-38.4-hash-api-keys-in-redis.md`
- `US-38.5-fix-nohtml-validator.md`
- `US-38.6-fix-controller-gauge-drift.md`
- `US-38.7-remove-dead-code.md`
- `US-38.8-consolidate-dual-patterns.md`
- `US-38.9-move-services-out-of-handlers.md`
- `US-38.10-add-pushcredentials-retry.md`
- `US-38.11-fix-kubernetes-client.md`
- `US-38.12-add-agentd-graceful-shutdown.md`

---

## Next Steps

All 12 stories have now completed five adversarial review passes. The stories are ready for implementation. Recommended execution order per the epic README:

- **Phase 1** (immediate): US-38.4, US-38.5
- **Phase 2** (security): US-38.3, US-38.6, US-38.10
- **Phase 3** (infrastructure): US-38.1, US-38.11, US-38.12
- **Phase 4** (structural): US-38.2, US-38.7, US-38.8, US-38.9
