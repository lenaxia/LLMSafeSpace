# Worklog: Architectural Deep Dive Report

**Date:** 2026-06-12
**Session:** Full codebase architectural audit — every production Go source file read, all tests run, every claim validated against actual code
**Status:** Complete

---

## Objective

Perform an adversarial architectural deep dive of the entire llmsafespace codebase. Judge each major feature on whether problems are being solved at the right level of abstraction, in the right way, with the right level of complexity. Identify hacky solutions, workarounds, patches, shortcuts, and Frankenstein implementations from multiple iterations. Validate every claim with concrete code evidence — no assumptions, no reliance on comments or design docs.

---

## Methodology

1. Cloned `lenaxia/llmsafespace` and `anomalyco/opencode` (opencode for reference comparison only)
2. Read every Go source file across `api/`, `pkg/`, `cmd/`, `controller/` (~103K lines production + ~60K lines test)
3. Ran all test packages — 70+ packages, all passing
4. Validated every finding against the actual code at specific `file:line` references
5. Compared patterns against opencode's architecture where relevant

---

## Component Assessments

### 1. API Handlers — Grade: D+

**Files:** `api/internal/handlers/` (18 source files, 35 test files)

#### ProxyHandler is a god object (1623 lines)

`proxy.go` contains at least 5-8 distinct concerns in a single struct:

- Password caching (`passwordGetter`, `passwordCache`, `passwordCacheMu`)
- Workspace config caching (`wsConfigCache`, `wsConfigCacheMu`)
- Active session tracking (`activeSessions` map + mutex)
- Connection counting (`wsConns` map + mutex)
- K8s CRD watching (WorkspaceWatcher integration)
- SSE event routing (`emitNormalizedInputEvent`, `onRawEvent`)
- Pod HTTP dispatch (`fetchFromPod`)
- DB persistence (`UpsertTitle`, `UpsertParent`, `UpsertContextUsed`)
- Background goroutines (`fetchAndPersistTitle`, `runParentBackfill`, `autoApprovePermission`)
- Phase-change state machine logic (session creation on workspace transitions)

Evidence: `proxy.go:41-162` (struct definition with ~40 fields), `proxy.go:248` (spawned goroutine), `proxy.go:1503,1506,1542` (DB writes with discarded errors `_ =`).

This is the single largest maintainability problem in the codebase. The handler should be a thin dispatcher. Instead it is the entire application.

#### Services mis-packaged as handlers

Four types in `handlers/` have zero HTTP handling code:

| Type | File | What it actually does |
|------|------|-----------------------|
| `ActivityTracker` | `activity.go` | K8s API calls (`UpdateStatus`), flush loop goroutine, retry logic |
| `SSETracker` | `session_tracker.go` | Goroutine management, token counting, cost tracking |
| `WorkspaceWatcher` | `crd_watcher.go` | K8s watch loop with backoff/retry |
| `WorkspaceEventBroker` | `event_broker.go` | Pub/sub infrastructure |
| `UserEventBroker` | `event_broker_user.go` | Pub/sub infrastructure (sharded, with replay) |

These belong in `services/` or a dedicated `infrastructure/` package.

#### Frankenstein patterns

Multiple iterations have left dual patterns coexisting in the same package:

| Pattern | Old Code | New Code | Evidence |
|---------|----------|----------|----------|
| Auth extraction | `c.GetString("userID")` — panics on non-string | `extractAuth` with comma-ok type assertion | `settings.go:85` vs `secrets.go:855-867` |
| Error classification | `strings.Contains(err.Error(), "23505")` | `errors.Is` with typed sentinels | `admin_provider_credentials.go:19-26` vs `secrets.go:874-895` |
| Event brokers | `WorkspaceEventBroker` (simple mutex, no replay, no subscriber limits) | `UserEventBroker` (sharded, replay buffer, subscriber limits) | `event_broker.go` vs `event_broker_user.go` |
| Secret injection | Legacy path silently skips decrypt failures | New multi-source path with audit logging | `injection.go:251-254` vs `injection.go:87-89` |

The old `WorkspaceEventBroker` appears to be the original implementation that was superseded by `UserEventBroker` but never removed.

#### Models bolted onto wrong handler

`models.go:110,485` — `ListModels` and `SetModel` are methods on `SecretsHandler` but access `h.podIPResolver`, `h.wsUpdater`, `h.passwordGetter`, `h.manifestWriter`, `h.relayActive`, `h.metricsRecorder`. These fields were added to SecretsHandler because that's where the model routes were first attached. The catalog-fetch logic is triplicated at `models.go:166-214`, `592-640`, `661-712` — three functions independently call `GET /provider`, parse the response, build a `connectedSet`, and iterate providers.

