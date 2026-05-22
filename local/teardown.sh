#!/usr/bin/env bash
# Tear down the kind cluster created by ./bootstrap.sh.
set -Eeuo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace}"

if kind get clusters 2>&1 | grep -qx "${CLUSTER_NAME}"; then
    echo "Deleting kind cluster: ${CLUSTER_NAME}"
    kind delete cluster --name "${CLUSTER_NAME}"
else
    echo "Cluster ${CLUSTER_NAME} not found; nothing to delete."
fi

# Clean up images we pushed into kind. Doesn't touch host's docker images
# (those remain available for re-loading into a fresh cluster).
echo "Done."
