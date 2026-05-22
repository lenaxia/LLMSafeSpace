FROM golang:1.23-bookworm AS redact-builder

ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -o /out/redact ./cmd/redact

FROM debian:bookworm-slim@sha256:40b107342c492725bc7aacbe93a49945f37a4143b8e1e87f2a6ff36f3f27a4ab

ARG TARGETARCH=amd64

# Override to use a different release: --build-arg OPENCODE_VERSION=<version>
ARG OPENCODE_VERSION=1.0.0

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://github.com/opencode-ai/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${TARGETARCH}" \
         -o /usr/local/bin/opencode \
    && curl -fsSL "https://github.com/opencode-ai/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${TARGETARCH}.sha256" \
         -o /tmp/opencode.sha256 \
    && echo "$(cat /tmp/opencode.sha256)  /usr/local/bin/opencode" | sha256sum --check \
    && chmod +x /usr/local/bin/opencode \
    && rm /tmp/opencode.sha256

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
