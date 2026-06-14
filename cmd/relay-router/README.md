# relay-router

In-cluster HTTP router that distributes workspace traffic across healthy relay VMs via WireGuard tunnels. Runs as a Deployment (1 replica) with a WireGuard sidecar.

## Responsibilities

1. **Weighted relay selection** — OCI gets 100% of traffic when healthy; GCP receives traffic only during OCI failure or rotation
2. **Health checking** — Probes each relay `/healthz` every 15s over WG; marks unhealthy after 3 consecutive failures (45s)
3. **429 detection (two-tier)** — Immediate probe on first 429; storm detection via 5-minute rolling window at 50% threshold
4. **Failover** — When OCI goes down, all traffic routes to GCP
5. **Fallback mode** — When both relays are down, proxies directly to upstream at 1 req/2s, max 1 concurrent

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | 200 OK — router health |
| `GET` | `/metrics` | Prometheus metrics |
| `*` | `/*` | Reverse proxy to selected relay (or fallback) |

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `relay_router_requests_total{relay,status}` | counter | Requests routed per relay by HTTP status |
| `relay_router_active_streams{relay}` | gauge | In-flight streaming connections per relay |
| `relay_router_relay_healthy{relay}` | gauge | Router's health view (1=healthy, 0=unhealthy) |
| `relay_router_relay_egress_bytes{relay}` | counter | Per-relay egress bytes (for GCP quota tracking) |
| `relay_router_fallback_active` | gauge | 1 when in direct fallback mode |

## Configuration (env vars)

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | Listen address |
| `UPSTREAM_URL` | `https://opencode.ai/zen/v1` | Direct fallback upstream |
| `PEER_CONFIG_PATH` | `/etc/relay-router/peers.json` | ConfigMap mount path |
| `PEER_POLL_INTERVAL` | `5s` | ConfigMap re-read interval |
| `HEALTH_INTERVAL` | `15s` | Health check interval |
| `HEALTH_TIMEOUT` | `5s` | Health check timeout |
| `HEALTH_THRESHOLD` | `3` | Consecutive failures → unhealthy |
| `RELAY_PORT` | `8080` | Relay VM proxy port |
| `MAX_429_RATE` | `0.5` | 429 rate threshold for storm detection |
| `DETECTION_WINDOW` | `5m` | Rolling window for 429 counting |
| `FALLBACK_RATE` | `0.5` | Fallback rate limit (req/s) |
| `FALLBACK_MAX_CONCURRENT` | `1` | Max concurrent fallback requests |

## Peer ConfigMap

The controller writes `relay-router-peers` ConfigMap in this JSON format:

```json
{
  "relays": [
    {"id": "oci-1", "wgIP": "10.42.42.2", "provider": "oci", "state": "healthy"},
    {"id": "gcp-1", "wgIP": "10.42.42.3", "provider": "gcp", "state": "healthy"}
  ]
}
```

## Deployment

See `design/stories/epic-42-multi-cloud-inference-relay/README.md` Layer 3 for architecture details. Helm chart integration in US-42.10.
