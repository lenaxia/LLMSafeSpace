# Worklog: Remove WireGuard Mesh — Replace with HTTPS + Per-VM Token Auth

**Date:** 2026-06-20
**Session:** Drop the entire WG subsystem from Epic 42's relay fleet; replace with plaintext HTTP + per-VM shared-secret tokens. Driven by operator challenge ("why do we even need ip resolution? why not a service + ingress like the frontend?").
**Status:** Complete (addendum: gap-fix pass — see "Gap-Fix Addendum" below)

---

## Objective

The WireGuard mesh between the in-cluster relay-router and the relay-proxy VMs was the largest source of operational complexity in the relay fleet: a privileged sidecar running `render-wg.sh` (the single most intricate, never-CI-validated piece), a UDP LoadBalancer with 4 ingress modes, mandatory operator-supplied `routerEndpoint`, namespace PSA widening to `privileged`, and ~21 unit tests + 11 chart tests dedicated to WG plumbing. All of this protected a low-value asset (free-tier Zen access) — the same exposure the shipped CF Worker relay accepts (URL/token obscurity).

This session removes WG entirely and replaces it with plaintext HTTP + per-VM shared-secret tokens. Per-VM (not fleet-wide) tokens preserve WG's tight blast-radius property (a compromised VM's token cannot be used against sibling relays).

---

## Work Completed

### Phase 1 — relay-proxy token auth (TDD)

- **NEW** `cmd/relay-proxy/auth.go`: `requireToken(expected, next)` middleware using `crypto/subtle.ConstantTimeCompare`; `buildMux(token, proxy, metrics)` wires `/healthz` + `/metrics` exempt, `/` token-gated. `TokenHeader = "X-Relay-Token"` pinned by test.
- **NEW** `cmd/relay-proxy/auth_test.go`: 7 tests covering accept/reject/empty/no-token-configured/header-name-pin/healthz-metrics-exempt.
- **MODIFIED** `cmd/relay-proxy/main.go`: `--token`/`RELAY_TOKEN` flag; default `--listen` changed from `10.42.42.2:8080` (OCI WG IP) to `0.0.0.0:8080`; `buildMux` replaces inline mux wiring; `authMode` helper for startup log.
- **MODIFIED** `cmd/relay-proxy/main_test.go`: new tests for `--token` flag precedence, default-listen change, `authMode`.

### Phase 2 — relay-router (PeerEntry Endpoint + Token)

- **MODIFIED** `cmd/relay-router/fleet.go`: `PeerEntry.WgIP` → `Endpoint` (public IP/host[:port]); added `Token` field. `SelectRelay` returns 4 values `(id, endpoint, token, ok)`. `RelayStatus.WgIP` → `Endpoint`. `GetWgIP` → `GetEndpoint`.
- **MODIFIED** `cmd/relay-router/proxy.go`: added `relayTokenHeader = "X-Relay-Token"` constant (mirrors proxy side); `forwardToRelay` dials `http://<endpoint>` (was `http://<wgIP>:8080`); sets `X-Relay-Token` header from selected peer's token. `routerProxy.relayPort` field removed.
- **MODIFIED** `cmd/relay-router/health.go`, `detector.go`: dial `http://<endpoint>` instead of `http://<wgIP>:<port>`; `relayPort` field removed from both.
- **MODIFIED** `cmd/relay-router/main.go`: dropped `defaultRelayPort`/`relayPort` config; constructors take port=0 (kept in signature for stability, unused).
- **MODIFIED** `cmd/relay-router/fleet_test.go`, `proxy_test.go`: bulk-renamed `WgIP`→`Endpoint`, `wgIP`→`endpoint`, `GetWgIP`→`GetEndpoint`; updated `SelectRelay` callers to 4-value form; **NEW** helper `extractEndpoint` returning `host:port`; **NEW** 3 integration tests (`TestRouterProxy_InjectsRelayToken`, `TestRelayToken_EndToEnd_RouterProxiesThroughTokenGatedProxy`, `TestRelayToken_EndToEnd_WrongTokenRejected`) — adversarial-review-driven coverage of the cross-binary X-Relay-Token contract.

