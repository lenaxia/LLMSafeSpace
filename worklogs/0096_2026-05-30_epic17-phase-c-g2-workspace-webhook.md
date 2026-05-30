# 0096 — Epic 17 Phase C/G2: workspace admission webhook (5 findings)

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, finding cluster G2
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes five pentest findings in a single webhook PR:

- **F1.2.1 / RT-2.18 / RT-6.10 (Critical)** — `Spec.Runtime` accepted
  arbitrary registry refs. Pre-fix, `runtime: "evil.example.com/malicious:latest"`
  caused the controller to pull and run that image without any
  validation.
- **F1.2.2 (Critical)** — On CREATE, a user could populate
  `Status.PodIP` / `Status.PodName` / `Status.Endpoint`; the API proxy
  later routed user requests to the attacker-supplied pod IP.
- **F1.2.9 (Medium)** — `Spec.Storage.StorageClassName` had no
  allow-list; a user could request hostPath / NFS / arbitrary CSIs.
- **RT-6.1 (High)** — Webhook accepted `runtime: "../../etc/passwd"`
  and `storage.size: "999999Gi"`. The CRD pattern allowed any digit
  count.

The pre-fix system shipped a `ValidatingWebhookConfiguration` that ONLY
validated `runtimeenvironments` resources (not `workspaces`). This PR
adds a Workspace-level admission webhook with five distinct contracts
and registers it in the chart and controller.

---

## Stated assumptions (validated up-front)

- **A1** — A `ValidatingWebhookConfiguration` exists and is wired to
  the controller webhook server. (Validated: read
  `charts/llmsafespace/templates/validating-webhook.yaml`; webhook server
  starts at port 9443 via `controller/main.go:59`.)
- **A2** — The current webhook does NOT validate Workspace resources;
  only RuntimeEnvironment. (Validated: read
  `controller/internal/webhooks/runtimeenvironment_webhook.go` —
  there is no workspace_webhook.go in the repo pre-fix.)
- **A3** — `Status` subresource is enabled on the Workspace CRD, so
  the kube-apiserver normally enforces the spec/status split on
  UPDATE — but NOT on CREATE. (Validated: read
  `charts/llmsafespace/crds/workspace.yaml:242` `subresources: { status: {} }`.)
- **A4** — A registry-prefix string allow-list is sufficient for
  fine-grained image-pull control. (Validated: walked through the
  full list of legitimate runtime values the live cluster uses;
  `kubectl get workspaces -o jsonpath='{.items[*].spec.runtime}'`
  returned only `base` (RuntimeEnvironment name) and
  `ghcr.io/lenaxia/llmsafespace/base:latest` — the default chart
  prefix `ghcr.io/lenaxia/` covers them.)
