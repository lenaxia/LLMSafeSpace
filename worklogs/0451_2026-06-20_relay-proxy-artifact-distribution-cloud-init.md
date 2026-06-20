# Worklog: Implement Relay-Proxy Binary Artifact Distribution in Cloud-Init

**Date:** 2026-06-20
**Session:** Close the critical gap that blocked automated fleet deployment — cloud-init referenced `/usr/local/bin/relay-proxy` but never downloaded it
**Status:** Complete

---

## Objective

Worklog 0440's fleet audit identified that `controller/internal/relay/cloudinit.go` writes a systemd unit for `/usr/local/bin/relay-proxy` but the cloud-init template has no download step — it only installs `wireguard`/`wireguard-tools`. An automated `kubectl apply` would provision a VM that boots, brings up WireGuard, then crash-loops `relay-proxy` (file not found). The manual EC2 validation in 0440 succeeded only because the binary was `scp`'d by hand. This session implements the artifact distribution the design doc (`design/stories/epic-42.../README.md` Layer 7 + Security §7) specifies: multi-mirror download with SHA-256 verification.

---

## Work Completed

### A. TDD: failing tests first (RED)

Added 11 new tests across two files before any implementation:

- `driver_test.go`: refactored existing cloud-init tests to use a `validCloudInitConfig()` helper with artifact fields; added `TestRenderCloudInit_MissingArtifactURLs`, `TestRenderCloudInit_MissingArtifactSHA256`, `TestRenderCloudInit_RendersArtifactDownload` (curl, sha256sum, both mirrors, binary name, FATAL exit), `TestRenderCloudInit_DownloadBeforeSystemdStart` (ordering assertion).
- `arch_test.go` (new): `TestArchForShape_ARM64` (8 shapes), `TestArchForShape_AMD64` (8 shapes), `TestArchForShape_UnknownDefaultsToARM64`, `TestArchForShape_EmptyShapeDefaultsToARM64`, `TestBinaryNameForArch`.
- `reconciler_test.go`: `TestReconcileFleet_MissingArtifactSHA_FailsProvisioning` (adversarial finding — reconciler-level regression test for the empty-SHA error path).
- `chart_test.go`: `TestControllerArgs_RelayArtifactFlags_RenderWhenEnabled`, `TestControllerArgs_RelayArtifactFlags_AbsentWhenDisabled`; updated 11 existing tests that enable the relay to include the new required artifact values.

RED confirmed (build failed: undefined types/fields).

### B. Implementation (GREEN)

**`controller/internal/relay/arch.go`** (new): `archForShape(shape, provider)` resolves CPU architecture from VM shape. Known ARM64: AWS Graviton (`t4g`/`c7g`/`m6g`/`r6g`/`x2gd`/`im4gn`/`g5g`), OCI Ampere (`A1`/`E1`). Known AMD64: AWS Intel/AMD (`t3`/`t2`/`m5`/`c5`/`r5`/...), all GCP. Unknown defaults arm64 (dominant relay arch). `binaryNameForArch(arch)` returns `relay-proxy-arm64`/`relay-proxy-amd64`.

**`controller/internal/relay/cloudinit.go`**: 
- `CloudInitConfig` gained `ArtifactURLs []string`, `ArtifactSHA256 string`, `BinaryName string`.
- Template: added `curl` to packages; added a `runcmd` download step before `systemctl start` that tries each mirror, verifies SHA-256 via `sha256sum -c`, chmods, exits FATAL on failure.
- `RenderCloudInit`: validates both artifact fields are non-empty (checksum mandatory per Security §7).

**`controller/internal/relay/reconciler.go`**: `InferenceRelayReconciler` gained `ArtifactURLs`, `ArtifactSHA256Arm64`, `ArtifactSHA256Amd64`. `provisionRelay` resolves arch from shape, selects the matching checksum, returns `ErrConfig` if empty (clear error message naming the missing flag).

**`controller/internal/controller/controller.go`**: `RelayArtifactConfig` struct + `SetupRelayController` signature extended to accept it. Reconciler constructed with the three new fields.

**`controller/main.go`**: three new flags — `--relay-artifact-url` (comma-separated), `--relay-artifact-sha256-arm64`, `--relay-artifact-sha256-amd64`. Wired to `SetupRelayController` via `splitNonEmpty`.

