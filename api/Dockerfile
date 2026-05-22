# syntax=docker/dockerfile:1.7
# Build the API server binary.
# Build context MUST be the repository root (single Go module at root).
#
#   docker build -f api/Dockerfile -t llmsafespace/api:dev .

FROM golang:1.23 AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY api/ api/
COPY pkg/ pkg/
COPY mocks/ mocks/
COPY cmd/ cmd/
COPY controller/ controller/

ARG VERSION=dev
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
        -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
        -o /out/api \
        ./api/cmd/api

FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/api /usr/local/bin/api
COPY api/config/config.yaml /etc/llmsafespace/config.yaml
COPY api/migrations /etc/llmsafespace/migrations

ENV CONFIG_PATH=/etc/llmsafespace/config.yaml

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
