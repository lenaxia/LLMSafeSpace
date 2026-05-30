# RT-1.9 — Build-time Supply Chain Audit

**Phase:** 1 (Reconnaissance)
**Date scanned:** 2026-05-30
**Branch / commit:** working tree at `main` (post-Epic-17 Phase 0 prod kit, same tree as RT-1.5/1.6/1.8).
**Cross-references:** RT-1.5/1.6 image and dependency analysis (`RT-1.5-RT-1.6-deps-and-images.md`) — file:line citations are reused below where the same code is at issue.

---

## 0. TL;DR — where an attacker could inject a backdoor

This codebase has six independent build-supply-chain insertion points. From most to least useful to an attacker:

1. **The `runtimes/base` ad-hoc downloads of `opencode` and `mise`** (`runtimes/base/Dockerfile:67-78` and `:86-98`) — TLS-only integrity, no checksum, no signature. **Compromise of either upstream's GitHub release asset, or a MITM against `github.com` from the CI runner, lands a backdoor in every workspace pod the project ships.** Documented in-tree as a known gap (`runtimes/base/Dockerfile:59-66`); RT-1.6-F2 also flagged.
2. **`runtimes/base/Dockerfile:120` sets `MISE_GITHUB_ATTESTATIONS=0`** before installing seven language runtimes (python, node, rust, go, java, maven, gradle). This *opts out* of mise's own provenance check on toolchain downloads. So mise — itself unverified — fetches Python/Node/etc. with attestations explicitly disabled. **A compromise of the upstream rustup mirror, the Node.js distribution server, etc. is propagated unimpeded into the runtime image.**
3. **Floating base-image tags everywhere** (RT-1.6-F1: `golang:1.25`, `gcr.io/distroless/static:nonroot`, `debian:bookworm-slim`, `node:22-bookworm-slim`, `nginx:1.27-alpine`, `ghcr.io/lenaxia/llmsafespace/base:latest`). A tag-poisoning attack at Docker Hub / GHCR / GCR (e.g. compromised credential of a maintainer who can re-tag) substitutes the build's foundation. The CI workflow does not pin by digest and does not verify base-image signatures.
4. **`runtimes/nodejs/Dockerfile:8` pipes `https://deb.nodesource.com/setup_18.x` directly into `bash` as root.** No GPG verification, no checksum. A NodeSource compromise — or any TLS interception against `deb.nodesource.com` from a runner — runs attacker shell code as root in the image build. The script *itself* configures an apt repo, so the surface persists into any downstream `apt-get` after that point.
5. **`runtimes/go/Dockerfile:12-13` sets `GOPROXY=direct` AND `GOSUMDB=off`**. This is the runtime in which sandboxed user code compiles Go programs, but the same disablement is broadcast to anyone using the toolchain. Module fetches bypass the public sum DB; a hostile dependency or compromised mirror is no longer detected.
6. **CI image push has no signing.** `docker/build-push-action@v6` pushes by tag to `ghcr.io`. There is **no `cosign sign`, no `actions/attest-build-provenance`, no SBOM emission**. Anyone who steals `secrets.GITHUB_TOKEN` for a millisecond — or compromises a third-party action used in the workflow — can publish an image under the project's namespace and downstream consumers have no signature to verify. Combined with the floating tags above, the same attacker can re-tag `dev`/`latest`/`sha-…` to point at a backdoor.

The rest of this document audits each of the seven categories asked for in the brief. Findings are summarised in §8.

---

## 1. Base image trust — pinning and tag-poisoning exposure