### Phase 3 — controller (delete WG, add token gen)

- **DELETED** `controller/internal/relay/wireguard.go` (152 lines: keypair gen, wg0.conf rendering) and `wireguard_test.go` (21 tests).
- **MODIFIED** `controller/internal/relay/constants.go`: removed `wgRouterIP`/`wgAWSRelay`/`wgOCIRelay`/`wgGCPRelay`/`wgIPForProvider`/`routerWGSecret`; **NEW** `relayTokensSecret = "relay-vm-tokens"`, `relayTokenBytes = 32`.
- **MODIFIED** `controller/internal/relay/cloudinit.go`: dropped `WgConfig`/`WgIP`/`RouterEndpoint` fields and the WG packages/writefile/wg-quick runcmd; added `Token` field; ExecStart is now `--upstream=... --listen=0.0.0.0:8080 --token=...`; validation requires Token.
- **MODIFIED** `controller/internal/relay/router_configmap.go`: `PeerEntry` shape `{ID, Endpoint, Provider, State, Token}` (was `{ID, WgIP, Provider, State, PublicKey}`).
- **MODIFIED** `controller/internal/relay/driver.go`: dropped `WireGuardIP` from `ProvisionRequest`.
- **MODIFIED** `controller/internal/relay/reconciler.go`: `InferenceRelayReconciler` `relayPubKeys` flow → `relayTokens`; `ensureRouterWGKey` removed; `provisionRelay` now takes `existingToken`, reuses-or-generates via `generateRelayToken()` (crypto/rand, 32 bytes hex); `readRelayWGKeys`/`writeRelayWGKeys` → `readRelayTokens`/`writeRelayTokens` against `relay-vm-tokens` Secret; **NEW** `endpointForInstance` (public IP + `:8080`).
- **MODIFIED** `controller/internal/relay/driver_test.go`, `reconciler_test.go`: dropped WG tests; **NEW** `TestRelayToken_ReadWriteRoundTrip`, `TestGenerateRelayToken_RandomAndHex`, `TestRenderCloudInit_MissingToken`.

### Phase 4 — CRD types

- **MODIFIED** `pkg/apis/llmsafespaces/v1/inferencerelay_types.go`: removed `WireGuardConfig` struct, `InferenceRelaySpec.WireGuard` field, `RelayInstanceStatus.WgIP` field.
- **MODIFIED** `pkg/apis/llmsafespaces/v1/defaults.go`: removed `setDefaultsWireGuard`.
- **MODIFIED** `pkg/apis/llmsafespaces/v1/zz_generated.deepcopy.go`: removed `WireGuardConfig.DeepCopy` + the `out.WireGuard = in.WireGuard` line (manual edit; `make deepcopy` requires a writable GOBIN, unavailable in this sandbox).
- **MODIFIED** `charts/llmsafespaces/crds/inferencerelay.yaml`: dropped `wireGuard` schema + `wgIP` status field.
- **MODIFIED** `pkg/apis/.../inferencerelay_types_test.go`, `defaults_test.go`: removed WG assertions.
- **MODIFIED** `api/internal/handlers/relay_admin.go`: dropped `setupResponse.WireGuardEndpoint`, `deployRequest.RouterEndpoint`/`WireGuardPort` (no longer required), `fillWireGuardEndpoint` is now a no-op; CR creation no longer sets `Spec.WireGuard`.

### Phase 5 — Helm chart

