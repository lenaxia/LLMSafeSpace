# US-1.6: Port Injection Detection

**DEFERRED to V2.1** — Not on the critical path. The proxy works without injection detection. Revisit when hardening the platform.

**Epic:** 1 - Foundation
**Priority:** Medium

## User Story

As a platform operator, I want prompt injection detection in the proxy pipeline, so that agent output containing injection attempts is flagged for callers.

## Acceptance Criteria

- [ ] `pkg/injection/detect.go` implements 5 regex patterns from k8s-mechanic
- [ ] `Detect(text string) bool` returns true if injection detected
- [ ] Unit tests for each pattern
- [ ] `go build ./pkg/injection/` succeeds

## Technical Details

**New files:**

| File | Purpose |
|------|---------|
| `pkg/injection/detect.go` | Detection engine — 5 patterns |
| `pkg/injection/detect_test.go` | Unit tests |

**Patterns (from design §9.5):**

```
1. (ignore|disregard|forget)...(previous|prior)...(instructions|rules|prompts|context)
2. you are now (in a)?(different|new|maintenance|admin|root|debug) mode
3. (override|bypass|disable) (all)? (hard)? rules
4. system: (you are|act as|behave as)
5. stop (following|obeying) (the|these|all)? (rules|instructions|guidelines|prompts)
```

**Source:** Port from `k8s-mechanic/internal/domain/injection.go`

**Integration:** The proxy handler (Epic 3) will call `injection.Detect()` on proxied responses and set `injection_detected: true` header.

## Design Reference

Section 9.5: Prompt Injection Detection

## Effort

Small (2-3 hours)
