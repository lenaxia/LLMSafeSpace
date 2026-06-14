# Worklog: US-38.11: Fix Kubernetes Client Issues

**Date:** 2026-06-13
**Session:** Address review feedback on PR #147 (US-38.11 K8s client fixes)
**Status:** Complete

---

## Objective

Close out the five reviewer-requested items blocking PR #147 (`fix/epic-38-us11-k8s-client`): a TDD gap (no error-path tests for the new `LlmsafespaceV1()` contract), untested `InformerFactory` behaviours (idempotency + caching), a test-file mock typo, a missing worklog, and a dead struct field.

---

## Work Completed

### 1. Removed dead `workspaceID` field (`api/internal/handlers/proxy.go`)

The `workspaceConfig` struct carried a `workspaceID string` field that was never set (none of the `workspaceConfig{...}` literals across `proxy.go` or any test populated it) and never read. Confirmed via grep that every other `.workspaceID` reference in the package belongs to a different struct (`sessionInfo`, session-index event records). Dropped the field.

### 2. Fixed mock typo (`api/internal/handlers/proxy_test.go:2218`)

`k8sMock.On("LlmsafespaceV1").Return(llmMock, nil, nil, nil)` passed four return values for a two-return method (`LlmsafespaceV1() (LLMSafespaceV1Interface, error)`). testify's mock only inspects the first two, so the test passed by accident and masked the real contract. Corrected to `.Return(llmMock, nil)`.

### 3. Added `LlmsafespaceV1()` error-path tests (`api/internal/services/workspace/workspace_service_test.go`)

The US-38.11 refactor changed `LlmsafespaceV1()` from returning a single value to `(client, error)` via `sync.Once`. No caller exercised the error return — a TDD gap. Added three tests that drive the error through `workspaceCRDClient()`:

- `TestCreateWorkspace_LlmsafespaceV1Error_ReturnsInternal` — the typed-client construction error surfaces as `workspace_creation_failed` and the workspace interface is never reached.
- `TestGetWorkspaceStatus_LlmsafespaceV1Error_ReturnsInternal` — after a successful owner check, the error surfaces as `workspace_get_failed` instead of nil-dereferencing the client.
- `TestDeleteWorkspace_LlmsafespaceV1Error_ReturnsInternal` — error surfaces as `workspace_deletion_failed`.

Each asserts no panic, the correct `apierrors` code, and that the underlying `LLMSafespaceV1` construction error is preserved (via the `initialize LLMSafespaceV1 client` wrap) for diagnosis. A `newSvcWithLlmsafespaceV1Error` helper centralises the mock wiring.

### 4. Added `InformerFactory` tests (`pkg/kubernetes/informers_test.go`, new)

The US-38.11 InformerFactory added a `started` guard and per-informer caching with no tests. Added:

- `TestStartInformers_Idempotent` — injects two `countingInformer` doubles (a `cache.SharedIndexInformer` implementation whose `Run()` blocks on `stopCh` and counts invocations), calls `StartInformers` twice, and asserts each informer's `Run()` is invoked exactly once. This is the direct, deterministic measure of "only one set of goroutines" — no flaky goroutine-counting.
- `TestStartInformers_NotStartedByDefault` — a fresh factory reports `started == false`.
- `TestInformerFactory_CachesInformerInstances` — `RuntimeEnvironmentInformer()` and `WorkspaceInformer()` each return the same pointer across repeated calls, and the two informer types are distinct instances. Validates the caching contract that lets callers attach handlers to a stable object before `StartInformers`.

The `countingInformer` double intentionally `panic`s on every method other than `Run` so any future drift in what `StartInformers` invokes on the informer surfaces immediately.

### 5. Worklog

This file (0251) — the US-38.11 change shipped without one.

### Rebase

Rebased `fix/epic-38-us11-k8s-client` onto `origin/main` (which had advanced with PRs #138/#139). Rebase applied cleanly with no conflicts.

---

## Key Decisions

- **`countingInformer` double over goroutine counting.** Verifying idempotency by snapshotting `runtime.NumGoroutine()` is flaky in the test runner (background goroutines from the testing framework skew the count). A double that records `Run()` invocations and blocks on `stopCh` gives a deterministic, race-free assertion of the actual contract: "the second `StartInformers` does not re-invoke `Run`." `require.Eventually` synchronises the goroutine-launch observation.

- **Three caller-level error tests, not one.** The reviewer asked for "at least one," but `workspaceCRDClient()` is called from many methods with different error-wrapping codes (`workspace_creation_failed`, `workspace_get_failed`, `workspace_deletion_failed`). Testing three representative paths (create, read-status, delete) locks in the per-call-site wrap rather than a single happy-path stub.

- **Same-package (`package kubernetes`) informer test.** The factory's `started`, `runtimeEnvInf`, and `workspaceInf` fields are private. An internal test (`informers_test.go` in `package kubernetes`, matching the existing `client_test.go`) can inject doubles directly without exporting test-only seams — consistent with how the existing serializer tests reach into the package.

---

## Blockers

None.

---

## Tests Run

- `go build ./...` — passes.
- `go vet ./pkg/kubernetes/... ./api/internal/services/workspace/... ./api/internal/handlers/...` — clean.
- `go test ./pkg/kubernetes/...` — 6/6 pass (3 pre-existing + 3 new).
- `go test ./api/internal/services/workspace/...` — all pass (including the 3 new error-path tests).
- `go test ./api/internal/handlers/... -run TestProxy_OnSessionIdle_RecordsSessionIndexWithoutWsConfig` — passes (the typo-fixed test).

---

## Next Steps

Force-push the branch; await re-review on PR #147.

---

## Files Modified

- `api/internal/handlers/proxy.go` — removed dead `workspaceConfig.workspaceID` field.
- `api/internal/handlers/proxy_test.go` — fixed `.Return(llmMock, nil, nil, nil)` → `.Return(llmMock, nil)`.
- `api/internal/services/workspace/workspace_service_test.go` — added 3 `LlmsafespaceV1()` error-path tests + helper.
- `pkg/kubernetes/informers_test.go` (new) — added idempotency + caching + default-state tests.
- `worklogs/0251_2026-06-13_epic38-us11-k8s-client-fixes.md` (this file).
