#!/usr/bin/env bash
# Phase 0 (prod) — deploy the deliberately-vulnerable control fixture.
#
# The fixture lives in the dedicated `pentest-control-fixture` namespace,
# NOT in `default`. This is critical: planting privileged pods or
# cluster-admin SAs in `default` would be visible to the LLMSafeSpace
# controller and to other Helm releases co-tenanted there. The
# pentest-control-fixture ns is solely owned by this kit and is removed
# wholesale by `cleanup.sh`.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

echo "==> Applying control fixture into ${NS_FIXTURE}"
kubectl --context "${KUBE_CONTEXT}" apply -f "${SCRIPT_DIR}/02-control-fixture.yaml"

echo "==> Waiting for fixture pod to be Ready (up to 60s)"
kubectl --context "${KUBE_CONTEXT}" -n "${NS_FIXTURE}" wait \
    --for=condition=ready pod/control-fixture-pod --timeout=60s

cat <<EOF
✓ Control fixture deployed to ${NS_FIXTURE}.

The fixture pod (${NS_FIXTURE}/control-fixture-pod) carries:

  Bug 1: hostNetwork + hostPID + privileged + SYS_ADMIN/NET_ADMIN caps.
  Bug 2: busybox:1.30 base image with multiple HIGH-rated CVEs.
  Bug 3: ServiceAccount with ClusterRoleBinding to cluster-admin and
         AutomountServiceAccountToken left at default (true).

This pod is harmless (sleep loop) but is a real privilege-escalation
liability while it exists. Run ./cleanup.sh as soon as the pentest is
done.

Run ./02-verify-control-fixture.sh next to confirm tooling detects them.
EOF
