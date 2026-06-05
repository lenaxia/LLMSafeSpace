# US-1.2: Fix Controller Compile Errors

**Epic:** 1 - Foundation
**Priority:** Critical
**Depends on:** US-0.2
**Blocks:** All controller stories

## User Story

As a developer, I want the controller to compile and start, so that I can deploy it and begin adding the Workspace reconciler.

## Acceptance Criteria

- [ ] `go build ./controller/...` succeeds with zero errors
- [ ] Controller starts and registers Sandbox reconciler with the manager

## Technical Details

**Prerequisite:** US-0.2 fixes the webhook decoder pattern. This story handles the remaining compile errors.

**Files to fix:**

| File | Issue | Fix |
|------|-------|-----|
| `controller/main.go:58-63` | `common.LeaderElectionConfig` and `time.Second` without imports | Add `common` and `time` imports |

**Webhook decoder fix is in US-0.2 (separate story) because it affects all 5 webhooks identically:**

| File | Issue |
|------|-------|
| `controller/internal/resources/sandbox_webhook.go` | `*admission.Decoder` → `admission.Decoder` |
| `controller/internal/resources/runtimeenvironment_webhook.go` | Same |
| `controller/internal/resources/sandboxprofile_webhook.go` | Same |
| `controller/internal/resources/warmpod_webhook.go` | Same |
| `controller/internal/resources/warmpool_webhook.go` | Same |

**Also clean up:** `controller/internal/controller/setup.go` is dead code (duplicate of `controller.go`). `main.go` calls `controller.SetupControllers()` — the `setup.go` file's `InitializeControllers()` is never called. Delete it.

## Design Reference

N/A — fixing existing code.

## Effort

Small (1-2 hours)
