# Epic 34: Session Security — Remember Me & DEK Encryption Enforcement

**Status:** Ready to Implement
**Depends On:** Epic 10 (DEK infrastructure — confirmed in place)
**Estimated Effort:** ~8 hours

---

## Validated Assumptions

Every claim is verified against live code. No unvalidated claims are made.

| # | Assumption | Verified At | Result |
|---|---|---|---|
| A1 | `setSessionCookie` hardcodes `Max-Age=86400` | `router.go:346` | Confirmed |
| A2 | `setSessionCookie` called from exactly 2 places | `router.go:389, 405` | Confirmed — logout uses direct `c.SetCookie` at line 444 |
| A3 | `cfg.Auth.CookieName` exists in config but is never read | `config.go:69`; no read-site in production code | Confirmed — cookie name is hardcoded `"lsp_session"` in 4 production locations |
| A4 | The 4 hardcoded `"lsp_session"` locations | `router.go:346`, `router.go:436`, `router.go:444`, `auth.go:717` | Confirmed |
| A5 | `auth.go:717` is in `extractToken()`, called by BOTH `AuthMiddleware()` (line 725) and `OptionalAuthMiddleware()` (line 765) — the service has `s.config` available in both | `auth.go:712–719, 725, 765`, `auth.go:81–98` | Confirmed — both call sites must be updated; fix is `s.config.Auth.CookieName` with empty-string fallback |
| A6 | `middleware.AuthMiddleware` (the package-level function) is **never** used in the router | `router.go` — no `middleware.AuthMiddleware` calls | Confirmed — router exclusively uses `services.GetAuth().AuthMiddleware()` |
| A7 | `middleware.AuthConfig.CookieName` is therefore irrelevant to this epic | A6 | Confirmed — dead code path for this app; do not touch |
| A8 | `registerAuthRoutes` signature is `(rg, services, instanceSettings, logger)` — does NOT receive `RouterConfig` | `router.go:350` | Confirmed — threading cookie name requires a signature change |
| A9 | `RouterConfig` does not currently have a `CookieName` field | `router.go:27–72` | Confirmed |
| A10 | The router tests are in `package server` using `newAuthFixture` which uses mocked `AuthService` | `router_auth_test.go:1,40–58` | Confirmed — tests use `httptest.ResponseRecorder`; `rec.Result().Cookies()` gives `Set-Cookie` access |
| A11 | `TestLogin_Success` currently returns `AuthResponse{Token: "jwt-token"}` with zero `TokenTTL` | `router_auth_test.go:191–213` | Confirmed — after this change, zero-guard fires and produces `Max-Age=86400`; test must assert this |
| A12 | `AuthService` interface: `Login(ctx, LoginRequest) (*AuthResponse, error)` — no sig change needed | `interfaces/interfaces.go:38` | Confirmed |
| A13 | `GenerateToken` is on the `AuthService` interface | `interfaces/interfaces.go:28` | Confirmed — `GenerateTokenWithDuration` must NOT be added to interface |
| A14 | Neither mock (`mocks/middleware_mocks.go` nor `middleware/tests/auth_test.go:60`) needs changes | A13 | Confirmed — interface unchanged |
| A15 | Viper `AutomaticEnv` + `SetEnvKeyReplacer("."→"_")` + prefix `LLMSAFESPACE` maps `auth.rememberMeDuration` to `LLMSAFESPACE_AUTH_REMEMBEREDURATION` but does NOT reliably parse `time.Duration` | `config.go:122–124`; `LockoutDuration` pattern at `config.go:172–176` | Confirmed — correct env var name is `LLMSAFESPACE_AUTH_REMEMBEREDURATION`; correct approach is manual `os.Getenv + time.ParseDuration` block |
| A16 | `RevokeToken` uses `time.Until(expTime)` — works for any JWT duration | `auth.go:230` | Confirmed |
| A17 | `keyService` is behind `if s.keyService != nil` guards in `Login` (line 607) and `Register` (lines 487, 500) | `auth.go:487, 500, 607` | Confirmed |
| A18 | `Register` has multiple return paths after `GenerateToken`; `TokenTTL` set on success path only | `auth.go:495–514` | Confirmed — error paths return `nil, err` |
| A19 | `fakeKeyService.unlockCalls` records `TTL time.Duration` | `auth_test.go:642–668` | Confirmed |
| A20 | `deriveServerKey` signature is `func(purpose string) []byte`, matching `secrets.AdminKeyDeriver` | `secrets/credential_store.go:65`, `secrets_adapters.go:424` | Confirmed — must not change signature |
| A21 | `deriveServerKey` current minimum check is `len(masterHex) < 32` chars — accepts 16-byte keys | `secrets_adapters.go:430` | Confirmed — bug |
| A22 | Helm generates master secret via `randAlphaNum 64` — alphanumeric, not hex | `charts/secret.yaml:105` | Confirmed — `hex.DecodeString` always fails on this; raw-bytes fallback is the actual live path |
| A23 | `LLMSAFESPACE_MASTER_SECRET` already injected by Helm chart, auto-generated on first install | `api-deployment.yaml:63–67`, `secret.yaml:99–107` | Confirmed — no Helm changes needed |
| A24 | `app.New` receives `log *logger.Logger` at line 52 — structured logger is available at line 112 where DEK cache is constructed | `app.go:52, 112` | Confirmed — `log.Printf` is unnecessary and wrong; use the structured logger |
| A25 | `ensureFreeTierCredential` nil-check at `secrets_adapters.go:568` becomes dead code after US-34.2 | `app.go:170–172` | Confirmed — harmless, leave in place |
| A26 | Zero `rememberMe` / `RememberMeDuration` / `TokenTTL` code anywhere in the codebase | full grep | Confirmed — clean slate |
| A27 | `auth.New` receives `log *logger.Logger` as second parameter | `auth.go:162` | Confirmed — W4 mitigation warning can use it |
| A28 | `logger.Logger` wraps `*zap.Logger` with no exported observer/hook; tests asserting on log output require constructing `&Logger{logger: zap.New(core)}` — but this only works within `package logger` (unexported field). Cross-package tests (auth, app) need an exported constructor | `logger/logger.go:16–18`; `logger/logger_test.go:46` | Confirmed — `logger.Logger.logger` is unexported; `logger_test.go` uses same-package struct literal. Tests in `auth` and `app` packages require `logger.NewObserved() (*Logger, *observer.ObservedLogs)` exported from the logger package |
| A29 | `validateMasterSecret` placement: it must be the **very first call in `app.New`**, before `kubernetes.New` at line 55. The previous version of this epic incorrectly placed it inside the bare `{ }` secrets block at line 106. | `app.go:52–65`; design §2b | Confirmed — placement before `kubernetes.New` is the only position that makes `app.New` unit-testable without live infra. See §2b for the rationale. |
| A31 | `app.New` requires live PostgreSQL, Redis, and Kubernetes — these are constructed at lines 55–65. However, `validateMasterSecret` is placed **first** in `app.New` before any infra construction. A unit test calling `app.New` with no master secret receives the validation error before K8s/DB is attempted. | `app.go:55–65`; design §2b | Confirmed — `TestApp_New_Fails*` tests CAN call `app.New` directly because validation fires first. A test with an invalid master secret never reaches infra. A test with a valid master secret fails at K8s (confirming validation passed). |

