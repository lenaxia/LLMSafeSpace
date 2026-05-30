# 0095 — Epic 17 Phase C/G1: G26 datastore credentials remediation

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, finding G26 (Critical)
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes the **Critical** finding G26 (THREAT-MODEL.md §5; Phase 4 RT-4.5):
the live cluster had `POSTGRES_PASSWORD=changeme` and Valkey running
with `requirepass=""`, plus zero NetworkPolicy gating ingress to either
datastore. After this commit, fresh chart installs and `helm upgrade`
against the documented vulnerable state both produce strong random
credentials, and a NetworkPolicy denies all in-namespace ingress to
postgres/valkey except from the API deployment and migration Job.

---

## Stated assumptions (validated up-front)

- **A1** — `helm install/upgrade` has cluster connectivity, so `lookup`
  inside the secret.yaml template can read the existing Secret and
  preserve random values across upgrades. (Validated: helm docs §template
  functions, "lookup".)
- **A2** — `helm template` (test driver) does NOT have cluster
  connectivity; `lookup` returns nil. The test harness exercises only
  the no-existing-Secret branch, which is the install-time path.
  (Validated: ran `helm template` and confirmed `randAlphaNum` output.)
- **A3** — Postgres + Valkey are deployed via `local/postgres-redis.yaml`
  (kind dev) or `local/deps-{postgres,valkey}.yaml` (live home cluster);
  both must be updated to consume the chart-managed Secret. (Validated:
  `kubectl get deploy -n default` against home cluster shows `postgres`
  and `valkey` Deployments matching `local/deps-*.yaml`. Bootstrap
  scripts apply `local/postgres-redis.yaml` to kind clusters.)
- **A4** — Pod labels for postgres/valkey are `app: postgres` and
  `app: valkey` in the live deps manifests. NetworkPolicy selectors
  must match these. (Validated: read `local/deps-*.yaml` pod template
  metadata.labels.)
- **A5** — Workspace pods carry `component=workspace` and would be
  blocked by a NetPol that allows only `component=api` and
  `component=migrate`. (Validated: read `controller/internal/workspace/
  controller.go` and `charts/llmsafespace/templates/workspace-network-
  policy.yaml` selectors.)

---

## Changes

### Chart-level

1. `charts/llmsafespace/templates/secret.yaml` — rewrote to use
   `lookup` for rotation safety, with explicit re-randomisation when
   the live Secret contains the historical insecure defaults
   (`postgres-password=changeme`, `redis-password=""`). The `lookup`
   pattern preserves operator-pinned values verbatim across upgrades.

2. `charts/llmsafespace/values.yaml` — flipped `postgresPassword`
   default from `"changeme"` to `""` (empty triggers auto-generate).
   Added new `datastore.networkPolicy` section with sub-toggle and
   pod-selector overrides.

3. `charts/llmsafespace/templates/datastore-network-policy.yaml` —
   NEW. Renders two NetworkPolicy objects: `app=postgres` ingress
   allow-list (5432 from API + migration job) and `app=valkey`
   ingress allow-list (6379 from API only). Gated by master toggle
   `networkPolicy.enabled` AND sub-toggle
   `datastore.networkPolicy.enabled` (both default true).

4. `charts/llmsafespace/templates/NOTES.txt` — added DATASTORE
   CREDENTIALS section explaining: rotation safety, the manual
   ALTER USER step required for in-place postgres role rotation,
   the valkey rollout-restart step, and the NetworkPolicy
   selector-override knob for non-default deployments.

### Local manifests

5. `local/deps-postgres.yaml` — switched `POSTGRES_PASSWORD` to
   `secretKeyRef: { name: llmsafespace-credentials, key: postgres-password }`.

6. `local/deps-valkey.yaml` — added `REDIS_PASSWORD` env from the
   chart Secret + `--requirepass "$REDIS_PASSWORD"` arg via
   `sh -c 'exec valkey-server ...'`. Readiness probe authenticates
   with the same password.

7. `local/postgres-redis.yaml` — rewrote to also use the Secret;
   replaced `redis-master` (Redis) with `valkey` (Deployment) +
   added an alias Service `redis-master` so existing chart values
   still resolve. Pod labels updated to match the chart NetPol
   selectors (`app: postgres` / `app: valkey`).

### Tests

8. `charts/llmsafespace/chart_test.go` — six new tests:
   - `TestG26_DefaultRender_PostgresPasswordIsGenerated`
   - `TestG26_DefaultRender_RedisPasswordIsGenerated`
   - `TestG26_OperatorOverride_PostgresPasswordIsRespected`
   - `TestG26_DefaultRender_HasPostgresIngressPolicy`
   - `TestG26_DefaultRender_HasValkeyIngressPolicy`
   - `TestG26_DatastoreNetworkPolicy_OptOut`

   All six fail without the fix; mutation-validated by reverting
   `values.yaml` to `postgresPassword: "changeme"` and confirming
   `TestG26_DefaultRender_PostgresPasswordIsGenerated` red-tests.

