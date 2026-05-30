#!/usr/bin/env bash
# Phase 0 RT-0.3 deploy: install the deliberately-vulnerable control
# fixture. The fixture is the source of truth for tool calibration:
# Phase 1 tooling MUST detect the planted bugs in this pod, or the
# tooling has a false-negative.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"

if ! kind get clusters 2>&1 | grep -qx "${CLUSTER_NAME}"; then
    echo "ERROR: cluster ${CLUSTER_NAME} not found. Run 00-bootstrap.sh first." >&2
    exit 1
fi

echo "==> Applying control fixture (intentionally vulnerable)"
kubectl --context "${KUBE_CONTEXT}" apply -f "${SCRIPT_DIR}/02-control-fixture.yaml"

echo "==> Waiting for fixture pod to be Ready (up to 60s)"
kubectl --context "${KUBE_CONTEXT}" -n pentest-control-fixture wait \
    --for=condition=ready pod/control-fixture-pod --timeout=60s

cat <<'EOF'
✓ Control fixture deployed.

The fixture pod (pentest-control-fixture/control-fixture-pod) carries:

  Bug 1: hostNetwork + hostPID + privileged + SYS_ADMIN/NET_ADMIN caps.
  Bug 2: busybox:1.30 base image with multiple HIGH-rated CVEs.
  Bug 3: ServiceAccount with ClusterRoleBinding to cluster-admin and
         AutomountServiceAccountToken left at default (true).

Run ./02-verify-control-fixture.sh next to confirm tooling detects them.
EOF
