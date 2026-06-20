# Worklog: Fix relay-proxy --upstream Flag (was silently ignored)

**Date:** 2026-06-20
**Session:** Fix the --upstream CLI flag that cloud-init renders but the relay-proxy binary ignored
**Status:** Complete

---

## Objective

`controller/internal/relay/cloudinit.go:42` renders `ExecStart=/usr/local/bin/relay-proxy --upstream={{ .UpstreamURL }}` into each relay VM's systemd unit, fed from `relay.Spec.UpstreamURL`. But `cmd/relay-proxy/main.go` did no flag parsing — it read only the `UPSTREAM_URL` env var via `os.Getenv`, ignoring `os.Args` entirely. So the rendered `--upstream=<spec.upstreamURL>` was silently dropped and every VM fell back to the hardcoded `defaultUpstreamURL`.

This was masked while every default agreed (and after the 2026-06-20 revert, all defaults are `opencode.ai/zen/v1` again), but it breaks the moment an operator sets a per-CR `spec.upstreamURL` override — the override renders into cloud-init and is discarded. The CRD explicitly invites this override ("operators should override to their own endpoint"), so the bug is live.

---

## Work Completed

### Refactored `loadConfig` to parse CLI flags (flag > env > default precedence)

`cmd/relay-proxy/main.go`:
- `loadConfig()` → `loadConfig(args []string) (config, error)`, using `flag.NewFlagSet` so the call is testable and args are explicit (`os.Args[1:]` from `main`).
- Three flags for a complete, consistent CLI: `--upstream`, `--listen`, `--keepalive-interval`. Each defaults to the corresponding env var, which defaults to the hardcoded constant. Precedence: explicit flag > env > default.
- `main()` now handles the error return (rejects unknown flags rather than silently ignoring — defends against typos in the cloud-init template).

### TDD: 6 new tests in `cmd/relay-proxy/main_test.go` (was untested before)

- `TestLoadConfig_UpstreamFlagOverridesEnv` — flag beats env (the core bug)
- `TestLoadConfig_UpstreamFlagEqualsForm` — `--upstream=X` form (systemd tokenization)
- `TestLoadConfig_UpstreamEnvWhenNoFlag` — env honoured when flag absent (back-compat)
- `TestLoadConfig_UpstreamDefaultWhenNeither` — hardcoded default applies (bare invocation → zen)
- `TestLoadConfig_ListenAndKeepaliveFlags` — other knobs parse
- `TestLoadConfig_InvalidFlagReturnsError` — unknown flag rejected

RED confirmed before implementation (compile failure: old `loadConfig()` took no args, returned 1 value). GREEN after.

---

## Key Decisions

1. **Added all three flags, not just `--upstream`.** A partial CLI (only `--upstream`, env-only for the rest) is inconsistent and surprising. `--listen` especially matters — see Finding F1 below. Three flags is idiomatic Go (`flag` package, define-all-and-parse) and not over-engineering; it's the minimal complete interface.

2. **Precedence = flag > env > default.** Standard CLI convention; lets cloud-init (flag) override any operator-set env, while env still works for bare invocations.

---

## Assumptions (Rule 7) and validation

- **A-FLAG-NAME: cloud-init renders `--upstream`, binary must parse `upstream`.** Validated: `controller/internal/relay/cloudinit.go:42` → `--upstream={{ .UpstreamURL }}`; Go's `flag` package accepts both `--upstream X` and `--upstream=X` (test covers both).
- **A-PRECEDENCE: flag should beat env.** Validated by reasoning: cloud-init is the controller's authoritative declaration per-CR; an operator-set env on the VM image would be a stale default that the per-CR override must win over.
- **A-NO-BREAKAGE: no other caller of `loadConfig`.** Validated: `loadConfig` is package-private, called only from `main()` in the same package; signature change is contained.

---

## Adversarial Self-Review (Rule 11)

### F1 — REAL BUG (distinct from this fix): AWS/GCP relay VMs cannot bind (hardcoded OCI listen address)

**Finding:** `relay-proxy`'s `defaultListenAddr = "10.42.42.2:8080"` is the OCI relay's WG IP (`wgOCIRelay`, `constants.go:27`). AWS relays get `wgAWSRelay = "10.42.42.4"` (`constants.go:26`), GCP gets `.3`. The WG config (`wireguard.go:91`) correctly assigns each VM its own wgIP on wg0. But `cloudinit.go` renders only `--upstream` into the systemd unit — it does NOT pass `--listen` or set `LISTEN_ADDR`. So on an AWS VM (wg0 has `.4`), `relay-proxy` tries to bind to `10.42.42.2:8080`, an IP that does not exist on that VM → `EADDRNOTAVAIL` → the relay-proxy service fails to start. Only the OCI relay binds correctly, by coincidence.

**Severity:** High — breaks the entire non-OCI half of the fleet. Currently masked because `controller.inferenceRelay.enabled` defaults to `false` and no live fleet is running; would surface immediately on any real deploy with AWS.

**Remediation (not done in this fix — separate scope):** `cloudinit.go` should render `--listen=<wgIP>:8080` alongside `--upstream`, where `wgIP` is the per-provider value from `wgIPForProvider()`. Requires threading `wgIP` into `CloudInitConfig` (it currently only has the rendered `WgConfig` string, `UpstreamURL`, `RouterEndpoint`). Trivial change to the reconciler's `provisionRelay` (which already computes `wgIPForProvider(providerSpec.Provider)` at `reconciler.go:350,380`).

**Recommendation:** fold into the workspace→router wiring work (next item) or do as a standalone follow-up before any live fleet deploy. The `--listen` flag parsing shipped here is the prerequisite — once cloud-init renders `--listen`, it will work.

### False alarms (documented per Rule 11)

- *"Does Go's flag package accept `--upstream=X` from systemd ExecStart?"* Yes — `flag.Parse` accepts both `-flag value`, `--flag value`, `-flag=value`, `--flag=value`. Covered by `TestLoadConfig_UpstreamFlagEqualsForm`.
- *"Will the new error-on-bad-flag break the running fleet?"* No fleet is running (feature disabled by default); and rejecting bad flags is strictly safer than the prior silent-ignore.

---

## Tests Run

- `go test ./cmd/relay-proxy/` (full suite, incl. existing keepalive + proxy tests) — PASS
- `go vet ./cmd/relay-proxy/` — clean
- `go build ./cmd/relay-proxy/` — clean

---

## Next Steps

1. **F1 fix (recommend before live fleet deploy):** thread `wgIP` into `CloudInitConfig`, render `--listen=<wgIP>:8080` in cloud-init. The `--listen` flag is already parsed and tested.
2. **Workspace→router wiring (the other open gap):** make `INFERENCE_RELAY_BASEURL` resolve to `http://relay-router:8080` when `controller.inferenceRelay.enabled`, per Design Principle 6. Pairs naturally with F1 since both touch the relay-VM→pod path.

---

## Files Modified

- `cmd/relay-proxy/main.go` — `loadConfig` refactored to parse flags; `main` updated for new signature; added `flag` import
- `cmd/relay-proxy/main_test.go` — new file, 6 tests