---

## Problems Being Solved

### Problem 1 — No remember-me; `Max-Age` hardcoded and divorced from JWT TTL

Every session expires after 24 hours. `Max-Age=86400` in `setSessionCookie` (`router.go:346`) is hardcoded independently of `cfg.Auth.TokenDuration`. If an operator changes `tokenDuration`, the cookie and JWT expiry diverge silently.

### Problem 2 — DEK encryption in Redis unenforced; minimum key length wrong

`RedisDEKCache` stores the DEK as plaintext hex when `masterKey` is nil. No warning. Two compounding bugs:
- `deriveServerKey` rejects inputs shorter than 32 *chars* — accepts 16-byte keys, below AES-256 minimum of 32 bytes.
- Helm generates alphanumeric secrets (`randAlphaNum 64`); `hex.DecodeString` fails on these and the code falls back to raw bytes silently. This works but is undocumented.

### Problem 3 — `cfg.Auth.CookieName` is a dead config field

The field exists in `config.go:69` but is never read. Cookie name is hardcoded in 4 locations. The epic touches `setSessionCookie` and should fix this rather than deepening the inconsistency.

---

## Design

### Story 1 — US-34.1: Remember Me & Fix Cookie TTL

#### 1a. `pkg/types/types.go`

```go
type LoginRequest struct {
    Email      string `json:"email"      binding:"required,email"`
    Password   string `json:"password"   binding:"required"`
    RememberMe bool   `json:"rememberMe"`
}
```

Zero value of `RememberMe` is `false`. Existing clients unaffected.

```go
type AuthResponse struct {
    Token       string        `json:"token"`
    User        User          `json:"user"`
    RecoveryKey string        `json:"recoveryKey,omitempty"`
    TokenTTL    time.Duration `json:"-"` // router-internal: not serialised
}
```

`json:"-"` is the standard Go idiom for in-process transport fields. `TokenTTL` never appears in HTTP responses — clients already have `exp` in the JWT. The field exists so `Login`/`Register` can communicate the effective TTL to the router without changing the `AuthService` interface.

#### 1b. `api/internal/config/config.go` and `api/config/config.yaml`

Add to `Auth` struct:

```go
RememberMeDuration time.Duration `mapstructure:"rememberMeDuration"`
```

Add env override following the exact `LockoutDuration` pattern at `config.go:172–176`:

```go
if v := os.Getenv("LLMSAFESPACE_AUTH_REMEMBEREDURATION"); v != "" {
    if d, err := time.ParseDuration(v); err == nil && d > 0 {
        config.Auth.RememberMeDuration = d
    }
}
```

`config.yaml` additions:

```yaml
auth:
  rememberMeDuration: 720h  # 30 days
  cookieName: lsp_session   # fixes dead CookieName field; makes cookie name configurable
```

#### 1c. `api/internal/logger/logger.go` — prerequisite: export `NewObserved` constructor

Tests in `auth` and `app` packages that assert on log output need an observable logger. `logger.Logger.logger` is unexported, so `&Logger{logger: zap.New(core)}` only works within `package logger`. The architecturally correct fix is to export a test-only constructor from the logger package:

```go
// NewObserved returns a Logger backed by a zaptest observer core,
// and the ObservedLogs sink for assertions. Intended for unit tests only.
//
// Usage:
//
//	log, logs := logger.NewObserved()
//	svc, _ := auth.New(cfg, log, db, cache)
//	// ... exercise svc ...
//	require.Equal(t, 1, logs.FilterMessage("auth: rememberMeDuration is shorter...").Len())
func NewObserved() (*Logger, *observer.ObservedLogs) {
    core, logs := observer.New(zapcore.WarnLevel)
    return &Logger{logger: zap.New(core)}, logs
}
```

Import needed: `"go.uber.org/zap/zaptest/observer"` — already in the module (confirmed: `logger_test.go` uses it). This function lives in a new file `api/internal/logger/observed.go` so it is always compiled (not just in test builds) — the observer package is a test helper but is part of the main `go.uber.org/zap` module, not a separate test-only dependency. Alternatively it can live in `logger_test_helpers_test.go` but then it cannot be imported by other packages. **It must be in a non-test file to be importable.**



**Constructor — W4 mitigation:**

