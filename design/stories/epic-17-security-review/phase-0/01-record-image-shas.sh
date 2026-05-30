#!/usr/bin/env bash
# Phase 0 — RT-0.2 deliverable: record image digests by sha256.
#
# Purpose: every Phase 1+ finding cites image SHAs so the issue is
# unambiguously tied to a specific build. If a future operator re-runs
# the pentest and gets a different finding, comparing this file proves
# whether the underlying image changed.
#
# Output: phase-0-artefacts/image-shas.json
#
#   {
#     "recorded_at": "2026-05-29T19:00:00Z",
#     "cluster":     "kind-llmsafespace-pentest",
#     "images": {
#       "api":             {"name": "...", "tag": "...", "digest_sha256": "..."},
#       "controller":      {"name": "...", "tag": "...", "digest_sha256": "..."},
#       "runtime-base":    {"name": "...", "tag": "...", "digest_sha256": "..."},
#       "cilium":          {"name": "...", "tag": "...", "digest_sha256": "..."}
#     }
#   }
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"
mkdir -p "${ARTEFACTS_DIR}"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_TAG="${IMAGE_TAG:-pentest}"

if ! kind get clusters 2>&1 | grep -qx "${CLUSTER_NAME}"; then
    echo "ERROR: cluster ${CLUSTER_NAME} not found. Run 00-bootstrap.sh first." >&2
    exit 1
fi

# image_digest <name>:<tag>
# Resolves the digest from the kind node's containerd image store. We do
# this rather than `docker inspect` because kind's images live inside the
# node container, not the host docker daemon.
image_digest() {
    local image="$1"
    local node="${CLUSTER_NAME}-control-plane"
    docker exec "${node}" crictl inspecti "${image}" 2>/dev/null \
        | jq -r '.status.repoDigests[0] // empty' \
        | sed -E 's/^[^@]+@//'
}

API_IMAGE="docker.io/llmsafespace/api:${IMAGE_TAG}"
CTRL_IMAGE="docker.io/llmsafespace/controller:${IMAGE_TAG}"
RT_IMAGE="docker.io/llmsafespace/runtime-base:${IMAGE_TAG}"

API_DIGEST="$(image_digest "${API_IMAGE}")"
CTRL_DIGEST="$(image_digest "${CTRL_IMAGE}")"
RT_DIGEST="$(image_digest "${RT_IMAGE}")"

# Cilium image — read off the running deployment.
CILIUM_FULL=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="cilium-agent")].image}' 2>/dev/null \
    || echo "")
CILIUM_DIGEST=""
if [[ -n "${CILIUM_FULL}" ]]; then
    # If image is referenced by tag in the manifest, look up the running
    # pod's image_id which carries the digest.
    CILIUM_DIGEST=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system \
        get pods -l k8s-app=cilium -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' \
        | sed -E 's|^[^@]+@||')
fi

if [[ -z "${API_DIGEST}" || -z "${CTRL_DIGEST}" || -z "${RT_DIGEST}" ]]; then
    echo "ERROR: one or more image digests could not be resolved" >&2
    echo "  api=${API_DIGEST}" >&2
    echo "  controller=${CTRL_DIGEST}" >&2
    echo "  runtime-base=${RT_DIGEST}" >&2
    exit 1
fi

OUT="${ARTEFACTS_DIR}/image-shas.json"
jq -n \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg ctx "${KUBE_CONTEXT}" \
    --arg api "${API_IMAGE}" --arg apidig "${API_DIGEST}" \
    --arg ctrl "${CTRL_IMAGE}" --arg ctrldig "${CTRL_DIGEST}" \
    --arg rt "${RT_IMAGE}" --arg rtdig "${RT_DIGEST}" \
    --arg cil "${CILIUM_FULL}" --arg cildig "${CILIUM_DIGEST}" \
    '{
        recorded_at: $ts,
        cluster: $ctx,
        images: {
            api:           { reference: $api, digest_sha256: $apidig },
            controller:    { reference: $ctrl, digest_sha256: $ctrldig },
            "runtime-base": { reference: $rt, digest_sha256: $rtdig },
            cilium:        { reference: $cil, digest_sha256: $cildig }
        }
    }' > "${OUT}"

echo "✓ image SHAs written to ${OUT}"
jq '.images | to_entries[] | "\(.key): \(.value.digest_sha256[:16])..."' "${OUT}" -r