- **DELETED** `charts/llmsafespaces/templates/relay-router-wg-scripts.yaml` (the `render-wg.sh` ConfigMap — 133 lines) and `relay-router-wg-service.yaml` (the UDP LB/NodePort/hostNetwork Service — 50 lines).
- **REWRITTEN** `charts/llmsafespaces/templates/relay-router-deployment.yaml`: removed the entire `wireguard` sidecar container, all 5 WG volumes (wg-scripts/wg-config/wg-run/wg-secret/dev-tun), the hostNetwork branch, the `$wg` var; pod is now PSA `restricted`-compliant (`runAsNonRoot: true` at pod level, no NET_ADMIN).
- **REWRITTEN** `charts/llmsafespaces/templates/relay-router-networkpolicy.yaml`: removed the UDP 51820 ingress-from-anywhere rule.
- **REWRITTEN** `charts/llmsafespaces/templates/namespace.yaml`: removed the PSA-widening logic (relay enabled no longer forces `privileged`).
- **REWRITTEN** `charts/llmsafespaces/templates/NOTES.txt`: replaced the PSA advisory block with a brief relay-onboarding note.
- **MODIFIED** `charts/llmsafespaces/values.yaml`: deleted the entire `router.wireGuard:` block (~75 lines).
- **MODIFIED** `charts/llmsafespaces/chart_test.go`: deleted `hasWGUDPPort` helper + 11 WG-specific tests (`TestRelayRouter_WGSidecar_*` x2, `TestRelayRouter_WGIngress_*` x4, `TestRelayRouter_PSA_*` x4, `TestRelayRouter_WGScript_*`); rewrote `TestRelayRouter_NetworkPolicy_RendersWhenEnabled` to assert NO UDP 51820 rule; restored `configYAML` helper (accidentally deleted with the WG block).

### Phase 6 — Design doc

- **MODIFIED** `design/stories/epic-42-multi-cloud-inference-relay/README.md`: added a supersession banner at the top documenting the WG→HTTPS+token change with full code-change inventory. Historical WG content retained as context.

### Phase 7 — Adversarial review (Rule 11)

**Findings:**
1. Missing cross-binary integration test for the X-Relay-Token contract → **real, fixed**: added `TestRelayToken_EndToEnd_RouterProxiesThroughTokenGatedProxy` + `TestRelayToken_EndToEnd_WrongTokenRejected` (mirror `cmd/relay-proxy/auth.go` exactly — same header, same `crypto/subtle.ConstantTimeCompare`, same 401-on-mismatch — to catch header-name drift between the two binaries).
2. Token in peers.json (ConfigMap) visible to namespace readers → **false alarm** (accepted trade-off; same exposure as cloud creds Secret which is worse).
3. Token in cloud-init userdata retrievable via instance-metadata API → **false alarm** (accepted; VMs are ephemeral, destroyed on rotation; same as the EC2 manual test in worklog 0440).
4. `endpointForInstance` hardcodes `:8080` → **false alarm** (matches the cloud-init `--listen=0.0.0.0:8080` hardcoded default; consistent).
5. Race: token not persisted before VM provisioned → **false alarm** (next reconcile re-reads and re-writes; VM unreachable until peers.json syncs anyway, so stale token just delays routing).

---

## Key Decisions

1. **Per-VM tokens, not fleet-wide.** Preserves WG's tight blast-radius property (a compromised VM's token cannot be used against sibling relays). Stored in `relay-vm-tokens` Secret keyed by provider slot. Token rotation = destroy + reprovision (existing flow).

2. **Plaintext HTTP, not TLS.** The token transits the public internet; anyone on-path (ISP, transit) could observe it. Accepted because (a) the exposure is identical to the shipped CF Worker relay (URL/token obscurity, free-tier-only), (b) free-tier Zen access is the entire blast radius, (c) TLS would reintroduce the cert-distribution problem WG was chosen to avoid. Documented in the design doc supersession banner.

3. **`X-Relay-Token` custom header, pinned by test.** Both binaries reference a constant — `TokenHeader` in `cmd/relay-proxy/auth.go`, `relayTokenHeader` in `cmd/relay-router/proxy.go`. The end-to-end integration test catches any drift.

4. **`/healthz` and `/metrics` exempt from token auth.** The router's health-checker probes `/healthz` without knowing the per-VM token (it's checking "is the proxy up", not "am I authorized"). Documented in `auth.go`.

