# syntax=docker/dockerfile:1.7
# Build the relay-router binary.
# Build context MUST be the repository root (single Go module at root).
#
#   docker build -f cmd/relay-router/Dockerfile -t llmsafespaces/relay-router:dev .

ARG TARGETARCH=amd64

FROM golang:1.25 AS builder

WORKDIR /workspace

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -o /out/relay-router \
        ./cmd/relay-router

FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/relay-router /usr/local/bin/relay-router

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/relay-router"]
