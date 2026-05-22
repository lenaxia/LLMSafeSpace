# US-5.1: Build PATH-Shadowing Wrappers

**DEFERRED to V2.1** — The redact binary provides core protection. Wrappers add incremental hardening for high-security mode. Revisit when implementing high-security sandboxes.

**Epic:** 5 - Security Hardening
**Priority:** High

## User Story

As a platform operator, I want tool output redacted in high-security sandboxes, so that leaked credentials cannot be exfiltrated through curl/wget/git output.

## Acceptance Criteria

- [ ] Wrapper scripts for curl, wget, git
- [ ] Each wrapper pipes output through `redact` binary
- [ ] Wrappers check `/sandbox-cfg/high-security` sentinel
- [ ] Fail-closed: if redact binary missing, exit 1
- [ ] Wrappers installed only in hardened Dockerfile

## Technical Details

**New files:**

| File | Purpose |
|------|---------|
| `runtimes/base/tools/wrappers/curl` | Wraps curl, pipes output through redact |
| `runtimes/base/tools/wrappers/wget` | Wraps wget, pipes output through redact |
| `runtimes/base/tools/wrappers/git` | Wraps git, pipes output through redact |

**Wrapper pattern (from k8s-mechanic):**

```bash
#!/usr/bin/env bash
if [[ "$(cat /sandbox-cfg/high-security 2>/dev/null)" == "true" ]]; then
    /usr/bin/curl.real "$@" 2>&1 | /usr/local/bin/redact
else
    exec /usr/bin/curl.real "$@"
fi
```

**Do NOT add kubectl wrapper** — kubectl is not in sandbox images.

## Design Reference

Section 9.4: PATH-Shadowing Wrappers

## Effort

Small (2-3 hours)
