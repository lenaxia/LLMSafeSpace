# Worklog: Workspace→Relay-Router Wiring (Design Principle 6, Epic 42)

**Date:** 2026-06-20
**Session:** Wire workspace pods to route free-model traffic through the in-cluster relay-router when the fleet is enabled
**Status:** Complete (local validation limited — see Tests Run)

---

## Objective

Design Principle 6 (Epic 42 README): "The workspace controller still injects a single `INFERENCE_RELAY_BASEURL` — it now points at the in-cluster router Service instead of an external hostname." Before this change, enabling the fleet (`controller.inferenceRelay.enabled=true`) only affected controller-side `/metrics` scraping; workspace pods kept routing free-model traffic to the external CF Worker (`relay.safespaces.dev`) regardless, because the chart set `--inference-relay-url` independently from `.Values.inferenceRelayURL`. The fleet was built but never on the live workspace request path (the exact "incomplete work" state Rule 0's DoD forbids).

This wires the switch: when the fleet is enabled, workspace pods point at the router.

---

## Work Completed

### Chart: route workspace traffic through the router when the fleet is enabled

`charts/llmsafespaces/templates/controller-deployment.yaml`:
- When `controller.inferenceRelay.enabled=true`: `--inference-relay-url` = the relay-router (cross-namespace FQDN), and `--inference-relay-secret` is **omitted** (the router authenticates via WireGuard, not the CF Worker's path-secret).
- When disabled (default): unchanged — `--inference-relay-url` = `.Values.inferenceRelayURL` (CF Worker) + secret wiring preserved (Epic 26 path intact).

### New value `controller.inferenceRelay.workspaceRouterURL`

`charts/llmsafespaces/values.yaml`: separate value for the workspace-facing router URL. Defaults empty → chart derives the cross-namespace FQDN `http://relay-router.<release-ns>.svc.cluster.local:8080`. Operators who deploy the router in a separate privileged namespace (documented advanced case) set this explicitly. This is a **different consumer** from `routerURL` (which the controller uses for `/metrics` scraping — the controller is always in the release namespace, so its short name is correct and unchanged).

### Why FQDN, not short name

Workspace pods may run in any namespace — the controller watches cluster-wide by default (`controller.watchNamespaces: ""`), and the API's `config.Namespace` is the release namespace but operators can create Workspace CRs elsewhere. The relay-router Service lives in `.Release.Namespace`. A short `http://relay-router:8080` resolves only from the release namespace; from a workspace pod in another namespace it fails DNS. The FQDN resolves from anywhere in the cluster. Zero-cost robustness.

### No controller-side change needed

`controller/internal/workspace/pod_builder.go:170-178` already embeds the secret as a path segment only when `r.InferenceRelaySecret != ""`. The controller's `--inference-relay-secret` flag defaults `""` (`controller/main.go:82`); the chart omits it in fleet mode → the field stays empty → `pod_builder` injects `INFERENCE_RELAY_BASEURL = <router URL>` with no path segment. Correct as-is.

---

## Key Decisions

1. **Fleet flag is the switch; operator doesn't coordinate two URLs.** Enabling `controller.inferenceRelay.enabled` automatically re-routes workspace traffic through the router. The alternative (operator must also flip `inferenceRelayURL`) is error-prone and defeats the "pods don't know about the fleet" design goal.

2. **Separate `workspaceRouterURL` from `routerURL`.** They are different consumers with different namespace contexts: the controller (always release-ns) vs workspace pods (possibly cross-ns). One value with a short-name default would silently break cross-ns workspaces; one value with an FQDN default would needlessly change the controller's metrics-scrape URL (existing behaviour). Two values is the correct, minimal design.

3. **Omit the path-secret in fleet mode.** The router's auth boundary is WireGuard public-key pinning (Epic 42 Layer 1). Embedding a CF-Worker-style path-secret against the router would be inert (the router ignores path) and misleading. `pod_builder`'s existing `InferenceRelaySecret != ""` guard handles this cleanly.

---

## Assumptions (Rule 7) and validation

- **A-CONTROLLER-SECRET-DEFAULT: omitting `--inference-relay-secret` leaves the field empty.** Validated: `controller/main.go:82` `flag.StringVar(&inferenceRelaySecret, "inference-relay-secret", "", ...)`; `controller/internal/controller/controller.go:47` passes it through; `pod_builder.go:172` gates embedding on `!= ""`.
- **A-WORKSPACE-NS-MAY-DIFFER: workspace pods can run outside the release namespace.** Validated: `controller.watchNamespaces` defaults `""` = cluster-wide (`controller/main.go:130`); API creates Workspace CRs in `config.Namespace` (= release ns by default via the downward API at `api-deployment.yaml:81`), but operators can create Workspace CRs in other namespaces directly. FQDN covers both.
- **A-ROUTER-IN-RELEASE-NS-BY-DEFAULT: the relay-router Service is in `.Release.Namespace`.** Validated: `relay-router-service.yaml:6`.
- **A-CHART-RENDER-CORRECT: the new template branch produces valid YAML.** NOT validated locally — see Tests Run.

---

## Adversarial Self-Review (Rule 11)

### F1 (real, mitigated) — both `inferenceRelayURL` and fleet enabled simultaneously
If an operator sets both `.Values.inferenceRelayURL` (CF Worker) AND `controller.inferenceRelay.enabled=true`, the fleet branch wins and `inferenceRelayURL` is silently ignored. This is correct (the fleet flag is the explicit "use the router" switch) but the silence could confuse. **Mitigation:** could add a NOTES.txt warning when both are set. Deferred — not load-bearing; the chart comment documents the precedence. Flagged as a minor follow-up.

### F2 (false alarm) — "router may not exist when first workspace starts"
The relay-router Deployment is created at install; workspaces are user-created later. By the time any workspace pod reads `INFERENCE_RELAY_BASEURL` and the relay injector runs (~T+7s of pod boot), the router is up. The agentd relay injector is one-shot and tolerates the router being briefly unreachable (opencode retries). Not a real race.

### F3 (real, documented limitation) — router deployed in a separate namespace
The derived FQDN assumes the router is in `.Release.Namespace`. Operators who deploy relay-router into a separate privileged namespace (the documented out-of-band case) must set `workspaceRouterURL` explicitly. This is the same posture as any other cross-namespace service reference and is documented in the value's comment. Not a bug; an explicit advanced-config knob.

---

## Tests Run

- `go build ./...` — clean.
- `go vet ./charts/llmsafespaces/ ./controller/...` — clean (chart tests compile).
- `go test ./controller/... ./cmd/relay-proxy/... ./cmd/relay-router/... ./pkg/apis/...` — all pass.
- **Chart render tests: NOT RUN locally** — `helm` binary is unavailable in this sandbox and there is no network to install it. The 3 new chart tests (`TestControllerArgs_RoutesWorkspacesThroughRouterWhenFleetEnabled`, `TestControllerArgs_WorkspaceRouterURLOverride`, `TestControllerArgs_PreservesCFWorkerURLWhenFleetDisabled`) skip locally with `helm not on PATH; skipping chart render test` and will execute in CI, where helm is available. This is the validation gate for the template change. The template logic was verified by careful reading against Helm's conditional/`printf`/`default` semantics.

---

## Next Steps

1. **CI validation:** the 3 chart render tests are the gate for this change. If CI flags a template syntax or logic error, fix and re-push.
2. **Optional follow-up (F1):** NOTES.txt warning when both `inferenceRelayURL` and `controller.inferenceRelay.enabled` are set.
3. **End-to-end:** with this wiring + worklogs 0437 (`--upstream` flag) + 0438 (`--listen` per VM), the full free-model path is now structurally wired: pod → relay-router → relay VM (correct WG bind, correct per-CR upstream) → zen. A live deploy test against a real cluster is the final DoD confirmation.

---

## Files Modified

- `charts/llmsafespaces/templates/controller-deployment.yaml` — fleet-enabled branch routes workspace traffic through the router (FQDN); secret omitted in this mode
- `charts/llmsafespaces/values.yaml` — new `controller.inferenceRelay.workspaceRouterURL` value + comment
- `charts/llmsafespaces/chart_test.go` — 3 new tests (fleet-on FQDN, override, fleet-off CF Worker preserved)
