# Worklog: CPU Zero-Magnitude Validation Gap

**Date:** 2026-06-19
**Session:** Close the parallel CPU zero-magnitude gap that PR #269 missed
**Status:** Complete

---

## Objective

PR #269 (settings normalize+validate, merged 2026-06-19) tightened memory and storage patterns from `[0-9]+` to `[1-9][0-9]*` to reject zero-magnitude values like `"0Gi"`. CPU was left at `^([0-9]+m|[0-9]+\.[0-9]+)$` because the original "8gi" production failure was memory-only. Self-review afterward caught this asymmetry: CPU still accepts `"0m"` and `"0.0"`, both of which would reach the validating webhook (which has no `n<1` check for CPU, unlike memory/storage), then get rejected by the Kubernetes apiserver itself with a less-helpful error than our admission webhook.

User direction: "Yes, fix the CPU zero-magnitude gap."

## Work Completed

### Backend
- `pkg/settings/quantity_patterns.go`: tightened `CPUQuantityPattern` to `^([1-9][0-9]*m|[1-9][0-9]*\.[0-9]+|0\.[0-9]*[1-9][0-9]*)$`. Three alternations cover the three valid shapes:
  - `[1-9][0-9]*m` — positive millicores (`500m`, `1000m`)
  - `[1-9][0-9]*\.[0-9]+` — non-zero whole part (`1.0`, `16.0`)
  - `0\.[0-9]*[1-9][0-9]*` — zero whole, non-zero somewhere in fractional (`0.5`, `0.001`)

  Rejected: `"0m"`, `"0.0"`, `"0.00"`, `"0"`, `"0."`. The third alternation requires at least one non-zero digit in the fractional part, so `"0.0"` and `"0.000"` both fail.

- `controller/internal/webhooks/workspace_webhook.go`: `cpuPattern` updated to match the canonical pattern (parser-side capture groups: `^([1-9][0-9]*)m$|^([1-9][0-9]*\.[0-9]+|0\.[0-9]*[1-9][0-9]*)$`). Error message in `parseCPUMillis` updated to reflect the current regex.

- `pkg/apis/llmsafespaces/v1/workspace_types.go`: kubebuilder annotations on `CPU` and `CPULimit` updated to match.

- `pkg/settings/schema.go`: `SchemaVersion` 3 → 4.

- `sdks/canary/go/scenarios/s-user-settings/main.go`: `expectedSchemaVersion` 3 → 4.

### Tests

- `pkg/settings/schema_test.go TestValidate_CPU_RejectsBogusValues`: added `"0m"`, `"0.0"`, `"0.00"`, `"0"`, `"0."` to the rejected set; added `"0.001"`, `"0.5"`, `"1m"` to the accepted set as edge-case proofs that the pattern doesn't over-reject.

- `controller/internal/webhooks/workspace_webhook_test.go TestWebhookRegexAcceptsSameInputsAsSettingsPattern`: added `"0m"`, `"0.0"`, `"0.00"`, `"0"`, `"0.001"`, `"1m"` probes to the CPU drift-guard matrix. Both regexes (canonical + webhook) must accept-or-reject identically on every probe.

## Key Decisions

1. **Pattern over runtime check.** Could have left the pattern liberal and added an `n < 1` check in `parseCPUMillis` (mirroring `parseMemoryMi`/`storageSizeGi`). Chose pattern-side rejection because: (a) symmetric with memory/storage, (b) error message is clearer (the regex hint includes the constraint), (c) catches at admin save time instead of workspace creation time.

2. **Pattern complexity vs simplicity.** The three-alternation regex is more complex than the original two-alternation one. Justified because Kubernetes Quantity grammar genuinely has three valid positive shapes; collapsing them would either over-reject (`0.5` is valid and common) or under-reject (`0.0` should fail). The doc comment on `CPUQuantityPattern` enumerates each branch with examples.

3. **Did NOT add `n < 1` check in `parseCPUMillis`.** The pattern catches `0m`, `0.0`, etc. before the parser runs. Adding a redundant `n < 1` would be defense-in-depth but the pattern is the authoritative gate. If the pattern is bypassed (manually editing the regex), failures elsewhere would surface — not silent acceptance.

## Alternatives Considered

- **Leave the gap.** The "0m" failure mode is "pod stuck Pending", not "cryptic webhook error" (the bug we fixed). User explicitly chose to close it for symmetry.

- **Defense-in-depth with both pattern AND `n < 1` parser check.** Considered; rejected because the pattern is sufficient and adding both creates two error messages for the same case (which one fires depends on whether the value matches the regex but parses to 0 — impossible with the current pattern, so the parser check would be dead code).

- **Add `n < 1` check to `parseCPUMillis` instead of tightening pattern.** This was the minimum-invasive option. Rejected because it leaves the schema and CRD annotations accepting `"0m"`, so the webhook becomes the only line of defense — and an admin saving `"0m"` via the admin UI would not see immediate feedback (the value would silently land in the DB and only fail at next workspace creation). The pattern-side fix gives synchronous feedback at save time.

## Blockers

None.

## Tests Run

```
$ go build ./...
(clean)

$ go test ./pkg/settings/... ./controller/internal/webhooks/...
ok  	github.com/lenaxia/llmsafespaces/pkg/settings	0.077s
ok  	github.com/lenaxia/llmsafespaces/controller/internal/webhooks	0.094s

$ go vet ./...
(clean)

$ golangci-lint run --timeout=5m
0 issues.

$ make repolint
ok    worklogs sequence (388 worklogs, max 0389)
ok    worklogs no mainline collisions (next available: 0390)
ok    chart migrations match api/migrations/
ok    CRD drift (8 bindings checked)
repolint: all checks passed
```

End-to-end probe:
```
Normalize("0M") = "0m"      Validate = error: pattern mismatch
Normalize("0m") = "0m"      Validate = error: pattern mismatch
Normalize("0.0") = "0.0"    Validate = error: pattern mismatch
Normalize("500M") = "500m"  Validate = nil
Normalize("0.001") = "0.001" Validate = nil
Normalize("0.5") = "0.5"    Validate = nil
```

## Next Steps

- Merge the PR.
- Self-audit complete: memory, storage, CPU all consistent. No further parallel gaps identified in the settings → webhook → apiserver chain for these three fields.
- Frontend `lib/settingsNormalize.ts` already uses the same regex via `normalizeSettingValue`; no change needed there because the normalizer never produced a zero-magnitude value (it doesn't transform `"0M"` into anything different from `"0m"` — the rejection happens at pattern check, which the frontend mirrors).

## Files Modified

| File | Change |
|---|---|
| `pkg/settings/quantity_patterns.go` | `CPUQuantityPattern` tightened to reject zero-magnitude |
| `pkg/settings/schema.go` | `SchemaVersion` 3 → 4 |
| `pkg/settings/schema_test.go` | `TestValidate_CPU_RejectsBogusValues` adds zero-magnitude probes |
| `controller/internal/webhooks/workspace_webhook.go` | `cpuPattern` matches canonical; `parseCPUMillis` error string updated |
| `controller/internal/webhooks/workspace_webhook_test.go` | Drift-guard CPU probes include zero-magnitude |
| `pkg/apis/llmsafespaces/v1/workspace_types.go` | kubebuilder annotations on `CPU`, `CPULimit` updated |
| `sdks/canary/go/scenarios/s-user-settings/main.go` | `expectedSchemaVersion` 3 → 4 |
| `worklogs/0390_2026-06-19_cpu-zero-magnitude-gap.md` | This file |