```go
func New(cfg *config.Config, log *logger.Logger, ...) (*Service, error) {
    if cfg.Auth.JWTSecret == "" {
        return nil, errors.New("JWT secret is required")
    }
    if cfg.Auth.RememberMeDuration > 0 && cfg.Auth.RememberMeDuration < cfg.Auth.TokenDuration {
        log.Warn("auth: rememberMeDuration is shorter than tokenDuration; " +
            "remember-me sessions will expire sooner than standard sessions",
            "rememberMeDuration", cfg.Auth.RememberMeDuration,
            "tokenDuration", cfg.Auth.TokenDuration)
    }
    // ... rest unchanged ...
}
```

**`GenerateTokenWithDuration` — new private method, NOT on the interface:**

```go
func (s *Service) GenerateToken(userID string) (string, error) {
    return s.GenerateTokenWithDuration(userID, s.tokenDuration)
}

func (s *Service) GenerateTokenWithDuration(userID string, duration time.Duration) (string, error) {
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
        "sub": userID,
        "jti": uuid.New().String(),
        "exp": time.Now().Add(duration).Unix(),
        "iat": time.Now().Unix(),
    })
    tokenString, err := token.SignedString(s.jwtSecret)
    if err != nil {
        return "", fmt.Errorf("failed to sign token: %w", err)
    }
    return tokenString, nil
}
```

**`Login` update:** replace `s.GenerateToken` with duration-aware version; thread `TokenTTL`:

```go
tokenDur := s.tokenDuration
if req.RememberMe && s.config.Auth.RememberMeDuration > 0 {
    tokenDur = s.config.Auth.RememberMeDuration
}

token, err := s.GenerateTokenWithDuration(user.ID, tokenDur)
if err != nil {
    return nil, errors.New("login failed")
}

if s.keyService != nil {
    jti := utilities.ExtractJTI(token)
    if jti != "" {
        hasKeys, _ := s.keyService.HasKeys(ctx, user.ID)
        if !hasKeys {
            if _, err := s.keyService.InitializeUserKeys(ctx, user.ID, []byte(req.Password)); err != nil {
                s.logger.Warn("Login: failed to auto-init keys", "user_id", user.ID, "error", err.Error())
            }
        }
        if err := s.keyService.UnlockDEK(ctx, user.ID, []byte(req.Password), jti, tokenDur); err != nil {
            s.logger.Warn("Login: failed to unlock DEK", "user_id", user.ID, "error", err.Error())
        }
    }
}

user.PasswordHash = ""
return &types.AuthResponse{Token: token, User: *user, TokenTTL: tokenDur}, nil
```

**`Register` update:** set `TokenTTL` on the single success path only (the two error paths return `nil, err`):

```go
// token, err := s.GenerateToken(userID) -- unchanged, uses s.tokenDuration
// ... existing empty-jti guard unchanged ...
// ... existing UnlockDEK call unchanged (uses s.tokenDuration) ...
user.PasswordHash = ""
return &types.AuthResponse{Token: token, User: *user, RecoveryKey: recoveryKey, TokenTTL: s.tokenDuration}, nil
```

**`extractToken` (line 712) — fix hardcoded cookie name:**

```go
func (s *Service) extractToken(c *gin.Context) string {
    name := s.config.Auth.CookieName
    if name == "" {
        name = "lsp_session"
    }
    return utilities.ExtractToken(c, utilities.TokenExtractorConfig{
        HeaderName: "Authorization",
        TokenType:  "Bearer",
        CookieName: name,
    })
}
```

Note: `extractToken` is currently a package-level function. Since it needs `s.config`, it must become a method on `*Service`. Both `AuthMiddleware` (line 725) and `OptionalAuthMiddleware` (line 765) call `extractToken(c)` — both must be updated to `s.extractToken(c)`.

#### 1d. `api/internal/server/router.go`

**Add `CookieName` to `RouterConfig`:**

```go
type RouterConfig struct {
    // ... existing fields ...
    // CookieName is the session cookie name. Defaults to "lsp_session".
    CookieName string
}
```

**Pass `cfg` to `registerAuthRoutes`** — change its signature:

```go
// Before:
func registerAuthRoutes(rg *gin.RouterGroup, services interfaces.Services, instanceSettings *settings.InstanceService, logger *apilogger.Logger)

// After:
func registerAuthRoutes(rg *gin.RouterGroup, services interfaces.Services, instanceSettings *settings.InstanceService, logger *apilogger.Logger, cookieName string)
```

Call site at `router.go:131`:

```go
registerAuthRoutes(authGroup, services, cfg.InstanceSettings, logger, cfg.cookieName())
```

Where `cookieName()` is a helper method on `RouterConfig`:

```go
func (c RouterConfig) cookieName() string {
    if c.CookieName == "" {
        return "lsp_session"
    }
    return c.CookieName
}
```

**`setSessionCookie` — takes explicit `maxAge` and `cookieName`:**

```go
func setSessionCookie(c *gin.Context, token string, maxAge int, cookieName string) {
    c.SetCookie(cookieName, token, maxAge, "/", "", true, true)
}
```

**Login handler** inside `registerAuthRoutes`:

```go
resp, err := authSvc.Login(c.Request.Context(), req)
if err != nil {
    c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
    return
}
maxAge := int(resp.TokenTTL.Seconds())
if maxAge <= 0 {
    maxAge = 86400 // safe fallback: zero TokenTTL means mocked/stub service
}
setSessionCookie(c, resp.Token, maxAge, cookieName)
c.JSON(http.StatusOK, resp)
```

**Register handler** — identical `maxAge` pattern.

**Logout handler** — replace hardcoded `"lsp_session"` in two places:

```go
// Token extractor for revocation:
token := utilities.ExtractToken(c, utilities.TokenExtractorConfig{
    HeaderName: "Authorization",
    TokenType:  "Bearer",
    CookieName: cookieName,
})
// Cookie clear:
c.SetCookie(cookieName, "", -1, "/", "", true, true)
```

