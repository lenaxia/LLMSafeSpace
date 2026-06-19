# Worklog 0435 — Default Upstream → ai.thekao.cloud (A23 resolution, option 2)

**Date:** 2026-06-19
**Epic:** 42 (Multi-Cloud Inference Relay)
**Status:** Complete — resolves A23 (operator chose option 2)

---

## Objective

A23 (worklog 0420) disproved the premise that `public` authorizes Zen inference.
#297 (worklog 0430) shipped the decision-independent fix (router injects a real
upstream key). The operator then chose **option (2)**: re-point the relay at the
operator-owned gateway (`ai.thekao.cloud`), which already returns 200 and is the
live inference path (the validating agent runs as `thekao cloud/glm-5.2`). This
worklog flips the project defaults from the now-dead `opencode.ai/zen/v1` to
`ai.thekao.cloud/v1` so a default deploy produces working inference.

---

## Blockers

- **OCI A22** still pending (no OCI creds in any session this far).
- **Operator must still create the `relay-upstream-key` Secret** with a real
  `ai.thekao.cloud` key before flipping `inferenceRelay.enabled=true` — the chart
  never embeds the key value (it lives only in the operator's K8s Secret).
- Free-tier `claude-opus/fable/etc.` from Zen is gone (that access was dead
  regardless). `ai.thekao.cloud` offers `bedrock-claude-sonnet-4.6`, so a Claude
  path survives via Bedrock.

---

## Assumptions (Rule 7) and validation

- **A-THEKAO-WORKS: `ai.thekao.cloud` returns 200 for inference.** Validated
  (worklog 0420): `POST ai.thekao.cloud/v1/chat/completions` + the real key → 200,
  real `glm-5.2` completion. And the validating agent itself runs on this path.
- **A-THEKAO-CLAUDE: thekao offers a Claude model, so flipping does not lose
  Claude entirely.** Validated: thekao's `/v1/models` list (worklog 0184) includes
  `bedrock-claude-sonnet-4.6`. Only Zen's free-tier `claude-opus/fable/etc.` is
  lost — and that was dead via `public` regardless of this choice.
- **A-DRIFT: CRD Go struct default must match the chart CRD schema default.**
  Validated by `pkg/repolint` CRD-drift check (passes after the edit).
- **A-NO-REAL-ZEN-KEY: no real Zen key exists in the env, so option (1) was not
  actionable anyway.** Validated: env had no `ZEN_API_KEY`; only `ai.thekao.cloud`'s
  key. Option (1) would have required a key the operator does not have on hand.

---

## Work Completed

Flipped the default upstream from `opencode.ai/zen/v1` → `ai.thekao.cloud/v1` in
every place that defaults it, so a default deploy is consistent end-to-end:

- `cmd/relay-router/main.go` — `defaultRouterUpstream` const (router fallback path).
- `api/internal/handlers/relay_admin.go:455` — admin Deploy handler default (what
  populates `spec.upstreamURL` on the CR when the operator doesn't supply one).
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go:145` — CRD type
  `+kubebuilder:default` (+ comment explaining the change + A23).
- `charts/llmsafespaces/crds/inferencerelay.yaml:43` — chart CRD schema default
  (manually edited to match the Go struct; repolint CRD-drift check validates
  agreement).
- `charts/llmsafespaces/values.yaml` — chart `upstreamURL` value.

Test + doc:
- `api/internal/handlers/relay_admin_test.go` — `TestRelayDeploy_Defaults_UpstreamURL`
  updated to expect the new default.
- Epic 42 README A23 row — marked **RESOLVED (option 2)** with the rationale and
  the `bedrock-claude-sonnet-4.6` note.

---

## Key Decisions

1. **Option (2), not (1) or (3).** (1) needed a real Zen key the operator doesn't
   have; (3) the operator can't pursue from code. (2) is the evidenced working
   path (200, live agent runs on it) and keeps a Claude model via Bedrock. The
   operator explicitly chose this.
2. **Flip all five default sites, not just the chart.** Leaving the handler/CRD
   defaults on Zen would mean the relay VMs still provision against Zen (401)
   even with the chart changed — a partial flip produces a broken default deploy.
   The repolint CRD-drift check enforces Go-struct ↔ chart-schema agreement, so
   the CRD YAML default had to move too.
3. **`ai.thekao.cloud` is the maintainer's gateway; the doc notes operators should
   override.** This is a single-maintainer repo; baking the maintainer's gateway as
   the default is the operator's call. Non-maintainer operators override
   `upstreamURL` + supply their own key Secret.

---

## Adversarial Self-Review (Rule 11)

- **Did flipping the default break any test that assumed Zen?** Only
  `TestRelayDeploy_Defaults_UpstreamURL` asserted the default; updated. Other tests
  send `upstreamURL` explicitly in the request body (Zen) — those still pass
  (explicit values are accepted unchanged; only the *default* moved). Verified:
  full handler + router + chart + repolint suites green.
- **Does the relay VM still forward to Zen via cloud-init?** No — cloud-init renders
  `--upstream={{ .UpstreamURL }}` from the CR's `spec.upstreamURL`, which now
  defaults to thekao (via the handler). The design-doc's hardcoded
  `Environment=UPSTREAM_URL=https://opencode.ai/zen/v1` (line ~686) is the original
  cloud-init *example* in the narrative, not the rendered value — left as historical
  record (append-only convention); the actual rendered value flows from the CR.
- **Is the CRD-drift check actually satisfied?** Yes — `go test -run CRD ./pkg/repolint`
  passes after the manual YAML edit.
- **Did I bake a secret into a default?** No — only the *URL* (public) is defaulted.
  The key still comes from the operator's K8s Secret (#297); no key value is in any
  committed file.

Zero real findings.

---

## Tests Run

- `go test -run CRD ./pkg/repolint/...` → PASS (CRD drift check).
- `go test -run TestRelayDeploy_Defaults_UpstreamURL ./api/internal/handlers/` → PASS.
- `go test ./api/internal/handlers/` → PASS (no regressions).
- `go test ./cmd/relay-router/` → PASS.
- `go test ./charts/llmsafespaces/...` → PASS.
- `make helm-render` → PASS.

---

## Files Modified

- `cmd/relay-router/main.go` — `defaultRouterUpstream`
- `api/internal/handlers/relay_admin.go` — handler default
- `api/internal/handlers/relay_admin_test.go` — default assertion
- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go` — CRD type default + comment
- `charts/llmsafespaces/crds/inferencerelay.yaml` — chart CRD schema default
- `charts/llmsafespaces/values.yaml` — chart `upstreamURL`
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` — A23 marked RESOLVED
- `worklogs/0435_2026-06-19_default-upstream-thekao.md` — this worklog

---

## Next Steps

1. **Operator: create the `relay-upstream-key` Secret** with a real
   `ai.thekao.cloud` key, set `controller.inferenceRelay.enabled=true`, deploy.
   No more code changes required for a working default fleet.
2. **OCI A22** remains an operator manual step if OCI is wanted in the fleet.
3. **Optional:** once live, evaluate whether Epic 42's multi-cloud WG machinery
   still earns its complexity for an operator-owned gateway upstream (thekao isn't
   IP-blocking CF/cloud ranges like Zen was). Out of scope here.
