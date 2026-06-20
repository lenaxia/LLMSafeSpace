# Worklog: Fix relay-proxy Listen Address per VM (F1 — AWS/GCP bind failure)

**Date:** 2026-06-20
**Session:** Resolve finding F1 from worklog 0437 — relay VMs bound to the wrong WG IP
**Status:** Complete

---

## Objective

Worklog 0437's adversarial review surfaced F1: `relay-proxy`'s `defaultListenAddr = "10.42.42.2:8080"` is the OCI relay's WG IP (`wgOCIRelay`), but AWS relays get `wgAWSRelay = "10.42.42.4"` and GCP `.3` (`constants.go:26-28`). The WG config (`wireguard.go:91`) correctly assigns each VM its own wgIP on wg0, but `cloudinit.go` rendered only `--upstream` into the systemd unit — no `--listen` / `LISTEN_ADDR`. So on an AWS VM (wg0 = .4), relay-proxy tried to bind `10.42.42.2:8080`, an IP that doesn't exist on that VM → `EADDRNOTAVAIL` → relay-proxy would fail to start. Only OCI bound, by coincidence. Masked because `controller.inferenceRelay.enabled` defaults to false.

The `--listen` flag parsing shipped in 0437 is the prerequisite; this worklog renders it from cloud-init with the per-relay wgIP.

---

## Work Completed

- `controller/internal/relay/cloudinit.go`: added `WgIP` field to `CloudInitConfig`; template now renders `ExecStart=/usr/local/bin/relay-proxy --upstream={{ .UpstreamURL }} --listen={{ .WgIP }}:8080`; added validation that `WgIP` is non-empty (rejects loudly rather than rendering `--listen=:8080`, which would bind `0.0.0.0` and break the WG-only-listener security posture from Epic 42 Layer 1).
- `controller/internal/relay/reconciler.go`: `provisionRelay` passes `WgIP: wgIPForProvider(providerSpec.Provider)` into `RenderCloudInit` (the value was already computed at line 350 for the WG config — reused, not recomputed).
- `controller/internal/relay/driver_test.go`: existing `TestRenderCloudInit_ValidConfig` updated to pass `WgIP` and assert `--listen=10.42.42.4:8080`; the two missing-field tests updated to include `WgIP`; new `TestRenderCloudInit_MissingWgIP` covers the empty rejection.

---

## Key Decisions

1. **Reject empty WgIP rather than fall back.** An empty wgIP would render `--listen=:8080` → bind `0.0.0.0` → relay exposed on the VM's public interface, defeating the WG-only design (Epic 42 Layer 1: "LISTEN_ADDR ... WG interface only, not 0.0.0.0"). Failing loud at render time is strictly safer. In practice `wgIPForProvider` never returns empty for a known provider (aws/oci/gcp), and unknown providers are rejected earlier at the driver lookup (`reconciler.go:320`) — so this guard is pure defense-in-depth.

2. **Bind to the WG IP, not `0.0.0.0` + firewall.** The design chose WG-IP binding as the boundary (one UDP port public, relay unreachable off-mesh). Keeping that.

---

## Assumptions (Rule 7) and validation

- **A-WGIP-PER-VM: each relay VM's wg0 carries exactly its `wgIPForProvider` IP.** Validated: `wireguard.go:91` writes `Address = <WgIP>/24` into wg0.conf from the same `wgIPForProvider` source the reconciler now also passes to cloud-init. Same source → guaranteed agreement between the interface address and the bind address.
- **A-FLAG-PARSED: `--listen=<ip>:8080` reaches the binary and is applied.** Validated: 0437 added `--listen` flag parsing + `TestLoadConfig_ListenAndKeepaliveFlags` confirms `10.42.42.4:8080` is accepted and applied to `cfg.listenAddr`.
- **A-NO-OTHER-CALLERS: `RenderCloudInit` has one caller.** Validated via grep — only `reconciler.go:359`.

---

## Tests Run

- `go test ./controller/internal/relay/` (full package) — PASS
- `go vet ./controller/internal/relay/` — clean
- `go build ./controller/...` — clean

---

## Next Steps

1. **#1 — workspace→router wiring:** `INFERENCE_RELAY_BASEURL` → `http://relay-router:8080` when `controller.inferenceRelay.enabled` (Design Principle 6). With F1 + 0437, relay VMs now correctly bind their per-VM WG IP and honour per-CR upstreams — the VM side of the path is sound; the remaining gap is workspace pods routing through the router at all.

---

## Files Modified

- `controller/internal/relay/cloudinit.go` — `WgIP` field + template `--listen` + validation
- `controller/internal/relay/reconciler.go` — pass `WgIP` to `RenderCloudInit`
- `controller/internal/relay/driver_test.go` — updated + new test
