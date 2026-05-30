#!/usr/bin/env bash
# Phase 0 (prod) — record the deployed state for reproducibility.
#
# Captures:
#   - Image SHAs of LLMSafeSpace components and Cilium.
#   - Deployed Helm chart values (helm get values --all).
#   - Default-namespace inventory: deployments, statefulsets, services,
#     ingresses, configmaps, networkpolicies, plus llmsafespace CRDs.
#
# Does NOT touch any other namespace. Does NOT dump cluster-scoped
# resources beyond what the LLMSafeSpace release contains.
#
# Output: phase-0-prod-artefacts/state-${TIMESTAMP}/...
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: ${ARTEFACTS_DIR}/preflight.env missing — run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUT_DIR="${ARTEFACTS_DIR}/state-${TIMESTAMP}"
mkdir -p "${OUT_DIR}"

echo "==> Recording state at ${TIMESTAMP}"

# --- Image SHAs -----------------------------------------------------------
# In a real production cluster the kubelet writes container statuses with
# imageID = registry@sha256:... We collect those from the running pods.
echo "  → image SHAs"
{
    echo "{"
    echo "  \"recorded_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
    echo "  \"context\": \"${KUBE_CONTEXT}\","
    echo "  \"namespace\": \"${NS_TARGET}\","
    echo "  \"helm_release\": {"
    echo "    \"name\": \"${RELEASE_NAME}\","
    echo "    \"chart\": \"${RELEASE_CHART}\","
    echo "    \"revision\": \"${RELEASE_REVISION}\""
    echo "  },"
    echo "  \"images\": {"

    # api, controller, frontend deployments
    FIRST=1
    for d in llmsafespace-api llmsafespace-controller llmsafespace-frontend; do
        IMG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
            get deploy "$d" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
        IMG_ID=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
            get pods -l "app.kubernetes.io/instance=${RELEASE_NAME},app.kubernetes.io/component=${d#llmsafespace-}" \
            -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' 2>/dev/null || echo "")
        DIGEST=$(echo "${IMG_ID}" | sed -E 's|^[^@]+@||')
        [[ ${FIRST} -eq 0 ]] && echo "    ,"
        echo "    \"${d}\": { \"reference\": \"${IMG}\", \"digest_sha256\": \"${DIGEST}\" }"
        FIRST=0
    done

    # Runtime base — read off any running sandbox pod (we only need the
    # image, not pod identity)
    RT_IMG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
        get pods -l component=workspace --field-selector status.phase=Running \
        -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null || echo "")
    RT_DIGEST=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
        get pods -l component=workspace --field-selector status.phase=Running \
        -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' 2>/dev/null \
        | sed -E 's|^[^@]+@||')
    echo "    ,"
    echo "    \"runtime-base\": { \"reference\": \"${RT_IMG}\", \"digest_sha256\": \"${RT_DIGEST}\" }"

    # Cilium
    CIL_IMG=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="cilium-agent")].image}' 2>/dev/null || echo "")
    CIL_DIGEST=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system \
        get pods -l k8s-app=cilium -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="cilium-agent")].imageID}' 2>/dev/null \
        | sed -E 's|^[^@]+@||')
    echo "    ,"
    echo "    \"cilium\": { \"reference\": \"${CIL_IMG}\", \"digest_sha256\": \"${CIL_DIGEST}\" }"

    echo "  }"
    echo "}"
} > "${OUT_DIR}/image-shas.json"
ok_msg() { echo "    ✓ $*"; }
ok_msg "image-shas.json ($(jq '.images | keys | length' "${OUT_DIR}/image-shas.json") components)"

# --- Helm values ---------------------------------------------------------
echo "  → helm release values"
helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    get values "${RELEASE_NAME}" --all -o yaml \
    > "${OUT_DIR}/helm-values.yaml"
ok_msg "helm-values.yaml"

helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    get manifest "${RELEASE_NAME}" \
    > "${OUT_DIR}/helm-manifest.yaml"
ok_msg "helm-manifest.yaml"

# --- Default-ns inventory ------------------------------------------------
echo "  → default ns inventory"
# We list resources scoped to default; this captures llmsafespace and
# the other releases sharing the namespace. None of the other releases
# are part of LLMSafeSpace; we record them so post-test diff can ignore
# them as expected pre-existing state.
for kind in deployments statefulsets daemonsets services ingresses networkpolicies configmaps secrets serviceaccounts; do
    kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" get "$kind" -o yaml \
        > "${OUT_DIR}/${kind}.yaml" 2>/dev/null || true
done
ok_msg "default ns resource inventory"

# llmsafespace CRDs (workspaces, runtimeenvironments) — ns-scoped
for crd in workspaces runtimeenvironments; do
    if kubectl --context "${KUBE_CONTEXT}" get crd "${crd}.llmsafespace.dev" >/dev/null 2>&1; then
        kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" get "${crd}.llmsafespace.dev" -o yaml \
            > "${OUT_DIR}/${crd}.yaml"
    fi
done
ok_msg "llmsafespace CRDs in default ns"

# --- DB schema fingerprint -----------------------------------------------
# Schema migrations applied — useful for "is the DB at the expected
# revision" assertions in later phases.
echo "  → DB schema migrations"
kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    exec deploy/postgres -- psql -U llmsafespace -d llmsafespace \
    -c "SELECT version, dirty FROM schema_migrations" -t -A 2>/dev/null \
    > "${OUT_DIR}/db-schema-version.txt" || true
ok_msg "db-schema-version.txt"

# --- Pack ----------------------------------------------------------------
PACK="${ARTEFACTS_DIR}/state-${TIMESTAMP}.tar.gz"
tar -czf "${PACK}" -C "${ARTEFACTS_DIR}" "state-${TIMESTAMP}"
SHA=$(sha256sum "${PACK}" | cut -c1-64)
echo "${SHA}  $(basename "${PACK}")" > "${PACK}.sha256"
echo "✓ state recorded → ${PACK}"
echo "  sha256: ${SHA}"

# Symlink for "latest" so subsequent scripts don't have to know the
# timestamp.
ln -snf "state-${TIMESTAMP}" "${ARTEFACTS_DIR}/state-latest"
ln -snf "state-${TIMESTAMP}.tar.gz" "${ARTEFACTS_DIR}/state-latest.tar.gz"
