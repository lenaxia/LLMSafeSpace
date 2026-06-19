# Worklog 0430 — Router Upstream Auth-Key Injection (A23 fix, decision-independent)

**Date:** 2026-06-19
**Epic:** 42 (Multi-Cloud Inference Relay)
**Status:** Complete — unblocks inference regardless of which upstream the operator chooses

---

## Objective

A23 (worklog 0420) showed the `public` anonymous key gets 401 on `/chat/completions`
from every IP and header format, so a relay forwarding the client's `Bearer public`
cannot produce inference. The operator handed the architecture decision back with
"don't make assumptions." This worklog implements the **one change both architecture
options need in common** — router-side injection of a real upstream key — without
picking Zen-vs-operator-gateway for the operator.

Whether the operator points the fleet at a real Zen key (keeps free-tier
`claude-*`, reopens the secret-on-VM conversation) or at their own gateway
(`ai.thekao.cloud` + real key, already 200), the **router must swap the client's
`public` for a real key before forwarding**. That swap is what this ships.

---

## Blockers

- **Operator still must choose the upstream + supply the key.** This worklog makes
  the mechanism work for either choice; it does not pick. The `upstreamAuth.keySecret`
  is empty by default — inference stays broken (401 on Zen) until the operator creates
  the Secret and sets `upstreamURL`. That is intentional: the chart must not require a
  Secret that doesn't exist, and the choice of upstream is the operator's.
- **OCI A22** still pending (no OCI creds).

---

## Assumptions (Rule 7) and validation

