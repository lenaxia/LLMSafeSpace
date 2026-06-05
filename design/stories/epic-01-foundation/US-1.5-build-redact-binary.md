# US-1.5: Build Redact Binary

**Epic:** 1 - Foundation
**Priority:** High

## User Story

As a platform operator, I want a secret redaction binary included in runtime images, so that tool output is scanned for leaked credentials before being returned to users.

## Acceptance Criteria

- [ ] `pkg/redact/redact.go` implements 16 regex patterns from k8s-mechanic
- [ ] `cmd/redact/main.go` reads stdin, redacts, writes stdout
- [ ] Binary fails closed (exit 1) if redact library returns error
- [ ] User-extensible patterns via config file
- [ ] Unit tests for each pattern
- [ ] `go build ./cmd/redact/` produces working binary

## Technical Details

**Note:** The `cmd/` directory does not currently exist in the repository. It must be created as a top-level directory (`cmd/redact/main.go`).

**New files:**

| File | Purpose |
|------|---------|
| `pkg/redact/redact.go` | Redaction engine — 16 compiled regex rules |
| `pkg/redact/redact_test.go` | Unit tests per pattern + edge cases |
| `cmd/redact/main.go` | CLI: stdin → redact → stdout (new top-level cmd/ directory) |

**Patterns (from design §9.3):**

```
URL credentials, Bearer tokens, GitHub tokens, JSON passwords,
password=, token=, secret=, api_key=, x-api-key=, PEM private keys,
age keys, OpenAI/Anthropic keys (sk-), AWS IAM (AKIA), JWTs,
Auth headers, long base64 strings
```

**Source:** Port from `k8s-mechanic/internal/domain/redact.go`

**Usage in PATH-shadowing wrappers (future):**
```bash
curl.real "$@" 2>&1 | redact
```

**Extensible patterns:** Read additional patterns from `/sandbox-cfg/redact-patterns.json` if present.

## Design Reference

Section 9.3: Secret Redaction Pipeline

## Effort

Medium (4-6 hours)
