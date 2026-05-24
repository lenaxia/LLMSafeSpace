# US-7.7: Base Dockerfile Rewrite

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.1 (daemon binary), US-7.2 (wrapper binary)

## Objective

Rewrite `runtimes/base/Dockerfile` to include the system daemon, wrapper binary, binary relocation, and policy file structure. Single image works in both Docker (passthrough) and Kubernetes (full enforcement) based on a sentinel file.

## Design

### Sentinel File

`/etc/llmsafespace/mode` — its presence activates enforcement. Contents are optional config:

```json
{
  "enforcement": "full",
  "daemon": "/run/llmsafespace/system.sock"
}
```

If the file exists but is empty, defaults apply:
- `enforcement`: `"full"` (all wrappers enforce policy)
- `daemon`: `"/run/llmsafespace/system.sock"` (apt wrapper connects here)

If the file does not exist:
- Wrappers are pure passthrough (exec real binary immediately)
- No daemon expected (Docker mode — apt works directly as root)

### Deployment Modes

| Environment | Sentinel | Behavior |
|-------------|----------|----------|
| Docker (homelab) | Absent | Root, no enforcement, no daemon. Just opencode. |
| Docker (security-conscious) | Bind-mounted | Enforcement active, daemon runs, privilege separation |
| Kubernetes | Mounted via ConfigMap by controller | Full enforcement, daemon, UID separation |

### Build Stages

```dockerfile
# Stage 1: Build redact (existing)
FROM golang:1.25-bookworm AS redact-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 go build -trimpath -o /out/redact ./cmd/redact

# Stage 2: Build system daemon
FROM golang:1.25-bookworm AS daemon-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 go build -trimpath -o /out/system-daemon ./cmd/system-daemon

# Stage 3: Build wrapper (multi-call binary)
FROM golang:1.25-bookworm AS wrapper-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 go build -trimpath -o /out/wrapper ./cmd/wrapper

# Stage 4: Final image
FROM debian:bookworm-slim@sha256:...

ARG TARGETARCH=amd64
ARG OPENCODE_VERSION=1.2.27

ENV DEBIAN_FRONTEND=noninteractive

# Install base tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip \
    && rm -rf /var/lib/apt/lists/*

# Install opencode (existing logic)
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) OC_ARCH=x64 ;; \
        arm64) OC_ARCH=arm64 ;; \
        *) exit 1 ;; \
    esac; \
    curl --fail --show-error --location \
        "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${OC_ARCH}.tar.gz" \
        -o /tmp/opencode.tar.gz; \
    tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin/ opencode; \
    chmod +x /usr/local/bin/opencode; \
    rm /tmp/opencode.tar.gz

# Copy built binaries
COPY --from=redact-builder --chmod=755 /out/redact /usr/local/bin/redact
COPY --from=daemon-builder --chmod=755 /out/system-daemon /usr/local/bin/system-daemon
COPY --from=wrapper-builder --chmod=755 /out/wrapper /opt/llmsafespace/wrapper

# Binary relocation: move real binaries to hidden location
RUN mkdir -p /opt/llmsafespace/.bin && \
    mv /usr/bin/apt /opt/llmsafespace/.bin/apt && \
    mv /usr/bin/apt-get /opt/llmsafespace/.bin/apt-get && \
    chmod 750 /opt/llmsafespace/.bin && \
    chown root:root /opt/llmsafespace/.bin/*

# Install wrappers at original paths (hard links to multi-call binary)
RUN ln -f /opt/llmsafespace/wrapper /usr/bin/apt && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/apt-get

# Language runtime/pkg manager wrappers (binaries don't exist yet in base,
# but wrapper handles "not installed" gracefully)
RUN ln -f /opt/llmsafespace/wrapper /usr/bin/python3 && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/pip3 && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/node && \
    ln -f /opt/llmsafespace/wrapper /usr/local/bin/npm && \
    ln -f /opt/llmsafespace/wrapper /usr/local/bin/go

# Policy file structure (empty — populated by ConfigMap or bind mount)
RUN mkdir -p /etc/llmsafespace/policies \
             /etc/llmsafespace/daemon \
             /var/log/llmsafespace \
             /run/llmsafespace

# Default policy files (import hooks, restricted module lists)
COPY runtimes/base/policies/ /opt/llmsafespace/policies/

# Entrypoints
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-common.sh /usr/local/bin/
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-opencode.sh /usr/local/bin/

# Smoke test
COPY --chmod=755 runtimes/base/tools/smoke-test.sh /usr/local/bin/smoke-test.sh
RUN /usr/local/bin/smoke-test.sh

# Create sandbox user
RUN useradd -u 1000 -m -s /bin/bash sandbox

# NOTE: No sentinel file (/etc/llmsafespace/mode) by default.
# Docker: runs as root, wrappers passthrough, no daemon.
# Kubernetes: controller mounts sentinel via ConfigMap → wrappers activate.
#             controller adds daemon sidecar container separately.

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]
```

