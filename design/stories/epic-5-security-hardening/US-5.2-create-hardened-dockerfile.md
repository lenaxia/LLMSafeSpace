# US-5.2: Create Hardened Runtime Dockerfile

**DEFERRED to V2.1** — Only needed when high-security mode is required. Standard security mode is sufficient for V1.

**Epic:** 5 - Security Hardening
**Priority:** High
**Depends on:** US-1.8, US-5.1

## User Story

As a platform operator, I want a hardened runtime image for high-security sandboxes, so that tool output is redacted and network access is restricted.

## Acceptance Criteria

- [ ] `runtimes/base/Dockerfile.hardened` extends base image
- [ ] Renames real binaries (git → git.real)
- [ ] Installs wrapper scripts at original paths
- [ ] Controller selects hardened image when securityLevel=high

## Technical Details

**New file:** `runtimes/base/Dockerfile.hardened`

```dockerfile
FROM llmsafespace/base:latest
RUN mv /usr/bin/git /usr/bin/git.real || true
COPY --chmod=755 tools/wrappers/git    /usr/local/bin/git
COPY --chmod=755 tools/wrappers/curl   /usr/local/bin/curl
COPY --chmod=755 tools/wrappers/wget   /usr/local/bin/wget
```

**Controller change:** Sandbox reconciler selects hardened image when workspace has `spec.securityLevel: high`.

## Design Reference

Section 13.4: High-Security Variant

## Effort

Small (1-2 hours)
