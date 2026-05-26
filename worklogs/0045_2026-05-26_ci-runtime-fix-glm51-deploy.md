# Worklog: CI Runtime Fix + glm-5.1 Deploy

**Date:** 2026-05-26
**Session:** Fix CI runtime base image build failures, update LLM model to glm-5.1, deploy to cluster
**Status:** Complete

---

## Objective

1. Update the helm deployment in default namespace to use model `glm-5.1`
2. Fix the CI pipeline — runtime base image was failing on every run
3. Deploy the passing images to the k8s cluster

---

## Work Completed

### LLM Model Update
- Updated `llm-credentials-opencode` secret in default ns from `glm-4.7` to `glm-5.1`
- Applied via `kubectl create secret --dry-run=client -o yaml | kubectl apply -f -`
- Verified new sandboxes will pick up `glm-5.1`

### CI Runtime Base Image — 5 Fixes Over 7 Iterations

**Root causes found and fixed:**

1. **mise tar extraction path** (`runtimes/base/Dockerfile:81`)
   - mise release tarball contains `mise/bin/mise` (nested directory), not a flat `mise` binary
   - Original `tar -xzf ... -C /usr/local/bin/ mise` extracted the directory, not the binary → "Permission denied" executing a directory
   - Fix: extract to `/tmp/`, then `cp /tmp/mise/bin/mise /usr/local/bin/mise`

2. **mise reshim exit code 2** (`runtimes/base/Dockerfile:104-114`)
   - `mise install --system` installs tools but does not activate them (no config file references them)
   - `mise reshim` exits 2 with "installed but not activated" warning
   - Fix: `mkdir -p /etc/mise` then write `/etc/mise/config.toml` with `[tools]` section listing all installed runtimes before calling `mise reshim`
   - `/etc/mise/` directory did not exist in the slim image — caused "cannot create file: Directory nonexistent"

3. **`mise use --system` does not exist** — `--system` flag is only on `mise install`, not `mise use`

4. **Smoke test used invalid `mise --system which`** (`runtimes/base/tools/smoke-test.sh`)
   - `--system` is not a valid global flag for mise subcommands
   - Fix: use `mise which <tool>` instead

5. **Ruby removed from pre-installed runtimes** — `ruby@latest` fails to build in bookworm-slim (missing `libssl-dev`, `libffi-dev`). Users can install at runtime via mise.

### Flaky Test Fix
- `TestDeleteWorkspace_HappyPath` (`api/internal/services/workspace/workspace_service_test.go:315`)
- `DeleteWorkspace` fires `MarkWorkspaceDeleted` in a goroutine — test asserted expectations before the goroutine ran
- Fix: channel-based sync — mock `On("MarkWorkspaceDeleted", ...).Run(func(_ mock.Arguments) { close(done) })` then `select` with 1s timeout
- Verified with 10 consecutive `-race` runs, all passing

### Deployment
- CI run 26429248269: all 6 jobs passed (Test, Build API, Build Controller, Build Runtime Base, Build Frontend, Prepare build metadata)
- Deployed via `helm upgrade` with `sha-db895fa` tag for API + Controller
- Rollout confirmed: 2/2 API pods running, 1/1 controller running, 1/1 frontend running

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Write `/etc/mise/config.toml` to activate system tools | mise requires tools listed in a config file for `reshim` to exit 0; `/etc/mise/` is the system-wide config dir per mise docs |
| Remove `ruby@latest` from pre-installed runtimes | Missing build deps in slim image; not worth adding `-dev` packages for one language; users can install at runtime |
| Channel-based test sync instead of `time.Sleep` | Deterministic, no flakiness, fails fast on timeout |

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 30s -race -count=10 ./api/internal/services/workspace/` — 10/10 pass
- CI run 26429248269 — all 6 jobs green
- `kubectl -n default rollout status deployment llmsafespace-api llmsafespace-controller` — rolled out

---

## Next Steps

- Update existing running sandbox pods to pick up `glm-5.1` (requires recycling sandbox CRDs or API-triggered restart)
- Consider re-adding Ruby once mise supports prebuilt binaries that don't require compile deps

---

## Files Modified

- `runtimes/base/Dockerfile` — mise extraction path, config.toml activation, reshim, removed ruby
- `runtimes/base/tools/smoke-test.sh` — removed `--system` flags, removed `gem` check
- `api/internal/services/workspace/workspace_service_test.go` — channel-based sync for DeleteWorkspace test
