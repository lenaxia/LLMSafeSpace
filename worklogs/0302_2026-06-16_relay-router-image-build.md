# Worklog 0302 — Relay Router Image Build Pipeline

**Date:** 2026-06-16
**Scope:** Dockerfiles + CI build jobs for relay-router and relay-proxy

## Summary

The relay infrastructure code was merged but never deployable because no
container images existed. The Helm chart references
`ghcr.io/lenaxia/llmsafespace/relay-router` but that image was never built.

## Changes

### Dockerfiles
- `cmd/relay-router/Dockerfile`: Multi-stage build (golang:1.25 → distroless).
  Builds the in-cluster router binary that distributes inference traffic.
- `cmd/relay-proxy/Dockerfile`: Multi-stage build for the VM-side proxy
  binary. This image is pushed to the registry so cloud-init on relay VMs
  can pull it.

### CI (.github/workflows/ci.yml)
- Added `RELAY_ROUTER_IMAGE` and `RELAY_PROXY_IMAGE` env vars
- Added `build-relay-router` job: multi-platform (amd64+arm64), GHA cache,
  digest upload
- Added `merge-relay-router` job: manifest list creation with sha/ts/dev/semver tags
- Follows the exact pattern of build-api/build-controller (buildx, metadata-action,
  digest artifacts, manifest merge)

## What this unblocks

After merge, the relay-router image will be published to ghcr.io on every
push to main. Operators can then:
1. Set `controller.inferenceRelay.enabled=true` in values.local.yaml
2. Provide provider credentials via K8s Secrets
3. Deploy — the relay-router pod will start and the controller will reconcile
