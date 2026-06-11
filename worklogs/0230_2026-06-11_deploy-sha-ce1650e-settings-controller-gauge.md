# Worklog 0230 — Deploy sha-ce1650e: Settings Panel Fix + Controller Gauge Seed

**Date:** 2026-06-11
**Session:** Resume in-progress helm upgrade to deploy monitoring observability stack and latest fixes
**Status:** Complete

---

## Objective

Pick up a helm upgrade that was interrupted. Two commits had landed on main since the last
deploy (ts-1781197595):
- `b71bcb90` — fix(settings): admin panel UX audit, ApiKeysTab implementation (#113)
- `ce1650e4` — fix(controller): seed WorkspacesRunning gauge from cluster state at startup (#114)

Also restore monitoring configuration that was dropped from `values.local.yaml`.

---

## Work Completed

### Root Cause Analysis

1. **Image tag identification:** The CI build for `ce1650e4` produced `sha-ce1650e` but the
   ts-manifest merge job did not complete (only `sha-` digest tags present in GHCR, no
   corresponding `ts-` tag). `sha-ce1650e == dev` tag — verified via manifest digest match
   (`sha256:0d4c024999bd844b29c5bc734dfe24e641170d5eb3bbf99678b2a5d146da77e9`).

2. **Wrong namespace:** `make helm-deploy` defaults to `RELEASE_NS=llmsafespace` but the
   release is in `default`. Required `RELEASE_NS=default` on every invocation.

3. **Missing IMAGE_TAG:** First attempt ran without `IMAGE_TAG=` which caused pods to pull
   the chart default `0.1.0` tag (non-existent) → `ImagePullBackOff`. Fixed by passing
   `IMAGE_TAG=sha-ce1650e`.

4. **values.local.yaml out of sync:** The gitignored `values.local.yaml` had lost the
   monitoring, inferenceRelayURL, webhooks, and api.ingress settings that were present in
   revision 213. These were restored to match the previous known-good state.

### Steps Taken

1. Updated `values-cluster.yaml` to document `sha-ce1650e` as deployed tag (with note that
   ts- merge did not complete).
2. Restored `charts/llmsafespace/values.local.yaml` with all cluster-specific overrides:
   - `api.ingress.enabled: false`
   - `redis.host: valkey`, `redis.port: 6379`
   - `mcp.enabled: false`
   - `inferenceRelayURL: https://relay.safespaces.dev`
   - `webhooks.enabled: true` + `allowedImageRegistries`
   - `monitoring.enabled: true` (dashboards, prometheusRules, serviceMonitors in `default` ns)
3. Ran `make helm-deploy RELEASE_NS=default IMAGE_TAG=sha-ce1650e` — revision 216, all
   rollouts succeeded.

### Validated

- All pods Running: 2× api, 1× controller, 1× frontend
- `helm get values` confirms monitoring enabled, inferenceRelayURL set, sha-ce1650e on all images
- Revision 216 matches revision 213's user-supplied values (restored from `helm get values --revision 213`)

---

## Key Decisions

**Use sha-ce1650e instead of ts- tag:** The CI ts-manifest-merge job for `ce1650e4` did not
complete. `sha-ce1650e` is a valid multi-arch OCI index manifest (content-type:
`application/vnd.oci.image.index.v1+json`) pointing to the same digest as `dev`. Using
`sha-` is equivalent and immutable. No ts- tag available for this commit.

**Restore values.local.yaml from revision 213 helm history:** Rather than guessing what
was in the file, used `helm get values --revision 213` to reconstruct the exact settings
that were previously applied to the cluster.

---

## Blockers

None.

---

## Tests Run

- `kubectl get pods` — all Running, 0 restarts
- `helm get values llmsafespace -n default` — monitoring, inferenceRelayURL, webhooks confirmed

---

## Next Steps

If CI ts-manifest-merge is consistently failing, investigate the GitHub Actions merge-manifest
job for the `ce1650e4` workflow run. The `sha-` tags exist for all four images but the `ts-`
tag was never pushed.

---

## Files Modified

- `values-cluster.yaml` — updated to sha-ce1650e with clarifying notes
- `charts/llmsafespace/values.local.yaml` — restored full cluster override set (gitignored)