| Image | Dockerfile:line | Pin type | Tag-poisoning risk | Notes |
|---|---|---|---|---|
| `golang:1.25` | `api/Dockerfile:7`, `controller/Dockerfile:7` | **floating tag** | High — any actor who can push to `library/golang` on Docker Hub re-tags the build foundation. | Resolved at build time; no `--platform` digest pin. |
| `golang:1.25-bookworm` | `runtimes/base/Dockerfile:1`, `:17` | **floating tag** | High — same as above. | Used for both `redact` and `workspace-agentd` builders. |
| `gcr.io/distroless/static:nonroot` | `api/Dockerfile:36`, `controller/Dockerfile:36` | **floating tag** | Medium — Google-controlled, lower likelihood, but a compromised release pipeline is still possible. | Distroless does publish per-image digests; this Dockerfile does not consume them. |
| `debian:bookworm-slim` | `runtimes/base/Dockerfile:33` | **floating tag** | High — `library/debian` on Docker Hub. Also the *largest* package surface of any of our images. | A poisoned `debian:bookworm-slim` re-tag would land in every workspace. |
| `node:22-bookworm-slim` | `frontend/Dockerfile:1` | **floating tag** | High. | Frontend build only — but injects into the JS bundle that runs in the operator browser. |
| `nginx:1.27-alpine` | `frontend/Dockerfile:8` | **floating tag** | High. | Final runtime for the frontend image. |
| `ghcr.io/lenaxia/llmsafespace/base:latest` | `runtimes/python/Dockerfile:1`, `runtimes/nodejs/Dockerfile:1`, `runtimes/go/Dockerfile:1`, `runtimes/python/Dockerfile.ml` (transitively) | **`:latest` tag** | Self-poisoning — any push to GHCR with `latest` tag substitutes the parent for downstream language images at next build. | Effectively makes the language images non-reproducible across rebuilds. |

**No `FROM image@sha256:...` references exist anywhere in the tree.**

```bash
$ grep -rE 'FROM\s+[^[:space:]]+@sha256' api/Dockerfile controller/Dockerfile frontend/Dockerfile runtimes/
$  # (zero hits)
```

The CI workflow (`.github/workflows/ci.yml`) does **not** verify any base-image signatures. There is no `cosign verify` step, no `docker buildx imagetools inspect --raw` digest lock, no Notary v2 step. Renovate (`.github/renovate.json:8-11`) is configured to automerge minor/patch base-image updates by tag — which means a poisoned re-tag is bot-promoted to a green PR and merged automatically.

> **R-1.9-F1 (HIGH):** All `FROM` lines float. Pin every `FROM` by `tag@sha256:...`. Add Renovate `pinDigests: true` and disable `automerge` for docker datasources until digests are pinned. Combined with cosign verification (§4 below) this also fixes the re-tag attack.

---

## 2. Binary downloads and verification

Every `curl` / `wget` / language-installer call that crosses a network boundary, file:line and verification status:

