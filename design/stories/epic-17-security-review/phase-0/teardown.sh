#!/usr/bin/env bash
# Phase 0 teardown: remove the pentest cluster and clear local artefacts.
#
# This is the rollback-plan deliverable mentioned in the Phase 0 exit
# criteria.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"

if kind get clusters 2>&1 | grep -qx "${CLUSTER_NAME}"; then
    echo "==> Deleting kind cluster ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}"
else
    echo "==> Cluster ${CLUSTER_NAME} not present"
fi

if [[ -d "${ARTEFACTS_DIR}" ]]; then
    echo "==> Removing artefacts at ${ARTEFACTS_DIR}"
    rm -rf "${ARTEFACTS_DIR}"
fi

echo "✓ teardown complete"
