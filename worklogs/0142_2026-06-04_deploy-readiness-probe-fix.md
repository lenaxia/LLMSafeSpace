# Worklog: Deploy Readiness-Probe Active Gate Fix

**Date:** 2026-06-04
**Session:** Deploy controller sha-fa5bad9 to k8s cluster; monitor CI
**Status:** Complete

---

## Objective

Deploy the readiness-probe Active gate fix (worklog 0140) to the production cluster and confirm clean rollout.

---

## Work Completed

- CI run 26935347794 passed all jobs (one transient arm64 Docker buildx blob error re-run; no code issues)
- `kubectl set image deployment/llmsafespace-controller manager=ghcr.io/lenaxia/llmsafespace/controller:sha-fa5bad9` — rolled out cleanly
- Controller pod `llmsafespace-controller-687dcf8d5c-8cfpk` running; zero errors in logs post-deploy
- Previous image: `sha-33d3ef2` → new image: `sha-fa5bad9`

---

## Key Decisions

None — straightforward deploy of the prior session's fix.

---

## Blockers

None.

---

## Tests Run

CI green (all jobs passed after one transient arm64 re-run).

---

## Next Steps

Monitor cluster for absence of 500s on `POST /sessions/new` immediately after workspace Active transitions.

---

## Files Modified

- `worklogs/0142_2026-06-04_deploy-readiness-probe-fix.md` — this file
