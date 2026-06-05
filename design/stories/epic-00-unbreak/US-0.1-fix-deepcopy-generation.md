# US-0.1: Fix Deepcopy Generation

**Epic:** 0 - Unbreak
**Priority:** Critical
**Blocks:** ALL other stories (monorepo build is broken)

## User Story

As a developer, I want `go build ./...` to succeed, so that I can begin implementing V2 features.

## Acceptance Criteria

- [ ] `go build ./...` succeeds with zero errors
- [ ] `pkg/types/zz_generated.deepcopy.go` no longer references `DeepCopyInto` on `time.Time` or `DeepCopyWSConnection` on interface types
- [ ] All existing tests pass

## Technical Details

**Root cause:** `pkg/types/zz_generated.deepcopy.go` was auto-generated against types containing `time.Time` (primitive, no `DeepCopyInto`) and `WSConnection` (interface, no `DeepCopyWSConnection`). The code generator should have been told to skip these.

**Errors (6 total):**

| Line | Error |
|------|-------|
| 161 | `in.ModTime.DeepCopyInto undefined` |
| 494 | `in.CreatedAt.DeepCopyInto undefined` |
| 495 | `in.UpdatedAt.DeepCopyInto undefined` |
| 726 | `in.Conn.DeepCopyWSConnection undefined` |
| 728 | `in.CreatedAt.DeepCopyInto undefined` |
| 761 | `in.CreatedAt.DeepCopyInto undefined` |

**Fix options (choose one):**

1. **Delete the generated file entirely** — `pkg/types/` contains API transfer objects, not CRD types. DeepCopy is rarely needed for these (they're request/response structs). If any code calls `DeepCopy` on these types, implement it manually on a case-by-case basis.

2. **Add `// +k8s:deepcopy-gen=false` tags** to structs containing `time.Time` or interface fields, then regenerate.

3. **Replace `time.Time` with `metav1.Time`** in types that need deepcopy (metav1.Time implements `DeepCopyInto`), and remove deepcopy from interface-containing types.

**Recommendation:** Option 1 (delete). The `pkg/types/` types are API-layer DTOs. Add manual `DeepCopy` methods only where profiling shows they're needed.

**Files to edit/delete:**

| File | Action |
|------|--------|
| `pkg/types/zz_generated.deepcopy.go` | Delete |
| `pkg/types/doc.go` | Remove `// +k8s:deepcopy-gen=package` if present |
| Any code referencing `DeepCopy` on `pkg/types` structs | Fix calls |

## Design Reference

Section P.2 (CRD Type Ownership Model)

## Effort

Small (1-2 hours)