#### 1e. `api/internal/app/app.go`

Thread `CookieName` when constructing the router:

```go
router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
    // ... existing fields ...
    CookieName: cfg.Auth.CookieName,
})
```

**Known gap — `SameSite` attribute:** Gin's `c.SetCookie` does not expose `SameSite`. Browser default is `Lax` — correct for same-origin. `Strict` requires `http.SetCookie` directly. Tracked as future hardening, not blocking.

---

### Story 2 — US-34.2: Enforce and Fix Master Key Handling

#### 2a. Fix `deriveServerKey` (`api/internal/app/secrets_adapters.go`)

The function must not change its signature (it is passed by reference as `secrets.AdminKeyDeriver`). It must not log (it is a pure derivation function — side effects in a function-typed value are unexpected and untestable). The fixes are purely to the length gate and documentation:

```go
// deriveServerKey derives a 32-byte purpose-scoped key from LLMSAFESPACE_MASTER_SECRET.
//
// Accepted input formats (auto-detected):
//   - Valid lowercase hex (even length, [0-9a-f]): decoded to raw bytes.
//     Minimum 64 hex chars = 32 decoded bytes.
//   - Any other string (alphanumeric from Helm randAlphaNum, base64, etc.):
//     used as raw bytes directly. Minimum 32 bytes.
//
// Returns nil if the env var is absent/empty OR if the decoded/raw key is
// shorter than 32 bytes (AES-256-GCM minimum). Callers must treat nil as
// "master secret not configured or invalid."
//
// Logging: this function is intentionally side-effect-free. Callers that
// need diagnostics (e.g. validateMasterSecret in app.go) must inspect
// the raw env var independently.
func deriveServerKey(purpose string) []byte {
    masterRaw := os.Getenv("LLMSAFESPACE_MASTER_SECRET")
    if masterRaw == "" {
        masterRaw = os.Getenv("LLMSAFESPACE_DEK_MASTER_KEY") // legacy alias
    }
    if masterRaw == "" {
        return nil
    }

    var master []byte
    if decoded, err := hex.DecodeString(masterRaw); err == nil {
        master = decoded // valid hex path
    } else {
        master = []byte(masterRaw) // raw bytes path (Helm alphanumeric, base64, etc.)
    }

    if len(master) < 32 { // AES-256-GCM requires exactly 32 bytes
        return nil
    }

    key, err := secrets.DeriveKEK(master, []byte("llmsafespace-server"), purpose)
    if err != nil {
        return nil
    }
    return key
}
```

#### 2b. Add `validateMasterSecret` in `app.go` — placement and wiring

`validateMasterSecret` must be the **very first operation in `app.New`**, before `kubernetes.New`, before `services.New`, before any infrastructure construction. It is a pure env-var check with no dependencies:

```go
// validateMasterSecret verifies LLMSAFESPACE_MASTER_SECRET is present and
// decodes to at least 32 bytes (the AES-256-GCM key size minimum).
// Returns nil on success. Logs a structured Warn when the secret is present
// but too short so operators can distinguish "forgot to set it" from "set it wrong."
// masterRaw is intentionally NOT included in any log output.
func validateMasterSecret(log *logger.Logger) error {
    masterRaw := os.Getenv("LLMSAFESPACE_MASTER_SECRET")
    if masterRaw == "" {
        masterRaw = os.Getenv("LLMSAFESPACE_DEK_MASTER_KEY")
    }
    if masterRaw == "" {
        return errors.New(
            "LLMSAFESPACE_MASTER_SECRET is required but not set; " +
                "refusing to start without DEK encryption at rest. " +
                "Generate with: openssl rand -hex 32")
    }
    var master []byte
    if decoded, err := hex.DecodeString(masterRaw); err == nil {
        master = decoded
    } else {
        master = []byte(masterRaw)
    }
    if len(master) < 32 {
        log.Warn("LLMSAFESPACE_MASTER_SECRET is set but too short for AES-256-GCM",
            "decoded_bytes", len(master), "required_bytes", 32)
        return fmt.Errorf(
            "LLMSAFESPACE_MASTER_SECRET decodes to %d bytes; minimum is 32", len(master))
    }
    return nil
}
```

Placement at the top of `app.New`:

```go
func New(cfg *config.Config, log *logger.Logger) (*App, error) {
    ctx, cancel := context.WithCancel(context.Background())

    // Validate master secret before touching any infrastructure.
    // Intentionally first: cheap env-var check; gives a clear error rather
    // than a misleading K8s/DB connection error.
    if err := validateMasterSecret(log); err != nil {
        cancel()
        return nil, err
    }

    k8sClient, err := kubernetes.New(&cfg.Kubernetes, log)
    // ... rest unchanged ...
}
```

**Why first, not inside the secrets block:** This placement enables unit-testing the `app.New` enforcement directly. `validateMasterSecret` fires before `kubernetes.New` — a unit test can call `app.New` with no master secret, receive the validation error, and confirm the wiring is correct — all without a live K8s cluster or database. `kubernetes.New` always fails in a unit test environment, but `validateMasterSecret` returns first, so the test receives the correct error and no infrastructure is touched.

Inside the secrets block, keep a defensive nil-guard on `mk`:

```go
{
    mk := dekMasterKey()
    if mk == nil {
        // Unreachable after validateMasterSecret passed — env var is
        // immutable for the process lifetime. Guards against future
        // refactors that move validateMasterSecret.
        cancel()
        return nil, errors.New("internal: dekMasterKey returned nil after validateMasterSecret passed")
    }
    dekCacheClient = redis.NewClient(...)
    dekCache := secrets.NewRedisDEKCache(dekCacheClient, mk)
    // ... rest unchanged ...
}
```