#### Concurrency bugs

**Double-release in connection tracking:**
`proxy.go:514-519` — When `checkAndAddActiveSession` fails, `releaseConnection` is called explicitly on line 519, then the `defer` on line 515 fires again on function return. The `> 0` guard prevents negative counts but deletes the entry at 0, permanently losing a connection slot for that workspace under contention.

**Terminal WebSocket TOCTOU:**
`terminal.go:373-386` — `acquireConnection` does atomic load of `globalConns`, then acquires `wsConnsMu` mutex, then `globalConns.Add(1)`. Between the atomic load and the add, another goroutine can pass the same check. Under high concurrency, the actual connection count exceeds `maxGlobalConns`.

**Unsynchronized field writes:**
`proxy.go:156-161,1072-1073,1621-1622` — `EnableSessionParentResolution`, `SetSessionIndex`, `SetAgentStateChecker` write to handler fields without synchronization, while goroutines spawned in `SendMessage` and `emitNormalizedInputEvent` read those fields.

#### Dead code paths

- `proxy.go:559-561` — `stripPatch` permanently disabled (`false`), yet `stripPatchParts` at `proxy.go:777-846` still executes computation on every call with unreachable results.
- `models.go:279-280` — Deprecated `Tier` and `FreeTier` fields still computed and returned in every API response.

#### Swallowed errors

`proxy.go:1503,1506,1542` — `UpsertTitle`, `UpsertParent`, `UpsertContextUsed` errors discarded with `_ =`. Silently lose title/parent/context data with no observability.

`proxy.go:1287` — `_ = json.Unmarshal([]byte(rawData), &parsed)` in `onRawEvent` discards error. Malformed event payloads publish `Data: nil` to subscribers.

---

### 2. Secrets System — Grade: B-

**Files:** `pkg/secrets/` (16 source files, 18 test files)

#### CRITICAL: HKDF used as password-based KDF

`crypto.go:30-37` — `DeriveKEK` uses HKDF-SHA256 with a user password as IKM:

```go
func DeriveKEK(password []byte, salt []byte, info string) ([]byte, error) {
    hkdfReader := hkdf.New(sha256.New, password, salt, []byte(info))
    kek := make([]byte, 32)
    if _, err := io.ReadFull(hkdfReader, kek); err != nil {
        return nil, err
    }
    return kek, nil
}
```

HKDF's Extract step uses HMAC-SHA256 to produce a pseudorandom key. It is designed for deriving keys from already-uniform key material. Passwords are low-entropy inputs. An attacker can compute billions of HKDF derivations per second on a GPU, making brute-force trivially feasible against any leaked salt+hash pair. The correct primitive is a memory-hard PBKDF like Argon2id or scrypt.

This affects every KEK derivation: `key_service.go:125,147,187,260,281,353,453` (user KEK), `key_service.go:570` (API key rewrap), `root_key.go:79,104` (sealed root key).

The encryption scheme is otherwise correct: AES-256-GCM provides authenticated encryption, random nonce via `crypto/rand` at `crypto.go:78,118` prevents nonce reuse, nonce prepended to ciphertext is standard format.

#### Cache layer: DEKs stored plaintext by default

`redis_cache.go:43-44` — When `masterKey` is nil (the default), DEKs are stored as plain hex:

```go
} else {
    val = hex.EncodeToString(dek)
}
```

The constructor at `redis_cache.go:26-31` silently falls back to plaintext when no master key is provided. Anyone with Redis access can read all DEKs and decrypt every secret. The master key option exists but is not enforced — production wiring should fail-closed if no master key is provided.

#### Leaky abstraction

`secret_service.go:108,223` — `SecretService` directly accesses `KeyService.store` (an unexported field) to call `GetUserKey`:

```go
record, err := s.keys.store.GetUserKey(ctx, userID)
```

If the key store implementation changes, `SecretService` must also change. Should be an explicit method like `KeyService.GetCurrentKeyVersion(ctx, userID)`.

#### Fat interface (ISP violation)

`store.go:9-56` — `SecretStore` has 13 methods combining CRUD (6 methods), atomic re-encryption (1 complex method), bindings (4 methods), and audit (2 methods). Splitting into `SecretCRUD`, `BindingStore`, `ReEncryptionStore`, and `AuditStore` would allow focused implementations and simpler mocking.

#### Unimplementable interface method