| Dockerfile:line | Tool | URL | Verification |
|---|---|---|---|
| `runtimes/base/Dockerfile:73-77` | `curl --fail --show-error --location` for **opencode** tarball | `https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${OC_ARCH}.tar.gz` | **TLS only.** No SHA256, no signature, no GitHub attestation. Comment at lines 59–66 explicitly documents this gap and the upstream's failure to publish Sigstore attestations. |
| `runtimes/base/Dockerfile:92-98` | `curl --fail --show-error --location` for **mise** tarball | `https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-${MISE_ARCH}.tar.gz` | **TLS only.** No checksum and no signature, despite `jdx/mise` actually publishing a `mise-v*-linux-*.tar.gz.sha256` file alongside every release (we just don't fetch it). This is fixable today without upstream cooperation. |
| `runtimes/base/Dockerfile:119-130` | `mise install --system` for python/node/rust/go/java/maven/gradle | mise's own per-tool plugin URLs, e.g. `nodejs.org`, `static.rust-lang.org`, `dl.google.com/go`, etc. | **TLS only AND `MISE_GITHUB_ATTESTATIONS=0`** (see §5 below). Mise normally verifies GitHub-hosted downloads against the GH attestation store; this is explicitly disabled. |
| `runtimes/nodejs/Dockerfile:8` | `curl -fsSL https://deb.nodesource.com/setup_18.x \| bash -` | NodeSource bash installer | **TLS only AND piped to `bash` as root.** No `.sig`/`.asc`, no checksum. Installer adds an apt source list which downstream `apt-get install nodejs` consumes — the trust scope therefore extends to all subsequent apt operations. |
| `runtimes/go/Dockerfile:8` | `curl -sSL https://golang.org/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz \| tar -C /usr/local -xz` | Go toolchain tarball | **TLS only.** `golang.org/dl/` publishes `.sha256` files at predictable URLs and Go's release notes recommend verifying — we do not. Unzipped directly into `/usr/local/`. |
| `runtimes/go/Dockerfile:21-29` | `go install …@v…` for `gorilla/mux`, `gin-gonic/gin`, `spf13/cobra`, `stretchr/testify`, `gonum.org/v1/gonum` | Go module proxy | **`GOPROXY=direct` and `GOSUMDB=off`** (lines 12–13) — both checks bypassed. Modules are fetched straight from origin VCS with no checksum-database verification. |
| `runtimes/python/Dockerfile:19-24` | `pip install --no-cache-dir … requests==…` etc. | PyPI | TLS to PyPI; pip 23+ checks PyPI hash from the JSON index. **No `--require-hashes` and no requirements.txt with hashes** — so the protection only catches a registry-side misconfiguration, not a registry compromise. |
| `runtimes/python/Dockerfile.ml:20-33` | `pip install … numpy/pandas/matplotlib/sklearn/tensorflow/torch …` | PyPI + `download.pytorch.org` | Same as above. PyTorch pulled from `https://download.pytorch.org/whl/cpu` — a registry that has no hash-on-disk and where a server compromise replaces wheels silently. No `--require-hashes`. |
| `runtimes/nodejs/Dockerfile:15-32` | `npm install -g typescript@5.1.6 …` | npm registry | npm 9+ checks the integrity hash from `package-lock.json` — but **no `package-lock.json` is generated or shipped in this Dockerfile;** packages are installed by name+version directly via the registry's JSON `dist.integrity`. Equivalent to PyPI: registry-side compromise ⇒ undetected. |
| `frontend/Dockerfile:4` | `npm ci` | npm registry | **`npm ci` *does* enforce `package-lock.json` integrity hashes.** This is the only download in the entire tree that is cryptographically verified end-to-end. |
| `api/Dockerfile:18`, `controller/Dockerfile:18`, `runtimes/base/Dockerfile:11`/`:27` | `go mod download` | `proxy.golang.org` then `sum.golang.org` | **Verified.** Module checksums in `go.sum` are checked against `sum.golang.org` automatically (`GOSUMDB=sum.golang.org` is the default and is not overridden in these three files). End-to-end verified provided `go.sum` is trustworthy. |

**Summary:** of the 11 download sites, only 2 (`npm ci` for the frontend, `go mod download` for the Go binaries) are cryptographically verified. The other 9 rely on TLS to a single upstream, with no integrity check. The 9 unverified downloads are concentrated in the runtime images — i.e. in the workspace pods that host opencode and execute agent / user code.

> **R-1.9-F2 (HIGH):** Add SHA256 verification to every binary download. For mise this requires fetching the published `.sha256`. For opencode this requires committing the expected hash to git alongside the version pin (since upstream doesn't publish one). For Go toolchain in `runtimes/go/Dockerfile`, fetch and verify `.sha256` from `golang.org/dl/`.

> **R-1.9-F3 (HIGH):** Replace `curl … | bash -` in `runtimes/nodejs/Dockerfile:8` with the apt key/repo procedure that sets `signed-by=/etc/apt/keyrings/nodesource.gpg`. This is documented by NodeSource in its readme; the current pipe-to-bash is the legacy install path.

> **R-1.9-F4 (MEDIUM):** Convert pip installs in `runtimes/python/Dockerfile`/`Dockerfile.ml` to a `requirements.txt` + `pip install --require-hashes -r requirements.txt`. This eliminates the registry-side-compromise window.

> **R-1.9-F5 (MEDIUM):** Convert npm installs in `runtimes/nodejs/Dockerfile:15-32` to use a committed `package.json` + `package-lock.json` and `npm ci`, mirroring the protection already in place for the frontend image.

---

## 3. Build-arg secrets

ARGs in the Dockerfiles, with sensitivity assessment:

| File:line | ARG | Source | Sensitivity |
|---|---|---|---|
| `api/Dockerfile:14`, `controller/Dockerfile:14`, `runtimes/base/Dockerfile:7,23` | `GOPROXY=https://proxy.golang.org,direct` | hardcoded default | None — endpoint URL only. |
| `api/Dockerfile:26-29`, `controller/Dockerfile:26-29` | `VERSION=dev`, `BUILD_TIME=unknown`, `TARGETARCH=amd64` | hardcoded defaults | None — embedded in `-ldflags`, visible from `--version`. |
| `runtimes/base/Dockerfile:3,19,35` | `TARGETARCH=amd64` | buildx-injected | None. |
| `runtimes/base/Dockerfile:43,45` | `OPENCODE_VERSION=1.15.12`, `MISE_VERSION=2026.5.15` | hardcoded defaults | None — public version strings. |
| `runtimes/python/Dockerfile.ml:10-15` | NUMPY/PANDAS/MATPLOTLIB/SCIKIT_LEARN/TENSORFLOW/PYTORCH versions | hardcoded defaults | None — public version strings. |
| `runtimes/go/Dockerfile:6-7` | `TARGETARCH`, `GO_VERSION=1.20.5` | hardcoded | None. |

**No credentials are ever passed as build-args.** The CI workflow (`.github/workflows/ci.yml`) likewise references only one secret — `secrets.GITHUB_TOKEN` (lines 119, 163, 207, 251) — and that secret is consumed by `docker/login-action@v3` to push to GHCR. It is **not** forwarded into the `docker build` context as a `--build-arg` or via `--secret`.

```bash
$ grep -nE '^\s*build-args:|^\s*secrets:|--build-arg|--secret' .github/workflows/ci.yml
# (zero hits — no secrets are passed into Docker builds)
```

> **R-1.9-F6 (informational, no action):** Build-arg secret hygiene is currently *clean*. As the project adds private-registry push or Code Artifact mirroring, future ARGs MUST use `--mount=type=secret` (BuildKit) rather than `ARG` to avoid landing creds in the image history.

---

## 4. Image signing on push

**Status: no signing of any kind.** Every image push job in `.github/workflows/ci.yml` follows this template:

```yaml
- uses: docker/login-action@v3
  with:
    registry: ghcr.io
    username: ${{ github.actor }}
    password: ${{ secrets.GITHUB_TOKEN }}
- uses: docker/metadata-action@v5
  ...
- uses: docker/build-push-action@v6
  with:
    push: ${{ github.event_name != 'pull_request' }}
    tags: ${{ steps.meta.outputs.tags }}
```

(See `build-api` lines 100–142, `build-controller` lines 144–186, `build-runtime` lines 188–230, `build-frontend` lines 232–274.)

**What is missing:**
- No `sigstore/cosign-installer@v3` step.
- No `cosign sign` of pushed image digests.
- No `actions/attest-build-provenance@v2`.
- No SBOM emission via `anchore/sbom-action` or `aquasecurity/trivy-action`.
- No `--provenance=true` flag on `docker/build-push-action` (which would emit a buildkit-native attestation in-band; default is provenance generation but NOT signed).
- The `buildx` step (`docker/setup-buildx-action@v3`) defaults to producing in-toto provenance attestations with `mode=min`, but those attestations are **unsigned**: anyone with push access can append/replace them on the registry side.

**Registry properties (GHCR):** GHCR enforces immutability per *digest* — a digest, once written, cannot be rewritten. But **tag mutability is permitted**. The CI emits 5+ tag aliases per image (`sha-<commit>`, `ts-<unix>`, `dev`, semver patterns, `latest` — see lines 125–132). All of those tags are mutable: a future workflow run, or a manual `docker push`, can repoint any of them to any digest. Without signature verification at the consumer, downstream Helm charts (`charts/llmsafespace/values.yaml` references `:dev` etc.) have no way to detect a tag-swap attack.

> **R-1.9-F7 (HIGH):** Add cosign keyless (Fulcio) signing to every push job. Pattern:
> ```yaml
> - uses: sigstore/cosign-installer@v3
> - run: cosign sign --yes ghcr.io/lenaxia/llmsafespace/api@${{ steps.build.outputs.digest }}
> ```
> Combined with `cosign verify` at deploy time (controller image-pull policy or admission webhook) this defeats both tag-swap and credential-theft attacks.

> **R-1.9-F8 (HIGH):** Add `actions/attest-build-provenance@v2` after each push to emit a SLSA Build L3 statement. GitHub's hosted Fulcio root makes this nearly free.

> **R-1.9-F9 (MEDIUM):** Add SBOM generation. `docker/build-push-action@v6` supports `sbom: true`; complement with `anchore/sbom-action` or `syft` for a CycloneDX/SPDX upload. This closes the SBOM gap RT-1.5-F10 already noted.

---

## 5. Transitive build trust — the dependency-of-dependency exposure

The runtime base image installs a *language runtime manager* (mise) which then installs language runtimes. Each layer adds an unverified-download surface:

```
GitHub (github.com)
  ↓  https://github.com/anomalyco/opencode/releases/...      ← TLS only, no checksum (RT-1.9-F2)
  ↓  https://github.com/jdx/mise/releases/...                ← TLS only, no checksum (RT-1.9-F2)
mise (now installed, unverified)
  ↓  MISE_GITHUB_ATTESTATIONS=0   (runtimes/base/Dockerfile:120)
  ↓  mise's per-tool plugins fetch from:
       - python.org / nodejs.org / static.rust-lang.org / dl.google.com/go
       - GitHub release assets for several tools
mise toolchain ecosystem (now installed, unverified)
  ↓  user code in workspace pods does:
       pip install …, npm install …, cargo install …, go install …
```

Every arrow is TLS-only. There is no signed-by-vendor attestation that survives end-to-end.

**Critical detail: `MISE_GITHUB_ATTESTATIONS=0`.** Mise normally fetches each GitHub-hosted toolchain release asset and verifies its provenance against `https://attestation.dev/api/v2/...` (the GitHub Sigstore attestation store). Setting `MISE_GITHUB_ATTESTATIONS=0` bypasses that check entirely. The Dockerfile justifies this on lines 117–118 (*"no outbound OIDC calls in restricted build environments"*) — but the same flag is set on the GitHub-hosted CI runner where outbound is unrestricted. That is a misconfiguration: in CI, the attestation check should be enabled.

**Other transitive trust paths in the tree:**
- `npm install -g typescript@5.1.6 …` in `runtimes/nodejs/Dockerfile:15-32`: registry trust, no integrity hash file (no lockfile), depends on the npm registry's per-package signature (which exists for some packages but is not enforced by `npm install` without `--strict-integrity`).
- `pip install requests==2.31.0 …` in `runtimes/python/Dockerfile:19-24`: registry trust (PyPI), no `--require-hashes`, no `--no-binary :all:` to exclude pre-built wheels (so a backdoor in `requests-2.31.0-cp39-cp39-…whl` is not detected by source-code review).
- The `nvm`-equivalent in `runtimes/nodejs/Dockerfile:8` is the NodeSource setup script piped to bash. **This is itself a transitive-trust attack vector**: NodeSource is a third party; any compromise of `deb.nodesource.com` repaints node binaries with attacker code, root-installed.

> **R-1.9-F10 (HIGH):** Set `MISE_GITHUB_ATTESTATIONS=1` (default) in `runtimes/base/Dockerfile:120` for CI builds. Keep the override for restricted air-gapped builds, but make the secure default the CI default. Or: use a build-arg `ARG MISE_GITHUB_ATTESTATIONS=1` and only set 0 when explicitly overridden.

> **R-1.9-F11 (MEDIUM):** Once mise is verified, lock the runtime versions installed by mise. Currently `python@latest`, `node@lts`, etc. (`runtimes/base/Dockerfile:122-128`) are floating. Pin to specific versions in `/etc/mise/config.toml` so a poisoned-but-newer toolchain release does not silently land on the next image rebuild.

---

## 6. Reproducibility

**Result: builds are NOT reproducible.** Failure modes:

| Source of nondeterminism | File:line | Effect |
|---|---|---|
| Floating base-image tags | All 6 Dockerfiles (see §1) | Two builds against `golang:1.25` weeks apart get different binaries. |
| `go mod download` against `proxy.golang.org` | `api/Dockerfile:18`, `controller/Dockerfile:18`, `runtimes/base/Dockerfile:11`/`:27` | `go.sum` pins versions, but proxy infrastructure (cache state, response variation) and absent `-trimpath` in api/controller leave residual non-determinism. **`runtimes/base/Dockerfile:14-15` and `:30-31` *do* set `-trimpath`** — good — but the api and controller Dockerfiles do **not** (`api/Dockerfile:30-34`, `controller/Dockerfile:30-34`). Without `-trimpath`, the binary embeds `$GOPATH` and absolute build paths. |
| `mise install … @latest`/`@lts`/`@stable` | `runtimes/base/Dockerfile:122-128` | Floats. Today's `python@latest` is not tomorrow's. |
| `apt-get update && apt-get install …` with no pin | `runtimes/base/Dockerfile:49-51`, `runtimes/python/Dockerfile:6-12`, `runtimes/nodejs/Dockerfile:6-12` | Apt indexes float; package versions installed depend on the snapshot at build time. |
| `pip install` without `--require-hashes` | `runtimes/python/Dockerfile:19-24`, `Dockerfile.ml:20-33` | Although `==` is used, the wheel hash is not pinned. |
| `npm install` without lockfile | `runtimes/nodejs/Dockerfile:15-32` | `@5.1.6` etc. specifies major+minor+patch, but transitive dep resolution is not pinned. |
| `BUILD_TIME` ldflag | `api/Dockerfile:27,32`, `controller/Dockerfile:27,32` | Embeds wall-clock; differs every build. (Defaulted to `unknown` if not passed; CI does not pass it, so in CI this is *not* a source of non-reproducibility — but `Makefile`'s `docker-build` does pass `$(date)`.) |
| Buildx GHA cache (`cache-from: type=gha`, `cache-to: type=gha,mode=max`) | `.github/workflows/ci.yml:141-142,185-186,229-230,273-274` | Cached layers may be reused across builds in ways that hide nondeterminism — and a poisoned cache entry from one workflow run is inherited by the next. |
| `--no-cache` | nowhere | Not used. |
| `SOURCE_DATE_EPOCH` | nowhere | Not set. No reproducible-builds-style timestamp pinning. |

> **R-1.9-F12 (MEDIUM):** Add `-trimpath -buildvcs=false` to `api/Dockerfile:30` and `controller/Dockerfile:30`. Set `SOURCE_DATE_EPOCH` to the commit timestamp in CI and propagate via `--build-arg` so layer mtimes are deterministic. Together with digest-pinned bases (R-1.9-F1) this approaches per-architecture bit-for-bit reproducibility.

> **R-1.9-F13 (MEDIUM):** Disable `cache-to: type=gha,mode=max` in production (release) builds; keep it for PR builds only. A poisoned cache entry survives across runs because BuildKit content-addresses layer outputs by command-string + input-hash; an attacker who briefly compromises the cache can persist a backdoor that activates on the next build with a matching command-string.

---

## 7. Provenance attestation — SLSA level

**Current SLSA level: ~L0.** Concretely:

| SLSA L1 requirement | Status |
|---|---|
| Build process is scripted | **Met** — `.github/workflows/ci.yml`. |
| Build platform generates provenance | **Partial** — buildx default emits in-toto provenance into the image manifest, but it is unsigned and not verified anywhere. |
| Provenance is available to consumer | **Not met** — provenance is published as part of the image manifest list, but no attestation index, no tlog reference, no documented consumer verification. |

| SLSA L2 requirement | Status |
|---|---|
| Hosted build platform | **Met** — GitHub Actions hosted runners. |
| Provenance is signed | **Not met** — no cosign / no sigstore / no `actions/attest-build-provenance`. |
| Provenance binds source revision | **Partial** — buildx provenance does include the source ref/digest, but again unsigned. |

| SLSA L3 requirement | Status |
|---|---|
| Build is hermetic and parameterless | **Not met** — `--build-arg GOPROXY=` is exposed; runtime base downloads from the public internet at build time. |
| Provenance is non-falsifiable | **Not met** — no signing key bound to the build platform. |
| Source is version-controlled | **Met** — git/GitHub. |

**No GitHub Actions artifact attestations.** A search for `actions/attest-` in the workflow returns zero hits. The repository does not opt into the (free, hosted) `actions/attest-build-provenance` flow that would produce a signed SLSA L3 statement bound to the runner identity, or `actions/attest-sbom` for SBOM attestations.

> **R-1.9-F14 (HIGH):** Add `actions/attest-build-provenance@v2` and `actions/attest-sbom@v2` to every build job. With `cosign verify-attestation` at deploy time (admission webhook or pull-policy), this raises the project from L0 to L3 in roughly 30 lines of YAML and requires no new infrastructure. SLSA L3 is the threshold below which "trust the registry" remains the de-facto policy.

---

## 8. Findings summary

| ID | Severity | Issue | Phase-2 action |
|---|---|---|---|
| RT-1.9-F1 | **High** | All `FROM` lines float; no digest pins. Tag-poisoning at any of 6 upstream registries lands a malicious base. Renovate auto-merges base-image bumps (`.github/renovate.json:8-11`). | Pin `image:tag@sha256:...` for every `FROM`. Disable Renovate automerge for docker until pinned; then enable `pinDigests: true`. |
| RT-1.9-F2 | **High** | `runtimes/base/Dockerfile:73-77` (opencode) and `:92-98` (mise) are TLS-only — no checksum, no signature. Comments at lines 59-66 acknowledge this is a known gap. | Commit expected SHA256 to git, verify with `sha256sum -c`. Switch to `cosign verify-blob` once upstream publishes Sigstore attestations. |
| RT-1.9-F3 | **High** | `runtimes/nodejs/Dockerfile:8` pipes NodeSource installer to root bash with no GPG/checksum verification. | Replace with the documented apt-keyring procedure (`signed-by=/etc/apt/keyrings/nodesource.gpg`). |
| RT-1.9-F4 | Medium | `runtimes/python/Dockerfile:19-24` and `Dockerfile.ml:20-33` use `pip install` without `--require-hashes`. PyPI compromise = silent backdoor. | Switch to `requirements.txt` + `pip install --require-hashes`. |
| RT-1.9-F5 | Medium | `runtimes/nodejs/Dockerfile:15-32` uses `npm install` without a lockfile. Registry compromise = silent backdoor. | Convert to `package.json` + `package-lock.json` + `npm ci`. |
| RT-1.9-F6 | Informational | No build-arg secrets currently. | Future-proof: when adding private registries, use BuildKit `--mount=type=secret`, never `ARG`. |
| RT-1.9-F7 | **High** | No image signing on push. Every push to GHCR is unsigned. No `cosign sign` step. | Add `sigstore/cosign-installer@v3` + keyless `cosign sign` after each `build-push-action`. Verify at deploy. |
| RT-1.9-F8 | **High** | No SLSA build provenance attestation. | Add `actions/attest-build-provenance@v2` to each push job. Combined with §7 lifts project to SLSA L3. |
| RT-1.9-F9 | Medium | No SBOM. Cross-references RT-1.5-F10. | Enable `sbom: true` on `docker/build-push-action`; add `actions/attest-sbom@v2`. |
| RT-1.9-F10 | **High** | `MISE_GITHUB_ATTESTATIONS=0` (`runtimes/base/Dockerfile:120`) disables the only provenance check on language runtime downloads. | Default to `1`; allow `ARG`-override only for air-gapped restricted builds. |
| RT-1.9-F11 | Medium | mise installs use `@latest`/`@lts`/`@stable` — floating. | Pin specific versions in `/etc/mise/config.toml`. |
| RT-1.9-F12 | Medium | Builds non-reproducible. `api`/`controller` Dockerfiles miss `-trimpath`. No `SOURCE_DATE_EPOCH`. | Add `-trimpath -buildvcs=false`; set `SOURCE_DATE_EPOCH`. |
| RT-1.9-F13 | Medium | GHA buildx cache persists across runs (`cache-to: type=gha,mode=max`). A poisoned cache entry is inherited. | Disable max-mode cache for release builds; keep for PR builds. |
| RT-1.9-F14 | **High** | Project effectively at SLSA L0. No signed provenance. | Implement F7 + F8. |
| RT-1.9-F15 | **High** | `runtimes/go/Dockerfile:12-13`: `GOPROXY=direct` AND `GOSUMDB=off`. Go module verification bypassed for the toolchain that compiles user code. | Remove these overrides; let Go default to `proxy.golang.org` + `sum.golang.org`. |
| RT-1.9-F16 | Medium | `runtimes/go/Dockerfile:8` curls `golang.org/dl/go${GO_VERSION}.linux-…tar.gz` with no checksum. Go publishes `.sha256` at predictable URLs. | Fetch and verify `.sha256` next to the tarball. |

---

## 9. Cross-references

- RT-1.5-RT-1.6 (`design/stories/epic-17-security-review/phase-1/RT-1.5-RT-1.6-deps-and-images.md`):
  - RT-1.5-F3 (Go runtime stdlib CVEs) intersects with RT-1.9-F1 — pinning `golang:1.25.10-bookworm@sha256:...` simultaneously fixes both.
  - RT-1.6-F1 (floating base tags) is the same finding as RT-1.9-F1; this report adds the CI-pipeline angle and re-tag exposure.
  - RT-1.6-F2 (opencode/mise unverified downloads + `MISE_GITHUB_ATTESTATIONS=0`) is the same finding as RT-1.9-F2 + RT-1.9-F10.
  - RT-1.5-F10 (no SBOM) is the same finding as RT-1.9-F9.
- Phase 0 worklog `0083_2026-05-30_epic17-phase-0-prod-kit.md` documents that `cosign`, `syft`, `trivy` are NOT yet in the analyst toolchain; the recommended fixes assume those are installed before Phase 2 exploits begin.
- The Helm chart references images by *floating* tag (e.g. `:dev`, `:latest`, `:sha-<commit>`) — in deployment, even if signing is added, the chart must be amended to pin by digest for cosign verification to bind. That work is out of scope for RT-1.9 but tracked on the Phase-2 to-do.

---

## 10. Phase-1 conclusion: where attackers could inject a backdoor

Threat model, ordered by realistic insertion difficulty for a remote attacker:

1. **Compromise of `anomalyco/opencode` GitHub release publishing key.** Single point of failure; no signature; lands in every workspace pod. RT-1.9-F2.
2. **Compromise of `jdx/mise` GitHub release publishing key.** Same — lands as the polyglot runtime manager that subsequently installs Python/Node/Go/Rust/Java. RT-1.9-F2 + RT-1.9-F10.
3. **Compromise of NodeSource `setup_18.x` script delivery.** Pipe-to-bash as root — RCE in image build for the nodejs runtime. RT-1.9-F3.
4. **Tag-swap on `library/debian:bookworm-slim`, `library/golang:1.25`, etc.** Requires registry-side compromise; less likely for Docker Hub, more likely for less-watched mirrors. Renovate automerge accelerates the impact. RT-1.9-F1.
5. **Compromise of GitHub Actions `secrets.GITHUB_TOKEN` for the few seconds between `docker/login-action` and `docker/build-push-action`.** Low likelihood per individual run, but cumulative; with no signing the attacker can publish under our namespace and downstream consumers cannot detect. RT-1.9-F7 + RT-1.9-F8.
6. **GHA cache poisoning.** `cache-to: type=gha,mode=max`; an attacker with brief write access to the GHA cache injects a layer that activates on subsequent rebuilds. RT-1.9-F13.
7. **PyPI / npm / Go module registry compromise.** Defeats source-level review since wheels/binaries are pre-built. Mitigated for our own Go modules (`go.sum` + GOSUMDB) but **not** for the runtime images' pip/npm installs. RT-1.9-F4 + RT-1.9-F5 + RT-1.9-F15.

The fix set is mechanical and small: pin digests, add cosign signing, add SLSA provenance attestations, verify the two binary downloads in `runtimes/base/Dockerfile`, fix `MISE_GITHUB_ATTESTATIONS`, fix the Node.js install path, restore `GOSUMDB`. The combination raises the project from SLSA L0 to a signed, verifiable, reproducible build pipeline at SLSA L3 with no new infrastructure cost (GitHub-hosted Sigstore root is free and integrated).

Phase 2 should validate any of items 1–7 above by attempting in-tree exploitation: in particular, test whether a controlled image push under a sibling tag is acted on by the deployment Helm chart without any signature check.