**Why `validateMasterSecret` and `deriveServerKey` both parse the env var (TOCTOU):**
`validateMasterSecret` reads and validates the raw value; `deriveServerKey` derives the key from it. The two calls are sequential in `app.New`. In production the env var is static for the pod lifetime. In tests, `t.Setenv` is goroutine-local and safe. The double-parse is the correct trade-off to keep `deriveServerKey` a pure, side-effect-free function compatible with `AdminKeyDeriver`.

---

## Weak Points and Failure Scenarios — Mitigations

### W1 — `TokenTTL = 0` from mocked services → `Max-Age=0` deletes cookie

**Scenario:** Any test or code path that constructs `AuthResponse{Token: "..."}` without setting `TokenTTL`. `int(0) == 0` → router sets `Max-Age=0` → browser deletes cookie.

**Mitigation (in design):** Zero-guard in both login and register handlers: `if maxAge <= 0 { maxAge = 86400 }`. The fallback value matches the default `tokenDuration` — correct in all cases where `TokenTTL` was not populated.

**Test implication:** `TestLogin_Success` in `router_auth_test.go` currently returns a mock response without `TokenTTL`. After this change, it will exercise the zero-guard path. The test should be updated to assert `Max-Age=86400` (the zero-guard output), making the contract explicit rather than incidentally tested.

### W2 — Cookie assertions require the real router or real `auth.Service`

**Scenario:** `setupRealAuthRouter` in `auth_e2e_all_test.go` does not call `setSessionCookie`. Cookie `Max-Age` assertions against it always miss.

**Mitigation (in design):** Cookie tests go in `router_auth_test.go` (package `server`), using `newAuthFixture` with the real `NewRouter`. The mock's `Login` return value sets `TokenTTL` explicitly to the expected duration. `rec.Result().Cookies()` provides direct `Set-Cookie` inspection. No new test file needed — extend the existing router test file.

### W3 — `validateMasterSecret` and `deriveServerKey` both parse the env var

**Scenario:** Concurrent test setting/unsetting the env var between the two calls.

**Mitigation:** Tests that need a master secret use `t.Setenv` which is goroutine-safe and auto-restored. In production this is not a real risk. The defensive `if mk == nil` check after `dekMasterKey()` handles the TOCTOU case.

### W4 — `RememberMeDuration` misconfigured shorter than `TokenDuration`

**Mitigation (in design):** `auth.New` logs a structured Warn with both values. Not a startup failure — may be intentional during incident response. Fires once at startup using the already-available structured logger.

### W5 — `extractToken` becoming a method on `*Service`

**Scenario:** `extractToken` is currently a package-level function. Making it a method changes two call sites: `AuthMiddleware` (`extractToken(c)` → `s.extractToken(c)` at line 725) and `OptionalAuthMiddleware` (`extractToken(c)` → `s.extractToken(c)` at line 765). Mechanical change with no semantic risk; the function body is unchanged.

**Impact on tests:** Tests for both middleware functions test behaviour through the full call chain — unaffected by method vs. function distinction.

### W6 — `registerAuthRoutes` signature change

**Scenario:** Adding `cookieName string` as a parameter. There is exactly one call site (`router.go:131`) and one definition. No other callers exist (confirmed by grep).

**Mitigation:** Mechanical change. The router tests in `package server` call `NewRouter` (not `registerAuthRoutes` directly) so they are unaffected.

### W7 — Clock skew; Redis revocation memory; TTL precision

None of these are risks at current scale. See previous analysis. No action needed.

---

## Acceptance Criteria

### US-34.1

- `POST /auth/login` with `{"rememberMe": true}` → JWT `exp ≈ now + 30d`, `Set-Cookie: <cookieName>=...; Max-Age=2592000; Path=/; HttpOnly; Secure`
- `POST /auth/login` with `{"rememberMe": false}` or absent → `exp ≈ now + 24h`, `Max-Age=86400`
- `POST /auth/register` → cookie `Max-Age = int(cfg.Auth.TokenDuration.Seconds())`; not hardcoded
- `keyService.UnlockDEK` called with `rememberMeDuration` when `RememberMe: true`; `tokenDuration` otherwise
- `AuthResponse` JSON body never contains `"tokenTTL"` key
- `rememberMeDuration: 0` in config → `RememberMe: true` falls back to `tokenDuration`; no zero-TTL JWT ever issued
- Revocation works for 30-day tokens: logout → subsequent request returns 401
- Cookie name is read from `cfg.Auth.CookieName`, defaulting to `"lsp_session"` when empty; all 4 hardcoded occurrences replaced
- `auth.New` logs a structured Warn when `RememberMeDuration > 0 && RememberMeDuration < TokenDuration`
- `TokenTTL` field does not appear in any JSON response body (verified by `json.Unmarshal` round-trip test)

### US-34.2

- `validateMasterSecret(log)` returns an error containing `"LLMSAFESPACE_MASTER_SECRET"` when the env var is absent
- `validateMasterSecret(log)` emits a structured Warn with `decoded_bytes` field and returns an error when the secret decodes to < 32 bytes
- `validateMasterSecret(log)` returns nil for a 64-char alphanumeric secret (Helm format)
- `validateMasterSecret(log)` returns nil for a 64-char hex secret
- No log output from `validateMasterSecret` contains any portion of the secret value
- `app.New` returns an error containing `"LLMSAFESPACE_MASTER_SECRET"` when the env var is absent — before K8s or DB is attempted
- `app.New` with a valid master secret proceeds past validation and fails at the infra layer (K8s/DB) — not at the master key check
- `deriveServerKey` returns nil for any input decoding to < 32 bytes; returns a 32-byte key for valid hex and alphanumeric inputs
- `RedisDEKCache.CacheDEK` with a master key stores a ciphertext value longer than 64 hex chars (verified by existing `TestRedisDEKCache_MasterKey_ValueEncryptedInRedis` in `pkg/secrets/redis_cache_test.go` — no new test needed)

