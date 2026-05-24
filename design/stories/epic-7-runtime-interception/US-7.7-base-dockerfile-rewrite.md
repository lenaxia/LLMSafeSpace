# US-7.7: Base Dockerfile Rewrite

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.1 (daemon binary), US-7.2 (wrapper binary), US-7.3 (policy files)

## Objective

Rewrite `runtimes/base/Dockerfile` to include the system daemon, wrapper binary, binary relocation, immutability setup, and policy file structure. The image remains slim — no language toolchains pre-installed.

## Design

### Build Stages

```dockerfile
# Stage 1: Build redact (existing)
FROM golang:1.25-bookworm AS redact-builder
# ... existing redact build ...

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

# Install base tools (existing)
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip \
    && rm -rf /var/lib/apt/lists/*

# Install opencode (existing)
# ...

# Copy built binaries
COPY --from=redact-builder --chmod=755 /out/redact /usr/local/bin/redact
COPY --from=daemon-builder --chmod=755 /out/system-daemon /usr/local/bin/system-daemon
COPY --from=wrapper-builder --chmod=755 /out/wrapper /opt/llmsafespace/wrapper

# Binary relocation: move real binaries to hidden location
RUN mkdir -p /opt/llmsafespace/.bin && \
    cp /usr/bin/apt /opt/llmsafespace/.bin/apt && \
    cp /usr/bin/apt-get /opt/llmsafespace/.bin/apt-get && \
    chmod 750 /opt/llmsafespace/.bin/*

# Install wrappers at original paths (hardlinks to multi-call binary)
RUN ln -f /opt/llmsafespace/wrapper /usr/bin/apt && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/apt-get && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/pip3 && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/pip && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/python3 && \
    ln -f /opt/llmsafespace/wrapper /usr/bin/node && \
    ln -f /opt/llmsafespace/wrapper /usr/local/bin/npm && \
    ln -f /opt/llmsafespace/wrapper /usr/local/bin/go

# Make wrappers immutable (cannot be replaced even by root at runtime)
RUN chattr +i /usr/bin/apt /usr/bin/apt-get /usr/bin/pip3 /usr/bin/pip \
    /usr/bin/python3 /usr/bin/node /usr/local/bin/npm /usr/local/bin/go \
    /opt/llmsafespace/wrapper

# Policy file structure (empty defaults — populated by ConfigMap mounts)
RUN mkdir -p /etc/llmsafespace/policies/python \
             /etc/llmsafespace/policies/nodejs \
             /etc/llmsafespace/policies/go \
             /etc/llmsafespace/daemon \
             /var/log/llmsafespace \
             /run/llmsafespace

# Copy default policy files (import hooks, restricted module lists)
COPY runtimes/base/policies/ /opt/llmsafespace/policies/

# Daemon socket directory
RUN mkdir -p /run/llmsafespace && chown root:sandbox /run/llmsafespace && chmod 750 /run/llmsafespace

# Audit log directory
RUN mkdir -p /var/log/llmsafespace && chown root:sandbox /var/log/llmsafespace && chmod 770 /var/log/llmsafespace

# Entrypoints (existing + daemon)
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-common.sh /usr/local/bin/
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-opencode.sh /usr/local/bin/

# User setup (existing)
RUN useradd -u 1000 -m -s /bin/bash -g sandbox sandbox 2>/dev/null || true

USER root
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/system-daemon"]
# Daemon starts, creates socket, then execs entrypoint-opencode.sh as sandbox user
```

### Entrypoint Chain

```
system-daemon (PID 1, root)
  ├── creates /run/llmsafespace/system.sock
  ├── loads policy from /etc/llmsafespace/daemon/policy.json
  ├── forks: entrypoint-opencode.sh (as sandbox user, UID 1000)
  │     └── opencode serve
  └── listens on socket for install requests
```

The daemon is PID 1. It handles signals (SIGTERM → graceful shutdown). It forks the opencode entrypoint as the sandbox user. This avoids needing a separate sidecar container.

### Note on Wrapper Targets

The wrapper binary handles binaries that don't exist yet (python3, node, go aren't installed in the base image). The wrapper detects "real binary not found" and returns a helpful message:

```
$ python3 --version
[llmsafespace] python3 is not installed. Install with: apt install python3
```

This is better UX than the default "command not found" because it tells the agent exactly what to do.

## Files Modified

| File | Change |
|------|--------|
| `runtimes/base/Dockerfile` | Full rewrite per above |
| `runtimes/base/tools/entrypoints/entrypoint-opencode.sh` | Remove PID 1 logic (daemon is now PID 1) |
| `runtimes/base/policies/` | New directory with default policy files |

## Acceptance Criteria

1. `docker build` succeeds
2. Container starts with daemon as PID 1
3. Socket exists at `/run/llmsafespace/system.sock` with correct permissions
4. `apt install curl` from inside container (as sandbox user) succeeds via daemon
5. Wrappers are immutable (`lsattr` shows `i` flag)
6. Real binaries at `/opt/llmsafespace/.bin/` are not accessible to sandbox user
7. `opencode serve` starts successfully as sandbox user
8. `python3` (not installed) returns helpful "not installed" message
9. Image size remains <200MB
