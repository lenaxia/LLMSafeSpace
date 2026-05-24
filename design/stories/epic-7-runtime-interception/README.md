# Epic 7: Runtime Interception Layer

**Status:** Planning
**Created:** 2026-05-24
**Priority:** Medium
**Depends on:** Epic 6 (Collapse Sandbox into Workspace)

## Rationale

The V1 "one container image per language" model is dead. CI only builds a single `base` image. The `runtimes/python/`, `runtimes/nodejs/`, `runtimes/go/` directories and the `RuntimeEnvironment` CRD's multi-image registry design are legacy cruft.

The real need is:
1. Agents install toolchains themselves at runtime (no pod recreation)
2. Some installs require root (apt packages, system libraries) — handled by a privileged daemon
3. Security policies activate per-language when a runtime is detected/installed
4. Users can use any runtime; explicitly supported ones get additional hardening
5. Works identically in Docker, docker-compose, Kubernetes, and homelab setups

## Architecture

### Core Concept: Sentinel-Driven Dual Mode

One image. Two behaviors based on a sentinel file (`/etc/llmsafespace/mode`):

**Docker (no sentinel):** Container runs as root. No daemon. No wrappers. No policy. Agent has full access. Just opencode.

**Kubernetes (sentinel present):** Main container runs as UID 1000 with wrappers active. A sidecar container runs the daemon as root with minimal capabilities. They share a volume for the Unix socket. Wrappers in the main container talk to the sidecar for apt installs.

```
Docker/Homelab (no sentinel):
┌─────────────────────────────────────────┐
│  Container (root, full access)           │
│  entrypoint-opencode.sh → opencode serve │
│  apt/pip/npm/python → real binaries      │
└─────────────────────────────────────────┘

Kubernetes (sentinel present):
┌─────────────────────────────────────────────────────────────┐
│  Pod                                                         │
│                                                              │
│  ┌─────────────────────────────────┐  ┌──────────────────┐  │
│  │  Main: workspace (UID 1000)     │  │  Sidecar: daemon  │  │
│  │  opencode serve                 │  │  (root, minimal   │  │
│  │  wrappers → policy enforcement  │  │   caps)           │  │
│  │  apt wrapper → socket ──────────┼──┼→ system.sock      │  │
│  │  pip/npm → policy → direct exec │  │  handles apt      │  │
│  │  python/node → policy → exec    │  │  audit log        │  │
│  └─────────────────────────────────┘  └──────────────────┘  │
│                                                              │
│  Shared volumes:                                             │
│    - /run/llmsafespace/ (emptyDir) — socket                  │
│    - /workspace (PVC) — persistent data                      │
│    - /etc/llmsafespace/ (ConfigMap) — sentinel + policies    │
└─────────────────────────────────────────────────────────────┘
```

### Shadow PATH Interception

Real binaries are relocated at image build time. Wrappers replace them at the original paths. Wrappers are owned by root (0755) — UID 1000 cannot modify them.

Two interception modes:

| Mode | Target | Purpose | Mechanism |
|------|--------|---------|-----------|
| **Privileged** | System package managers (apt, apk) | Root escalation for installs | Wrapper → Unix socket → daemon executes as root |
| **Policy** | Language runtimes (python, node, go) + language package managers (pip, npm, cargo) | Security hardening + source/package restrictions | Wrapper applies policy then exec's real binary (no root needed) |

```
/usr/bin/apt        → wrapper (root:root 755) → socket → daemon → /opt/llmsafespace/.bin/apt
/usr/bin/pip3       → wrapper (root:root 755) → policy check → /opt/llmsafespace/.bin/pip3
/usr/bin/python3    → wrapper (root:root 755) → policy check → /opt/llmsafespace/.bin/python3
/usr/bin/node       → wrapper (root:root 755) → policy check → /opt/llmsafespace/.bin/node

/opt/llmsafespace/.bin/  → real binaries (root:root 750, not in PATH)
```

### Security Model

**UID separation is the security boundary:**

