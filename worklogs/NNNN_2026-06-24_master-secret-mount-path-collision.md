# Master-secret mount path collision fix

## Problem

Fresh helm installs of the chart (or any environment relying on chart-default
values) crashloop the api Deployment with:

```
OCI runtime create failed: ... mount src=...volume-subpaths/master-secret/api/1,
dst=/etc/llmsafespaces/master-secret, ...: not a directory
```

Discovered while attempting to redeploy the controller image to re-benchmark
worklog 0541 (boot-path optimizations). The api pod has been crashlooping on
this cluster's last successful release (`llmsafespace` rev 29) — it kept
running only because a custom `values.local.yaml` (now lost) overrode the
chart default. Anyone applying chart defaults hits this immediately.

## Root cause

`charts/llmsafespaces/templates/api-deployment.yaml` mounts:

1. `config` (ConfigMap) at `/etc/llmsafespaces` (line 110)
2. `master-secret` (Secret, subPath) at the chart-default
   `masterSecret.fileMountPath` of `/etc/llmsafespaces/master-secret` (line 114)

Mount #2 sits *inside* mount #1's destination. Kubernetes accepts the spec but
runc rejects the bind-mount on container start because the destination path
doesn't exist as a regular file inside the source ConfigMap volume — only
`config.yaml` does. Some kernel/runtime combos fail this with "not a directory"
rather than auto-creating, leaving the api in CrashLoopBackOff.

The bug was introduced in #318 (epic-50 US-50.1, "deliver master KEK as a file
mount, not an env var"). The PR's reviewer-suggested default landed under the
existing config mountPath without testing nested-mount semantics on a real
cluster.

## Fix

Move the chart default for `masterSecret.fileMountPath` to a sibling location:
`/var/run/secrets/llmsafespaces/master-secret`. This is the FHS-correct
location for projected service secrets (mirroring kubernetes' own
`/var/run/secrets/kubernetes.io/serviceaccount/`) and is outside any other
volumeMount path, so the inner subPath bind is unambiguous to runc.

Updates:

- `charts/llmsafespaces/values.yaml` — default `fileMountPath` + comment
  explaining why nesting under another mount is unsafe.
- `charts/llmsafespaces/templates/api-deployment.yaml` — `default` fallbacks
  for env value + volumeMount path (2 sites).
- `charts/llmsafespaces/chart_master_secret_test.go` — update the two
  default-path assertions (`TestUS501_DefaultRender_KEKViaFileMount_NotEnvVar`).
- `charts/llmsafespaces/KEK-ROTATION.md` — operator-facing path references.

## Regression test

Added `TestMasterSecret_MountPathNotNestedInOtherVolume` (chart_master_secret_test.go).
Iterates all volumeMounts on the rendered api container and asserts that no
other volumeMount's mountPath is a strict ancestor directory of the
master-secret mountPath. Guards the entire bug class — not just this specific
path string — so a future template author can't reintroduce the same shape
under a different path.

The test uses `helm template` (same path as operators + the helm-deploy
target), so it exercises the real rendering pipeline. Skips when helm isn't
on $PATH; runs in `go test ./charts/llmsafespaces/...`.

## Migration impact

Operators with explicit `masterSecret.fileMountPath` overrides keep their
override (it's still respected). Operators on chart defaults will see the
projected secret move from `/etc/llmsafespaces/master-secret` to
`/var/run/secrets/llmsafespaces/master-secret` on next upgrade. The api pod
restarts (Deployment rollout) with the new env + mount; no data migration,
no manual steps.

The `LLMSAFESPACES_MASTER_SECRET_FILE` env var is set from the same chart
value, so it tracks the mountPath atomically — no period where the path env
disagrees with the actual mount.

KEK rotation runbook references updated to the new default.

## Verification

- `helm lint charts/llmsafespaces/` — clean.
- `helm template ... | grep master-secret` — rendered env + mount agree on
  `/var/run/secrets/llmsafespaces/master-secret`.
- `go test ./charts/llmsafespaces/...` — full suite passes (89s),
  including the new regression test and updated US-50.1 path assertions.
- Live cluster reproduction (worker-03, kernel 6.x, containerd 2.x):
  patching the live api Deployment to point env + mountPath at
  `/var/run/secrets/llmsafespaces/master-secret` (without other changes)
  resolves the runc "not a directory" error; api advances past the mount
  failure to the next dependency (Redis lookup, unrelated to this fix).
  Confirms the path collision was the proximate cause and that the new
  default resolves it.

## Out of scope

This PR fixes only the mount-path collision. The cluster also has unrelated
config drift (postgres user/db naming, redis service name) surfaced while
investigating. Those are operator-side and tracked separately.
