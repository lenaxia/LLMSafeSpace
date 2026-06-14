# Worklog: Epic 42 — Multi-Cloud Inference Relay (US-42.1 + US-42.3 Foundation)

**Date:** 2026-06-14
**Session:** Started Epic 42 implementation — portable relay binary (US-42.1) and InferenceRelay CRD types (US-42.3)
**Status:** Complete

---

## Objective

Begin implementation of Epic 42 (Multi-Cloud Inference Relay). The epic replaces the Cloudflare Worker relay (blocked by Zen IP-throttling CF egress ranges) with a portable relay binary on OCI/GCP VMs connected via WireGuard, fronted by an in-cluster router.

This session implemented the two dependency-free stories on the critical path:
- **US-42.1**: Portable relay Go binary (proxy + health + metrics + keepalive)
- **US-42.3**: InferenceRelay CRD types + DeepCopy + scheme registration

---

## Work Completed

### US-42.1: Portable Relay Binary (`cmd/relay-proxy/`)

Created a standalone Go binary with stdlib-only dependencies (~230 lines of logic). No TLS, no auth — WireGuard is the security boundary.

**Files created:**
- `cmd/relay-proxy/main.go` — Entry point: env config (UPSTREAM_URL, LISTEN_ADDR, KEEPALIVE_INTERVAL), HTTP routing (/healthz, /metrics, /*), graceful shutdown (SIGINT/SIGTERM), HTTP client with dial/TLS timeouts and `DisableCompression: true` for transparent proxying
- `cmd/relay-proxy/proxy.go` — `proxyHandler` (transparent reverse proxy with streaming pass-through via 32KB buffer + Flush), `relayMetrics` (thread-safe Prometheus-format metrics with `sync.Mutex` + `atomic.Int64`), hop-by-hop header stripping, URL construction (upstream base + incoming path + query)
- `cmd/relay-proxy/keepalive.go` — Periodic upstream probe goroutine (`GET {UPSTREAM_URL}/models` every 30s default), increments keepalive counter, context-cancellable, does NOT increment relay_requests_total (distinct metric)
- `cmd/relay-proxy/proxy_test.go` — 27 tests: metrics unit tests (record/concurrent/format/sorted/empty), proxy forwarding tests (GET/POST/query params/status codes/headers/hop-by-hop stripping/streaming), error tests (upstream unreachable 502, timeout 502, client cancel no-metric), metrics recording tests (request counts/egress bytes), handler tests (healthz/metrics), 2 E2E integration tests (full proxy path + metrics, healthz not proxied)
- `cmd/relay-proxy/keepalive_test.go` — 5 tests: probes upstream /models, increments counter, handles upstream failure without crashing, stops on context cancel, does not record request metrics
- `cmd/relay-proxy/README.md` — Deployment guide with endpoints, metrics, configuration, build/run instructions

**Endpoints:**
- `GET /healthz` → 200 OK (no body)
- `GET /metrics` → Prometheus format (`relay_requests_total{status}`, `relay_egress_bytes_total`, `relay_keepalive_total`)
- `* /*` → transparent proxy to UPSTREAM_URL with streaming response

**Key design decisions:**
- `DisableCompression: true` on transport — prevents Go from adding `Accept-Encoding: gzip` and transparently decompressing, making the proxy truly transparent
- `ReadHeaderTimeout: 10s` on server (slowloris protection), no WriteTimeout (streaming responses can take minutes)
- Client cancellation handled: if `r.Context().Err() != nil` when upstream fails, no 502 or metric recorded (client already gone)
- Metrics use snapshot approach in `writePrometheus` — single mutex lock, copy map, release lock, then format

**Makefile:** Added `relay-bin` target for cross-compilation (arm64 + amd64)

### US-42.3: InferenceRelay CRD Types (`pkg/apis/llmsafespace/v1/`)

Created the cluster-scoped InferenceRelay CRD types matching the design document specification exactly.

**Files created:**
- `pkg/apis/llmsafespace/v1/inferencerelay_types.go` — All types:
  - `InferenceRelay` (root, cluster-scoped, shortName=irelay, status subresource)
  - `InferenceRelayList`
  - `InferenceRelaySpec` (UpstreamURL, Providers, WireGuard, HealthCheck, Rotation, Fallback)
  - `RelayProviderSpec` (Provider enum: oci/gcp, Region, CredentialsRef, Shape)
  - `WireGuardConfig` (CIDR, Port, RouterEndpoint, RouterPrivateKeyRef)
  - `HealthCheckConfig` (Interval, Timeout, UnhealthyThreshold, ReplacementTimeout)
  - `RotationConfig` (Enabled, Max429Rate, DetectionWindow, Cooldown)
  - `FallbackConfig` (Enabled, Rate, MaxConcurrent)
  - `InferenceRelayStatus` (Instances, HealthyReplicas, Conditions, LastRotation)
  - `RelayInstanceStatus` (ID, Provider, Region, WgIP, PublicIP, State, Healthy, metrics, provisioning info)
  - Typed constants: `InferenceRelayConditionType` (Ready, Degraded, ProvisioningFailed, Rotating, FallbackActive), `RelayInstanceState` (provisioning, healthy, draining, unhealthy, quota-exhausted, terminated, provisioning-failed)
- `pkg/apis/llmsafespace/v1/inferencerelay_types_test.go` — 12 tests: condition constants, state constants, field shape verification for 6 structs (JSON tags), JSON round-trip with full spec/status, DeepCopy independence verification, nil DeepCopy safety, DeepCopyObject, list DeepCopy

**Files modified:**
- `pkg/apis/llmsafespace/v1/register.go` — Added `&InferenceRelay{}` and `&InferenceRelayList{}` to `AddToScheme`
- `pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go` — Added DeepCopy methods for all 10 new types (FallbackConfig, HealthCheckConfig, InferenceRelay, InferenceRelayList, InferenceRelaySpec, InferenceRelayStatus, RelayInstanceStatus, RelayProviderSpec, RotationConfig, WireGuardConfig). Added `metav1` import for `metav1.Condition` DeepCopy.
- `pkg/apis/llmsafespace/v1/types_test.go` — Added InferenceRelay and InferenceRelayList to `TestSchemeRegistration`
- `Makefile` — Added `relay-bin` to `.PHONY` and as a build target

---

## Key Decisions

1. **Stdlib-only for relay-proxy binary.** The design doc specifies "no external dependencies beyond stdlib." Prometheus metrics are written in raw text format using `fmt.Fprintf` into a `strings.Builder`, not using `client_golang`. This keeps the binary lean and self-contained for VM deployment.

2. **Streaming pass-through via io.Copy pattern.** The proxy uses the same 32KB buffer + Flush pattern as `api/internal/handlers/proxy.go:358-377`. SSE streams pass through without buffering. `DisableCompression: true` on the transport prevents Go from interfering with content encoding.

3. **DeepCopy methods manually added to generated file.** `zz_generated.deepcopy.go` has a `// Code generated by controller-gen. DO NOT EDIT.` header. I manually added the InferenceRelay DeepCopy methods following the exact controller-gen pattern. Running `make deepcopy` will regenerate the file with identical output. This is necessary because controller-gen is not available in this environment.

4. **RelayInstanceStatus.Requests429 has JSON tag `"429Count"`.** This is unusual (starts with a digit) but matches the design document specification exactly (line 518 of epic-42 README).

---

## Blockers

1. **Full project build limited by disk space.** `go build ./...` and `go test ./...` exhaust /tmp space after cache clean. Individual package builds and tests pass. This is an environment constraint, not a code issue. The relay-proxy package (stdlib-only) and v1 package both build and test cleanly with `-race -count=1`.

2. **golangci-lint not available.** `go vet` was used as a substitute. Both new packages pass `go vet` clean.

---

## Tests Run

```bash
# Relay proxy binary (35 tests, all pass)
GOTMPDIR=/tmp/opencode go test -timeout 30s -race -count=1 ./cmd/relay-proxy/
# Result: ok  1.694s

# InferenceRelay CRD types (all pass, includes existing Workspace/RuntimeEnvironment tests)
GOTMPDIR=/tmp/opencode go test -timeout 60s -race -count=1 ./pkg/apis/llmsafespace/v1/
# Result: ok  1.066s

# Build verification
go build ./cmd/relay-proxy/
go build ./pkg/apis/llmsafespace/v1/
# Both: clean

# Formatting
gofmt -l cmd/relay-proxy/ pkg/apis/llmsafespace/v1/
# Clean (no unformatted files)

# Vet
go vet ./cmd/relay-proxy/ ./pkg/apis/llmsafespace/v1/
# Clean
```

---

## Next Steps

1. **US-42.4: WireGuard keypair generation + config rendering** (`controller/internal/relay/wireguard.go`). No cloud dependencies — can be fully unit-tested. Depends on nothing.

2. **US-42.2: Cloud-init template + artifact publishing + day-one validation.** Requires deploying relay binary on OCI/GCP VMs and curling `opencode.ai/zen/v1` to verify IPs are not blocked (A22 gate). This is the cheapest de-risking step.

3. **US-42.7: Relay router** (`cmd/relay-router/`). Depends on US-42.3 (CRD types done). Weighted relay selection, health checking, 429 detection, ConfigMap polling, fallback mode. This is the largest single story (2d).

4. **CRD YAML generation.** Run `make deepcopy` and controller-gen to generate the CRD YAML for the Helm chart. Add to `charts/llmsafespace/crds/inferencerelay.yaml`. Belongs to US-42.10.

5. **Validating webhook for InferenceRelay.** Check CredentialsRef Secret existence + required keys (oci: tenancy/user/fingerprint/key/region; gcp: service-account-json). Part of US-42.3 but deferred to when the webhook infrastructure is set up.

---

## Files Modified

**Created:**
- `cmd/relay-proxy/main.go`
- `cmd/relay-proxy/proxy.go`
- `cmd/relay-proxy/proxy_test.go`
- `cmd/relay-proxy/keepalive.go`
- `cmd/relay-proxy/keepalive_test.go`
- `cmd/relay-proxy/README.md`
- `pkg/apis/llmsafespace/v1/inferencerelay_types.go`
- `pkg/apis/llmsafespace/v1/inferencerelay_types_test.go`
- `worklogs/0260_2026-06-14_epic42-relay-proxy-and-crd-types.md` (this file)

**Modified:**
- `pkg/apis/llmsafespace/v1/register.go` (added InferenceRelay types to AddToScheme)
- `pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go` (added DeepCopy for 10 new types + metav1 import)
- `pkg/apis/llmsafespace/v1/types_test.go` (added InferenceRelay to TestSchemeRegistration)
- `Makefile` (added relay-bin target and .PHONY entry)
