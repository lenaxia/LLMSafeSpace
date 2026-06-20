# Worklog: Relay Fleet Audit — A23 Correction, Default Revert, and Wiring Fixes

**Date:** 2026-06-20
**Session:** Audit PRs #273/#276/#297/#298; disprove A23; correct thekao default; wire the free-model path end-to-end
**Status:** Complete (one DoD item open — 3 Helm tests gated on CI)

---

## Objective

The operator asked: "Inspect what we've done with PR 298 and other relay-related PRs. Is everything wired up?" What followed was a multi-part session that (1) audited the relay subsystem's wiring, (2) caught and disproved a foundational false premise (A23) that had driven two merged PRs, (3) reverted a shipped default change built on that premise, and (4) fixed the three wiring gaps that left the fleet structurally built but not on the live request path.

This worklog documents the whole arc and indexes the four code-change worklogs (0436–0439) that carry the per-fix detail.

---

## Work Completed

### A. Initial audit (no code change — findings only)

Read PRs #273 (relay CR on first deploy), #276 (router WireGuard sidecar), #297 (router upstream auth injection), #298 (default → thekao), plus Epic 26 (CF Worker origin) and Epic 42 (multi-cloud fleet) design docs. Found three gaps:

1. **Workspace→router not wired.** Enabling the fleet only affected controller-side `/metrics` scraping; workspace pods kept routing free-model traffic to the external CF Worker regardless (Design Principle 6 unimplemented).
2. **relay-proxy ignored its `--upstream` flag.** `cloudinit.go:42` rendered `--upstream=<spec.upstreamURL>` into systemd ExecStart, but `cmd/relay-proxy/main.go` did no flag parsing — it read only the `UPSTREAM_URL` env. Per-CR upstream overrides were silently dropped. Masked while all defaults agreed.
3. **AWS/GCP relay VMs bound to the wrong WG IP** (surfaced during the `--upstream` fix's adversarial review). `relay-proxy`'s hardcoded `defaultListenAddr = "10.42.42.2:8080"` is the OCI IP; AWS (`.4`) and GCP (`.3`) VMs would get `EADDRNOTAVAIL`. Only OCI bound, by coincidence.

### B. A23 disproven (the foundational correction)

Worklog 0420 (2026-06-19) claimed `public` "no longer authorizes Zen inference" — 401 from every IP/header, header-agnostic, IP-independent — based on probing 5 models. The operator pushed back: "A23 is wrong." After initially re-citing 0420 without validating (a process failure I logged), I re-probed live and cloned the opencode repo to read the actual mechanism:

- **Definitive re-probe (residential IP `24.18.52.209`, same IP as the original 401s):**
  - `POST /v1/chat/completions` model=`big-pickle` + `Authorization: Bearer public` → **HTTP 200**, real completion (`deepseek-v4-flash`, 111 tokens).
  - Same key + same IP + `claude-fable-5` → 401.
- **Mechanism (opencode `packages/console/app/src/routes/zen/util/handler.ts:599-603` + `model.ts:26`):** `public` normalizes to `undefined`; `authenticate()` returns OK iff `modelInfo.allowAnonymous` — a **per-model flag** in ZenData, loaded from deploy-time SST secrets (not in the opencode repo, so invisible to the 06-19 probe). Inference authorization is per-model, not per-key, not per-IP.

**Conclusion:** A23 is false. The relay's original purpose (IP distribution for anonymous free-tier traffic, A0) is restored as valid. `public` works for any model Zen flags `allowAnonymous`.

**Process lesson logged:** I leaned on 0420's conclusion instead of re-validating, and over-talked through several false starts before grounding. Adversarial review (Rule 11) on 0420 asked "could this be transient?" but not "could this be per-model?" — the per-model dimension was never tested.

### C. Documentation corrections (8 files)

Corrected every A23 reference to reflect the per-model `allowAnonymous` mechanism:

- `worklogs/0420` — supersession banner + full correction block with re-probe evidence.
- `worklogs/0435` (#298) — marked **⛔ INCORRECT — DO NOT RELY ON THIS WORKLOG.** at the top; redirected to 0420 (correction) and 0436 (the revert).
- `worklogs/0430` (#297) — premise-superseded banner (mechanism sound, rationale unfounded).
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` — A23 row, A22 row, US-42.2 row, day-one-gate, Security §2.
- `cmd/relay-router/proxy.go` — `upstreamAuth` doc (optional capability, not requirement).
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go` — `UpstreamURL` comment.
- `charts/llmsafespaces/crds/inferencerelay.yaml` — CRD schema description.
- `charts/llmsafespaces/values.yaml` — `upstreamAuth` block comment.
- Test comments updated in `cmd/relay-router/proxy_test.go`, `charts/llmsafespaces/chart_test.go`, `tests/epic26/relay_contract_test.go` (assertions verified correct — only stale comments changed).

### D. Default revert: thekao → zen (worklog 0436)

PR #298 flipped the relay fleet's default upstream to `https://ai.thekao.cloud/v1` based on A23. With A23 disproven, the flip solved a non-problem AND broke the out-of-the-box contract: `ai.thekao.cloud` is the maintainer's personal gateway, not something any other operator will have configured. Reverted across all 8 sites to `https://opencode.ai/zen/v1`:

- `pkg/apis/.../inferencerelay_types.go` (kubebuilder annotation + comment)
- `pkg/apis/.../defaults.go` (runtime defaulter — a site PR #298 missed)
- `charts/.../crds/inferencerelay.yaml` (CRD schema default + description)
- `charts/.../values.yaml` (chart value + comment)
- `cmd/relay-router/main.go` (const)
- `cmd/relay-proxy/main.go` (const)
- `api/internal/handlers/relay_admin.go` (Deploy handler default)
- Tests: `defaults_test.go`, `relay_admin_test.go`

Default deploy now: Zen + `Bearer public`, no key injection, no maintainer dependency. `upstreamAuth` mechanism from #297 retained as optional (off by default).

### E. relay-proxy `--upstream` flag fix (worklog 0437)

Refactored `loadConfig()` → `loadConfig(args []string) (config, error)` using `flag.NewFlagSet`. Three flags (`--upstream`, `--listen`, `--keepalive-interval`) with flag > env > default precedence. `main()` rejects unknown flags. 6 TDD tests in new `cmd/relay-proxy/main_test.go`. RED confirmed before implementation.

### F. cloud-init `--listen` per-VM fix (worklog 0438)

`cloudinit.go`: added `WgIP` field; template renders `--listen={{ .WgIP }}:8080`; rejects empty `WgIP` (defense-in-depth — would otherwise bind `0.0.0.0`, breaking the WG-only-listener posture). `reconciler.go` passes `wgIPForProvider(providerSpec.Provider)`. 4 cloud-init tests in `driver_test.go`.

### G. workspace→router wiring (worklog 0439)

`controller-deployment.yaml`: when `controller.inferenceRelay.enabled=true`, `--inference-relay-url` = relay-router via cross-namespace FQDN, `--inference-relay-secret` omitted (router uses WG auth). New `workspaceRouterURL` value (separate from `routerURL` — different consumers, different namespace contexts). FQDN default covers workspace pods in any namespace. 3 chart render tests added.

---

## Key Decisions

1. **`public` is the free-tier key; relay is for free models only.** Operator-confirmed ground truth, restored as the foundational premise (A0). Paid-model traffic never touches the relay (already enforced in agentd `annotateModels`, unchanged).

2. **Default = Zen + `public`, not thekao.** A default deploy must work for any operator. thekao is now an opt-in choice, not the default.

3. **Fleet flag is the workspace-routing switch.** Enabling `controller.inferenceRelay.enabled` automatically re-routes workspace traffic through the router. Operators don't coordinate two URLs.

4. **Separate `workspaceRouterURL` from `routerURL`.** Different consumers (workspace pods possibly cross-ns vs controller always in release-ns). FQDN default for cross-ns correctness.

5. **Did NOT revert #297's injection mechanism.** Sound optional capability for non-Zen upstreams needing a real key; just no longer the necessity 0430 framed it as.

---

## Blockers

- **Helm unavailable locally.** Chart render tests skip in this sandbox (no helm binary, no network to install). The 3 workspace-wiring tests in `chart_test.go` are gated on CI — that is the validation gate for the template change in 0439. Template logic was verified by careful reading against Helm semantics.

---

## Tests Run

- **Ran locally, green:**
  - `go build ./...` — clean.
  - `go vet ./charts/llmsafespaces/ ./controller/...` — clean (chart tests compile).
  - `go test ./controller/internal/relay/` — green (4 cloud-init tests, full package).
  - `go test ./cmd/relay-proxy/` — green (6 new flag tests + existing suite).
  - `go test ./cmd/relay-router/` — green.
  - `go test ./pkg/apis/llmsafespaces/v1/` — green (defaults test updated to zen).
  - `go test ./pkg/repolint/` — green (CRD-drift: Go struct ↔ chart CRD schema agree after revert).
- **Gated on CI (skip locally — no helm):**
  - 3 chart render tests in `charts/llmsafespaces/chart_test.go`: `TestControllerArgs_RoutesWorkspacesThroughRouterWhenFleetEnabled`, `TestControllerArgs_WorkspaceRouterURLOverride`, `TestControllerArgs_PreservesCFWorkerURLWhenFleetDisabled`.
- **Live probes (no test impact):** `big-pickle`/`claude-fable-5`/`deepseek-v4-flash-free` against `opencode.ai/zen/v1` with `Bearer public` from residential IP — confirmed per-model `allowAnonymous` gating.

---

## Next Steps

1. **CI validation of the 3 Helm tests** — the DoD gate for 0439. Fix and re-push on any template-logic failure.
2. **Optional (F1 from 0439):** NOTES.txt warning when both `inferenceRelayURL` and `controller.inferenceRelay.enabled` are set (currently fleet branch wins silently — correct but could confuse).
3. **End-to-end live deploy test** — with 0437+0438+0439, the free-model path is structurally complete: pod → relay-router (cross-ns FQDN) → relay VM (correct per-VM WG bind + per-CR upstream) → zen. A real cluster deploy is the final DoD confirmation.

---

## Files Modified

**Documentation corrections (A23):**
- `worklogs/0420_2026-06-19_us-42.2-inference-probe-public-key-blocker.md` — supersession + correction block
- `worklogs/0430_2026-06-19_router-upstream-auth-injection.md` — premise-superseded banner
- `worklogs/0435_2026-06-19_default-upstream-thekao.md` — marked INCORRECT/ignore
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` — A23/A22/US-42.2/Security §2
- `cmd/relay-router/proxy.go` — `upstreamAuth` comment
- `cmd/relay-router/proxy_test.go` — section comment
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go` — `UpstreamURL` comment
- `charts/llmsafespaces/crds/inferencerelay.yaml` — CRD description
- `charts/llmsafespaces/values.yaml` — `upstreamAuth` comment
- `charts/llmsafespaces/chart_test.go` — test comments
- `tests/epic26/relay_contract_test.go` — diagnostic comment

**Default revert (thekao → zen):**
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go`, `defaults.go`, `defaults_test.go`
- `charts/llmsafespaces/crds/inferencerelay.yaml`, `values.yaml`
- `cmd/relay-router/main.go`, `cmd/relay-proxy/main.go`
- `api/internal/handlers/relay_admin.go`, `relay_admin_test.go`

**relay-proxy flag fix (0437):**
- `cmd/relay-proxy/main.go`, `cmd/relay-proxy/main_test.go` (new)

**cloud-init --listen fix (0438):**
- `controller/internal/relay/cloudinit.go`, `reconciler.go`, `driver_test.go`

**workspace→router wiring (0439):**
- `charts/llmsafespaces/templates/controller-deployment.yaml`, `values.yaml`, `chart_test.go`

**New worklogs:**
- `worklogs/0436_2026-06-20_revert-default-upstream-to-zen.md`
- `worklogs/0437_2026-06-20_fix-relay-proxy-upstream-flag.md`
- `worklogs/0438_2026-06-20_fix-relay-listen-address-per-vm.md`
- `worklogs/0439_2026-06-20_workspace-relay-router-wiring.md`
- `worklogs/0445_2026-06-20_relay-fleet-audit-a23-revert-wiring.md` (this entry)
