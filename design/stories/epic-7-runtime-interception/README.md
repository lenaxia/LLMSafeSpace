# Epic 7: Runtime Interception Layer

**Status:** Planning
**Created:** 2026-05-24
**Priority:** Medium
**Depends on:** Epic 6 (Collapse Sandbox into Workspace)

## Rationale

The V1 "one container image per language" model is dead. CI only builds a single `base` image. The `runtimes/python/`, `runtimes/nodejs/`, `runtimes/go/` directories and the `RuntimeEnvironment` CRD's multi-image registry design are legacy cruft.

The real need is:
1. Agents install toolchains themselves (unprivileged: `go install`, `pip install`)
2. Some installs require root (apt packages, system libraries) — currently impossible without pod recreation
3. Security policies should activate per-language when a runtime is detected/installed
4. Users can use any runtime; explicitly supported ones get additional hardening

## Architecture

### Core Concept: Shadow PATH Interception

Replace real binaries with thin wrapper binaries at the same path. The wrappers are immutable (`chattr +i` or read-only filesystem layer). Real binaries are moved to a hidden, non-PATH location.

```
/usr/bin/apt        → wrapper (immutable) → daemon socket → /opt/llmsafespace/.bin/apt (root)
/usr/bin/pip        → wrapper (immutable) → daemon socket → /opt/llmsafespace/.bin/pip (root)
/usr/bin/npm        → wrapper (immutable) → daemon socket → /opt/llmsafespace/.bin/npm (root)
/usr/bin/python3    → wrapper (immutable) → /opt/llmsafespace/.bin/python3 (policy enforcement)
/usr/bin/node       → wrapper (immutable) → /opt/llmsafespace/.bin/node (policy enforcement)
/usr/bin/go         → wrapper (immutable) → /opt/llmsafespace/.bin/go (policy enforcement)
```

Two interception modes:

| Mode | Target | Purpose | Mechanism |
|------|--------|---------|-----------|
| **Privileged** | Package managers (apt, pip, npm, cargo) | Root escalation for installs | Forward to system daemon via Unix socket |
| **Policy** | Language runtimes (python, node, go) | Security hardening | Wrapper applies policy then exec's real binary |

### System Daemon

A root-owned process (PID 1 or sidecar) that:
- Listens on `/run/llmsafespace/system.sock` (Unix socket, `0660 root:sandbox`)
- Accepts install requests from package manager wrappers
- Validates against policy (allowlisted sources, blocked packages, rate limits)
- Executes the real package manager as root
- Streams stdout/stderr back to the wrapper
- Logs all operations for audit

### Security Model

- **Wrappers are immutable** — cannot be replaced or modified by the sandbox user
- **Real binaries are hidden** — `/opt/llmsafespace/.bin/` is not in PATH and not directly executable by sandbox user
- **Daemon validates all requests** — allowlist of permitted operations, not a blocklist
- **PATH bypass is impossible** — the binary at the canonical path IS the wrapper; virtualenvs, pyenv, nvm cannot override it because the underlying binary they'd call is also wrapped
- **Interception is strictly additive security** — worst case (wrapper bug), behavior degrades to passthrough, which is the status quo without the system
- **Defense in depth** — seccomp/apparmor profiles are the hard boundary; wrappers are the UX-friendly policy layer on top

### Policy Activation

When a language runtime is first installed (detected by the daemon), the daemon:
1. Enables the corresponding security wrapper for that language
2. Deploys restricted module lists / import hooks
3. Applies language-specific seccomp profile additions
4. Reports the activated policy back to the controller (status update)

