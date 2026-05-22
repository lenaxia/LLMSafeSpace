# US-0.2: Fix Webhook Decoder Pointer-to-Interface

**Epic:** 0 - Unbreak
**Priority:** Critical
**Blocks:** US-1.2 and all controller stories

## User Story

As a developer, I want the controller to compile, so that I can begin adding the Workspace reconciler.

## Acceptance Criteria

- [ ] `go build ./controller/...` succeeds with zero errors
- [ ] All 5 webhook validators compile correctly

## Technical Details

**Root cause:** All 5 webhook validators store `decoder` as `*admission.Decoder` (pointer-to-interface). In Go, a pointer to an interface has no methods. The field type should be `admission.Decoder` (the interface value itself, which is already a pointer to the concrete implementation).

**Errors (5 files, identical issue):**

| File | Line | Error |
|------|------|-------|
| `sandbox_webhook.go` | 24 | `v.decoder.Decode undefined` |
| `runtimeenvironment_webhook.go` | 21 | `v.decoder.Decode undefined` |
| `sandboxprofile_webhook.go` | 21 | `v.decoder.Decode undefined` |
| `warmpod_webhook.go` | 23 | `v.decoder.Decode undefined` |
| `warmpool_webhook.go` | 23 | `v.decoder.Decode undefined` |

**Fix:** In each webhook struct, change `decoder *admission.Decoder` to `decoder admission.Decoder`. Also update the `InjectDecoder` receiver to match.

```go
// Before:
type SandboxWebhook struct {
    decoder *admission.Decoder
}
func (w *SandboxWebhook) InjectDecoder(d *admission.Decoder) error {
    w.decoder = d
    return nil
}

// After:
type SandboxWebhook struct {
    decoder admission.Decoder
}
func (w *SandboxWebhook) InjectDecoder(d admission.Decoder) error {
    w.decoder = d
    return nil
}
```

**Note:** `admission.Decoder` is an interface in controller-runtime v0.20.x. The `InjectDecoder` method must accept the interface value, not a pointer to it.

## Design Reference

N/A — fixing existing code.

## Effort

Small (1-2 hours)