| Actor | UID | Can do | Cannot do |
|-------|-----|--------|-----------|
| Daemon | 0 (root) | apt install, listen on socket, fork processes | N/A (it's root, but only runs allowlisted commands) |
| Agent (opencode) | 1000 | Read/write /workspace, /tmp, /home/sandbox; run language tools; talk to daemon socket | Write to /usr/bin, /opt/llmsafespace/.bin, /etc/llmsafespace; kill daemon; escalate to root |

**Why UID 1000 cannot escalate:**
- No SUID binaries in the image
- No `sudo` installed
- No capabilities on the container (except minimal set for daemon)
- `AllowPrivilegeEscalation: false` on Kubernetes (optional)
- Daemon socket validates requests against policy — not arbitrary command execution

**Defense in depth layers:**
1. Unix file permissions (wrappers and real binaries owned by root)
2. Daemon policy engine (allowlist, rate limit, source restrictions)
3. Seccomp profile (optional, blocks dangerous syscalls)
4. Kubernetes RuntimeClass for multi-tenant (gVisor/Kata/Firecracker)
5. Network policy (optional, restricts egress)

### Container Security Context (Kubernetes)

Main container (workspace):
```yaml
securityContext:
  runAsUser: 1000
  runAsGroup: 1000
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
  seccompProfile:
    type: RuntimeDefault
```

Sidecar container (system-daemon):
```yaml
securityContext:
  runAsUser: 0
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
    add: [CHOWN, DAC_OVERRIDE, FOWNER, SETUID, SETGID]
  seccompProfile:
    type: RuntimeDefault
```

Shared volumes:
```yaml
volumes:
  - name: daemon-socket
    emptyDir: {}
  - name: policies
    configMap:
      name: {workspace}-policies  # includes sentinel + language policies
  - name: workspace
    persistentVolumeClaim:
      claimName: {workspace}-pvc
```

For multi-tenant deployments, add:
```yaml
runtimeClassName: gvisor  # or kata, firecracker
```

### Docker Compatibility

```bash
# Homelab — just works, no enforcement, root, full access
docker run -v workspace:/workspace ghcr.io/lenaxia/llmsafespace/base

# Homelab with security opt-in (empty sentinel = defaults)
docker run -v workspace:/workspace \
  -v ./mode:/etc/llmsafespace/mode \
  ghcr.io/lenaxia/llmsafespace/base

# docker-compose (no sidecar needed — security layer is off)
services:
  workspace:
    image: ghcr.io/lenaxia/llmsafespace/base
    volumes:
      - workspace:/workspace
```

No sidecars in Docker. The sidecar only exists in Kubernetes where the controller builds the pod spec. Docker users get a single container with full root access — the simplest possible experience.

### Sentinel File: `/etc/llmsafespace/mode`

The sentinel controls whether enforcement is active. One file, two behaviors:

| Sentinel State | Behavior |
|----------------|----------|
| **Absent** | Docker mode. Daemon exec's opencode directly. Wrappers passthrough. No policy. No UID separation. |
| **Present (empty)** | Full enforcement with defaults: daemon on socket, UID 1000, all policies active. |
| **Present (with JSON)** | Full enforcement with custom config. |

Sentinel JSON (all fields optional, shown with defaults):
```json
{
  "enforcement": "full",
  "daemon": "/run/llmsafespace/system.sock"
}
```

- `enforcement`: `"full"` (all wrappers enforce policy) or `"audit"` (log only, don't block)
- `daemon`: socket path for apt wrapper to connect to. If empty/absent, apt wrapper returns an error instead of passthrough (prevents accidental unprotected installs in K8s).

**Kubernetes**: Controller mounts sentinel as a ConfigMap key at `/etc/llmsafespace/mode`.
**Docker (opt-in)**: User bind-mounts a file (even an empty one) to activate.
**Docker (default)**: No sentinel. No enforcement. Agent runs as root with full access.

### Policy Activation

When a language runtime is installed (detected by daemon or wrapper), the corresponding security policy activates:

```json
// /etc/llmsafespace/policies/python.json
{
  "language": "python",
  "enabled": true,
  "restrictedModules": ["ctypes", "subprocess"],
  "allowedSources": ["https://pypi.org/simple/"],
  "blockedPackages": ["os-sys-calls"],
  "blockedFlags": ["--trusted-host"]
}
```

Policies are defined in a `RuntimePolicy` CRD (Kubernetes) or config files (Docker). The workspace spec declares which policies to activate. If no policy is declared, wrappers pass through with no restrictions.

### Workspace Spec Integration

```yaml
apiVersion: llmsafespace.dev/v1
kind: Workspace
spec:
  runtime: base
  runtimeClass: ""              # default (runc). Set to "gvisor" for multi-tenant.
  languages:
    - name: python
      policy: python-hardened
    - name: go
      policy: go-standard
    - name: typescript
      policy: none              # passthrough, no restrictions
```

- `languages` is optional. Omit = all runtimes work with no policy.
- `runtimeClass` is optional. Omit = cluster default.
- No `privilegedPackages` field — agent installs at runtime via daemon.

### What Happens to RuntimeEnvironment CRD

Stripped to its only used purpose (image resolution):

```go
type RuntimeEnvironmentSpec struct {
    Image               string `json:"image"`
    Language            string `json:"language,omitempty"`
    Version             string `json:"version,omitempty"`
    RequiresCredentials bool   `json:"requiresCredentials,omitempty"`
}
```

All security/policy fields move to `RuntimePolicy` CRD.

## Story List

| Story | Title | Scope |
|-------|-------|-------|
| US-7.1 | System Daemon | Sidecar container (K8s only); root; Unix socket; policy engine; audit log |
| US-7.2 | Package Manager Wrappers | apt wrapper → daemon (privilege); pip/npm/cargo wrappers → policy only (no root) |
| US-7.3 | Language Runtime Wrappers | python/node/go wrappers with policy enforcement; config-driven |
| US-7.4 | RuntimePolicy CRD | New CRD type for per-language security config |
| US-7.5 | Workspace Spec: languages + runtimeClass | CRD changes, controller mounts policy ConfigMaps, runtimeClassName |
| US-7.6 | RuntimeEnvironment Cleanup | Trim dead fields, deduplicate resolveRuntimeImage() |
| US-7.7 | Base Dockerfile Rewrite | Binary relocation, wrapper install, daemon as entrypoint, drop read-only rootfs |
| US-7.8 | Delete Legacy Runtime Artifacts | Remove runtimes/python/, nodejs/, go/, tests/, design/RUNTIMEENV.md |

## Dependency Graph

```
US-7.1 (daemon) ──────────────────┐
                                   ├── US-7.7 (Dockerfile)
US-7.2 (pkg mgr wrappers) ────────┤
                                   │
US-7.3 (lang wrappers) ───────────┘

US-7.4 (RuntimePolicy CRD) ── US-7.5 (workspace spec)

US-7.6 (RTE cleanup) ── independent
US-7.8 (delete legacy) ── independent
```

## Key Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Sidecar for daemon in K8s, no daemon in Docker | K8s gets proper container isolation between root daemon and UID 1000 agent. Docker skips the security layer entirely (homelab, trusted). |
| D2 | Sentinel file controls mode | One image, two behaviors. No build-time branching. Kubernetes mounts it via ConfigMap; Docker users ignore it or opt in. |
| D3 | Drop ReadOnlyRootFilesystem on main container | Agent must install packages (pip/npm to user paths). Security comes from UID 1000 + file ownership, not filesystem flags. |
| D4 | Wrappers are compiled Go binaries | <1ms overhead. Static. No dependency on wrapped language. Cannot be modified by UID 1000 (root-owned). |
| D5 | Binary relocation at build time | Real binaries at /opt/llmsafespace/.bin/ (root:root 750). Not in PATH. Not accessible to UID 1000. |
| D6 | Daemon only handles apt/apk | pip/npm/cargo/go work as UID 1000 directly. Only system package managers need root. Minimizes daemon scope and attack surface. |
| D7 | Policy is optional | No sentinel or no policy = passthrough. Supported runtimes get hardening. Unsupported runtimes just work. |
| D8 | RuntimeClass for multi-tenant isolation | Same image, swap runtime (gVisor/Kata/Firecracker). No code changes needed. |
| D9 | Main container keeps RunAsNonRoot in K8s | Agent never has root. Only the sidecar has root with minimal caps. Clean separation. |

## Security Comparison

| Property | Before (V1/current) | After — Docker (no sentinel) | After — K8s (sentinel present) |
|----------|---------------------|------------------------------|-------------------------------|
| Root in container | No | Yes (everything) | Yes (daemon sidecar only) |
| Agent runs as | UID 1000 | root | UID 1000 |
| Agent can write /usr/bin | No (read-only rootfs) | Yes (root) | No (root-owned, UID 1000 can't write) |
| Agent can apt install | No | Yes (direct) | Via daemon only (policy-gated) |
| Agent can pip/npm install | To /workspace only | Anywhere | Anywhere UID 1000 can write (policy-gated) |
| Policy enforcement | None | None (opt-in via sentinel) | Full (wrappers active) |
| Container escape risk | Low | N/A (homelab, trusted) | Medium → mitigated by RuntimeClass |
| Multi-tenant ready | Yes (but agent can't function) | No | Yes (add RuntimeClass: gvisor) |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Container escape via root daemon | Very Low | High | Minimal caps, seccomp, RuntimeClass for multi-tenant |
| Daemon socket abuse via prompt injection | Medium | Low | Allowlist, rate limit, source restrictions. Same risk as agent having shell access at all. |
| Wrapper breaks package manager flags | Medium | Medium | Passthrough by default; only block explicitly disallowed flags |
| Agent bypasses wrapper (downloads binary to /workspace) | Medium | Low | Seccomp is hard boundary. Downloaded binaries still run as UID 1000 under same restrictions. |

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
- Controller changes: drop ReadOnlyRootFilesystem, drop RunAsNonRoot, add capabilities, add runtimeClassName
- Net: +500 lines but replaces dead code with working infrastructure
- New binaries: `cmd/system-daemon/` (~400 lines), `cmd/wrapper/` (~300 lines)
