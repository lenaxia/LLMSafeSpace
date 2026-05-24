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

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip \
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

COPY --from=redact-builder --chmod=755 /out/redact /usr/local/bin/redact

COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-common.sh  /usr/local/bin/entrypoint-common.sh
COPY --chmod=755 runtimes/base/tools/entrypoints/entrypoint-opencode.sh /usr/local/bin/entrypoint-opencode.sh

COPY --chmod=755 runtimes/base/tools/smoke-test.sh /usr/local/bin/smoke-test.sh
RUN /usr/local/bin/smoke-test.sh

RUN useradd -u 1000 -m -s /bin/bash sandbox

# /tmp and /home/sandbox require emptyDir volumes in the pod spec (readOnlyRootFilesystem: true)
USER sandbox
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]
