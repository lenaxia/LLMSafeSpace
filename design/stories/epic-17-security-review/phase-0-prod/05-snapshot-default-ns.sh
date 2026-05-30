#!/usr/bin/env bash
# Phase 0 (prod) — snapshot scoped to default + pentest-control-fixture
# namespaces. Does NOT touch any other namespace.
#
# Purpose: post-test diff. Anything that changed in default ns during
# the pentest is enumerable by diffing this snapshot against a snapshot
# taken at Phase 7's end.
#
# Output: phase-0-prod-artefacts/baseline-default-${TIMESTAMP}.tar.gz
#         + .sha256 sidecar.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT

echo "==> Snapshotting ${NS_TARGET} + ${NS_FIXTURE} (timestamp ${TIMESTAMP})"

# helm get all
helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    get all "${RELEASE_NAME}" > "${WORKDIR}/helm-get-all.yaml"
echo "  ✓ helm-get-all.yaml"

# Per-namespace dumps
for ns in "${NS_TARGET}" "${NS_FIXTURE}"; do
    if ! kubectl --context "${KUBE_CONTEXT}" get ns "$ns" >/dev/null 2>&1; then
        continue
    fi
    OUT="${WORKDIR}/ns-${ns}.yaml"
    kubectl --context "${KUBE_CONTEXT}" -n "$ns" get \
        deployments,statefulsets,daemonsets,services,ingresses,networkpolicies,configmaps,secrets,serviceaccounts,roles,rolebindings \
        -o yaml > "${OUT}" 2>/dev/null || true
    echo "  ✓ ns-${ns}.yaml"
done

# llmsafespace CRDs in default
for crd in workspaces runtimeenvironments; do
    if kubectl --context "${KUBE_CONTEXT}" get crd "${crd}.llmsafespace.dev" >/dev/null 2>&1; then
        kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
            get "${crd}.llmsafespace.dev" -o yaml > "${WORKDIR}/${crd}.yaml"
    fi
done
echo "  ✓ llmsafespace CRDs in ${NS_TARGET}"

# Pod inventory in default — this is the most volatile and most
# pentest-relevant. We dump pod names + spec hash so post-test diff can
# enumerate which sandbox pods existed before vs after.
kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" get pods -o yaml \
    > "${WORKDIR}/pods.yaml"
echo "  ✓ pods.yaml ($(grep -c '^- ' "${WORKDIR}/pods.yaml" 2>/dev/null || echo 0) entries)"

# DB row count fingerprint (NOT data — just count per table)
mkdir -p "${WORKDIR}/db-fingerprint"
for tbl in users workspaces sandboxes user_keys user_secrets api_keys; do
    COUNT=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
        exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -t -A \
        -c "SELECT count(*) FROM ${tbl}" 2>/dev/null || echo "ERR")
    echo "${tbl}=${COUNT}" >> "${WORKDIR}/db-fingerprint/counts.txt"
done
echo "  ✓ db-fingerprint/counts.txt"

# Image SHAs (copy from latest record-state run if present)
if [[ -d "${ARTEFACTS_DIR}/state-latest" ]]; then
    cp "${ARTEFACTS_DIR}/state-latest/image-shas.json" "${WORKDIR}/image-shas.json"
    echo "  ✓ image-shas.json (from state-latest)"
fi

PACK="${ARTEFACTS_DIR}/baseline-default-${TIMESTAMP}.tar.gz"
tar -czf "${PACK}" -C "${WORKDIR}" .
SHA=$(sha256sum "${PACK}" | cut -c1-64)
echo "${SHA}  $(basename "${PACK}")" > "${PACK}.sha256"

ln -snf "$(basename "${PACK}")" "${ARTEFACTS_DIR}/baseline-default-latest.tar.gz"

echo "✓ baseline → ${PACK}"
echo "  sha256: ${SHA}"
