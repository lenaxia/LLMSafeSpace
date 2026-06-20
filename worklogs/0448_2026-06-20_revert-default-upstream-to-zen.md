# Worklog: Revert Default Upstream → opencode.ai/zen (A23 false, restore free-model default)

**Date:** 2026-06-20
**Session:** Revert PR #298's default-upstream flip (thekao → zen) after A23 was disproven
**Status:** Complete

---

## Objective

PR #298 flipped the relay fleet's default upstream from `https://opencode.ai/zen/v1` to `https://ai.thekao.cloud/v1` based on A23 (worklog 0420), which claimed `public` "no longer authorizes Zen inference." A23 was disproven earlier this session (worklog 0420 correction): `public` still authorizes inference for any model Zen flags `allowAnonymous` (`big-pickle` → HTTP 200 from residential IP `24.18.52.209`).

The thekao default was therefore solving a non-problem and, worse, **broke the out-of-the-box contract**: `ai.thekao.cloud` is the maintainer's personal gateway, not something any other operator will have configured. A default deploy must produce working **free-model** inference for everyone, which means Zen + `Bearer public`, no injected key, no maintainer-specific dependency.

This worklog reverts the default across all 7 sites PR #298 touched (+1 site it missed: the runtime defaulter in `defaults.go`).

---

## Work Completed

### Reverted 7 default sites (thekao → `https://opencode.ai/zen/v1`)

| # | File | Site |
|---|------|------|
| 1 | `pkg/apis/llmsafespaces/v1/inferencerelay_types.go` | kubebuilder `+kubebuilder:default` annotation + comment |
| 2 | `pkg/apis/llmsafespaces/v1/defaults.go` | runtime defaulter `SetDefaults_InferenceRelaySpec` (must match #1) |
| 3 | `charts/llmsafespaces/crds/inferencerelay.yaml` | CRD schema `default:` + `description:` |
| 4 | `charts/llmsafespaces/values.yaml` | `controller.inferenceRelay.upstreamURL` value + surrounding comment |
| 5 | `cmd/relay-router/main.go` | `defaultRouterUpstream` const (fallback path) |
| 6 | `cmd/relay-proxy/main.go` | `defaultUpstreamURL` const (VM cloud-init inherits from CR spec, but this is the binary's own fallback) |
| 7 | `api/internal/handlers/relay_admin.go` | admin Deploy handler default when `req.UpstreamURL == ""` |

### Updated 2 test assertions (thekao → zen)

- `pkg/apis/llmsafespaces/v1/defaults_test.go:159` — `SetDefaults_InferenceRelaySpec` default assertion
- `api/internal/handlers/relay_admin_test.go:614` — `TestRelayDeploy_Defaults_UpstreamURL` matched-value

### Verified default-deploy posture is correct

- `upstreamAuth.keySecret.name` defaults to `""` (unchanged from before) → router forwards client's `Authorization: Bearer public` unchanged → no key injection by default. Correct for a Zen free-model fleet.
- The key-injection mechanism from PR #297 (`applyUpstreamAuth`) remains in place as an **optional** capability — operators pointing at a paid gateway set `keySecret.name` to enable it. Empty = no-op, preserved.

---

## Key Decisions

1. **Default = Zen + `public`, not thekao.** Rationale: a default deploy must work for any operator, not just the maintainer. Zen + `public` produces working free-model inference for any model flagged `allowAnonymous` (verified 2026-06-20). thekao is a maintainer-specific gateway that no one else will have.

2. **Did NOT revert PR #297's mechanism.** The `upstreamAuth` / `applyUpstreamAuth` code stays — it's a sound optional capability for operators who point at a real-key-required upstream. Only the *default posture* (key injection off, `upstreamURL=zen`) is restored to the pre-A23 state.

3. **Did NOT remove thekao references entirely.** Operators can still set `upstreamURL: "https://ai.thekao.cloud/v1"` (+ `keySecret`) to use the maintainer's gateway. thekao is now an opt-in choice, not the default.

---

## Assumptions (Rule 7) and validation

- **A-PUBLIC-WORKS: `Bearer public` authorizes Zen free-model inference.** Validated 2026-06-20 (worklog 0420 correction): `POST /v1/chat/completions` model=`big-pickle` → HTTP 200, real completion, from residential IP `24.18.52.209`. Mechanism confirmed from opencode source: `packages/console/app/src/routes/zen/util/handler.ts:599-603` + `model.ts:26` (`allowAnonymous: z.boolean().optional()`).
- **A-ZEN-REACHABLE: `opencode.ai/zen/v1` is reachable from a default cluster's egress.** Validated: `GET /v1/models` → HTTP 200 from the same residential IP. Relay VMs in AWS/OCI/GCP also reach it (worklog 0410, A22).
- **A-DEFAULter-AGREES: the Go kubebuilder annotation, the runtime defaulter, and the chart CRD schema must all agree on the default.** Validated: repolint CRD-drift check passes after all three are set to `https://opencode.ai/zen/v1`.
- **A-NO-KEY-INJECTION-BY-DEFAULT: a default deploy does not inject a key.** Validated: `upstreamAuth.keySecret.name: ""` in values.yaml; `TestRelayRouter_UpstreamAuth_OmittedWhenSecretEmpty` confirms the env var is not rendered; `applyUpstreamAuth` is a no-op when `auth.key == ""`.

---

## Tests Run

- `go build ./...` — clean.
- `go test ./pkg/repolint/` — passes (CRD-drift: Go struct ↔ chart CRD schema agreement).
- `go test ./pkg/apis/llmsafespaces/v1/ ./api/internal/handlers/ ./cmd/relay-router/ ./cmd/relay-proxy/` — all pass.
- `go test ./charts/...` — passes (chart render tests).

---

## Next Steps

1. **The two pre-existing wiring gaps remain** (separate from this revert):
   - Workspace→router: `INFERENCE_RELAY_BASEURL` still points at the CF Worker (`relay.safespaces.dev`), not the in-cluster `relay-router:8080` when `controller.inferenceRelay.enabled`. Free-model traffic doesn't use the fleet yet.
   - `relay-proxy` ignores its `--upstream` flag (cloud-init renders it; binary reads only `UPSTREAM_URL` env). Currently masked because both default to zen; breaks on a per-CR `spec.upstreamURL` override.
2. **Operator decision deferred:** whether to keep `controller.inferenceRelay.enabled: false` as the default (current) or wire workspaces through the router. The revert here makes either choice produce working free-model inference.

---

## Files Modified

- `pkg/apis/llmsafespaces/v1/inferencerelay_types.go` — kubebuilder default + comment
- `pkg/apis/llmsafespaces/v1/defaults.go` — runtime defaulter
- `pkg/apis/llmsafespaces/v1/defaults_test.go` — default assertion
- `charts/llmsafespaces/crds/inferencerelay.yaml` — CRD schema default + description
- `charts/llmsafespaces/values.yaml` — chart value + comment
- `cmd/relay-router/main.go` — const
- `cmd/relay-proxy/main.go` — const
- `api/internal/handlers/relay_admin.go` — Deploy handler default
- `api/internal/handlers/relay_admin_test.go` — Deploy default test assertion
