# Worklog 0290 — Epic 42: InferenceRelay Reconciler Phase 1

**Date:** 2026-06-15
**Epic:** 42 (Multi-Cloud Inference Relay)
**Story:** US-42.9
**Scope:** Core reconciler skeleton + OCI provider driver

## Summary

Implemented the InferenceRelay reconciler (US-42.9) that connects the admin
UI (Epic 43) to actual VM lifecycle management. This is the missing piece that
was flagged as unimplemented after the Epic 42/43 merge cycle.

## Files created (10)

| File | Purpose |
|------|---------|
| `driver.go` | ProviderDriver interface (Provision/Destroy/GetStatus/ListInstances) + error classification (ErrCapacity, ErrConfig, ErrNotImplemented) |
| `constants.go` | Finalizer name, requeue intervals, WG IP allocation map (.1=router, .2=OCI, .3=GCP, .4=AWS), default shapes/regions |
| `cloudinit.go` | cloud-init template (wireguard + relay-proxy systemd service) |
| `health.go` | Router /metrics scraper → HealthReport with per-relay health/streams/requests/429s/egress |
| `router_configmap.go` | Syncs relay-router-peers ConfigMap with fleet state |
| `reconciler.go` | InferenceRelayReconciler — provision, health, rotation, pause, deletion, ConfigMap sync, status conditions |
| `oci_driver.go` | OCI Compute driver via raw REST API with RSA-SHA256 request signing (no SDK dependency) |
| `rsa.go` | RSA PEM key parsing + PKCS#1 v1.5 signing for OCI API auth |
| `aws_driver.go` | AWS stub (ErrNotImplemented) |
| `gcp_driver.go` | GCP stub (ErrNotImplemented) |

## Files modified (2)

| File | Change |
|------|--------|
| `controller/internal/controller/controller.go` | Added SetupRelayController() — feature-gated registration |
| `controller/main.go` | Added --enable-inference-relay and --relay-router-url flags |

## Reconciler capabilities

- Provisioning: generates WG keypair, renders cloud-init, calls provider driver
- Health checking: scrapes router /metrics, updates instance health/egress/429
- Rotation: reads annotation → destroys VM → next reconcile provisions replacement
- Pause: reads annotation → skips all provisioning
- Deletion: finalizer destroys all VMs before CR removal
- ConfigMap sync: writes relay-router-peers JSON
- Status conditions: Ready, Degraded, FallbackActive, Rotating, ProvisioningFailed
- Error classification: capacity (retry), config (circuit breaker), not-implemented (skip)

## OCI driver design

Uses raw OCI REST API (no SDK dependency) with RSA-SHA256 request signing.
Provision launches instance, waits for running, fetches Vnic public IP.
Error classification: HTTP 500/503/429 → ErrCapacity, 400/401/422 → ErrConfig.

## Tests

41 unit tests covering cloud-init rendering, metrics parsing, health reports,
WG IP allocation, error classification, OCI state mapping, OCI error
classification, RSA key parsing, driver stubs.

## What's NOT included (Phase 2 follow-up)

- AWS EC2 driver implementation (currently stub)
- GCP Compute driver implementation (currently stub)
- Relay metrics (llmsafespace_relay_healthy_replicas etc.)
- Provisioning circuit breaker (3-attempt cutoff)
- Egress quota tracking (quota-exhausted state)
- Graceful drain (state=draining, wait for active_streams==0)
- Replacement timeout (15min unhealthy → destroy+reprovision)
