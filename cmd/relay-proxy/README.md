# relay-proxy

Standalone Go binary that transparently proxies HTTP requests to an upstream LLM endpoint (default `ai.thekao.cloud/v1`). Runs on OCI and GCP free-tier VMs, reachable only via WireGuard tunnels. No TLS, no auth — WireGuard is the security boundary.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | `200 OK` (no body) — for controller/router health checks |
| `GET` | `/metrics` | Prometheus-format metrics |
| `*` | `/*` | Transparent proxy to `UPSTREAM_URL` |

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `relay_requests_total{status}` | counter | Proxied request count by HTTP status code |
| `relay_egress_bytes_total` | counter | Total response body bytes proxied |
| `relay_keepalive_total` | counter | Keepalive probes sent to upstream |

## Configuration (env vars)

| Variable | Default | Description |
|----------|---------|-------------|
| `UPSTREAM_URL` | `https://ai.thekao.cloud/v1` | Upstream LLM endpoint |
| `LISTEN_ADDR` | `10.42.42.2:8080` | Listen address (WG interface only) |
| `KEEPALIVE_INTERVAL` | `30s` | Upstream probe interval |

## Build

```bash
# From project root — cross-compiles for both architectures
make relay-bin

# Manual
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o deploy/relay-proxy-arm64 ./cmd/relay-proxy/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deploy/relay-proxy-amd64 ./cmd/relay-proxy/
```

## Run locally

```bash
UPSTREAM_URL=https://ai.thekao.cloud/v1 \
LISTEN_ADDR=127.0.0.1:8080 \
go run ./cmd/relay-proxy/
```

## Deployment

Deployed via cloud-init on OCI/GCP VMs. See `design/stories/epic-42-multi-cloud-inference-relay/README.md` Layer 7 for the cloud-init template.