---

## Tests (TDD — written before implementation)

### US-34.1 Unit Tests (`api/internal/services/auth/auth_test.go`)

Log-observation tests use `logger.NewObserved()` — the exported constructor added in step 1 of the implementation order. All other tests use the existing `newTestService` helper.

| Test | Verifies |
|---|---|
| `TestLogin_RememberMe_True_Generates30dJWT` | JWT `exp ≈ now + rememberMeDuration`; tolerance ±2s |
| `TestLogin_RememberMe_False_Generates24hJWT` | JWT `exp ≈ now + tokenDuration`; tolerance ±2s |
| `TestLogin_RememberMe_Absent_DefaultsFalse` | Zero-value `LoginRequest` → same TTL as explicit `RememberMe: false` |
| `TestLogin_RememberMe_DEKTTLIs30d` | `fakeKeyService.unlockCalls[0].TTL == rememberMeDuration` when `RememberMe: true` |
| `TestLogin_NoRememberMe_DEKTTLIsStandard` | `fakeKeyService.unlockCalls[0].TTL == tokenDuration` when `RememberMe: false` |
| `TestLogin_RememberMeDurationZero_FallsBackToTokenDuration` | `cfg.Auth.RememberMeDuration = 0`, `RememberMe: true` → `resp.TokenTTL == tokenDuration` |
| `TestLogin_TokenTTLPopulated` | `resp.TokenTTL == tokenDuration` when `RememberMe: false` |
| `TestLogin_TokenTTLPopulated_RememberMe` | `resp.TokenTTL == rememberMeDuration` when `RememberMe: true` |
| `TestRegister_TokenTTLPopulated` | `resp.TokenTTL == tokenDuration` after register |
| `TestGenerateTokenWithDuration_CorrectExpiry` | JWT `exp` matches given duration ±2s |
| `TestGenerateToken_DelegatesWithTokenDuration` | `GenerateToken(id)` and `GenerateTokenWithDuration(id, tokenDuration)` produce same-TTL JWTs |
| `TestNew_RememberMeShorterThanToken_LogsWarning` | `log, logs := logger.NewObserved(); New(cfg, log, ...)` with `RememberMeDuration(1m) < TokenDuration(24h)` → `logs.FilterMessage("auth: rememberMeDuration...").Len() == 1`; log contains both duration values |
| `TestNew_RememberMeZero_NoWarning` | `RememberMeDuration = 0` → `logs.Len() == 0` |
| `TestNew_RememberMeLongerThanToken_NoWarning` | Normal config → `logs.Len() == 0` |

### US-34.1 Router Tests (extend `api/internal/server/router_frontend_auth_test.go` and `router_auth_test.go`)

**Existing tests that must be updated (A30):** `router_frontend_auth_test.go` already has tests asserting `c.Name == "lsp_session"` and `MaxAge == 86400`. These will continue to pass but must be made explicit:

- `TestLogin_SetsCookie` (line 50): mock currently returns `TokenTTL=0`; after the change the zero-guard produces `MaxAge=86400`. Update mock to return `TokenTTL=24*time.Hour` and assert `MaxAge=86400` intentionally, not as a side-effect of the fallback. Add a comment: `// TokenTTL=24h → MaxAge=86400; zero-guard not exercised here`.
- `TestRegister_SetsCookie` (line 104): same — add explicit `TokenTTL=24*time.Hour` to mock response.

**New tests** (in `router_frontend_auth_test.go` to stay consistent with existing cookie test location):

Mock `Login`/`Register` must set `TokenTTL` explicitly. `rec.Result().Cookies()` provides `Set-Cookie` access.

| Test | Verifies |
|---|---|
| **Update** `TestLogin_SetsCookie` | Mock returns `TokenTTL=24*time.Hour` → `MaxAge=86400` (explicit, not fallback) |
| **Update** `TestRegister_SetsCookie` | Mock returns `TokenTTL=24*time.Hour` → cookie set correctly |
| `TestLogin_RememberMe_CookieMaxAge30Days` | Mock returns `TokenTTL=720*time.Hour` → `MaxAge=2592000` |
| `TestLogin_ZeroTokenTTL_FallbackMaxAge` | Mock returns `TokenTTL=0` → zero-guard fires → `MaxAge=86400` (documents the fallback explicitly) |
| `TestLogin_TokenTTLNotInResponseBody` | JSON body has no `"tokenTTL"` key |
| `TestCookieName_FromRouterConfig` | `RouterConfig{CookieName: "my_session"}` → `Set-Cookie: my_session=...` on login and register |
| `TestCookieName_DefaultsToLspSession` | `RouterConfig{}` → `Set-Cookie: lsp_session=...` (default) |
| `TestLogout_ClearsCorrectCookie` | `RouterConfig{CookieName: "my_session"}` → logout sets `my_session=; Max-Age=-1` |

### US-34.1 Config Tests (extend `api/internal/config/config_test.go`)

| Test | Verifies |
|---|---|
| `TestConfig_RememberMeDuration_DefaultFromYAML` | `720h` parsed into `cfg.Auth.RememberMeDuration` |
| `TestConfig_RememberMeDuration_EnvOverride` | `LLMSAFESPACE_AUTH_REMEMBEREDURATION=168h` overrides YAML |
| `TestConfig_RememberMeDuration_InvalidEnvIgnored` | Non-duration string leaves YAML default intact |
| `TestConfig_RememberMeDuration_ZeroEnvIgnored` | `=0` is ignored by `d > 0` guard |
| `TestConfig_CookieName_DefaultFromYAML` | `cfg.Auth.CookieName == "lsp_session"` from YAML |

### US-34.2 Unit Tests (extend `api/internal/app/secrets_adapters_test.go`)