- **A5** — RE2 (Go's regexp) is linear, so the `runtimeRunSafePattern`
  cannot ReDoS even on adversarial input. (Validated: known RE2
  property; package documentation.)

---

## Changes

### Controller code

1. `controller/internal/webhooks/workspace_webhook.go` — **NEW**.
   `WorkspaceValidator` admission webhook with seven validation
   contracts:
   1. `Spec.Runtime` is required.
   2. Length cap (512 chars for runtime; 253 for storageClassName).
   3. Traversal / NUL / whitespace / backslash rejection on runtime.
   4. ASCII-only runtime characters (`[a-zA-Z0-9._/:@-]`).
   5. Registry allow-list match for explicit image references; allow
      RuntimeEnvironment-name references unconditionally.
   6. Storage size shape (CRD pattern) AND magnitude (operator-supplied
      max).
   7. StorageClassName allow-list (optional; empty list = any allowed).
   8. Status forge rejection on CREATE (any non-zero status field).
   9. Status mutation rejection on UPDATE through the spec endpoint
      (defence-in-depth; kube-apiserver subresource split is the
      primary control).

2. `controller/internal/webhooks/workspace_webhook_test.go` — **NEW**.
   22 G2 tests covering all of the above, plus mutation-validation
   touchpoints for: registry allow-list, status forge, storage
   upper bound, traversal payloads, prefix-suffix homograph attack,
   length caps, empty-OldObject UPDATE bypass, nil-decoder safety.

3. `controller/main.go` — adds three CLI flags
   (`--allowed-image-registries`, `--allowed-storage-class-names`,
   `--max-workspace-storage-gi`) and registers the new validator at
   path `/validate-llmsafespace-dev-v1-workspace`.

4. `controller/watch_namespaces.go` — adds `splitNonEmpty` helper for
   parsing comma-separated flag values into `[]string`.

### Chart

5. `charts/llmsafespace/templates/validating-webhook.yaml` — adds a
   second webhook entry (`vworkspace.llmsafespace.dev`) routing
   `workspaces` CREATE/UPDATE to the new path.

6. `charts/llmsafespace/templates/controller-deployment.yaml` — passes
   the three new flags from `values.yaml` to the controller binary.

7. `charts/llmsafespace/values.yaml` — adds
   `webhooks.allowedImageRegistries: ["ghcr.io/lenaxia/"]`,
   `webhooks.allowedStorageClassNames: []`,
   `webhooks.maxWorkspaceStorageGi: 1024`.

### Chart tests

8. `charts/llmsafespace/chart_test.go` — adds 5 TestG2_* cases:
   - `TestG2_WebhookConfig_IncludesWorkspace` — webhook entry exists
   - `TestG2_ControllerArgs_PassesAllowedImageRegistries`
   - `TestG2_ControllerArgs_OmitsAllowedRegistriesWhenEmpty`
   - `TestG2_ControllerArgs_PassesMaxStorageGi`
   - `TestG2_ControllerArgs_HonorsOperatorOverride`

---

## Skeptical-validator pass

A separate validator agent attempted bypasses across runtime payloads,
status forge, storage size, storageClassName, and mutation-tested all
three checks. Results:

- **All listed runtime payloads correctly rejected** — including
  homograph attacks (Unicode middle-dot), prefix-suffix attacks
  (`ghcr.io.attacker.com/...`), case manipulation, traversal in path
  components, and 4096-char strings.
- **All status forge vectors correctly rejected** — all `Status` fields
  caught by `reflect.DeepEqual` against zero `WorkspaceStatus{}`.
- **One real bypass found**: empty `req.OldObject.Raw` on UPDATE
  silently skipped the status-mutation check. Fixed by treating
  missing OldObject as "old status was zero" — fail-closed.
- **Three minor follow-ups identified and fixed in the same PR**:
  (a) length caps on Runtime (512) and StorageClassName (253);
  (b) allow-list prefix slash normalisation (operator writing
  `ghcr.io/lenaxia` without trailing slash is automatically treated
  as `ghcr.io/lenaxia/`); (c) AdmissionReview decode failure on
  OldObject is now an admission error, not silent skip.
- **All three mutation tests confirmed teeth** — reverting the
  registry / status / storage checks each made the relevant test fail.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 30s ./controller/internal/webhooks/...` | PASS (22 G2 + 4 RuntimeEnvironment tests) |
| `go test -count=1 -timeout 60s ./charts/llmsafespace/...` | PASS (16 chart tests including 5 G2 + 6 G26 + 5 G16) |
| `go test -count=1 -timeout 60s ./controller/...` | PASS |
| `go build ./controller/...` | clean |
| `helm template --namespace test-ns --release-name test charts/llmsafespace/` | renders cleanly |
| Mutation: revert registry check → `TestG2Workspace_DeniesArbitraryRegistry` | FAIL as expected |
| Mutation: revert status-on-CREATE check → `TestG2Workspace_DeniesNonEmptyStatusOnCreate` | FAIL as expected |
| Mutation: revert storage upper bound → `TestG2Workspace_DeniesAbsurdStorageSize` | FAIL as expected |

---

## Live re-pentest plan (after CI builds the controller image)

1. CI builds and ships controller image `ghcr.io/lenaxia/llmsafespace/controller:sha-...`.
2. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values` — should roll out new controller and ValidatingWebhookConfiguration.
3. Wait for cert-manager Certificate `llmsafespace-webhook` to be Ready.
4. **RT-2.18 reproduction (must FAIL post-fix):**
   ```
   kubectl apply -f - <<EOF
   apiVersion: llmsafespace.dev/v1
   kind: Workspace
   metadata: { name: test-evil, namespace: default }
   spec:
     owner: { userID: u1 }
     runtime: "evil.example.com/malicious:latest"
     storage: { size: "5Gi" }
   EOF
   ```
   Must respond with: `admission webhook "vworkspace.llmsafespace.dev" denied the request: spec.runtime "evil.example.com/malicious:latest" is an explicit image reference but its registry is not in the allow-list.`
5. **F1.2.2 reproduction:** create with `status.podIP: "1.2.3.4"` — must be rejected with `spec.status fields must not be set on CREATE`.
6. **RT-6.1 reproduction:** create with `storage.size: "999999Gi"` — must be rejected with `exceeds the maximum 1024 Gi`.
7. **F1.2.9 (when AllowedStorageClassNames is non-empty):** create with `storage.storageClassName: "evil"` — must be rejected.
8. Re-run phase-2/run-phase2.py RT-2.18 and phase-6/run-phase6.py RT-6.10/RT-6.1 from the harness; all three must move from FAIL to PASS.
9. Confirm legitimate workspace creation with `runtime: "ghcr.io/lenaxia/llmsafespace/base:latest"` still works.

---

## Files changed

- `controller/internal/webhooks/workspace_webhook.go` (NEW; 230 LoC)
- `controller/internal/webhooks/workspace_webhook_test.go` (NEW; 380 LoC)
- `controller/main.go` (added 3 flags + new webhook registration)
- `controller/watch_namespaces.go` (added splitNonEmpty)
- `charts/llmsafespace/templates/validating-webhook.yaml` (added vworkspace entry)
- `charts/llmsafespace/templates/controller-deployment.yaml` (added 3 flags)
- `charts/llmsafespace/values.yaml` (added webhook allow-list values)
- `charts/llmsafespace/chart_test.go` (added 5 TestG2_* tests)

---

## Tracker update

`MASTER-TRACKER.md`:
- F1.2.1, F1.2.2, F1.2.9 → MINE / live-pending
- RT-2.18, RT-6.10, RT-6.1 → resolved by the same webhook PR

---

## Next finding

Phase C/G3 — G18 (`/auth/logout` doesn't call `RevokeToken`). 5-line fix
in `api/internal/api/router.go` to wire the existing `RevokeToken` into
the logout handler.
