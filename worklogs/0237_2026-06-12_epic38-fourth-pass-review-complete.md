# Worklog: Epic 38 Stories — Fourth-Pass Meta-Critical Review (US-38.4 through US-38.12)

**Date:** 2026-06-12
**Session:** Adversarial meta-critical review of remaining 9 epic-38 stories — identify why each solution won't work, then assess validity of each criticism
**Status:** Complete

---

## Objective

Complete the fourth-pass review that was interrupted in worklog 0236. For each of US-38.4 through US-38.12, raise every possible criticism of why the solution will not work, then assess the validity of each criticism with concrete code evidence. Also report weaknesses, gaps, failure modes, and mitigations for each story.

---

## Methodology

Every criticism was validated against the actual source files. No claims were made without reading the relevant code. Each criticism was assessed as VALID, PARTIALLY VALID, or INVALID with a one-sentence verdict supported by file:line evidence.

---

## US-38.4 — Hash API Keys in Redis

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Hashing makes Redis undebuggable — operational regression | PARTIALLY VALID — real tradeoff, mitigated by `echo -n key \| sha256sum` lookup; JWT already uses same pattern |
| 2 | SHA-256 is too slow for a hot cache path | INVALID — 136ns per call on a 68-byte key; existing JWT path already does this on every request |
| 3 | Fix misses other places where raw keys appear | PARTIALLY VALID — story has wrong function name ("validateAPIKeyCached" doesn't exist; correct name is `AuthenticateAPIKey`); all other `apikey:` usages are already hashed |
| 4 | Rolling deploy causes 15-minute cache miss window | VALID — acknowledged by story; bounded, self-healing, no data correctness issue |
| 5 | Security theater — attacker can replay hashed key | INVALID — cache value is user ID, not a credential; hash prevents key extraction from Redis dumps/MONITOR |
| 6 | Test impact underestimated (~15 assertions) | PARTIALLY VALID — actual count is ~20 assertions across both test files, not ~15 |

**Actionable defects:**
- Fix function name: "validateAPIKeyCached" → "AuthenticateAPIKey" in Implementation Step 1
- Correct assertion count to ~20 (12 in `auth_test.go` + 8 in `auth_apikey_dek_test.go`)
- Add DeleteAPIKey revocation entry for cache invalidation on key deletion (pre-existing gap worth documenting)

**Weaknesses:**
- Two separate code paths for the fix (`AuthenticateAPIKey` at line 134, `validateAPIKey` at line 446) that must be updated atomically
- No cache revocation on `DeleteAPIKey` — deleted keys remain valid for up to 15 minutes (pre-existing, not introduced by this story)

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Developer fixes line 446 but misses line 134 due to wrong function name | MEDIUM | Partial fix: one path still leaks |
| Assertion count underestimate causes CI to fail with unfixed test | MEDIUM | CI red, no production impact |

---

## US-38.5 — Fix nohtml Validator

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Should use a proper HTML sanitizer, not string matching | PARTIALLY VALID — story scope is fixing a boolean inversion, not comprehensive XSS prevention; valid for a future story |
| 2 | Validator not used on any field that matters — no security impact | PARTIALLY VALID — story should confirm which production struct fields use `nohtml` rather than deferring to an audit step |
| 3 | `&&` will break legitimate inputs with angle brackets (e.g., "x > y") | PARTIALLY VALID — correct for HTML-prevention purposes; story should enumerate affected fields |
| 4 | `validate.Var` doesn't test in context of an actual HTTP request | VALID — `validateNoHTML` is unexported, tests are in `package tests` (separate package); cannot call unexported function; story acknowledges this but doesn't prescribe a resolution |
| 5 | Other XSS vectors not addressed (javascript: URLs, event handlers) | INVALID as a criticism of this story — out of scope; correct as a broader security observation |
| 6 | Name "nohtml" is misleading — only checks angle brackets | PARTIALLY VALID — pre-existing naming issue; fixing it requires updating all consumer struct tags, out of scope |

**Most critical defect:**
The test package problem (Criticism 4) is VALID and unresolved. `validateNoHTML` is unexported. Tests are in `package tests` (external). The story says "if `validateNoHTML` is unexported" as an afterthought. A fresh developer will attempt to call the unexported function from `package tests` and get a compile error. The story must prescribe: write tests in `api/internal/middleware/` with `package middleware` (white-box test) to access the unexported function.

**Gaps:**
- No specification of which production struct fields use `nohtml` — the audit is a blocked acceptance criterion, not optional
- No verification that the global `validate` instance (registered in `init()`) is the same one tests use

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Developer writes test in `package tests`, tries to call `validateNoHTML` — compile error | HIGH | Debugging time wasted |
| `&&` rejects `"1 > 2"` in a user bio field that real users submit | LOW | 422 errors on previously valid inputs |

---

## US-38.6 — Fix Controller Gauge Drift

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Adding Dec() before Status().Update will go negative if Update fails | VALID — the story identifies the problem and provides a rollback pattern, but the rollback code is incomplete for the actual `handleActive` code structure |
| 2 | workspaceGaugeSeeder corrects drift on restart — fix is unnecessary | INVALID — seeder only corrects at startup; persistent drift between restarts undermines alerts; safeModeGauge scalar issue cannot be fixed by seeder at all |
| 3 | handleTerminating fix is impossible — previous phase overwritten before the function runs | VALID — `handleDeletion` at `phase_terminating.go:64-71` sets `Phase = Terminating` before calling `handleTerminating`, so `previousState` field doesn't exist; must infer from status fields (PodIP/PodName) with risk of false decrements for Creating→Terminating paths |
| 4 | safeModeGauge GaugeVec will cause Prometheus cardinality explosion | INVALID — gauge only created for workspaces that enter safe mode (exceptional path); `DeleteLabelValues` prevents accumulation; existing `WorkspaceActiveSecondsTotal` already creates per-workspace series |
| 5 | Story misidentifies gauge drift locations | INVALID — all 7 listed Active→Creating transitions verified correct; but story separately lists `health.go:118` and `health.go:145` and they are confirmed real additional leak paths |
| 6 | Dec() needs labels (runtime, secLevel) that may not be available | PARTIALLY VALID — labels are immutable Spec fields, same pattern used in existing `phase_suspend.go:25`; risk of label mismatch from spec mutations is theoretically real but practically inapplicable |

**Actionable defects:**
1. The rollback pattern for `Dec()` + `Status().Update` failure must be spelled out concretely for every call site — the story's generic example doesn't map to the actual `handleActive` code structure (which returns `r.Status().Update` directly)
2. `health.go:118` and `health.go:145` are two additional Active→Creating transition points that must also receive `Dec()` — they are mentioned separately but not in the main implementation table
3. The `handleTerminating` fix must use PodIP/PodName as proxy for "was Active" rather than the nonexistent `previousState` field; story must explicitly warn about the Creating→Terminating false-decrement edge case
4. `safeModeGauge` else-branch must use `DeleteLabelValues` not `Set(0)` after conversion to GaugeVec

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| `health.go:118` and `health.go:145` missed during implementation | HIGH (listed separately, easy to miss) | Persistent gauge drift from health-check restarts |
| Dec() added before Status().Update; Update fails → gauge negative on retry | HIGH (any K8s API pressure) | Wrong gauge; may trigger false alerts |
| Creating→Terminating workspace incorrectly gets Dec() from PodName inference | MEDIUM | Gauge goes negative for short-lived workspaces |

---

## US-38.7 — Remove Dead Code

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Removing dead code is premature — kept for future use | INVALID — `strings.go` is a duplicate of active `masking.go`; zero information value |
| 2 | Some "dead" code references test infrastructure | PARTIALLY VALID — test file refs to `RequestIDMiddleware`/`CORSMiddleware` will appear as grep hits; story should explicitly note these are the test files scheduled for concurrent deletion |
| 3 | leader_election.go removal could break the controller | INVALID — zero external references confirmed by grep |
| 4 | Removing cors.go breaks CORS handling | PARTIALLY VALID — CORS handling is already broken (SecurityMiddleware OPTIONS bug); cors.go is not serving any requests; deletion doesn't change runtime behavior but removes the only reference implementation for fixing it |
| 5 | stripPatch was intentionally disabled, not removed | PARTIALLY VALID — story is correct that the code is dead; however the commit message should preserve the worklog 0070 gzip caveat to prevent future re-enablement mistakes |
| 6 | Dead code removal should happen after US-38.2 | INVALID — the stripped code is independent of proxy decomposition; grep-by-name is used, not line numbers |

**CRITICAL DEFECT — Item 4 is factually wrong:**
The story says `controller/internal/common/utils.go:19-59` are dead condition helpers. Reading the actual file: `SetCondition`, `AddFinalizer`, `RemoveFinalizer`, `IsPodReady`, `GenerateRandomString` are ALL actively called by production code (`phase_pending.go:23`, `phase_terminating.go:60`, `secrets.go:78`). Removing these lines would delete live controller code, breaking the build immediately and destroying workspace lifecycle management.

**Item 4 must be removed from the story entirely.** The condition helpers in `utils.go` are NOT dead code.

**Item 5 scope is also incomplete:**
Removing `stripPatchParts` leaves its call site at `proxy.go:671` (`filtered, filterErr := stripPatchParts(raw)`) — compile error. Must also remove the `shouldFilter` branch in `doProxy` (lines 655-679), remove the `stripPatch bool` parameter from `doProxy`, update both callers, and remove both G1 tests.

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Item 4 executed without grep verification — deletes live `SetCondition`/`AddFinalizer` | MEDIUM | HIGH — controller build breaks, no workspace lifecycle |
| Item 5 removes `stripPatchParts` but leaves call at proxy.go:671 — compile error | HIGH | Medium — build breaks, easily fixed |
| Deleting pkg/utilities/strings.go mistakenly deletes the directory | LOW | HIGH — breaks middleware, auth, logging builds |

---

## US-38.8 — Consolidate Dual Patterns

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | extractAuth migration changes error behavior — silent auth failure masking | VALID — actual `extractAuth` (secrets.go:855-867) does NOT write HTTP responses; story's migration template uses bare `return` after empty userID → HTTP 200 empty body for unauthenticated requests |
| 2 | EventBroker consolidation will change SSE behavior | PARTIALLY VALID — buffer size changes (16→128) and new subscriber limit (0→20) are semantic changes; story must call these out |
| 3 | Removing legacy injection path could break deployments | PARTIALLY VALID — production always wires `deriveAdminKey` (app validates master secret at startup); test code that doesn't call `SetAdminKeyDeriver` will break |
| 4 | ClassifyPostgresError uses wrong driver type | VALID — project uses `pgx/v5` (`*pgconn.PgError`), story sample imports `github.com/lib/pq` (`*pq.Error`); type assertion silently fails, all duplicate-key 409s become 500s |
| 5 | Story depends on US-38.2 but doesn't declare it | PARTIALLY VALID — Pattern 3 concurrent modification of proxy.go:84 creates merge conflict risk; dependency should be noted |
| 6 | Pattern consolidation is cosmetic, doesn't fix bugs | PARTIALLY VALID — Pattern 2 (errors.Is) and Pattern 4 (legacy removal) fix real correctness issues; Pattern 1 as written introduces a regression |

**CRITICAL DEFECTS:**

1. **Pattern 1 migration template is broken.** `extractAuth` does NOT write 401 responses. The story's template `if userID == "" { return }` returns HTTP 200 with no body for unauthenticated requests. Every migrated endpoint silently stops returning 401. Fix: add `c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})` before `return`, or document explicitly that `extractAuth` does not write responses and callers must.

2. **ClassifyPostgresError uses `*pq.Error` (wrong).** Must use `*pgconn.PgError` from `github.com/jackc/pgconn`. Code: `var pgErr *pgconn.PgError; if errors.As(err, &pgErr) { switch pgErr.Code { ... } }`.

3. **`terminal.go:139` missing from Pattern 1 migration list.** Only 10 of 11 `c.GetString("userID")` call sites listed.

4. **isNotFound function (same `strings.Contains` anti-pattern) not included in Pattern 2 scope.**

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| extractAuth template omits 401 — unauthenticated requests return 200 empty | HIGH (template is copy-paste ready) | HIGH — all 10-11 migrated endpoints silently stop returning 401 |
| ClassifyPostgresError uses wrong type — all duplicate-key 409s become 500s | HIGH (developer copies sample literally) | HIGH — credential creation returns 500 on duplicates |
| Legacy injection removal breaks tests not wiring deriveAdminKey | MEDIUM | Medium — test failures |

---

## US-38.9 — Move Services Out of Handlers

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Import path changes have enormous blast radius | PARTIALLY VALID — real but compile-time-safe; effort concern, not correctness risk |
| 2 | Services already well-encapsulated in handlers — moving creates unnecessary cross-package deps | PARTIALLY VALID — the `EventBroker` interface proposed has wrong methods; correct interface needed |
| 3 | ActivityTracker making K8s API calls should be in controller, not API service | INVALID — out of scope; move is correct within the bounded goal of package organization |
| 4 | EventBroker interface has wrong methods | VALID — `WorkspaceWatcher` calls `RecordWorkspaceOwner`/`CleanupWorkspace`, not `Publish`; `Publish` doesn't even exist on `UserEventBroker`; compile failure guaranteed |
| 5 | Moving SSETracker creates reverse dependency via callback types | PARTIALLY VALID — function types as callbacks (not concrete handler types) break the cycle; story needs explicit note |
| 6 | Duplicates US-38.2 | INVALID — complementary concerns; merging would be harmful; dependency correctly modeled |

**CRITICAL DEFECT:**
The `EventBroker` interface specification is concretely wrong. Story proposes: `type EventBroker interface { Publish(eventType string, payload interface{}) }`. `WorkspaceWatcher` actually calls `w.userBroker.RecordWorkspaceOwner(ws.Name, ws.Spec.Owner.UserID)` and `w.userBroker.CleanupWorkspace(name)`. These methods are not in the proposed interface. `Publish` does not exist on `UserEventBroker`. A developer following the story verbatim will create a non-compiling interface.

Correct interface: `type WorkspaceOwnerTracker interface { RecordWorkspaceOwner(workspaceID, userID string); CleanupWorkspace(workspaceID string) }`.

**Additional defects:**
- Field names wrong in proxy.go dependency table: `workspaceWatcher` should be `watcher`, `workspaceEventBroker` should be `broker`
- `WorkspaceSSEEvent` is defined in `event_broker.go` and shared by both brokers — must move to a shared types location first or both brokers must move together
- `crd_watcher.go` imports `api/internal/services/metrics` — moving it to `services/workspace` requires verifying no import cycle

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Developer creates wrong `EventBroker` interface — compile failure | HIGH (story specifies wrong methods) | Medium — blocked until fixed |
| Test files can't access unexported symbols after package change | HIGH (tests in `package handlers`) | Low — test-only failures |
| Import cycle via shared WorkspaceSSEEvent type | MEDIUM | Medium — build failure across services/* |

---

## US-38.10 — Add PushCredentials Retry

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | PUT /auth/:providerID is not idempotent — retry corrupts partial writes | INVALID — 5xx means server replied with complete error (no partial write); network errors mean no body arrived; partial-write-then-crash produces connection-refused on retry |
| 2 | Per-provider retry leaves partial credential state worse than no credentials | PARTIALLY VALID — pre-existing behavior; retry makes it strictly less likely, not more |
| 3 | Retry holds reloadMu for 3s per provider | PARTIALLY VALID — story has a factual error: `StageCredentials` is called AFTER `reloadMu.Unlock()` at `secrets.go:385`; latency is real but does not block concurrent reloads |
| 4 | 3-retry/1s-2s policy is arbitrary — no analysis of failure modes | PARTIALLY VALID — story should document opencode mid-restart as the primary target; policy is reasonable for intra-pod calls |
| 5 | Retry hides bugs by delaying failure surfacing | INVALID — 3 failed attempts surface as error with logs; persistent 500s delayed by ~3s, not hidden |
| 6 | Story uses `log.Printf` — introduces second logging system | VALID — `client.go` has no logger; `log.Printf` is stdlib; entire codebase uses zap; must inject `*zap.Logger` into `Client` |

**CRITICAL DEFECT:**
The story introduces `log.Printf` into `pkg/agent/opencode/client.go`, a library package that has zero logging today. The `cmd/workspace-agentd/` codebase uses `go.uber.org/zap` exclusively. Fix: add `logger *zap.Logger` to `Client` struct, inject via `NewClient`, default to `zap.NewNop()` if nil.

**Weaknesses:**
- No jitter on backoff — thundering herd when multiple providers fail simultaneously
- True worst-case latency is 33s per provider (3× 10s HTTP timeout), not 3s — story should document this

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| `log.Printf` compile error (if `log` identifier conflicts) | LOW | Build fails immediately |
| 33s per-provider latency under sustained 5xx — user timeout | MEDIUM | Reload-secrets handler timeout |
| 3 retries exhaust, partial credentials in auth.json | LOW | Provider state inconsistent until next full reload |

---

## US-38.11 — Fix Kubernetes Client

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Adding context to 14 CRUD methods is too high risk | PARTIALLY VALID — Go compiler enforces at compile time (no silent regression); risk is developers using `context.Background()` as shortcut |
| 2 | sync.Once caches failure forever | PARTIALLY VALID — real Go gotcha; in practice `newLLMSafespaceV1Client` cannot fail transiently (config validated at startup); warrants code comment |
| 3 | Namespace fix wrong — needs cluster-scoped factory method | PARTIALLY VALID — removing `.Namespace(r.ns)` IS correct; but story fails to remove the now-meaningless `namespace string` parameter from `RuntimeEnvironments()` |
| 4 | sync.Mutex insufficient for concurrent StartInformers | PARTIALLY VALID — mutex pattern is correct; but story's StartInformers calls `f.runtimeEnvInformer.Run(stopCh)` before initializing the informers — nil-pointer panic if no prior accessor call |
| 5 | RunOrDie replacement is unnecessary — it's in dead code | INVALID — story correctly identifies two `RunOrDie` calls: `client.go:157` (live) and `leader_election.go:70` (dead, handled by US-38.7); fixes the live one only |
| 6 | Interface change breaks mocks → breaks every test | INVALID — story explicitly accounts for mock update; Go compiler enforces compliance; no silent regression |

**CRITICAL DEFECT:**
After Fix 3 adds `ctx context.Context` to `List` and `Watch` methods, the `InformerFactory`'s `ListFunc`/`WatchFunc` closures in `informers.go` call `.List(options)` and `.Watch(options)` without a context. The `cache.ListWatch` API requires `func(options metav1.ListOptions) (runtime.Object, error)` — no context parameter. These closures **cannot compile** after Fix 3 unless addressed. Fix: store a root context in `InformerFactory` (set to `context.Background()` at construction) and use it in closures.

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Informer ListFunc/WatchFunc compile error after Fix 3 | HIGH (guaranteed if closures not updated) | Build fails entirely |
| StartInformers nil-pointer panic if called before informer accessors | MEDIUM | Process crash |
| Developers use `context.Background()` at all 60 call sites | HIGH | Zero improvement in cancellation |

---

## US-38.12 — Add Agentd Graceful Shutdown

### Critical Findings

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | K8s pods don't need graceful shutdown — kubelet handles it | INVALID — current code has NO signal handler; SIGTERM exits immediately; `proc.stop()` never called; opencode orphaned; in-flight requests not drained |
| 2 | Signal handling dangerous — SIGKILL races with shutdown | PARTIALLY VALID — 25s shared timeout for sequential user+admin shutdown is tight; story should document required `terminationGracePeriodSeconds: 35+` |
| 3 | relay injector uses context.Background() intentionally | PARTIALLY VALID — the health-wait loop is intentional; but the single HTTP fetch inside can and should be cancellable on shutdown |
| 4 | Cannot unit test — requires real K8s cluster | INVALID — component behaviors (server drain, context cancellation) are unit-testable; `signal.NotifyContext` cancellation is testable via stop() function |
| 5 | Shutdown order wrong — health probe must stay alive until after opencode stops | INVALID — once SIGTERM received, kubelet stops evaluating liveness probes for restart; user→admin→goroutines→proc is correct standard K8s pattern |
| 6 | proc.stop() already handles it — story adds unnecessary complexity | INVALID — `proc.stop()` is currently never called on normal pod termination; story adds the lifecycle orchestration that gates it correctly |

**Weaknesses:**
1. 25-second shared `shutdownCtx` is consumed sequentially by user server AND admin server — if user draining takes 24s, admin gets 1s
2. Relay injector goroutine (5-minute health-wait loop) is not joined or interrupted during shutdown
3. Redundant `defer bgCancel()` + explicit `bgCancel()` in proposed code is confusing

**Missing in story:**
- Required `terminationGracePeriodSeconds` setting not documented (story's 25s timeout + proc.stop()'s 5s SIGKILL = 30s minimum; pod manifest needs ≥35s)
- Relay injector goroutine lifecycle during shutdown not addressed
- `bgCancel()` called twice (should remove `defer`, keep explicit call)

**Failure Modes:**
| Mode | Likelihood | Blast Radius |
|---|---|---|
| Relay injector goroutine runs 5 min after SIGTERM | LOW (only at first-boot) | Goroutine leak, config writes to dying pod |
| 25s shared timeout exhausted before both servers drain | LOW | Admin server gets SIGKILL; health probes hard-fail |
| `terminationGracePeriodSeconds` not updated — kubelet SIGKILL races with proc.stop() | MEDIUM | Opencode not cleanly shut down |

---

## Summary of Blocking Defects Across All Nine Stories

| Story | Blocking Defect | Must Fix Before Implementation |
|-------|----------------|-------------------------------|
| US-38.4 | Wrong function name ("validateAPIKeyCached") would cause dev to miss line 134 | Yes |
| US-38.5 | Package access problem: test must be in `package middleware` not `package tests` | Yes |
| US-38.6 | Dec() + rollback pattern incomplete for actual handleActive code structure; health.go paths not in main table | Yes |
| **US-38.7** | **Item 4 is factually wrong — utils.go:19-59 are ALL actively used live code; deleting them breaks the controller** | **CRITICAL — remove Item 4 entirely** |
| US-38.8 | extractAuth migration template omits 401 response → silent 200; ClassifyPostgresError uses wrong driver (`*pq.Error` vs `*pgconn.PgError`) | Yes — both are silent regressions |
| **US-38.9** | **EventBroker interface has wrong methods (`Publish` vs `RecordWorkspaceOwner`/`CleanupWorkspace`) — guaranteed compile failure** | **Yes** |
| US-38.10 | `log.Printf` in a package with no logger; should use injected `*zap.Logger` | Yes |
| **US-38.11** | **InformerFactory ListFunc/WatchFunc closures cannot accept context — guaranteed build break after Fix 3** | **Critical — must exclude informer closures from Fix 3 or add stored context** |
| US-38.12 | `terminationGracePeriodSeconds` not documented; relay injector goroutine not joined | Yes |

---

## Files Modified

None — analysis only.

---

## Next Steps

Update each story file to address its blocking defects before any implementation begins:

1. US-38.4: Fix function name to `AuthenticateAPIKey`; correct assertion count to ~20
2. US-38.5: Add explicit package guidance ("tests must be in `package middleware`")
3. US-38.6: Spell out Dec()/rollback pattern for each call site; add health.go paths to main table; fix handleTerminating approach to use PodIP/PodName
4. US-38.7: Remove Item 4 entirely; expand Item 5 scope to include shouldFilter branch, doProxy signature, and both G1 tests
5. US-38.8: Fix extractAuth template to include 401 write; fix ClassifyPostgresError to use `*pgconn.PgError`; add terminal.go:139; add isNotFound to Pattern 2
6. US-38.9: Fix EventBroker interface to `WorkspaceOwnerTracker` with `RecordWorkspaceOwner`/`CleanupWorkspace`; fix field names in proxy.go table
7. US-38.10: Replace `log.Printf` with `*zap.Logger` injection; document true worst-case latency (33s); add jitter
8. US-38.11: Add stored context to InformerFactory for ListFunc/WatchFunc closures; note informer closures excluded from Fix 3; remove dead namespace param from `RuntimeEnvironments()`
9. US-38.12: Document `terminationGracePeriodSeconds: 35`; run user+admin server shutdowns concurrently; address relay injector goroutine lifecycle; remove redundant `defer bgCancel()`