| Test | Verifies |
|---|---|
| `TestDeriveServerKey_AbsentEnv_ReturnsNil` | Unset → nil |
| `TestDeriveServerKey_EmptyEnv_ReturnsNil` | Empty string → nil |
| `TestDeriveServerKey_ShortRawBytes_ReturnsNil` | 31-char non-hex → nil |
| `TestDeriveServerKey_Exactly32RawBytes_Returns32ByteKey` | 32-char non-hex → non-nil |
| `TestDeriveServerKey_AlphanumericHelmFormat_Returns32ByteKey` | 64-char alphanumeric → non-nil |
| `TestDeriveServerKey_ValidHex64Chars_Returns32ByteKey` | 64-char hex → non-nil |
| `TestDeriveServerKey_ShortHex_ReturnsNil` | 60-char hex (30 decoded bytes) → nil |
| `TestDeriveServerKey_InvalidHexLongEnough_FallsBackToRawBytes` | Non-hex 32+ char → non-nil |
| `TestDeriveServerKey_LegacyEnvVar_Accepted` | `LLMSAFESPACE_DEK_MASTER_KEY` works when primary absent |
| `TestDeriveServerKey_PrimaryEnvTakesPrecedence` | Both env vars set → primary wins |
| `TestDeriveServerKey_NoSideEffects` | Two calls with same input return equal keys; no state mutation |

### US-34.2 Unit Tests — `validateMasterSecret` and `app.New` wiring (new file `api/internal/app/app_master_key_test.go`, package `app`)

`validateMasterSecret` is a package-level function in `package app` — directly callable. Because `validateMasterSecret` is now the **first** check in `app.New` (before `kubernetes.New`), `app.New` itself is also testable for the wiring: a test with no master secret receives the validation error before any infra is attempted. No live K8s or database is needed. All tests use `t.Setenv`. Log-observation tests use `logger.NewObserved()`.

**Test config for `app.New` wiring tests:**

```go
// minimalCfg returns a *config.Config that makes kubernetes.New fail
// deterministically without touching any real infrastructure. Used by
// TestApp_New_WithValidMasterSecret_FailsAtInfra to confirm validateMasterSecret
// passes and that subsequent infra construction is attempted.
func minimalCfg() *config.Config {
    cfg := &config.Config{}
    cfg.Kubernetes.InCluster  = false
    cfg.Kubernetes.ConfigPath = "/nonexistent/kubeconfig-for-test"
    // Auth.JWTSecret is irrelevant for master-key tests — validateMasterSecret
    // fires before services.New (which calls auth.New which checks JWTSecret).
    return cfg
}
```

Confirmed: `kubernetes.New` with `InCluster=false` and a nonexistent `ConfigPath` returns `"stat /nonexistent/kubeconfig-for-test: no such file or directory"` — does not require a cluster and is deterministic on all environments.

| Test | What it calls | Verifies |
|---|---|---|
| `TestValidateMasterSecret_AbsentEnv_ReturnsError` | `validateMasterSecret(log)` | Error contains `"LLMSAFESPACE_MASTER_SECRET"`; no Warn emitted (`logs.Len()==0`) |
| `TestValidateMasterSecret_TooShort_LogsWarnAndReturnsError` | `validateMasterSecret(log)` | 10-char secret → Warn with `decoded_bytes=10`; error returned |
| `TestValidateMasterSecret_TooShort_DoesNotLogSecret` | `validateMasterSecret(log)` | No log context field contains any substring of the secret value |
| `TestValidateMasterSecret_AlphanumericHelmFormat_Succeeds` | `validateMasterSecret(log)` | 64-char alphanumeric → nil error |
| `TestValidateMasterSecret_HexFormat_Succeeds` | `validateMasterSecret(log)` | 64-char lowercase hex → nil error |
| `TestValidateMasterSecret_LegacyEnvVar_Accepted` | `validateMasterSecret(log)` | `LLMSAFESPACE_DEK_MASTER_KEY` with 32+ bytes → nil error when primary absent |
| `TestApp_New_FailsWithoutMasterSecret` | `app.New(minimalCfg(), log)` | No master secret → error contains `"LLMSAFESPACE_MASTER_SECRET"`; error does NOT contain `"kubernetes"` or `"kubeconfig"` (confirms infra not reached) |
| `TestApp_New_FailsWithTooShortMasterSecret` | `app.New(minimalCfg(), log)` | Short secret → validation error returned; error does NOT contain `"kubernetes"` |
| `TestApp_New_WithValidMasterSecret_FailsAtInfra` | `app.New(minimalCfg(), log)` | Valid 64-char secret set via `t.Setenv` → error contains `"kubernetes"` or `"kubeconfig"` (confirms validation passed and infra was attempted) |

### US-34.2 Unit Tests — `deriveServerKey` (new file `api/internal/app/secrets_adapters_test.go`)

`deriveServerKey` is a package-level function in `package app` — directly testable. All tests use `t.Setenv`.

| Test | Verifies |
|---|---|
| `TestDeriveServerKey_AbsentEnv_ReturnsNil` | Unset → nil |
| `TestDeriveServerKey_EmptyEnv_ReturnsNil` | Empty string → nil |
| `TestDeriveServerKey_ShortRawBytes_ReturnsNil` | 31-char non-hex → nil |
| `TestDeriveServerKey_Exactly32RawBytes_Returns32ByteKey` | 32-char non-hex → 32-byte key |
| `TestDeriveServerKey_AlphanumericHelmFormat_Returns32ByteKey` | 64-char alphanumeric → 32-byte key |
| `TestDeriveServerKey_ValidHex64Chars_Returns32ByteKey` | 64-char lowercase hex → 32-byte key |
| `TestDeriveServerKey_ShortHex_ReturnsNil` | 60-char hex (30 decoded bytes) → nil |
| `TestDeriveServerKey_InvalidHexLongEnough_FallsBackToRawBytes` | Non-hex 32+ char → non-nil key |
| `TestDeriveServerKey_LegacyEnvVar_Accepted` | `LLMSAFESPACE_DEK_MASTER_KEY` works when primary absent |
| `TestDeriveServerKey_PrimaryEnvTakesPrecedence` | Both vars set → primary wins |
| `TestDeriveServerKey_NoSideEffects` | Two calls with same input return equal keys |

