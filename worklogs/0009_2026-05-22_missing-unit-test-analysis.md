# Worklog 0009 — 2026-05-22 — Missing unit test analysis

## Session goals

Identify patterns of missing unit tests across the codebase by reading every
production Go file and comparing against existing test files.

## Methodology

1. Listed all non-test Go files with exported/unexported functions
2. Listed all test files
3. Cross-referenced by package — not by filename prefix heuristics
4. Verified which validation package is actually called (import grep) before
   recommending tests for it

## Findings

### Pattern 1: Pure functions with no tests — highest priority

These functions have no external dependencies. They are deterministic, trivially
testable, and used on every request path. None have a test file.

| Package | Functions | Risk |
|---|---|---|
| `api/internal/errors/errors.go` | `NewValidationError`, `NewNotFoundError`, `NewForbiddenError`, `NewInternalError`, `NewAuthenticationError`, `NewConflictError`, `NewRateLimitError`, `NewBadRequestError`, `NewNotImplementedError`, `StatusCode()`, `Error()`, `Unwrap()`, `IsSandboxNotFoundError` | High — used by every service method |
| `api/internal/services/sandbox/validation/validators.go` | `ValidateCreateSandboxRequest`, `validateResources`, `validateNetworkAccess`, `isValidSecurityLevel` | High — called on every sandbox creation, security-critical |
| `pkg/utilities/hashing.go` | `HashString` | Medium |
| `pkg/utilities/masking.go` | `MaskSensitiveFieldsWithList`, `MaskSensitiveFields`, `MaskString` | Medium — used in log sanitisation |
| `pkg/http/writer.go` | `BodyCaptureWriter.Write`, `WriteString`, `GetBody`, `NewBodyCaptureWriter` | Medium — used by logging middleware |

### Pattern 2: Database service — CreateUser and DeleteUser untested

`database_test.go` covers 12 of 15 methods. Two user-management methods are
missing:

| Missing | Notes |
|---|---|
| `CreateUser` | SQL correctness, constraint handling |
| `DeleteUser` | Simple but untested |

`Start` and `Stop` are also absent — acceptable since they are no-op/close
lifecycle methods tested indirectly through `TestNew`.

### Pattern 3: Cache service — individual error paths untested

Existing cache tests exercise methods as grouped scenarios (`GetSetDelete`,
`SessionOperations`). Specific missing coverage:

- `Get` with missing key — returns `""` not error? Behaviour unspecified.
- `GetObject` with corrupt JSON stored in Redis
- `Set` with zero expiration (never expires vs immediate expiry)
- `SetSession` / `GetSession` with TTL expiry (miniredis supports `FastForward`)

### Pattern 4: Controller — zero test coverage

No test files exist anywhere under `controller/`. Two categories:

**Pure functions (trivially testable):**
- `controller/internal/common/condition_adapter.go` — `ConvertToMetaV1Condition`, `ConvertFromMetaV1Condition`, `SetSandboxCondition`
- `controller/internal/common/utils.go` — `SetCondition`, `FindCondition`, `IsConditionTrue`, `AddFinalizer`, `RemoveFinalizer`, `IsPodReady`, `GenerateRandomString`

**Reconciler (requires controller-runtime fake client):**
- `controller/internal/sandbox/controller.go` — `Reconcile`, `handlePendingSandbox`, `handleCreatingSandbox`, `handleRunningSandbox`, `handleTerminatingSandbox`, `handleDeletion`

### Pattern 5: `api/internal/validation/` — dead package, should be deleted

`api/internal/validation/sandbox.go` and `api/internal/validation/validation.go`
contain 15 functions. Zero callers in production code (verified by import grep).
The sandbox service uses `api/internal/services/sandbox/validation/validators.go`
instead. Writing tests for dead code provides no value — the package should be
deleted.

## Priority order for next session

| Priority | Action | Justification |
|---|---|---|
| 1 | Delete `api/internal/validation/` | Dead code — 0 callers, misleading |
| 2 | TDD `api/internal/errors/` | Pure functions used everywhere; errors affect all API responses |
| 3 | TDD `api/internal/services/sandbox/validation/` | Called on every sandbox creation; security-critical input validation |
| 4 | TDD `pkg/utilities/` | Pure functions; masking used in production log paths |
| 5 | Add `CreateUser`/`DeleteUser` tests to `database_test.go` | Completes database service coverage |
| 6 | TDD `controller/internal/common/` pure functions | No runtime dependencies; condition/finalizer logic is critical for controller correctness |
| 7 | TDD `controller/internal/sandbox/` reconciler | Requires fake client setup; highest complexity but highest controller risk |

## State at end of session

- 166 tests, 0 failures, 13/13 packages
- No new code written this session — analysis only
- `api/internal/validation/` identified as dead code to delete next session

## Next steps (from full backlog)

- Address missing test patterns above (priority order above)
- US-1.5: Build `redact` binary
- US-1.7: Entrypoint scripts for runtime containers
- US-1.8: Rewrite base Dockerfile
- Epic 2: Workspace CRD
