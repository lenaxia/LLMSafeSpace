# Worklog: Epic 42 — Relay Router, WireGuard, Fallback (US-42.4 + US-42.7 + US-42.11)

**Date:** 2026-06-14
**Session:** Continued Epic 42 — WireGuard keypair gen, relay router, fallback mode
**Status:** Complete

---

## Objective

Continue implementation of Epic 42 critical path. Implemented the three stories that have no cloud dependencies:
- **US-42.4**: WireGuard keypair generation + config rendering
- **US-42.7**: Relay router (weighted selection, health checking, 429 detection, ConfigMap poll, metrics)
- **US-42.11**: Fallback mode (rate-limited direct routing when all relays unhealthy)

---

## Work Completed

### US-42.4: WireGuard Keypair Generation (`controller/internal/relay/wireguard.go`)

**Files created:**
- `controller/internal/relay/wireguard.go` — `GenerateKeypair()` using `crypto/ecdh` X25519 + `crypto/rand`, `DerivePublicKey()` for verification, `RenderRelayConfig()` for relay VM wg0.conf, `RenderRouterConfig()` for router wg0.conf with multiple peers
- `controller/internal/relay/wireguard_test.go` — 21 tests: keypair format/derivation/uniqueness/clamping, DerivePublicKey valid/invalid/wrong-length/empty, relay config rendering (all fields, ordering, defaults, validation), router config rendering (single/multiple peers, defaults, validation), integration test (generate keypair → render configs → verify round-trip)

**Key design decisions:**
- Uses `crypto/ecdh` (Go stdlib, available since Go 1.20) instead of `golang.org/x/crypto/curve25519` — avoids external dependency
- Private keys are base64-encoded 32-byte X25519 scalars, matching `wg` and `wg-quick` format
- `PersistentKeepalive = 25` default (standard NAT-traversal interval per WireGuard docs)
- Router config does NOT set `Endpoint` on peers — relay VMs connect to the router (server role), not vice versa
- Relay config's `AllowedIPs = 10.42.42.0/24` gives the relay full mesh reachability

### US-42.7: Relay Router (`cmd/relay-router/`)

The relay router is a Go HTTP server that distributes workspace traffic across healthy relay VMs. This is the only endpoint workspace pods talk to.

**Files created:**
- `cmd/relay-router/fleet.go` — `relayFleet` (thread-safe central state), `SelectRelay()` (weighted random: OCI=100%, GCP=0 when OCI healthy), `RecordRequest/HealthCheck/StreamStart/StreamEnd/Egress`, `Mark429Draining/Suspect`, `Check429Rate()` with window pruning, `ParsePeerConfig()` for ConfigMap JSON
- `cmd/relay-router/fleet_test.go` — 37 tests: peer config parsing, fleet updates (add/remove/preserve/update), relay selection (OCI primary, GCP failover, no healthy, 429 draining, empty fleet), health check recording (success clears, failures mark unhealthy), request/429 recording, stream tracking, egress tracking, 429 state management, HasHealthyRelay, weight function, concurrency safety, JSON round-trip
- `cmd/relay-router/health.go` — `healthChecker` background goroutine: probes `GET http://<wgIP>:<port>/healthz` every 15s, marks unhealthy after 3 failures, parallel health checks via WaitGroup
- `cmd/relay-router/detector.go` — Two-tier 429 detection: `OnResponse()` triggers immediate probe on first 429 (Tier 1), `runPeriodicCheck()` evaluates 429 storm rate every 30s (Tier 2), marks draining when rate ≥ threshold
- `cmd/relay-router/metrics.go` — `routerMetrics` Prometheus metrics: `relay_router_requests_total{relay,status}`, `relay_router_active_streams{relay}`, `relay_router_relay_healthy{relay}`, `relay_router_relay_egress_bytes{relay}`, `relay_router_fallback_active`
- `cmd/relay-router/proxy.go` — `routerProxy` HTTP handler: selects relay → forwards with streaming pass-through → records metrics → triggers 429 detector; strips `X-Workspace-Id` header; `fallbackProxy` for direct routing
- `cmd/relay-router/proxy_test.go` — 19 tests: proxy forwarding, fallback when no relays, 502 when no fallback, workspace header stripping, fallback rate limiting, concurrency limiting, fallback header, streaming, invalid URL, upstream errors, health checker success/failure/cancel, detector 429 clearing/storm detection, metrics format/fallback/multiple relays, 2 E2E integration tests
- `cmd/relay-router/main.go` — Entry point: config loading (14 env vars), peer ConfigMap polling (5s), health checker goroutine, 429 detector goroutine, graceful shutdown
- `cmd/relay-router/README.md` — Deployment guide with endpoints, metrics, configuration, peer ConfigMap format

### US-42.11: Fallback Mode (`cmd/relay-router/proxy.go` — `fallbackProxy`)

Implemented as part of the relay router. When no relays are healthy:
- **Token bucket rate limit** — 1 req/2s global (configurable via `FALLBACK_RATE`)
- **Concurrency cap** — max 1 in-flight request (configurable via `FALLBACK_MAX_CONCURRENT`)
- **`X-Relay-Status: fallback` header** on all responses for frontend degraded-mode banner
- **429 + Retry-After: 2** for rate-limited/concurrent-limited requests
- **Streaming pass-through** same as relay path
- **Auto-recovery** — exits fallback automatically as soon as any relay passes health check (SelectRelay returns ok)

