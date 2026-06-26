# Master-secret mount path collision fix

## Objective

Fix the runc "not a directory" crashloop on api Deployment caused by chart-default `masterSecret.fileMountPath` (`/etc/llmsafespaces/master-secret`) sitting inside the api config volume mountPath (`/etc/llmsafespaces`). Add a regression test that guards the whole bug class so a future template author can't reintroduce nested secret-over-configmap subPath mounts under a different path. Sync all canonical docs (README-LLM, README, THREAT-MODEL G48, epic-50 README) to the new default per Rule 5/7.

## Work Completed

### Chart fix

- `charts/llmsafespaces/values.yaml` — change default `fileMountPath` from `/etc/llmsafespaces/master-secret` to `/var/run/secrets/llmsafespaces/master-secret`. Sibling location, FHS-correct for projected service secrets (mirrors `/var/run/secrets/kubernetes.io/serviceaccount/`), outside any other volumeMount. Added inline comment explaining why nesting under another mount is unsafe.
- `charts/llmsafespaces/templates/api-deployment.yaml` — update both `default` fallbacks (env value `LLMSAFESPACES_MASTER_SECRET_FILE` + volumeMount `mountPath`) so they track each other atomically. Operators with explicit overrides keep their override.

### Regression test

- `charts/llmsafespaces/chart_master_secret_test.go` — added `TestMasterSecret_MountPathNotNestedInOtherVolume`. Iterates every volumeMount on the rendered api container, skips `master-secret`, and asserts no other mountPath is a strict ancestor (handles exact-equality and prefix cases with trailing-slash normalization). Uses `helm template` (same path as operators + the helm-deploy target). The test would fail under the old default — verified by running it against `git stash` of the values change.
- Two existing US-50.1 default-path assertions updated to the new path (`TestUS501_DefaultRender_KEKViaFileMount_NotEnvVar`).

### Documentation sync (Rule 5/7)

- `README-LLM.md:406` — Master KEK Delivery section path reference.
- `README.md:494` — top-level Security section.
- `design/stories/epic-17-security-review/THREAT-MODEL.md:75,136,373` — §2 Assets row, §4.1 attack-tree mitigation note, **G48** control entry. The threat model is the authoritative security-control registry; an auditor verifying G48 must see the actual shipped path.
- `design/stories/epic-50-master-kek-hardening/README.md:319,339` — story files plan + verification step.
- `charts/llmsafespaces/KEK-ROTATION.md` — operator runbook (4 sites: prereq, dry-run example, rotation-window helm values, multi-file env var format).

Historical references in earlier worklogs (`0518`, `0460`) and the regression test's docstring are intentionally preserved — they describe states that were true at the time and the bug being guarded against.

## Key Decisions

- **Mount-path choice: `/var/run/secrets/llmsafespaces/master-secret`.** Considered `/etc/secrets/llmsafespaces/master-secret` and `/var/lib/llmsafespaces/master-secret`. Picked `/var/run/secrets/<svc>/` because it's the convention Kubernetes uses for projected SA tokens, signals "in-memory tmpfs-backed" to operators reading the spec, and is unambiguously outside any application config directory.
- **Regression test guards class, not string.** The dangerous shape is "secret-as-subPath nested under any other volume's mountPath," not the specific old path. Asserting "≠ `/etc/llmsafespaces/master-secret`" would let a future author put it under `/etc/llmsafespaces/secret/master-secret` and reproduce the bug. The ancestor check covers all paths.
- **No backwards-compat shim.** Operators on chart defaults transparently move to the new path on next helm upgrade; the env var (`LLMSAFESPACES_MASTER_SECRET_FILE`) tracks the same chart value so there's no instant where path-env disagrees with mount. Operators with explicit `masterSecret.fileMountPath` overrides are unaffected. A migration shim (e.g. mounting at both paths during a rolling upgrade) would add complexity for no benefit.
- **Doc sync in same PR.** Per reviewer feedback (and Rules 5/7), canonical docs that describe shipped behaviour can't lag a default change by even one PR. Worklog 0518 set the precedent: silent assumption drift in the threat model is treated as a defect.

## Assumptions

- Kubelet creates intermediate directories for projected secret volumeMounts; `/var/run/secrets/llmsafespaces/` does not need to pre-exist on the container's rootfs. Verified by reading the rendered manifest + observing the live cluster patch reproduction work.
- The api container's `readOnlyRootFilesystem: true` does not interfere with the new mount; volumeMounts always overlay below the read-only rootfs (same mechanism as the default SA-token mount).
- No external integration depends on the file path being `/etc/llmsafespaces/master-secret` (e.g. an out-of-tree operator or sidecar that hardcoded the path). Only the api container reads it; the env var is the contract.

## Adversarial self-review

- *"What if `/var/run/secrets/llmsafespaces/` is reserved by another mount?"* — checked: no other volumeMount in the api Deployment touches `/var/run/secrets/`. The new regression test would catch any future addition that did.
- *"What if a kubelet/runtime version doesn't support nested secret subPath at all?"* — irrelevant to this fix. The new path is a sibling, not nested, so the question doesn't apply.
- *"What if an operator's `values.local.yaml` set `fileMountPath` to a child of another mount?"* — chart can't constrain operator overrides via templates alone. The chart test only validates default render. Acceptable: overrides are an explicit choice; the operator owns the consequences. (Could add a chart-level `helm test` that operators run pre-deploy in a future hardening pass.)
- *"Will helm upgrade restart pods even if no other change?"* — yes, both env value and mountPath change → Deployment template hash changes → rolling restart. This is the correct behaviour: same surface as any other env/mount edit.

## Blockers

None. CI green on PR #405 (23 success, 9 skipped, 0 failures).

## Tests Run

- `helm lint charts/llmsafespaces/` — clean.
- `helm template ... | grep -A 3 master-secret` — rendered env + mount agree on `/var/run/secrets/llmsafespaces/master-secret`.
- `go test ./charts/llmsafespaces/...` — full chart suite, 89s, all pass including new regression test and updated US-50.1 assertions.
- Live cluster reproduction (worker-03, kernel 6.x, containerd 2.x): patched only the api Deployment's env + mountPath to the new default. Confirmed runc "not a directory" error resolves; api advances past the mount failure to the next dependency (Redis lookup, unrelated to this fix).
- CI on PR #405: 23 SUCCESS / 9 SKIPPED / 0 FAILURE.

## Next Steps

After merge: re-attempt helm upgrade in the home cluster to deploy the controller image carrying worklogs 0541 (boot-path optimizations) and 0542/0544 (free-models cache); then run the workspace cold-start/suspend/resume benchmark. Out of scope for this PR.

## Files Modified

- `charts/llmsafespaces/values.yaml` — default `fileMountPath` + comment
- `charts/llmsafespaces/templates/api-deployment.yaml` — both `default` fallbacks
- `charts/llmsafespaces/chart_master_secret_test.go` — updated US-50.1 assertions + new `TestMasterSecret_MountPathNotNestedInOtherVolume`
- `charts/llmsafespaces/KEK-ROTATION.md` — 4 operator-runbook references
- `README-LLM.md:406` — Master KEK Delivery
- `README.md:494` — Security section
- `design/stories/epic-17-security-review/THREAT-MODEL.md:75,136,373` — §2, §4.1, G48
- `design/stories/epic-50-master-kek-hardening/README.md:319,339` — implementation plan + verification