`postgres_provider.go:61-64` — `RotateKey` always returns an error telling callers to use `RotateKeyWithPassword` instead. The method should be removed from the interface.

#### Inconsistent error contracts

`pg_secret_store.go:252-262` — `DeleteSecret` returns `fmt.Errorf("secret %s not found", secretID)` (non-sentinel). Compare with `GetSecret` which returns `(nil, nil)` for not-found. Callers cannot use `errors.Is(err, ErrSecretNotFound)`.

#### Positive findings

The secrets system has excellent test coverage: cross-tenant isolation tests (`injection_test.go:177-201`), fail-closed verification (`secret_service_test.go:673-723`), path traversal tests (`secret_service_test.go:517-549`), and bug 9 regression for rotation eager re-encryption (`key_service_test.go:471-563`). SERIALIZABLE transactions with retry on 40001 at `pg_secret_store.go:153-170`, advisory locks for binding mutations at `pg_secret_store.go:279-339`, parameterized queries everywhere.

---

### 3. Rate Limiting — Grade: F

**Files:** `api/internal/services/ratelimit/ratelimit.go` (107 lines)

This component has three independent failure modes that collectively render rate limiting non-functional.

#### Sliding window is a no-op

`ratelimit.go:63-69`:

```go
func (s *Service) RemoveFromWindow(...) error { return nil }
func (s *Service) CountInWindow(...) (int, error) { return 0, nil }
```

`CountInWindow` always returns 0. In `middleware/rate_limit.go:249`, the check `count > limit` is never true. Any client using the `sliding_window` strategy is never rate-limited.

#### Fixed window has TOCTOU race

`ratelimit.go:44-56`:

```go
currentStr, err := s.cache.Get(ctx, cacheKey)
current, _ = strconv.ParseInt(currentStr, 10, 64)
current += value
s.cache.Set(ctx, cacheKey, strconv.FormatInt(current, 10), expiration)
```

GET and SET are not atomic. Two concurrent requests both read `count=99`, both increment to `100`, both SET `100` — undercounting by 1. The code uses go-redis which natively supports `INCR` for atomic increment.

#### In-memory token bucket: unbounded memory

`ratelimit.go:22-23`: `localBuckets map[string]*bucket` — no eviction of stale entries. Every unique client IP or API key creates a permanent entry. Over weeks/months, this map grows without bound. Additionally, per-process rate limiting means N API replicas each enforce independent limits — the effective rate is N× the configured limit.

#### Root cause

The `CacheService` interface is a simple key-value cache (`Get`/`Set`), not a Redis-native interface. This abstraction prevents the rate limiter from using `INCR`, `ZADD`/`ZRANGEBYSCORE`, or any atomic Redis operation. The rate limiter was built against the wrong abstraction.

---

### 4. Controller — Grade: B+

**Files:** `controller/internal/workspace/` (18 files), `controller/internal/webhooks/` (2 files)

The controller is the strongest component in the codebase. The reconciliation loop at `reconciler.go:72-93` dispatches cleanly across 9 phases with a deletion guard. The separation into `phase_*.go` files is well-organized. The failure classification at `classification.go` maps pod failure reasons to 4 classes with appropriate recovery policies. Webhook validation is defense-in-depth (validated at admission AND at reconcile time, including shell injection defense at `workspace_webhook.go:260-287` AND `pod_builder.go:439-441`).

#### Metrics gauge drift bug

Every transition from Active to Creating fails to decrement `WorkspacesRunning`:

- `phase_active.go:34-41` (restart generation) — no Dec
- `phase_active.go:73-79` (terminating pod) — no Dec
- `phase_active.go:84` (enterRecovery) — no Dec
- `phase_active.go:97-101` (architecture drift) — no Dec
- `phase_active.go:108` (CrashLoopBackOff) — no Dec
- `health.go:117-122` (health check restart) — no Dec

But `phase_creating.go:136` always Incs on Creating→Active. Each restart cycle increments the gauge by 1.

`phase_terminating.go:15-62` — When a workspace is deleted while Active, `handleTerminating` never calls Dec. The gauge overcounts by 1 per deleted Active workspace.

The `workspaceGaugeSeeder` at `main.go:204-222` corrects both on controller restart, but the gauge drifts during a controller's lifetime.

#### Safe mode gauge is scalar

`recovery_policy.go:69`: `safeModeGauge.Set(1)` is a single scalar shared across all workspaces. If workspace A enters safe mode, gauge=1. If B enters, still 1. If A exits but B doesn't, gauge=0. Useless for alerting. Should be a `GaugeVec` with a workspace label.