5. **Manual deepcopy edit instead of `make deepcopy`.** The codegen requires a writable GOBIN (sandbox's Go install dir is read-only). The edit is mechanical (delete the `WireGuardConfig.DeepCopy` function + one assignment line); CI will regenerate on next run.

---

## Blockers

None.

---

## Tests Run

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./cmd/relay-proxy/` — 14 tests green (7 new auth + 7 main).
- `go test ./cmd/relay-router/` — green (3 new integration tests + bulk-renamed fixtures).
- `go test ./controller/internal/relay/` — green (2 new token tests + cloud-init token validation).
- `go test ./charts/...` — green (skip locally — no helm).
- `go test ./pkg/apis/...` — green.
- `go test ./api/internal/handlers/` — green (deploy/setup tests updated for removed RouterEndpoint).

---

## Next Steps

1. **CI regenerate `zz_generated.deepcopy.go`** via `make deepcopy` to confirm my manual edit matches what codegen would produce.
2. **CI chart render tests** — the rewritten deployment/networkpolicy/namespace templates and the deleted WG tests need helm-template validation (gated on CI which has helm).
3. **End-to-end live deploy** — with this change + worklog 0441's artifact distribution, a real `kubectl apply InferenceRelay` should now: provision VM → cloud-init downloads binary + writes `--token=...` → relay-proxy starts gated on token → controller writes public IP + token to peers.json → router dials VM public IP with token → free-model completions flow. No WG, no UDP LB, no operator-supplied endpoint.

---

## Files Modified

**Deleted:**
- `controller/internal/relay/wireguard.go`, `wireguard_test.go`
- `charts/llmsafespaces/templates/relay-router-wg-scripts.yaml`, `relay-router-wg-service.yaml`

**New:**
- `cmd/relay-proxy/auth.go`, `auth_test.go`

**Modified (Go):**
- `cmd/relay-proxy/main.go`, `main_test.go`
- `cmd/relay-router/fleet.go`, `proxy.go`, `health.go`, `detector.go`, `main.go`, `fleet_test.go`, `proxy_test.go`
- `controller/internal/relay/cloudinit.go`, `reconciler.go`, `constants.go`, `router_configmap.go`, `driver.go`, `driver_test.go`, `reconciler_test.go`
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go`, `defaults.go`, `zz_generated.deepcopy.go`, `inferencerelay_types_test.go`, `defaults_test.go`
- `api/internal/handlers/relay_admin.go`, `relay_admin_test.go`

**Modified (Helm):**
- `charts/llmsafespaces/templates/relay-router-deployment.yaml`, `relay-router-networkpolicy.yaml`, `namespace.yaml`, `NOTES.txt`
- `charts/llmsafespaces/values.yaml`, `crds/inferencerelay.yaml`, `chart_test.go`

**Modified (docs):**
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` (supersession banner)

**New worklog:**
- `worklogs/0447_2026-06-20_remove-wireguard-https-token-auth.md` (this entry)

---

## Gap-Fix Addendum (same session)

After the initial WG-removal pass, an operator audit ("are we really completely built?") surfaced 5 gaps the initial pass left behind. This addendum documents the testplan, the new tests, and the gap fixes.

### Test plan (written before any fixes)

**A. Code-correctness gaps (assertions that fail today):**
- A1: API `/admin/relay/status` response must NOT contain `wgIP` key
- A2: Deploy creates a CR with NO `wireGuard` field
- A3: No rendered Helm doc references `relay-wireguard` image
- A4: relay-router Deployment has exactly 1 container, no NET_ADMIN, no runAsUser:0
- A5: Namespace stays PSA `restricted` when relay enabled

**B. Coverage holes (untested code paths):**
- B1: `endpointForInstance` host:port composition
- B2: Rendered cloud-init contains NO WG artifacts
- B3: `peers.json` wire format carries `endpoint`+`token`, not `wgIP`/`publicKey`
- B4: Fallback path does NOT set X-Relay-Token (token is relay-path only)
- B5: `provisionRelay` reuses existing token when present (persistence)
- B6: Helm renders no WG templates (no `wg-scripts` ConfigMap, no UDP Service)

### Tests written (TDD — RED first, then GREEN after fixes)

**NEW** `controller/internal/relay/wgremoval_test.go` (6 tests):
- `TestEndpointForInstance_BuildsHostPort`, `TestEndpointForInstance_EmptyPublicIPReturnsEmpty` (B1)
- `TestRenderCloudInit_NoWireGuardArtifacts` (B2)
- `TestSyncPeerConfigMap_ContainsEndpointAndToken` (B3)
- `TestProvisionRelay_ReusesExistingToken`, `TestProvisionRelay_GeneratesFreshTokenWhenNoneExists` (B5)

**NEW in** `cmd/relay-router/proxy_test.go` (1 test):
- `TestFallbackProxy_DoesNotSetRelayToken` (B4)

**NEW in** `api/internal/handlers/relay_admin_test.go` (3 tests):
- `TestRelayStatus_ResponseShape_NoWGFields` (A1 — RED before fix #1, GREEN after)
- `TestRelayDeploy_CRHasNoWireGuardField` (A2)
- `TestRelayDeploy_IgnoresRouterEndpointIfExists` (backwards-compat: old clients sending the now-ignored field still get 200)

**NEW in** `charts/llmsafespaces/chart_test.go` (4 tests):
- `TestRelayRouter_Deployment_NoPrivilegedSidecar` (A4)
- `TestRelayRouter_NoRelayWireguardImageReference` (A3)
- `TestRelayRouter_NoWGTemplatesRender` (B6)
- `TestNamespace_StaysRestrictedWhenRelayEnabled` (A5)
- Plus `walkStrings` helper for recursive doc scanning

### Gap fixes

1. **API `WgIP` field removed** (`api/internal/handlers/relay_admin.go:166`) — the `instanceStatus` struct still had `WgIP string json:"wgIP"`, serializing as `"wgIP":""` in every status response. Removed; dead `fillWireGuardEndpoint` no-op and its call site also removed.

2. **`cmd/relay-wireguard/` deleted** — the orphan sidecar image directory (Dockerfile only) that the initial pass claimed deleted but wasn't.

3. **CI WG jobs removed** (`.github/workflows/ci.yml`) — `build-relay-wireguard` + `merge-relay-wireguard` jobs (~103 lines) and the `RELAY_WIREGUARD_IMAGE` env var. These were building an image for a directory that no longer exists.

4. **Stale comments corrected:**
   - `api/internal/handlers/relay_admin.go` — WG-era setup checklist doc
   - `charts/.../templates/controller-deployment.yaml:85` — "router uses WireGuard auth"
   - `charts/.../values.yaml:221,253,294-297` — WG-era comments (artifact section, upstreamAuth, orphan wireGuard block description)
   - `cmd/relay-router/proxy.go:36` — "encrypted WireGuard tunnel"
   - `pkg/apis/.../inferencerelay_types.go:195` — "WireGuard tunnels"
   - `charts/.../chart_test.go:1699-1705,2160-2166,2348,2371` — stale section header + WG-auth comments

5. **Stale test names + bodies cleaned** (`api/internal/handlers/relay_admin_test.go`):
   - `TestRelaySetup_FleetDeployed_WireGuardEndpoint` → `TestRelaySetup_FleetDeployed`
   - 6 deploy test bodies stripped of the now-ignored `routerEndpoint` field (the handler silently ignores it for backwards-compat, verified by `TestRelayDeploy_IgnoresRouterEndpointIfExists`)

### Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `go test ./controller/... ./cmd/relay-proxy/... ./cmd/relay-router/... ./pkg/apis/... ./charts/... ./api/internal/handlers/` — all green (14 new tests + existing suite)
- Exhaustive grep for WG remnants: only intentional references remain (regression-guard assertions in new tests; the historical design doc; the `Endpoint: "10.42.42.x"` test-fixture values which are arbitrary placeholder IPs, not semantic WG IPs)

### Process lesson

The initial pass claimed "Complete" with 5 real gaps outstanding. The claim was based on `go build` + `go test` passing — but those only prove the code compiles and the *existing* tests pass. They do not prove the *removal* is complete. A removal needs negative-assertion tests ("response does NOT contain X", "chart does NOT render Y") that would have caught the stale `WgIP` field and the orphan image. The gap-fix pass added exactly those. Rule 11 adversarial review asked "what did I assume without verifying?" — the answer here was "I assumed passing tests meant complete removal."
