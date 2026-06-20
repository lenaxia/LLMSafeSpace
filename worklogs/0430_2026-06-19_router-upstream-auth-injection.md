# Worklog 0430 — Router Upstream Auth-Key Injection (A23 fix, decision-independent)

> **⚠️ PREMISE SUPERSEDED 2026-06-20.** This PR's *mechanism* (router-side
> upstream-key injection via `applyUpstreamAuth`) is sound and unchanged. Its
> *rationale* — "A23 showed `public` gets 401 on `/chat/completions` from every
> IP, so a relay forwarding `Bearer public` cannot produce inference" — is
> **unfounded**: A23 was disproven (worklog 0420 correction). `public` still
> authorizes inference for any model Zen flags `allowAnonymous` (`big-pickle`
> → HTTP 200 from residential IP `24.18.52.209`). Whether router-side key
> injection should be the *default* posture is now an open operator decision,
> not the necessity this worklog framed it as. The code shipped here remains
> valuable as an *optional* capability (e.g. operators pointing at a non-Zen
> upstream that requires a real key). Read the Objective and Key Decisions
> below with that correction in mind.

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
  VM.
- **A-BLAST-RADIUS: a compromised relay VM can observe the upstream key in transit.**
  This is **true and fleet-wide**, not per-VM like the WG private key (Layer 2 §3).
  A compromised VM that logs/exports transit headers could steal the upstream key
  and impersonate the whole fleet's inference credential. This is a real increase
  in blast radius vs the pre-A23 `public` design (where the VM carried nothing
  worth stealing). Mitigations: destroy-and-recreate limits the exposure window;
  WG-only listener means only authenticated peers reach the relay; operators who
  can't accept fleet-wide exposure should put a rate-limited/quota-capped gateway
  key upstream, not their primary key. Documented in Epic 42 Security §2 (updated).
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

1. **Inject at the router, not the relay VM.** Keeps the upstream key off VM
   disks (it transits the encrypted WG tunnel only, in memory). This avoids the
   explicit secret-in-cloud-init distribution that injecting at the VM would
   require. **Caveat (PR review, Rule 11):** this does NOT fully preserve the
   pre-A23 "nothing worth stealing on a relay VM" posture — a compromised VM can
   still observe the fleet-wide upstream key in transit (see A-BLAST-RADIUS).
   The blast radius is now fleet-wide, not per-VM; Epic 42 Security §2 updated
   to state this honestly. Operators who can't accept that should use a
   rate-limited/quota-capped gateway key rather than their primary credential.
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