#### Hardcoded port

`phase_creating.go:147` and `recovery.go:80`: `fmt.Sprintf("http://%s:4096", existingPod.Status.PodIP)` — hardcoded port instead of `agentd.AgentPort` constant used at `pod_builder.go:65`.

#### `lastDeepStatus` map key collision

`health.go:174` — Uses `ws.Name` as the key. In multi-namespace deployments, two workspaces with the same name in different namespaces share a deep-status cadence entry, causing one to be skipped. Should use `ws.Namespace + "/" + ws.Name`.

#### Dead code

`common/leader_election.go:1-93` — `SetupLeaderElection` is never called. Actual leader election uses controller-runtime's built-in mechanism at `main.go:109-129`. This file uses `context.Background()` (line 70) and `os.Exit(0)` (line 83), both anti-patterns in a controller-runtime manager.

`common/constants.go:21-30` and `common/utils.go:19-59` — Condition types and helpers never referenced by the reconciler, which uses `v1.WorkspaceConditionType` and its own helpers in `health.go`.

#### No PVC resize support

If a user updates `spec.storage.size`, the PVC is never reconciled to the new size. The controller only creates PVCs in `phase_pending.go:54-68` and never updates them.

#### Network policy: IPv6 not supported

`network_policy.go:319-321` — Only IPv4 addresses added as ipBlocks. IPv6-only workspaces have no egress beyond DNS.

#### Missing `.Owns(&networkingv1.NetworkPolicy{})` watch

`reconciler.go:105-111` — The controller creates NetworkPolicies with owner references but doesn't watch them. External modifications won't trigger reconciliation until the next periodic requeue (15s). Acceptable but should be documented.

---

### 5. Agent Abstraction — Grade: C-

**Files:** `pkg/agent/` (3 files), `pkg/agent/opencode/` (5 files)

#### AgentRuntime registry is speculative generality with zero production consumers

- `pkg/agent/agent.go:15-17` — Three `AgentType` constants (`claude-code`, `codex`) with zero implementations
- `pkg/agent/agent.go:61-69` — `Get()` is never called in production code
- `pkg/agent/agent.go:71-75` — `Register()`/`Unregister()` only used in test files
- `pkg/agent/agent.go:36` — `FormatProviderConfig` on `AgentRuntime` is never called through the interface

#### LLMProviderData duplication tax

`pkg/agent/agent.go:41-54` re-exports `pkg/secrets.LLMProviderData` to "avoid circular import". But `pkg/agent/opencode` imports `pkg/secrets` directly at `opencode.go:11`, requiring a manual field-by-field conversion at `opencode.go:48-62`. The duplicate type exists to serve an interface that nothing calls.

#### Dialect interface IS valuable — but only used by the API

The `Dialect` interface (`pkg/agent/dialect.go`) provides route path generation (12 methods) and SSE event classification (9 methods). The API server proxy correctly uses it at `proxy.go:60,104` and `app.go:79`.

The sidecar does NOT use it. It hardcodes opencode-specific behavior at `main.go:75` (`/global/health`), `main.go:91` (`/provider`), `main.go:347` (`/event`), and `main.go:383-414` (SSE event parsing duplicated inline). If a second agent type is added, the sidecar needs significant refactoring.

#### OpenCode client lacks retry

