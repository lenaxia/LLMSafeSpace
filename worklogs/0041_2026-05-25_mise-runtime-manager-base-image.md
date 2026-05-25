# Worklog: Add mise to Base Runtime Image

**Date:** 2026-05-25
**Session:** Install mise into the base runtime image, bake language runtimes into the image layer, and redirect all package manager homes to the PVC for persistence across suspend/resume.
**Status:** Complete

---

## Objective

Add mise (polyglot runtime manager) to the base runtime image so the agent can manage language runtimes without root access. Pre-install Python, Node.js, Rust, Ruby, Go, Java, Maven, and Gradle into the image layer so they are available immediately. Ensure that anything the agent installs at runtime persists on the PVC and survives workspace suspend/resume without reinstallation.

---

## Work Completed

### Assumptions stated and validated

1. **`/home/sandbox` is ephemeral** — validated via `controller/internal/workspace/controller.go:615`: `{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}`. Any `.bashrc` written during the Docker build is shadowed at runtime by the emptyDir mount and has zero effect.

2. **`/workspace` is PVC-backed** — validated via `controller.go:610-611`: workspace volume is a `PersistentVolumeClaim`. Survives suspend (pod deleted, PVC retained) and is remounted on resume.

3. **mise `--system` installs into `/usr/local/share/mise`** — validated via mise docs: `MISE_SYSTEM_DATA_DIR` defaults to `/usr/local/share/mise`; `mise install --system` populates it. mise resolves tools by checking `MISE_DATA_DIR` first, then the system dir — image-layer tools are the read-only fallback.

4. **mise uses precompiled binaries for Python, Node, Go, etc.** — validated via mise docs: Python uses `astral-sh/python-build-standalone`, no system build deps (gcc, make) required. `xz-utils` needed for `.tar.xz` extraction.

5. **`MISE_GITHUB_ATTESTATIONS=0` required during Docker build** — build environments typically have no outbound OIDC connectivity; disabling attestation checks prevents build failures.

### Files modified

**`runtimes/base/Dockerfile`**
- Added `ARG MISE_VERSION=2026.5.15` (pinned to latest as of 2026-05-23)
- Added `xz-utils` to apt packages (needed for `.tar.xz` archive extraction by mise backends)
- Added mise install block: downloads `mise-v{version}-linux-{x64,arm64}.tar.gz` from GitHub releases, same arch-mapping pattern as the opencode install
- Added `RUN mise install --system python@latest node@lts rust@stable ruby@latest go@latest java@lts maven@latest gradle@latest && mise --system reshim` — bakes all runtimes into the image layer at `/usr/local/share/mise`
- Moved smoke-test run to after the runtime installs so it validates package managers
- Added full ENV block for PVC-directed package manager homes:
  - `MISE_DATA_DIR=/workspace/.local/share/mise`
  - `CARGO_HOME=/workspace/.local/share/cargo`
  - `GEM_HOME=/workspace/.local/share/gem`
  - `GOPATH=/workspace/.local/share/go`
  - `NPM_CONFIG_PREFIX=/workspace/.local`
  - `PYTHONUSERBASE=/workspace/.local`
  - `PATH` extended with PVC bin dirs and `/usr/local/share/mise/shims`
- Removed `.bashrc` write (was writing to image layer that gets shadowed by emptyDir at runtime — no effect)

**`runtimes/base/tools/entrypoints/entrypoint-opencode.sh`**
- Added `eval "$(mise activate bash)"` before opencode exec so mise-managed tool PATH is active for the agent process

**`runtimes/base/tools/smoke-test.sh`**
- Added `which mise`
- Added `mise --system which` checks for all 10 package manager binaries (python, pip, node, npm, cargo, gem, go, java, mvn, gradle) — validates image-layer installs at build time

**`README-LLM.md`**
- Added `mise (jdx/mise)` to the Technology Stack table

---

## Key Decisions

**Two-layer runtime model**: Image-layer runtimes (`/usr/local/share/mise`, read-only) provide defaults always present regardless of PVC state. PVC-layer runtimes (`/workspace/.local/share/mise`) are agent-managed and persist across suspend/resume. mise resolves tools in PVC-first order, so agent overrides always win.

**No `.bashrc` activation**: `/home/sandbox` is an emptyDir — any file written there during the build is gone at runtime. Shell activation for the agent process is handled exclusively in `entrypoint-opencode.sh`. Interactive shell activation (for humans sshing in) is a non-requirement for this use case.

**`MISE_GITHUB_ATTESTATIONS=0` during build**: Prevents build failures in restricted environments with no outbound OIDC connectivity. Transport integrity of mise itself is still TLS-secured via curl `--fail`.

**Maven and Gradle via mise**: Consistent with all other runtimes. User-overridable via `.mise.toml`. No apt bloat.

---

## Blockers

None.

---

## Tests Run

Smoke test (`/usr/local/bin/smoke-test.sh`) updated to verify all package manager binaries via `mise --system which`. Runs as a `RUN` step in the Dockerfile at build time — build fails if any binary is missing.

No cluster deployment test run in this session; image build validation depends on CI.

---

## Next Steps

- Trigger a CI build to validate the Dockerfile compiles and all smoke tests pass
- Consider pinning specific versions of the baked runtimes (e.g. `python@3.13` instead of `python@latest`) for reproducibility once desired versions are confirmed

---

## Files Modified

- `runtimes/base/Dockerfile`
- `runtimes/base/tools/entrypoints/entrypoint-opencode.sh`
- `runtimes/base/tools/smoke-test.sh`
- `README-LLM.md`
- `worklogs/0041_2026-05-25_mise-runtime-manager-base-image.md` (this file)
