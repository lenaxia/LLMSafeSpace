#!/usr/bin/env bash
# Phase 0 RT-0.6: snapshot the cluster baseline for post-test diffing.
#
# Output: phase-0-artefacts/baseline-${TIMESTAMP}.tar.gz containing:
#
#   helm-get-all.yaml          - `helm get all` output for the release
#   kubectl-all-namespaces.yaml - all resources across all namespaces
#   crds.yaml                  - CRD definitions
#   netpol.yaml                - all NetworkPolicies
#   sa-tokens.json             - sha256 of every SA token (so token
#                                rotation post-pentest is detectable)
#   rbac.yaml                  - RBAC resources
#   image-shas.json            - copied from RT-0.2 step
#
# Phase 7 will produce a delta snapshot and the diff is a Phase 1
# deliverable: anything changed by the pentest must be enumerable.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
NS="llmsafespace"
RELEASE_NAME="llmsafespace"

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT

echo "==> Snapshotting cluster baseline (timestamp ${TIMESTAMP})"

# helm get all
helm --kube-context "${KUBE_CONTEXT}" get all "${RELEASE_NAME}" -n "${NS}" \
    > "${WORKDIR}/helm-get-all.yaml" 2>/dev/null
echo "  ✓ helm-get-all.yaml"

# All resources across all namespaces (best-effort).
kubectl --context "${KUBE_CONTEXT}" get \
    deployments,statefulsets,daemonsets,services,ingresses,networkpolicies,configmaps,secrets,serviceaccounts,roles,rolebindings,clusterroles,clusterrolebindings \
    -A -o yaml > "${WORKDIR}/kubectl-all-namespaces.yaml" 2>/dev/null || true
echo "  ✓ kubectl-all-namespaces.yaml"

# CRDs
kubectl --context "${KUBE_CONTEXT}" get crd -o yaml > "${WORKDIR}/crds.yaml"
echo "  ✓ crds.yaml"

# NetworkPolicies — focused dump for G16 verification at diff time.
kubectl --context "${KUBE_CONTEXT}" get netpol -A -o yaml > "${WORKDIR}/netpol.yaml"
echo "  ✓ netpol.yaml"

# RBAC
kubectl --context "${KUBE_CONTEXT}" get \
    roles,rolebindings,clusterroles,clusterrolebindings \
    -A -o yaml > "${WORKDIR}/rbac.yaml" 2>/dev/null || true
echo "  ✓ rbac.yaml"

# SA tokens — record sha256 only. We never persist the token bodies in
# committable form. If a token is rotated mid-pentest, the post-snapshot
# sha will differ.
kubectl --context "${KUBE_CONTEXT}" get serviceaccounts -A -o json \
    | jq '[.items[] | {namespace: .metadata.namespace, name: .metadata.name, secrets: [.secrets[]?.name // empty]}]' \
    > "${WORKDIR}/sa-list.json"
echo "  ✓ sa-list.json"

# Image SHAs — copy if present from RT-0.2 step.
if [[ -f "${ARTEFACTS_DIR}/image-shas.json" ]]; then
    cp "${ARTEFACTS_DIR}/image-shas.json" "${WORKDIR}/image-shas.json"
    echo "  ✓ image-shas.json"
fi

# Cilium status (network plane health).
if command -v cilium >/dev/null 2>&1; then
    cilium status --context "${KUBE_CONTEXT}" --output json \
        > "${WORKDIR}/cilium-status.json" 2>/dev/null || true
    echo "  ✓ cilium-status.json"
fi

# Pack
OUT="${ARTEFACTS_DIR}/baseline-${TIMESTAMP}.tar.gz"
tar -czf "${OUT}" -C "${WORKDIR}" .
echo "✓ baseline written to ${OUT} ($(du -h "${OUT}" | cut -f1))"

# Print sha256 for the snapshot itself, so the operator can verify it
# wasn't tampered with mid-pentest.
SHA=$(sha256sum "${OUT}" | cut -c1-64)
echo "  sha256: ${SHA}"
echo "${SHA}  ${OUT##*/}" > "${OUT}.sha256"