### Entrypoint

In Docker: entrypoint runs opencode directly as root. No sentinel, no daemon, no wrappers active.

In Kubernetes: same entrypoint runs opencode as UID 1000 (pod security context sets RunAsUser). Wrappers detect sentinel and enforce policy. Daemon is a separate sidecar container.

### Wrapper Behavior Based on Sentinel

```go
// cmd/wrapper/main.go
func main() {
    name := filepath.Base(os.Args[0])
    realBin := filepath.Join("/opt/llmsafespace/.bin", name)
    
    if !modeEnabled() {
        // No sentinel — passthrough. Exec real binary (or original path if not relocated).
        if _, err := os.Stat(realBin); err == nil {
            syscall.Exec(realBin, os.Args, os.Environ())
        }
        // Real binary not at relocated path — maybe not installed yet
        fmt.Fprintf(os.Stderr, "%s: command not found\n", name)
        os.Exit(127)
    }
    
    // Sentinel present — full interception
    switch {
    case isSystemPkgMgr(name):
        handleSystemInstall(name)
    case isLangPkgMgr(name):
        handleLangPkgMgr(name)
    default:
        handleLanguageRuntime(name)
    }
}

func modeEnabled() bool {
    _, err := os.Stat("/etc/llmsafespace/mode")
    return err == nil
}
```

### File Ownership Summary

```
root:root  755  /usr/local/bin/system-daemon
root:root  755  /opt/llmsafespace/wrapper
root:root  755  /usr/bin/apt              (hard link to wrapper)
root:root  755  /usr/bin/python3          (hard link to wrapper)
root:root  750  /opt/llmsafespace/.bin/   (real binaries, hidden)
root:root  755  /opt/llmsafespace/policies/
root:root  755  /usr/local/bin/entrypoint-opencode.sh
sandbox    1000 /workspace                (writable)
sandbox    1000 /home/sandbox             (writable)
```

## Files Modified

| File | Change |
|------|--------|
| `runtimes/base/Dockerfile` | Full rewrite per above |
| `runtimes/base/tools/entrypoints/entrypoint-opencode.sh` | No changes (daemon calls it) |
| `runtimes/base/policies/` | New directory with default policy files |
| `runtimes/base/tools/smoke-test.sh` | Update to verify wrapper + daemon binaries exist |

## Acceptance Criteria

1. `docker build` succeeds, image <250MB
2. **Docker mode** (no sentinel): `docker run` → opencode starts as root, `apt install python3` works directly (wrapper passthrough)
3. **K8s mode** (sentinel mounted): wrappers enforce policy, apt wrapper connects to daemon socket
4. Wrappers exist at `/usr/bin/apt`, `/usr/bin/python3`, etc. (hard links to multi-call binary)
5. Real apt binary at `/opt/llmsafespace/.bin/apt` (root:root 750)
6. `python3` wrapper with no sentinel → passthrough (or "not installed" if python3 isn't installed)
7. `python3` wrapper with sentinel → policy enforcement
8. Image works identically with `docker run`, `docker-compose`, and Kubernetes
9. System daemon binary exists at `/usr/local/bin/system-daemon` (used by sidecar in K8s)
