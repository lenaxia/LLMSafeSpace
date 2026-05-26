FROM golang:1.25-bookworm AS redact-builder

ARG TARGETARCH=amd64

WORKDIR /src

# Allow operators to override the Go module proxy at build time. Useful in
# environments where proxy.golang.org is unreachable (e.g. some corporate
# VPNs); set --build-arg GOPROXY=direct to fetch modules directly.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -o /out/redact ./cmd/redact

FROM debian:bookworm-slim@sha256:0104b334637a5f19aa9c983a91b54c89887c0984081f2068983107a6f6c21eeb

ARG TARGETARCH=amd64

# Override to use a different release: --build-arg OPENCODE_VERSION=<version>
# Pinned to 1.2.27 (March 2026) — matches the version validated against the
# proxy code in api/internal/handlers/proxy.go. Newer releases may introduce
# HTTP API changes; bump deliberately and re-validate the proxy session/event
# paths before changing this default.
ARG OPENCODE_VERSION=1.2.27

ARG MISE_VERSION=2026.5.15

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Install opencode from upstream releases.
#
# Source: https://github.com/anomalyco/opencode/releases
# Asset naming: opencode-linux-{x64,arm64}.tar.gz
#   (Note: x64, NOT amd64, despite Docker's TARGETARCH using amd64.)
#
# SECURITY NOTE: Upstream does not publish .sha256 or signature files for
# release assets. Transport integrity relies on TLS to github.com plus curl's
# --fail flag rejecting non-2xx responses. For SLSA-grade verification, the
# correct path is `cosign verify-blob` against the Sigstore OIDC signature,
# but anomalyco/opencode does not currently publish Sigstore attestations.
# Track upstream: https://github.com/anomalyco/opencode/issues/?q=cosign
#
# When upstream adds .sha256 or Sigstore signatures, restore verification here.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) OC_ARCH=x64 ;; \
        arm64) OC_ARCH=arm64 ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl --fail --show-error --location \
        "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${OC_ARCH}.tar.gz" \
        -o /tmp/opencode.tar.gz; \
    tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin/ opencode; \
    chmod +x /usr/local/bin/opencode; \
    rm /tmp/opencode.tar.gz

# Install mise (polyglot runtime manager) so sandbox users can install
# language runtimes (Python, Node.js, Go, etc.) without root access.
#
# Source: https://github.com/jdx/mise/releases
# Asset naming: mise-v{version}-linux-{x64,arm64}.tar.gz
#   (x64, NOT amd64 — same convention as the opencode assets above.)
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) MISE_ARCH=x64 ;; \
        arm64) MISE_ARCH=arm64 ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl --fail --show-error --location \
        "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-${MISE_ARCH}.tar.gz" \
        -o /tmp/mise.tar.gz; \
    tar -xzf /tmp/mise.tar.gz -C /tmp/; \
    cp /tmp/mise/bin/mise /usr/local/bin/mise; \
    chmod +x /usr/local/bin/mise; \
    rm -rf /tmp/mise /tmp/mise.tar.gz

COPY --from=redact-builder --chmod=755 /out/redact /usr/local/bin/redact

COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-common.sh  /usr/local/bin/entrypoint-common.sh
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-opencode.sh /usr/local/bin/entrypoint-opencode.sh

COPY --chmod=755 runtimes/base/tools/smoke-test.sh /usr/local/bin/smoke-test.sh

# Pre-install runtimes into the system mise data dir so they are baked into
# the image layer and always available regardless of PVC state.
#
# mise --system installs into /usr/local/share/mise (MISE_SYSTEM_DATA_DIR).
# At runtime, MISE_DATA_DIR points at the PVC so agent installs persist there.
# mise resolves tools by checking MISE_DATA_DIR first, then the system dir,
# so image-layer tools are the fallback and PVC tools always win.
#
# MISE_YES=1                  — non-interactive (skip trust prompts)
# MISE_GITHUB_ATTESTATIONS=0  — skip GitHub attestation checks (no outbound
#                               OIDC calls in restricted build environments)
RUN MISE_YES=1 MISE_GITHUB_ATTESTATIONS=0 \
    mise install --system \
        python@latest \
        node@lts \
        rust@stable \
        go@latest \
        java@lts \
        maven@latest \
        gradle@latest \
    && mise --system reshim

RUN /usr/local/bin/smoke-test.sh

# --- Runtime environment variables ---
#
# MISE_DATA_DIR points at the PVC so everything the agent installs persists
# across suspend/resume cycles without reinstallation.
#
# Package manager homes are redirected to the PVC for the same reason.
# The PATH additions expose binaries from those homes immediately.
ENV MISE_DATA_DIR=/workspace/.local/share/mise \
    CARGO_HOME=/workspace/.local/share/cargo \
    GEM_HOME=/workspace/.local/share/gem \
    GOPATH=/workspace/.local/share/go \
    NPM_CONFIG_PREFIX=/workspace/.local \
    PYTHONUSERBASE=/workspace/.local \
    PATH=/workspace/.local/bin:/workspace/.local/share/go/bin:/workspace/.local/share/cargo/bin:/usr/local/share/mise/shims:$PATH

RUN useradd -u 1000 -m -s /bin/bash sandbox

# /tmp and /home/sandbox require emptyDir volumes in the pod spec (readOnlyRootFilesystem: true)
USER sandbox
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]