---

## Key Decisions

1. **OCI gets deterministic 100% when healthy, not probabilistic.** The initial weighted random implementation gave GCP ~1% even when OCI was healthy (weight 100 vs 1). The test `TestSelectRelay_OCIPrimaryWhenBothHealthy` caught this: GCP was selected on iteration 26, 69, 77. Fixed by zeroing GCP weight when any OCI relay is eligible. The design says "OCI receives 100% of traffic when healthy" — that's literal, not probabilistic.

2. **`crypto/ecdh` instead of `golang.zx2.dev/wireguard` or `golang.org/x/crypto/curve25519`.** Go 1.25 has `crypto/ecdh` with X25519 support in stdlib. This avoids adding a dependency for 3 function calls. The generated keys are compatible with `wg` and `wg-quick`.

3. **Canonical header key matching.** `X-Workspace-ID` (with capital D) is NOT Go's canonical form — `http.CanonicalHeaderKey("X-Workspace-ID")` produces `"X-Workspace-Id"` (lowercase d). The hop-by-hop header map must use canonical keys. The `TestRouterProxy_StripsWorkspaceHeader` test caught this — the header was passing through to the relay because the map lookup missed.

4. **Fallback rate limiting is local to the router replica.** Single replica deployment means no distributed coordination needed. Token bucket with mutex is sufficient.

5. **429 window pruning happens lazily** on every rate check, not on a timer. This is O(n) per prune but n is small (429s in a 5-minute window, typically <100).

---

## Assumptions Stated and Validated

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `crypto/ecdh` X25519 keys are compatible with WireGuard's key format | ✅ Verified: WG uses Curve25519 (X25519). `ecdh.X25519().GenerateKey()` produces the same 32-byte scalar + 32-byte public key that `wg genkey` / `wg pubkey` produce. Both are base64-encoded 32 bytes. |
| A2 | `http.CanonicalHeaderKey` normalizes header names for map lookups | ✅ Verified: Go stdlib documentation confirms canonical form is used for all `Header.Get/Set/Add` operations. |
| A3 | Weighted random with OCI=100, GCP=0 when OCI healthy is equivalent to "100% to OCI" | ✅ Validated by test: `TestSelectRelay_OCIPrimaryWhenBothHealthy` runs 100 iterations, all select OCI. |
| A4 | ConfigMap polling at 5s is sufficient for 2 relays | ✅ Validated by design doc: "At 2 relays and a 5s poll interval, the cost is negligible." (Layer 3) |

---

## Blockers

1. **US-42.2 (day-one validation gate)** — Requires manual VM deployment on OCI + GCP to verify `opencode.ai/zen` doesn't block their IP ranges. Cannot be done in this environment. This is the epic's cheapest de-risking step and should be done before any cloud driver work (US-42.5, US-42.6).

2. **Full project build still limited by disk space** in this environment. Individual packages build and test cleanly.

---

## Tests Run

```bash
# WireGuard keypair + config rendering (21 tests)
go test -timeout 30s -race -count=1 ./controller/internal/relay/
# ok  1.129s

# Relay router + fallback (56 tests)
go test -timeout 30s -race -count=1 ./cmd/relay-router/
# ok  1.644s

# All Epic 42 packages together
go test -timeout 30s -race -count=1 ./cmd/relay-router/ ./controller/internal/relay/ ./cmd/relay-proxy/ ./pkg/apis/llmsafespace/v1/
# ok  all 4 packages

# go vet
go vet ./cmd/relay-router/ ./controller/internal/relay/
# clean

# gofmt
gofmt -l cmd/relay-router/ controller/internal/relay/
# clean
```

---

## Next Steps

1. **US-42.2: Day-one validation gate** — Deploy relay-proxy binary on an OCI A1 VM and a GCP e2-micro, curl `opencode.ai/zen/v1/models` from each. If either IP range is blocked, the epic premise fails. **This is the #1 priority before any cloud driver work.**

2. **US-42.8: MetalLB + router WireGuard sidecar** — Install MetalLB, create the relay-router Deployment with WireGuard sidecar (NET_ADMIN capability), LoadBalancer Service on UDP 51820, NetworkPolicy.

3. **US-42.5 + US-42.6: OCI and GCP provider drivers** — Implement `ProviderDriver` interface with Provision/Destroy/GetStatus methods. Depends on US-42.2 validation passing.

4. **US-42.9: InferenceRelay reconciler** — Full lifecycle management: provision, health via router /metrics, graceful drain, destroy+recreate, ConfigMap sync, provisioning circuit breaker, egress quota tracking.

5. **US-42.10: Helm chart integration** — CRD YAML, router Deployment+Service+PDB, NetworkPolicy, controller flags, WG Secret.

6. **US-42.12: Observability** — Prometheus alert rules, CR conditions.

---

## Files Modified

**Created:**
- `controller/internal/relay/wireguard.go`
- `controller/internal/relay/wireguard_test.go`
- `cmd/relay-router/fleet.go`
- `cmd/relay-router/fleet_test.go`
- `cmd/relay-router/health.go`
- `cmd/relay-router/detector.go`
- `cmd/relay-router/metrics.go`
- `cmd/relay-router/proxy.go`
- `cmd/relay-router/proxy_test.go`
- `cmd/relay-router/main.go`
- `cmd/relay-router/README.md`
- `worklogs/0261_2026-06-14_epic42-router-wireguard-fallback.md` (this file)
