# Worklog — Epic 10 Step 1: llm-provider secret type

**Date:** 2026-06-02
**Operator:** opencode
**Start state:** sha before these changes
**End state:** Step 1 implementation complete

---

## Summary

Added the `llm-provider` secret type with structured `LLMProviderData`/`LLMModelConfig`
types, registered it in the validation layer, implemented the materializer staging
pipeline, and updated the restart trigger in workspace-agentd.

## Decisions

- **Model catalog stays at opencode layer** — validated against design doc and
  existing code. LLMSafeSpace does NOT maintain a model registry; only provides
  optional allowlisting via `Models []LLMModelConfig`.
- **Staging architecture** — `applyLLMProvider` validates and collects providers;
  `FlushProviders(formatter)` calls an agent-specific formatter callback and
  writes to `AgentConfigPath`. This keeps the materializer agent-agnostic.
- **Sentinel error** — added `ErrInvalidLLMProvider` to `pkg/secrets/errors.go`
  for validation failures, consistent with existing patterns.

## Files changed

### `pkg/secrets/errors.go`
- Added `ErrInvalidLLMProvider` sentinel error.

### `pkg/secrets/types.go`
- Added `SecretTypeLLMProvider = "llm-provider"` constant.
- Added `LLMModelConfig` struct (ID + optional Label).
- Added `LLMProviderData` struct (Provider, APIKey, BaseURL, Models, Default,
  SmallModel) with `Validate()` method.
- Updated `ValidSecretTypes`, `ValidSecretTypesList`, and
  `MetadataRequirementsBySecretType` to include the new type.

### `pkg/secrets/types_test.go` (already existed)
- Test marshaling/unmarshaling of `LLMProviderData` with all fields, minimal
  fields, invalid JSON, extra fields, and `omitempty` behavior.
- Test `SecretTypeLLMProvider` is in validation maps.

### `pkg/agentd/secrets/secrets.go`
- Added import alias `sec` for `pkg/secrets`.
- Added `LLMProviderFormatter` type alias for the formatter callback.
- Added `stagedProviders` field to `Materializer`.
- Added `applyLLMProvider()` — validates plaintext JSON, unmarshals, validates
  `LLMProviderData`, appends to staged slice.
- Added `FlushProviders()` — calls formatter with staged providers, writes
  result to `AgentConfigPath` with mode 0600.
- Updated `reset()` to clear `stagedProviders`.
- Updated `applyOne()` to dispatch `"llm-provider"` and exempt it from name
  validation (same as `api-key`).

### `pkg/agentd/secrets/secrets_test.go` (already existed)
- Tests for valid, minimal, empty, missing-field, invalid-JSON, bad-name,
  multi-provider, and mixed-type materialization.
- Tests for `FlushProviders` with formatter, no staged, nil formatter,
  formatter error, and mode 0600 verification.

### `cmd/workspace-agentd/secrets.go`
- Updated `shouldRestart()` to include `"llm-provider"` alongside `"env-secret"`
  and `"api-key"`.

### `cmd/workspace-agentd/secrets_test.go`
- Added import for `secrets` package alias.
- Added tests: `TestShouldRestart_LLMProvider`,
  `TestShouldRestart_LLMProviderMixed`, `TestShouldRestart_NoLLMProvider`,
  `TestShouldRestart_EmptyBatch`.

## Verification status

**Cannot compile/test in this sandbox** (Go 1.26.3 stdlib broken). Code should
compile cleanly with `go 1.23` in a proper environment. Key things to verify:

1. `go build ./pkg/secrets/...` — type definitions and validation
2. `go test ./pkg/secrets/...` — type marshaling tests
3. `go build ./pkg/agentd/secrets/...` — materializer implementation
4. `go test ./pkg/agentd/secrets/...` — materializer tests
5. `go test ./cmd/workspace-agentd/...` — shouldRestart tests

## Next steps

- Step 2: Implement `FormatCredentials()` in `pkg/agent/opencode/opencode.go`
  to render `[]LLMProviderData` → opencode config JSON format.
- Validate hot-reload: test `PATCH /config` and `PUT /auth/:id` against
  running opencode.