---

## Skeptical-validator pass

A separate validator agent re-derived the threat from RT-4.5 and
attempted bypass paths. Three REWORK items found and addressed:

1. **`helm upgrade` preserves "changeme"** — original lookup pattern
   would read it back and re-emit. Fixed by adding the
   `$insecurePostgres = list "changeme" ""` (and `$insecureRedis =
   list ""`) check that forces re-randomise when the live value is
   in the insecure-defaults set.

2. **`local/postgres-redis.yaml` orphaned** — bootstrap scripts apply
   that file, not `local/deps-*.yaml`. Without the rewrite the chart
   NetPol would not match the live pod labels. Fixed by rewriting
   `local/postgres-redis.yaml` to use Secret-keyref + `app: postgres`/
   `app: valkey` labels matching the new NetPol selectors.

3. **Postgres role-password rotation in-band** — `helm` rotates the
   K8s Secret; the data dir keeps the old superuser password until
   `ALTER USER` runs. Fixed by documenting the manual step
   prominently in NOTES.txt with copy-paste kubectl exec commands.

Plus minor: documented (a) operator-supplied password preservation
across upgrades, (b) empty-Secret edge case in `lookup` is safe via
`hasKey`, (c) randAlphaNum is alphanumeric so shell-escaping
`--requirepass` is safe.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 60s ./charts/llmsafespace/...` | PASS (all 11 tests) |
| Mutation: `values.yaml: postgresPassword: "changeme"` → run TestG26_DefaultRender_PostgresPasswordIsGenerated | FAIL (as expected) |
| Restore + re-run | PASS |
| `python3 -c 'yaml.safe_load_all(...)'` on local/{deps-postgres,deps-valkey,postgres-redis}.yaml | parses |
| `helm template --namespace test-ns --release-name test charts/llmsafespace/` | renders cleanly with random ≥24-char passwords |

---

## Live re-pentest plan (next step, after CI builds image)

1. CI builds and ships chart artefact (no image build needed for chart-only changes; this commit is chart + local manifests, nothing in API/controller/runtime images).
2. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values` — should re-randomise postgres/redis passwords because the live Secret has `changeme`/empty per RT-4.5.
3. `kubectl get secret llmsafespace-credentials -n default -o jsonpath='{.data.postgres-password}' | base64 -d` — must NOT be "changeme".
4. `kubectl exec -n default deploy/postgres -- psql -U llmsafespace -c "ALTER USER llmsafespace WITH PASSWORD '$NEW_PWD';"` — apply the rotation in-band.
5. `kubectl rollout restart -n default deploy/postgres deploy/valkey` — pick up new env-from refs.
6. Re-run RT-4.5 from `phase-4/run-phase4.py`: verify Valkey `CONFIG GET requirepass` is now non-empty AND postgres connection with "changeme" fails.
7. Verify NetPol holds: `kubectl run -n default test-pod --image=alpine -it --rm -- nc -zv postgres 5432` from a non-API pod should TIMEOUT (since it lacks `component=api`/`component=migrate` labels).

---

## Files modified

- `charts/llmsafespace/templates/secret.yaml` (rewritten with lookup + insecure-default detection)
- `charts/llmsafespace/templates/NOTES.txt` (added DATASTORE CREDENTIALS section)
- `charts/llmsafespace/templates/datastore-network-policy.yaml` (NEW)
- `charts/llmsafespace/values.yaml` (flipped postgresPassword default + added datastore.networkPolicy)
- `charts/llmsafespace/chart_test.go` (added six TestG26_* cases)
- `local/deps-postgres.yaml` (secretKeyRef for POSTGRES_PASSWORD)
- `local/deps-valkey.yaml` (added REDIS_PASSWORD + --requirepass)
- `local/postgres-redis.yaml` (rewritten end-to-end; valkey-only with redis-master alias)

---

## Tracker update

`design/stories/epic-17-security-review/remediation/MASTER-TRACKER.md`:
G26 status will move from **MINE / pending** to **MINE / live-pending**
after this commit lands. Once step 6 above passes RT-4.5 in the live
cluster the row moves to DONE.

---

## Next finding

Phase C/G2 — `F1.2.1 + F1.2.2 + RT-2.18 + RT-6.10 + RT-6.1`: webhook
validation for `Spec.Runtime`, `Spec.Status` subresource forgery,
storage-class allow-list, and traversal in spec. Single PR closes 5
findings. The other agent appears to be working on different secrets
hardening commits in parallel; webhook is fully outside their scope.