- **A-ROUTER-INJECTS: injecting at the router (not the relay VM) keeps the VM
  secret-free at rest.** Validated by reading `cmd/relay-proxy/proxy.go:134`
  (`copyNonHopByHopHeaders` — forwards whatever Authorization the router sent).
  So: router sets `Authorization: Bearer <realkey>` → encrypted WG tunnel → relay
  VM forwards it in memory → upstream. The key is never written to the VM's disk;
  on destroy/recreate (Epic 42's rotation model) the key is not recovered from the
  VM. This preserves Epic 42 Layer 2 §3 ("WG keypairs per-VM... the router's private
  key is in a K8s Secret") posture for the upstream key too.
- **A-NOBREAK: empty key = current behavior.** Validated by
  `TestApplyUpstreamAuth_NoOpWhenKeyEmpty` + `TestRelayRouter_UpstreamAuth_OmittedWhenSecretEmpty`.
  A default install (keySecret empty) forwards the client header unchanged — no
  regression for clusters that don't enable injection.
- **A-HEADER: both `Authorization: Bearer` and a custom header (e.g. `x-api-key`
  for Anthropic-native upstreams) are supported.** Validated by
  `TestApplyUpstreamAuth_CustomHeader`.

---

## Work Completed

### Router (`cmd/relay-router/proxy.go`)
- New `upstreamAuth` struct + pure `applyUpstreamAuth(dst http.Header, auth)`:
  when `key` is empty → no-op; else sets the header (default `Authorization` →
  `Bearer <key>`; custom header → raw key + drops the original `Authorization`).
- `auth upstreamAuth` field on both `routerProxy` and `fallbackProxy`, wired via
  `withUpstreamAuth` builders (existing 13 test call-sites + `main.go` unchanged
  when injection is unconfigured).
- `applyUpstreamAuth` called in `forwardToRelay` (relay path) and
  `fallbackProxy.forward` (direct-fallback path) after `copyRouterHeaders`.

### Router main (`cmd/relay-router/main.go`)
- `routerConfig.upstreamAuth` from env `UPSTREAM_AUTH_KEY` + `UPSTREAM_AUTH_HEADER`
  (default `""` = Authorization).
- `main` calls `.withUpstreamAuth(cfg.upstreamAuth)` on both proxies.

### Chart (`charts/llmsafespaces/`)
- `values.yaml`: `controller.inferenceRelay.upstreamURL` (default
  `https://opencode.ai/zen/v1`) + `upstreamAuth.{keySecret.{name,key}, header}`
  block, with operator instructions.
- `relay-router-deployment.yaml`: router container gets `UPSTREAM_URL` +
  `UPSTREAM_AUTH_KEY` (from the Secret, only when `keySecret.name` set) +
  `UPSTREAM_AUTH_HEADER` env.

### Tests (TDD: RED first, then GREEN)
- `cmd/relay-router/proxy_test.go` — 6 new tests: 4 unit tests on
  `applyUpstreamAuth` (replace public, custom header, no-op empty, replace-all-values)
  + 2 integration tests (relay path + fallback path inject the real key, client's
  `Bearer public` never reaches upstream).
- `charts/llmsafespaces/chart_test.go` — 2 new tests: Secret mounts when configured;
  no `UPSTREAM_AUTH_KEY` env rendered when `keySecret.name` empty (default).

---

## Key Decisions

1. **Inject at the router, not the relay VM.** Keeps relay VMs free of the upstream
   key at rest (the key transits the encrypted WG tunnel only). This preserves Epic
   42's "no secrets on relay VMs" posture for the upstream key, sidestepping the
   secret-distribution design conversation that option (1) would otherwise force.
2. **Don't pick the upstream.** `upstreamURL` + `upstreamAuth.keySecret` default to
   empty/`public`-forwarding; the operator creates the Secret and chooses Zen-real-key
   vs. operator-gateway. This is the decision-independent core; the operator's
   upstream choice is orthogonal and remains theirs.
3. **Builder setters, not constructor-param churn.** Adding a param to
   `newRouterProxy`/`newFallbackProxy` would touch 18 call-sites; the
   `withUpstreamAuth` builder touches zero existing tests and reads clearly at the
   `main.go` call site.

---

## Adversarial Self-Review (Rule 11)

- **Does injection break the `public`-on-`/models` listing path?** No — listing
  goes through the same forward path; if the operator sets a real key, listing
  works too (Zen accepts real keys for `/models`). No regression either way.
- **Could the real key leak in router logs?** No — `applyUpstreamAuth` operates on
  headers; the router never logs header values (verified: no `log.*Header` in
  proxy.go). The startup log line prints `upstream=URL`, not the key.
- **Does the relay VM now see the real key in transit?** Yes — but only over the
  encrypted WG tunnel and only in memory (the relay-proxy forwards it, never
  persists). A compromised relay VM could observe transit keys; that's the same
  trust boundary as the per-VM WG private key (Layer 2 §3). Documented in A-ROUTER-INJECTS.
- **Is the empty-default truly safe?** Yes — `TestRelayRouter_UpstreamAuth_OmittedWhenSecretEmpty`
  pins it; a default install forwards unchanged (the documented 401-on-Zen behavior,
  not a crash).
- **Did I pick the operator's upstream?** No. This is explicitly the
  decision-independent mechanism; the README/values comments name both options
  without choosing.

Zero real findings.

---

## Tests Run

- `go test ./cmd/relay-router/` → full pkg PASS (incl. 6 new tests).
- `go test ./charts/llmsafespaces/...` → full chart suite PASS (incl. 2 new tests).
- `make helm-render` → PASS.
- `go vet ./cmd/relay-router/... ./charts/llmsafespaces/...` → clean.

---

## Files Modified / Created

- `cmd/relay-router/proxy.go` — `upstreamAuth` + `applyUpstreamAuth` + field/builder on both proxies + injection in both forward paths
- `cmd/relay-router/main.go` — env wiring + builder calls
- `cmd/relay-router/proxy_test.go` — 6 tests
- `charts/llmsafespaces/values.yaml` — `upstreamURL` + `upstreamAuth` block
- `charts/llmsafespaces/templates/relay-router-deployment.yaml` — env mount
- `charts/llmsafespaces/chart_test.go` — 2 tests
- `worklogs/0430_2026-06-19_router-upstream-auth-injection.md` — this worklog

---

## Next Steps

1. **Operator: create the upstream-key Secret + pick `upstreamURL`.** Either a real
   Zen key (keeps `claude-*`) or `ai.thekao.cloud` + its key (already 200). The
   chart then does the rest.
2. **Optional follow-up:** if the operator confirms `ai.thekao.cloud` as the
   upstream, the relay VMs' IP-diversity value diminishes (the operator's gateway
   isn't IP-blocking CF/cloud ranges like Zen was) — worth a separate conversation
   on whether Epic 42's multi-cloud machinery is still warranted for that upstream.
   Out of scope here.