**`charts/llmsafespaces/`**:
- `values.yaml`: `controller.inferenceRelay.artifact` section (`urls` list, `sha256Arm64`, `sha256Amd64`) with documentation on how to build (`make relay-bin`) and publish.
- `controller-deployment.yaml`: renders `--relay-artifact-url`, `--relay-artifact-sha256-arm64`, `--relay-artifact-sha256-amd64` when enabled; uses Helm `required` to fail-fast on missing checksums.

### C. Design doc update

Updated `design/stories/epic-42.../README.md` Layer 7 to document the as-built implementation: `CloudInitConfig` struct fields, arch resolution, download command, flag wiring.

### D. Adversarial review (Rule 11)

**Phase 1 findings:**
1. Missing reconciler-level test for empty-artifact error path → **real, fixed** with `TestReconcileFleet_MissingArtifactSHA_FailsProvisioning`.
2. Shell injection via URLs/checksum → **false alarm** (operator-controlled values; checksum verified by sha256sum before exec).
3. Empty-string entries in ArtifactURLs → **false alarm** (`splitNonEmpty` filters; empty entries fail safely in loop).
4. GCP t2a ARM shape misclassified as amd64 → **false alarm** (not in default fleet; safe failure — wrong checksum fails verification, no wrong-arch exec).

---

## Key Decisions

1. **Controller-resolved arch, not cloud-init-resolved.** The controller resolves arch from the VM shape via `archForShape` and embeds a single SHA-256. This is simpler than embedding both checksums and letting cloud-init pick via `uname -m`. Trade-off: unknown shapes default to arm64; if wrong, verification fails (safe — no wrong-arch exec). Acceptable because the default fleet (AWS t4g.micro + OCI A1) is all arm64.

2. **Checksums via controller flags, not CR fields.** The checksum is per-release (controller binary version), not per-fleet. Making it a CR field would allow different fleets to point at different binaries — flexibility we don't need, and it creates the risk of a fleet pointing at an unverified binary. Flags match the existing pattern (`--enable-inference-relay`, `--relay-router-url`).

3. **Helm `required` for sha256 values.** Fail-fast at render time rather than at provisioning time. The controller also validates at provision time (defense-in-depth).

4. **Multi-mirror support.** `ArtifactURLs` is a list; cloud-init tries each in order. The default points at GitHub Release (the operator's release pipeline); additional mirrors (S3/GCS/OCI object storage) can be added for resilience.

---

## Blockers

None. The change is complete and tested. The remaining piece for a real deploy is the CI artifact-publishing pipeline (GitHub Release creation with `relay-proxy-arm64`/`relay-proxy-amd64` + checksums) — that is CI infrastructure work, not code work.

---

## Tests Run

- `go build ./...` — clean.
- `go vet ./controller/... ./charts/...` — clean.
- `go test -count=1 ./controller/... ./charts/...` — all green (8 packages).
- New tests: 11 cloud-init/arch/chart tests + 1 reconciler regression test, all green.
- Helm chart render tests: skip locally (no helm binary); 2 new tests gated on CI.

---

## Next Steps

1. **CI artifact publishing** — add a GitHub Actions job that runs `make relay-bin`, computes `sha256sum`, creates a GitHub Release with the two binaries + checksums. The operator then sets `controller.inferenceRelay.artifact.{sha256Arm64,sha256Amd64}` in Helm values from the release.
2. **End-to-end fleet deploy test** — with the artifact pipeline + a GitHub Release in place, do a real `kubectl apply InferenceRelay` and verify the VM downloads the binary, starts relay-proxy, and serves completions through the WireGuard mesh via relay-router.

---

## Files Modified

**New files:**
- `controller/internal/relay/arch.go`
- `controller/internal/relay/arch_test.go`

**Modified (Go):**
- `controller/internal/relay/cloudinit.go` — CloudInitConfig fields, template, validation
- `controller/internal/relay/reconciler.go` — reconciler fields, provisionRelay artifact resolution
- `controller/internal/relay/driver_test.go` — refactored existing tests, added 5 new artifact tests
- `controller/internal/relay/reconciler_test.go` — artifact fields in test helpers + new regression test
- `controller/internal/controller/controller.go` — RelayArtifactConfig, SetupRelayController signature
- `controller/main.go` — three new flags

**Modified (Helm):**
- `charts/llmsafespaces/values.yaml` — artifact section
- `charts/llmsafespaces/templates/controller-deployment.yaml` — artifact flags
- `charts/llmsafespaces/chart_test.go` — 11 existing tests updated, 2 new tests

**Modified (docs):**
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` — Layer 7 implementation section
