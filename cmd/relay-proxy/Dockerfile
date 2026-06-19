# syntax=docker/dockerfile:1.7
# Build the relay-proxy binary (runs on relay VMs, NOT in-cluster).
# This image is pushed to the registry so cloud-init can pull it.
#
#   docker build -f cmd/relay-proxy/Dockerfile -t llmsafespaces/relay-proxy:dev .

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
        -o /out/relay-proxy \
        ./cmd/relay-proxy

FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/relay-proxy /usr/local/bin/relay-proxy

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/relay-proxy"]