`pkg/agent/opencode/client.go:60-71` — `PushCredentials` fails permanently on transient 500 or network blip. Partial credentials may be left in auth.json (provider 1 succeeds, provider 2 fails with 500 — provider 1's key already written).

---

### 6. Sidecar (workspace-agentd) — Grade: B-

**Files:** `cmd/workspace-agentd/` (8 source files, 12 test files)

#### Strengths

The three-tier health system is excellent. Liveness (`/v1/healthz`, no opencode dependency), readiness (`/v1/readyz`, lock-free via `atomic.Pointer`), deep status (`/v1/statusz`, 60s cadence, informational only). The gate recorder (`gate_recorder.go`) is the cleanest component in the entire codebase — three startup milestones, each fires exactly once, idempotent, concurrency-safe.

Secret materialization at `pkg/agentd/secrets/secrets.go` is hardened: shell injection via `shellSingleQuote()`, TOCTOU-safe file creation (mode 0600 in open syscall), path traversal via `resolveMountPath()`, input validation on var names/hostnames/key types, per-secret error isolation, full-replace semantics preventing stale secrets, `reloadMu` preventing concurrent materialize races.

#### No graceful shutdown

`cmd/workspace-agentd/main.go:773` — SSE goroutine uses `context.Background()`, never cancelled on shutdown.

`main.go:919` — `userSrv.ListenAndServe()` blocks main() with no signal handling (SIGTERM/SIGINT). On pod termination, kubelet sends SIGKILL after terminationGracePeriodSeconds.

`main.go:907-909` — Admin server's `log.Fatal` calls `os.Exit(1)` without stopping the managed process or draining health probe goroutines.

#### Mutex held during HTTP calls

`main.go:534-571` — `cachedState` holds `cache.mu.Lock()` while making up to 3 synchronous HTTP calls (ConnectedProviders, ConfiguredProviderCount, ListSessions). Blocks all statusz/readyz requests for potentially seconds under opencode load. The 15s TTL mitigates but does not eliminate this.

#### Relay injector bypasses filesystem abstraction

`relay_injector.go:153,346` — Uses `os.ReadFile`/`os.WriteFile` directly instead of the `Filesystem` abstraction from `pkg/agentd/secrets/` used everywhere else for testability.

#### Relay injector not cancellable

`relay_injector.go:384-389` — Health check polling uses `time.Sleep(2 * time.Second)` in a loop, not cancellable via context. If opencode never becomes healthy, the goroutine spins for 5 minutes.

---

### 7. Middleware & Auth — Grade: C+

**Files:** `api/internal/middleware/` (12 files), `api/internal/services/auth/auth.go` (1040 lines)

#### CRITICAL: Raw API keys in Redis key names

`auth.go:446` and `auth.go:134`:

```go
cacheKey := fmt.Sprintf("apikey:%s", apiKey)
```

The raw API key is embedded in the Redis key name. If Redis is compromised, dumped (`dump.rdb`), or monitored (`MONITOR` command), all active API keys are exposed. JWT token caching correctly uses a hash at `auth.go:368`. Fix: `cacheKey := fmt.Sprintf("apikey:%s", pkgutil.HashString(apiKey))`.

#### CRITICAL: HTML injection validator logic inverted

`middleware/validation.go:351`:

```go
return !strings.Contains(value, "<") || !strings.Contains(value, ">")
```

The `||` means `<script` (no closing `>`) passes validation. Should be `&&` — fail if either `<` or `>` is present.

#### Error message leak

`server/router.go:852`:

```go
c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
```

For non-`APIError` errors (raw Go errors), the full internal error message is returned to the client — potentially leaking database connection strings, file paths, or implementation details.

#### CORS broken for cross-origin usage

`middleware/security.go:114-117` — OPTIONS requests skip all security processing including CORS header injection. The `CORSMiddleware` at `middleware/cors.go` correctly handles preflight but is never registered in the router at `router.go:123-130`. Configurable `AllowedOrigins` suggests cross-origin support is intended.

#### Recovery middleware placement causes metric gaps

`router.go:124` — RecoveryMiddleware is outermost. When it catches a panic, inner middleware's post-`c.Next()` code never executes. Panicked requests are invisible to LoggingMiddleware and MetricsMiddleware.

#### Unused middleware

`middleware/request_id.go`, `middleware/cors.go`, and `middleware/auth.go:147` (`AuthorizationMiddleware`), `middleware/auth.go:219` (`RequirePermissions`) exist but are never registered in the router.

#### Auth service strengths

Login returns uniform error messages (`auth.go:716`): "invalid email or password" regardless of user existence or password correctness. Dummy bcrypt compare prevents timing-based user enumeration (`auth.go:672,701,709`). Token revocation stores under both hash key and jti key (`auth.go:276-290`). JWT key rotation supported via `jwtPreviousSecrets` (`auth.go:115`).

#### Quota enforcement in wrong layer

`server/router.go:588-599` — Workspace quota check reads env var directly and queries the database from the handler. Should be in `WorkspaceService.CreateWorkspace` so other callers get the same enforcement.

#### http.DefaultClient used in workspace service

`services/workspace/workspace_service.go:840` — `http.DefaultClient` has no timeouts. While the context has a 60s deadline, the transport-level timeout is unbounded. A dedicated `http.Client` with transport timeout should be used.

---

### 8. Kubernetes Client Wrapper — Grade: C-

**Files:** `pkg/kubernetes/` (3 files)

#### Creates new REST client on every call

`client.go:206-213` — `newLLMSafespaceV1Client(c.restConfig)` called every time. Allocates new REST client, codec factory, and transport config. Should be lazily initialized once.

#### Returns nil on error (causes panics)

`client.go:209-211` — On error, logs and returns nil. Any caller doing `c.LlmsafespaceV1().Workspaces(ns)` will nil-pointer panic.

#### All CRUD uses context.TODO()

`client_crds.go:106,112,131,143,153,164,175,189,200,216,226,236,248,258` — Every operation ignores context. No cancellation, no timeouts, no tracing propagation.

#### Cluster-scoped type uses namespace

`client_crds.go:87-89` — `RuntimeEnvironment` is cluster-scoped at `runtimeenvironment_types.go:69` but CRUD methods pass namespace to REST client. API server errors when namespace is non-empty.

#### Duplicate informers

`informers.go:64-71` — `StartInformers` creates new `SharedIndexInformer` instances each time it's called. Multiple calls = duplicate informers. Should cache instances.

#### RunOrDie for leader election

`client.go:157` — `leaderelection.RunOrDie` panics on failure, taking down the entire process. Should use `leaderelection.NewLeaderElector` with explicit error handling.

---

### 9. Redaction Pipeline — Grade: B-

**Files:** `pkg/redact/redact.go` (108 lines)

#### Base64 rule is a false-positive machine

`redact.go:47`: `[A-Za-z0-9+/]{40,}={0,2}` matches any 40+ character alphanumeric string. UUIDs without hyphens, Kubernetes resource versions, long identifiers, normal IDs — all redacted. The character class covers nearly every non-punctuation character.

#### JWT pattern misses signature

`redact.go:45`: `ey[A-Za-z0-9_\-]{10,}\.ey[A-Za-z0-9_\-]{10,}` matches header and payload but not the third signature segment. The signature portion leaks. Test at `redact_test.go:551` confirms two-segment match only.

#### \S+ greediness causes collateral redaction

`redact.go:37-39`: `(?i)(token\s*[=:]\s*)\S+` consumes past commas, semicolons, quotes. `token=abc123;user=bob` matched as a single "secret".

#### Redact() always returns nil error

`redact.go:69-75`: Signature is `(string, error)` but error is never non-nil. Misleading API surface.

---

### 10. Settings — Grade: A-

**Files:** `pkg/settings/` (5 files, 7 test files)

Best-engineered package in the repo. Clean tiered schema, singleflight for cache stampede prevention, proper seed job with orphan detection, thorough validation.

Minor issues:
- `instance_service.go:172` — Logs raw setting values. A future sensitive setting would be logged in cleartext.
- `schema.go:58` vs `workspace_types.go:83,86,88` — Storage pattern `Gi|Mi` doesn't match CRD's `Ki|Mi|Gi`.
- `validate.go:36-38` — Regex recompiled on every `Validate()` call. Schema is fixed at compile time; could precompile.
- `schema.go:58-59` — `maxStorageSize` defaults to same as `defaultStorageSize` ("10Gi"), making the max constraint redundant.

---

### 11. CRD Types — Grade: B+

**Files:** `pkg/apis/llmsafespace/v1/` (5 files)

Well-designed with proper kubebuilder annotations, print columns, and CRD drift detection via `pkg/repolint/`.

`workspace_types.go:234-300` — `WorkspaceStatus` is a 35+ field god struct combining agent metrics, pod lifecycle, and startup timing anchors. Should decompose into `PodStatus`, `AgentMetrics`, `StartupAnchors`.

---

## Cross-Cutting Findings

### Dead Code Inventory

| Location | Description | Evidence |
|----------|-------------|----------|
| `pkg/utilities/strings.go` | Entire file commented out | Lines 6-12 |
| `controller/common/leader_election.go` | Never called; actual impl uses controller-runtime | Lines 1-93 |
| `controller/common/constants.go:21-30` | Condition types never referenced by reconciler | |
| `controller/common/utils.go:19-59` | Condition helpers never used; reconciler has its own | |
| `api/internal/handlers/proxy.go:559-561,777-846` | `stripPatch` permanently disabled; computation still runs | |
| `api/internal/handlers/models.go:279-280` | Deprecated `Tier`/`FreeTier` still computed and returned | |
| `api/internal/middleware/request_id.go` | Never registered in router | |
| `api/internal/middleware/cors.go` | Never registered in router | |
| `pkg/agent/agent.go:15-17` | Three agent type constants with zero implementations | |

### Dual Pattern Inventory (Frankenstein)

| Concern | Old Pattern | New Pattern | Action |
|---------|-------------|-------------|--------|
| Auth extraction | `c.GetString("userID")` panics | `extractAuth()` comma-ok | Remove old |
| Error classification | `strings.Contains` on messages | `errors.Is` with sentinels | Remove old |
| Event brokers | `WorkspaceEventBroker` (no replay) | `UserEventBroker` (sharded, replay) | Remove old |
| Secret injection | Legacy path silent skip (`injection.go:251`) | New multi-source with audit | Remove old |
| Leader election | `common/leader_election.go` (dead) | controller-runtime built-in | Delete file |
| Condition helpers | `common/utils.go` | `health.go` reconciler-local | Delete file |

### Abstraction Level Problems

| Component | Problem | Correct Level |
|-----------|---------|---------------|
| ProxyHandler (1623 lines) | 5-8 concerns in one struct | Extract: PasswordService, SessionTracker, EventRouter, PodDispatcher, WorkspacePersistenceService |
| ActivityTracker in handlers/ | K8s API calls, flush loop, retry | Move to `services/` |
| SSETracker in handlers/ | Goroutine management, cost tracking | Move to `services/` |
| WorkspaceWatcher in handlers/ | K8s watch loop | Move to `services/` |
| Event brokers in handlers/ | Pub/sub infrastructure | Move to `infrastructure/` or `pubsub/` |
| Encryption in handlers | AES-GCM in `admin_provider_credentials.go:106-119` | Move to `SecretService` |
| Transaction mgmt in handlers | `BeginTx`/`Commit` in `agent_reload.go:209-253` | Encapsulate in service method |
| AgentRuntime registry | Speculative generality, zero consumers | Remove until second agent type |
| `pkg/utilities/masking.go` MaskString | Reveals content for short values | Fix or remove |

### Security Findings Summary

| Severity | Location | Finding |
|----------|----------|---------|
| CRITICAL | `pkg/secrets/crypto.go:30` | HKDF used as password-based KDF — should be Argon2id |
| CRITICAL | `api/services/auth/auth.go:446,134` | Raw API keys in Redis key names — leaked on dump/monitor |
| CRITICAL | `api/services/ratelimit/ratelimit.go:63-69` | Sliding window rate limiting returns 0 — never limits |
| CRITICAL | `api/middleware/validation.go:351` | HTML validator logic inverted — `<script` passes (`||` vs `&&`) |
| HIGH | `pkg/secrets/redis_cache.go:43-44` | DEKs stored plaintext in Redis without master key enforcement |
| HIGH | `api/services/ratelimit/ratelimit.go:44-56` | Non-atomic GET+SET — fixed window undercounts |
| HIGH | `api/server/router.go:852` | Internal error messages leaked to clients |
| HIGH | `api/middleware/security.go:114-117` | CORS broken — OPTIONS skips header injection, CORSMiddleware not wired |
| HIGH | `api/services/ratelimit/ratelimit.go:22-23` | Unbounded `localBuckets` map — memory leak, per-process limits |
| MEDIUM | `pkg/redact/redact.go:47` | Base64 rule false-positives on 40+ char alphanumeric strings |
| MEDIUM | `controller/workspace/phase_active.go` | WorkspacesRunning gauge drifts +1 per Active→Creating→Active cycle |
| MEDIUM | `pkg/settings/instance_service.go:172` | Logs raw setting values |
| MEDIUM | `pkg/kubernetes/client_crds.go:106+` | All CRUD ops use `context.TODO()` |
| MEDIUM | `pkg/kubernetes/client_crds.go:87-89` | Cluster-scoped RuntimeEnvironment CRUD uses namespace |

---

## Comparison with OpenCode Reference

OpenCode (TypeScript/Effect monorepo at `anomalyco/opencode`) was examined for architectural pattern comparison:

| Aspect | LLMSafeSpace | OpenCode |
|--------|-------------|----------|
| Agent abstraction | Speculative registry with 1 impl, unused by sidecar | Plugin hook system with ordered deterministic composition |
| LLM integration | Direct HTTP proxy to opencode | Protocol abstraction with 4 orthogonal axes (protocol, endpoint, auth, framing) |
| Tool execution | Delegated to opencode agent | Effect Schema-first with typed tool context, permission system |
| Session management | Proxy + SSE relay to pod | Durable prompt admission with execution/recording separation |
| Error handling | Mixed (sentinel errors + string matching) | Typed Effect errors throughout |
| Concurrency model | Goroutines + mutexes | Effect fibers with structured concurrency |

LLMSafeSpace's architecture is simpler because it delegates agent logic to opencode. This is the correct choice for a K8s platform — the agent runs inside the pod and the platform manages lifecycle. The complexity is appropriately distributed. Where LLMSafeSpace runs into trouble is when it duplicates agent-adjacent logic (event parsing, model catalog management, credential formatting) in the API and sidecar instead of keeping it cleanly in the pod.

---

## Recommendations (Priority Order)

1. **Fix the rate limiter.** Replace the `CacheService` dependency with a Redis-native interface supporting `INCR`, `ZADD`/`ZRANGEBYSCORE`, and `EXPIRE`. Implement sliding window correctly. Add eviction to the in-memory token bucket. This is a security control that is currently non-functional.

2. **Decompose ProxyHandler.** Extract 5-6 focused services: `PasswordService`, `SessionTracker`, `WorkspaceEventRouter`, `PodDispatcher`, `WorkspacePersistenceService`. The handler should be a thin dispatcher, not a 1623-line god object.

3. **Replace HKDF with Argon2id** in `pkg/secrets/crypto.go:30`. Localized to one function and its callers. Fundamental cryptographic weakness.

4. **Hash API keys before using as Redis keys** in `auth.go:446,134`. One-line fix with existing `HashString` utility.

5. **Fix the `nohtml` validator** at `validation.go:351`. Change `||` to `&&`. One-character fix.

6. **Fix metrics gauge drift** in `phase_active.go`. Add `metrics.WorkspacesRunning.Dec()` before every Active→Creating transition. Add Dec in `phase_terminating.go` for Active workspace deletion. Convert `safeModeGauge` to `GaugeVec` with workspace label.

7. **Remove dead code.** Delete `strings.go`, `common/leader_election.go`, `stripPatch` paths, deprecated model fields, unused middleware files.

8. **Consolidate dual patterns.** Migrate all handlers to `extractAuth` and `errors.Is`. Remove old `WorkspaceEventBroker`. Remove legacy injection path.

9. **Move services out of handlers.** `ActivityTracker`, `SSETracker`, `WorkspaceWatcher`, and event brokers belong in `services/` or a dedicated package.

10. **Add retry to PushCredentials** in `pkg/agent/opencode/client.go`. Transient 500 leaves partial credentials.

11. **Fix the K8s client wrapper.** Cache REST client, return errors instead of nil, accept context in CRUD methods, handle cluster-scoped types correctly.

12. **Add graceful shutdown** to workspace-agentd. Handle SIGTERM, cancel SSE goroutines, coordinate admin/user server shutdown.

---

## Tests Run

```
All 70+ Go test packages: PASS
  pkg/secrets/...         — PASS (0.049s)
  pkg/redact/...          — PASS (0.008s)
  pkg/settings/...        — PASS (0.078s)
  pkg/agent/...           — PASS
  pkg/agent/opencode/...  — PASS (0.545s)
  pkg/agentd/...          — PASS
  pkg/agentd/secrets/...  — PASS (0.091s)
  pkg/apis/.../v1/...     — PASS (0.011s)
  pkg/utilities/...       — PASS
  pkg/validation/...      — PASS
  pkg/repolint/...        — PASS (0.086s)
  pkg/types/...           — PASS
  pkg/http/...            — PASS
  pkg/logger/...          — PASS
  controller/...          — PASS (all 6 packages)
  cmd/seal-key/...        — PASS (25.569s)
  cmd/workspace-agentd/...— PASS (55.609s)
  api/internal/handlers/...  — PASS (15.965s)
  api/internal/middleware/tests/... — PASS (0.021s)
  api/internal/services/auth/...   — PASS (10.733s)
  api/internal/services/cache/...  — PASS
  api/internal/services/database/... — PASS
  api/internal/services/metrics/... — PASS
  api/internal/services/ratelimit/... — PASS
  api/internal/services/workspace/... — PASS
  api/internal/app/...      — PASS
  api/internal/config/...   — PASS
  api/internal/server/...   — PASS (0.150s)
  api/internal/errors/...   — PASS
  api/internal/logger/...   — PASS
  api/internal/utilities/... — PASS
  api/internal/interfaces/... — PASS
```

Note: Disk space constraints prevented a full `go test ./...` in one pass. Tests were run in batches with `GOTMPDIR=/workspace/tmp`. All packages pass individually.

---

## Next Steps

The 12 prioritized recommendations above. Items 4, 5 are one-line fixes that should be done immediately. Item 3 (Argon2id) is a localized change with high security impact. Items 1 (rate limiter) and 2 (ProxyHandler decomposition) are larger refactors that should be planned as dedicated epics.