Policies are defined in a new CRD (replacing `RuntimeEnvironment`'s dead fields):

```yaml
apiVersion: llmsafespace.dev/v1
kind: RuntimePolicy
metadata:
  name: python-hardened
spec:
  language: python
  securityWrapper: /opt/llmsafespace/policies/python/wrapper.conf
  restrictedModules: /opt/llmsafespace/policies/python/restricted_modules.json
  seccompAdditions: /opt/llmsafespace/policies/python/seccomp-additions.json
  blockedPackages:
    - os-sys-calls
    - malicious-pkg
  allowedSources:
    - https://pypi.org/simple/
```

### Workspace Spec Integration

```yaml
apiVersion: llmsafespace.dev/v1
kind: Workspace
spec:
  runtime: base
  languages:
    - name: python
      policy: python-hardened    # references RuntimePolicy CRD
    - name: go
      policy: go-standard
    - name: typescript
      policy: none              # no restrictions, just passthrough
  privilegedPackages:            # pre-installed at pod creation (init container)
    - python3
    - python3-dev
    - build-essential
```

- `languages` is optional. If omitted, all runtimes work but with no policy enforcement.
- `privilegedPackages` are installed at pod creation time (no daemon needed for these).
- Additional packages can be installed at runtime via the daemon.

### What Happens to RuntimeEnvironment CRD

| Current Field | Fate |
|---------------|------|
| `spec.image` | Kept — still needed for image resolution (escape hatch for custom images) |
| `spec.language` | Moved to `RuntimePolicy` |
| `spec.version` | Moved to `RuntimePolicy` |
| `spec.tags` | Deleted (never read) |
| `spec.preInstalledPackages` | Replaced by `workspace.spec.privilegedPackages` |
| `spec.packageManager` | Deleted (never read) |
| `spec.securityFeatures` | Replaced by `RuntimePolicy` |
| `spec.resourceRequirements` | Deleted (never read; workspace has its own resource spec) |
| `spec.requiresCredentials` | Kept (used by API sandbox service) |
| `status.available` | Deleted (never written) |
| `status.lastValidated` | Deleted (never written) |

Minimal `RuntimeEnvironment` after cleanup:

```go
type RuntimeEnvironmentSpec struct {
    Image               string `json:"image"`
    RequiresCredentials bool   `json:"requiresCredentials,omitempty"`
}
```

Or collapse it entirely into a ConfigMap / Helm values. TBD in US-7.6.

## Story List

| Story | Title | Scope |
|-------|-------|-------|
| US-7.1 | System Daemon | Root process, Unix socket, request/response protocol, audit log |
| US-7.2 | Package Manager Wrappers | apt, pip, npm wrappers → daemon client; binary relocation; immutability |
| US-7.3 | Language Runtime Wrappers | python, node, go wrappers with policy enforcement; config-driven |
| US-7.4 | RuntimePolicy CRD | New CRD type, webhook validation, example manifests |
| US-7.5 | Workspace Spec: languages + privilegedPackages | CRD changes, init container for privilegedPackages, controller integration |
| US-7.6 | RuntimeEnvironment Cleanup | Trim dead fields, deduplicate resolveRuntimeImage(), delete V1 artifacts |
| US-7.7 | Base Dockerfile Rewrite | Binary relocation, wrapper installation, daemon entrypoint, immutability |
| US-7.8 | Delete Legacy Runtime Artifacts | Remove `runtimes/python/`, `runtimes/nodejs/`, `runtimes/go/`, `runtimes/tests/`, `design/RUNTIMEENV.md` |

## Dependency Graph

```
US-7.1 (daemon) ──────────────────┐
                                   ├── US-7.7 (Dockerfile)
US-7.2 (pkg mgr wrappers) ────────┤
                                   │
US-7.3 (lang wrappers) ───────────┘
                                   
US-7.4 (RuntimePolicy CRD) ── US-7.5 (workspace spec)

US-7.6 (RTE cleanup) ── independent
US-7.8 (delete legacy) ── independent (do first or last, doesn't matter)
```

US-7.1, US-7.2, US-7.3 are tightly coupled (daemon + its clients).
US-7.4, US-7.5 are the CRD/controller work.
US-7.6, US-7.8 are cleanup (can be done independently).

## Key Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Wrappers are compiled Go binaries | <1ms overhead per invocation. No dependency on the language being wrapped. Static linking = no shared lib issues. |
| D2 | Daemon uses Unix socket, not TCP | No network attack surface. Socket permissions (`0660 root:sandbox`) are the access control. |
| D3 | Binary relocation + immutability, not PATH ordering | Virtualenvs, pyenv, nvm, conda all prepend to PATH. Relocation makes bypass impossible. |
| D4 | Interception is strictly additive | Without the system, agent has unrestricted access. The daemon can only make things more restrictive. No new privilege is granted. |
| D5 | Policy is optional | `languages: []` or omitted = all runtimes work with no policy. Supported runtimes get hardening. Unsupported runtimes pass through. |
| D6 | Privileged packages declared in workspace spec | Known-needed root installs happen at pod creation (init container). Daemon handles runtime installs. Both paths exist. |
| D7 | RuntimePolicy is a separate CRD from RuntimeEnvironment | Separation of concerns: RTE maps name→image. RuntimePolicy maps language→security config. They serve different purposes. |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Wrapper breaks a package manager flag we didn't anticipate | Medium | Medium | Passthrough by default; only block explicitly disallowed flags. Extensive integration tests. |
| Daemon becomes a bottleneck under heavy install load | Low | Low | Installs are infrequent (once per workspace setup). Rate limiting is a feature, not a bug. |
| Agent discovers bypass (e.g., downloads binary directly, `chmod +x`) | Medium | Low | Seccomp/apparmor is the hard boundary. Wrapper is UX layer. Downloaded binaries still run under seccomp. |
| Maintenance burden of N wrappers | Low | Low | Wrappers are ~50 lines each. Same pattern, different binary path. |

## V1 Artifacts to Delete (US-7.8)

- `runtimes/python/` — Dockerfile, security/, tools/
- `runtimes/nodejs/` — Dockerfile, config/, security/, tools/
- `runtimes/go/` — Dockerfile, security/, tools/
- `runtimes/tests/` — test_runtime.py, run_tests.sh, results/
- `design/RUNTIMEENV.md` — superseded by this epic
- `controller/examples/runtimeenvironment.yaml` — references non-existent images

## Estimated Impact

- ~1500 lines deleted (legacy artifacts)
- ~2000 lines added (daemon, wrappers, CRD, Dockerfile changes)
- Net: +500 lines but replaces dead code with working infrastructure
- New binary: `cmd/system-daemon/` (~400 lines)
- New wrappers: `cmd/wrapper/` (~200 lines, shared binary with subcommand dispatch)
