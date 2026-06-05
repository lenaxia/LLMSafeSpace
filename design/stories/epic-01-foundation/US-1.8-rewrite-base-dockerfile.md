# US-1.8: Rewrite Base Dockerfile

**Epic:** 1 - Foundation
**Priority:** High

## User Story

As a developer, I want the base runtime image to include opencode, the redact binary, and entrypoint scripts, so that sandboxes run the V2 agent architecture.

## Acceptance Criteria

- [ ] Base image includes opencode binary (SHA256-verified)
- [ ] Redact binary included
- [ ] Entrypoint scripts included and executable
- [ ] Smoke test passes
- [ ] Non-root user, read-only root filesystem, dropped capabilities
- [ ] EmptyDir mounts configured for /tmp, /home/sandbox

## Technical Details

**Rewrite:** `runtimes/base/Dockerfile`

Key changes from V1:
- Remove old sandbox tools (cleanup-pod, execution-tracker, health-check, sandbox-monitor)
- Remove V1 language-specific security wrappers (python-security-wrapper.py, nodejs-security-wrapper.js, go-security-wrapper.go) — these conflict with the V2 opencode-based architecture
- Remove V1 restricted module lists (restricted_packages.json, restricted_modules.json) — opencode handles tool execution internally
- Remove V1 security profiles (apparmor, seccomp) — will be rebuilt for V2.1
- Add opencode binary (SHA256-verified download)
- Add redact binary (built from cmd/redact)
- Add entrypoint scripts
- ENTRYPOINT set to entrypoint-opencode.sh
- Base image pinned to digest: `debian:bookworm-slim@sha256:<pinned>`
- No PYTHON_VERSION in base (belongs in Python runtime)

**Per design §13.2:**

```
Multi-stage build:
  Stage 1: golang:1.23-bookworm → build redact binary
  Stage 2: debian:bookworm-slim (pinned digest) → runtime
    - bash, ca-certificates, curl, git, jq, unzip
    - SHA256-verified opencode binary
    - redact binary from stage 1
    - entrypoint scripts (755)
    - smoke test
    - Non-root user (sandbox, uid 1000)
    - WORKDIR /workspace
    - ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]
```

**Do NOT install PATH-shadowing wrappers** — deferred to V2.1 with high-security mode.

## Design Reference

Section 13.2: Dockerfile Pattern
Section 9.8: Supply Chain Security

## Effort

Medium (3-4 hours)
