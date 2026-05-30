# syntax=docker/dockerfile:1.7
# Build the controller manager binary.
# Build context MUST be the repository root (single Go module at root).
#
#   docker build -f controller/Dockerfile -t llmsafespace/controller:dev .

FROM golang:1.25 AS builder

WORKDIR /workspace

# Allow operators to override the Go module proxy at build time. Useful in
# environments where proxy.golang.org is unreachable (e.g. some corporate
# VPNs); set --build-arg GOPROXY=direct to fetch modules directly.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY controller/ controller/
COPY pkg/ pkg/
COPY mocks/ mocks/
COPY api/ api/
COPY cmd/ cmd/

ARG VERSION=dev
ARG BUILD_TIME=unknown

ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build \
        -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
        -o /out/manager \
        ./controller

FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/manager /usr/local/bin/manager

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/manager"]