### US-34.2 Wiring Test — DEK cache encryption

**No new tests needed here.** `pkg/secrets/redis_cache_test.go` already contains comprehensive DEK encryption tests using `miniredis`:

- `TestRedisDEKCache_MasterKey_ValueEncryptedInRedis` — verifies the raw Redis value is longer than 64 chars (plain DEK hex) when a master key is present; confirms encryption is applied
- `TestRedisDEKCache_NoMasterKey_BackwardsCompatible` — verifies the raw Redis value is exactly 64 hex chars without a master key
- `TestRedisDEKCache_MasterKey_CacheAndGet` — round-trip with master key
- `TestRedisDEKCache_MasterKey_WrongKeyCannotDecrypt` — wrong key fails decryption

These tests already run in CI. Adding duplicate tests in `api/internal/app/secrets_wiring_test.go` would violate DRY and create maintenance burden. The acceptance criterion "after enforcement, `CacheDEK` stores a 120-hex-char value" is already verified by the existing `TestRedisDEKCache_MasterKey_ValueEncryptedInRedis` (which asserts `len(rawVal) > 64`).

**Correction to the "120 hex chars" claim:** The AES-256-GCM output for a 32-byte plaintext is `nonce(12) + ciphertext(32) + tag(16) = 60 bytes = 120 hex chars`. This is correct for a 32-byte DEK. The existing test asserts `len(rawVal) > 64` which is true (120 > 64) but not tight. This is acceptable — the exact value depends on `gcm.NonceSize()` which is implementation-defined (standard GCM = 12 bytes). Asserting `> 64` is the correct long-term assertion; asserting `== 120` would be brittle if the implementation ever changes nonce size.

---

## Non-Requirements (Explicitly Out of Scope)

| Item | Rationale |
|---|---|
| `SameSite=Strict` on session cookie | Requires `http.SetCookie` directly; `Lax` is correct for same-origin; future hardening |
| Helm chart changes | Chart already auto-generates and injects `LLMSAFESPACE_MASTER_SECRET` |
| Remember-me at registration | Always standard-duration session |
| Per-user configurable remember-me duration | One server-wide value |
| Sliding session expiry | TTL fixed at issuance |
| API key DEK access | US-10.13 |
| Split-key DEK scheme | Master key AES-256-GCM closes the Redis-breach threat without overhead |
| `middleware.AuthConfig.CookieName` | `middleware.AuthMiddleware` is never used in the router; this field is dead for this application |
| Removing `ensureFreeTierCredential` dead-code | Harmless; separate cleanup |

---

## Implementation Order

```
1. Write all tests — must fail before implementation (README-LLM.md §0)
   This includes updating existing tests:
   - router_frontend_auth_test.go: update TestLogin_SetsCookie and TestRegister_SetsCookie
     to set explicit TokenTTL on mock responses (A30)

2. api/internal/logger/observed.go  (new file)
       Export NewObserved() (*Logger, *observer.ObservedLogs)
       Required before any log-assertion test can compile
       Import: "go.uber.org/zap/zaptest/observer" (already in module, used by logger_test.go)

3. api/internal/config/config.go + api/config/config.yaml
       Add Auth.RememberMeDuration
       Add LLMSAFESPACE_AUTH_REMEMBEREDURATION env override (LockoutDuration pattern)
       Add rememberMeDuration: 720h and cookieName: lsp_session to config.yaml

4. pkg/types/types.go
       LoginRequest.RememberMe bool
       AuthResponse.TokenTTL time.Duration `json:"-"`

5. api/internal/app/secrets_adapters.go
       Fix deriveServerKey: enforce len(decoded/raw) < 32; document both format paths;
       no signature change; no logging (pure function)

6. api/internal/app/app.go
       Add validateMasterSecret(log *logger.Logger) error
       Place call as the VERY FIRST operation in app.New, before kubernetes.New —
       this enables unit-testing the wiring via app.New itself (validation fires
       before any infra is touched); defensive nil-check on mk in secrets block;
       cancel+return on any failure

7. api/internal/services/auth/auth.go
       auth.New: add RememberMeDuration < TokenDuration Warn using structured logger
       GenerateTokenWithDuration (not on interface; GenerateToken delegates to it)
       Login: tokenDur selection; GenerateTokenWithDuration; UnlockDEK(tokenDur); TokenTTL
       Register: set TokenTTL = s.tokenDuration on success path only
       extractToken: make method on *Service; use s.config.Auth.CookieName with fallback
       AuthMiddleware (line 725): extractToken(c) → s.extractToken(c)
       OptionalAuthMiddleware (line 765): extractToken(c) → s.extractToken(c)

8. api/internal/server/router.go
       Add CookieName to RouterConfig
       Add cookieName() helper method on RouterConfig (empty-string fallback to "lsp_session")
       Add cookieName string parameter to registerAuthRoutes; update call site at line 131
       setSessionCookie(c, token string, maxAge int, cookieName string)
       Login handler: maxAge from resp.TokenTTL.Seconds() with zero-guard
       Register handler: same pattern
       Logout handler: replace 2 hardcoded "lsp_session" with cookieName param

9. api/internal/app/app.go
       Pass cfg.Auth.CookieName into server.RouterConfig{CookieName: ...}

10. Run: cd api && go test ./... -timeout 120s -race — fix all failures

11. Adversarial self-review (README-LLM.md §11)
```
