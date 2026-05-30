#!/usr/bin/env bash
# Phase 0 — RT-0.1 + RT-0.2: cluster + LLMSafeSpace install (pentest variant).
#
# Differs from local/bootstrap.sh:
#
#   - Uses kind-cluster-pentest.yaml (multi-node, no-default-CNI, audit
#     logging enabled).
#   - Installs Cilium for NetworkPolicy enforcement. Without this, the
#     G16 fix renders YAML but has zero runtime effect.
#   - Installs LLMSafeSpace with networkPolicy.enabled=true (the post-fix
#     default) and TLS-at-ingress disabled (the pentest cluster is not
#     internet-facing).
#   - Records image digests after rollout for reproducibility.
#
# Idempotent: re-runs reuse the existing cluster.
set -Eeuo pipefail

# -----------------------------------------------------------------------------
# Pretty logging — matches local/bootstrap.sh house style.
# -----------------------------------------------------------------------------
if [[ -t 1 ]]; then
    BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GREEN=$'\033[32m'
    YELLOW=$'\033[33m'; CYAN=$'\033[36m'; RESET=$'\033[0m'
else
    BOLD=''; DIM=''; RED=''; GREEN=''; YELLOW=''; CYAN=''; RESET=''
fi
log()  { printf '%s==>%s %s\n' "${CYAN}${BOLD}" "${RESET}" "$*"; }
ok()   { printf '%s ✓%s %s\n' "${GREEN}" "${RESET}" "$*"; }
warn() { printf '%s !%s %s\n' "${YELLOW}" "${RESET}" "$*" >&2; }
die()  { printf '%s ✗%s %s\n' "${RED}${BOLD}" "${RESET}" "$*" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Configuration
# -----------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
IMAGE_TAG="${IMAGE_TAG:-pentest}"
OPENCODE_VERSION="${OPENCODE_VERSION:-1.2.27}"
NS="llmsafespace"
RELEASE_NAME="llmsafespace"

CILIUM_VERSION="${CILIUM_VERSION:-1.15.6}"

API_IMAGE="llmsafespace/api:${IMAGE_TAG}"
CONTROLLER_IMAGE="llmsafespace/controller:${IMAGE_TAG}"
RUNTIME_IMAGE="llmsafespace/runtime-base:${IMAGE_TAG}"

mkdir -p "${ARTEFACTS_DIR}"

# Stage the audit policy where kind expects it during cluster create.
# kind mounts /etc/kubernetes/audit-policy.yaml from the host node
# container, so we need to materialise the file before `kind create`.
AUDIT_DIR="${ARTEFACTS_DIR}/audit"
mkdir -p "${AUDIT_DIR}"

# -----------------------------------------------------------------------------
# Phase 1: prereq checks
# -----------------------------------------------------------------------------
log "Phase 1/7 — prerequisite checks"
for cmd in kind kubectl helm docker go cilium jq; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        die "$cmd not found in PATH. Install per tools-manifest.txt and re-run."
    fi
done
docker info >/dev/null 2>&1 || die "Docker daemon not reachable."
ok "all required tools present"

# -----------------------------------------------------------------------------
# Phase 2: kind cluster (no default CNI, audit logging enabled)
# -----------------------------------------------------------------------------
log "Phase 2/7 — kind cluster"
if kind get clusters 2>&1 | grep -qx "${CLUSTER_NAME}"; then
    ok "cluster '${CLUSTER_NAME}' already exists"
else
    # kind reads paths in extraMounts relative to its CWD; run from the
    # phase-0 dir so ./phase-0-artefacts/audit resolves correctly.
    (cd "${SCRIPT_DIR}" && kind create cluster --config kind-cluster-pentest.yaml)
    ok "cluster created"
fi
kubectl --context "${KUBE_CONTEXT}" cluster-info >/dev/null
ok "kubectl context ${KUBE_CONTEXT} reachable"

# Stage the audit policy on each node container so kube-apiserver can read it
# via its hostPath mount. We do this AFTER cluster creation because the
# control-plane container is what mounts /etc/kubernetes/audit-policy.yaml.
log "  staging audit policy on node container"
for node in $(kind get nodes --name "${CLUSTER_NAME}" | grep control-plane); do
    docker cp "${SCRIPT_DIR}/audit-policy.yaml" \
        "${node}:/etc/kubernetes/audit-policy.yaml"
done
ok "audit policy staged"

# -----------------------------------------------------------------------------
# Phase 3: Cilium CNI (G16 enforcement)
# -----------------------------------------------------------------------------
log "Phase 3/7 — Cilium CNI"
if cilium status --context "${KUBE_CONTEXT}" >/dev/null 2>&1; then
    ok "Cilium already installed"
else
    cilium install --version "${CILIUM_VERSION}" \
        --context "${KUBE_CONTEXT}" \
        --set kubeProxyReplacement=true \
        --set k8sServiceHost=$(docker inspect "${CLUSTER_NAME}-control-plane" \
            --format '{{ .NetworkSettings.Networks.kind.IPAddress }}') \
        --set k8sServicePort=6443
    cilium status --context "${KUBE_CONTEXT}" --wait
    ok "Cilium installed and healthy"
fi

# -----------------------------------------------------------------------------
# Phase 4: build + load images
# -----------------------------------------------------------------------------
if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
    warn "SKIP_BUILD=1 — re-using existing images"
else
    log "Phase 4/7 — build images"
    cd "${REPO_ROOT}"
    BUILD_ARGS=(--network host --build-arg "GOPROXY=$(go env GOPROXY 2>/dev/null || echo direct)")

    log "  building ${API_IMAGE}"
    docker build "${BUILD_ARGS[@]}" -f api/Dockerfile -t "${API_IMAGE}" .
    log "  building ${CONTROLLER_IMAGE}"
    docker build "${BUILD_ARGS[@]}" -f controller/Dockerfile -t "${CONTROLLER_IMAGE}" .
    log "  building ${RUNTIME_IMAGE} (opencode ${OPENCODE_VERSION})"
    docker build "${BUILD_ARGS[@]}" \
        --build-arg "OPENCODE_VERSION=${OPENCODE_VERSION}" \
        -f runtimes/base/Dockerfile -t "${RUNTIME_IMAGE}" .

    log "  loading images into kind"
    for img in "${API_IMAGE}" "${CONTROLLER_IMAGE}" "${RUNTIME_IMAGE}"; do
        kind load docker-image "${img}" --name "${CLUSTER_NAME}"
    done
    ok "images loaded"
fi

# -----------------------------------------------------------------------------
# Phase 5: cert-manager + Postgres + Redis
# -----------------------------------------------------------------------------
log "Phase 5/7 — cert-manager + dependencies"
if ! kubectl --context "${KUBE_CONTEXT}" get ns cert-manager >/dev/null 2>&1; then
    kubectl --context "${KUBE_CONTEXT}" apply \
        -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml
fi
for d in cert-manager cert-manager-cainjector cert-manager-webhook; do
    kubectl --context "${KUBE_CONTEXT}" -n cert-manager rollout status \
        "deployment/${d}" --timeout=180s
done
ok "cert-manager ready"

# Reuse the dev Postgres+Redis manifest. It deploys into the llmsafespace
# namespace which we use for both control plane and workspaces in this
# pentest layout. (Production split is out of scope for this exercise.)
kubectl --context "${KUBE_CONTEXT}" apply -f "${REPO_ROOT}/local/postgres-redis.yaml"
kubectl --context "${KUBE_CONTEXT}" -n "${NS}" rollout status deployment/postgres --timeout=180s
kubectl --context "${KUBE_CONTEXT}" -n "${NS}" rollout status deployment/redis-master --timeout=180s
ok "Postgres + Redis ready"

# -----------------------------------------------------------------------------
# Phase 6: helm install LLMSafeSpace (with networkPolicy.enabled=true
# explicitly, even though it is the post-G16 default — make it visible
# in the install command so any future change is loud).
# -----------------------------------------------------------------------------
log "Phase 6/7 — helm install LLMSafeSpace"
helm --kube-context "${KUBE_CONTEXT}" upgrade --install "${RELEASE_NAME}" \
    "${REPO_ROOT}/charts/llmsafespace" \
    -n "${NS}" --create-namespace \
    --set "api.image.repository=llmsafespace/api" \
    --set "api.image.tag=${IMAGE_TAG}" \
    --set "api.image.pullPolicy=IfNotPresent" \
    --set "controller.image.repository=llmsafespace/controller" \
    --set "controller.image.tag=${IMAGE_TAG}" \
    --set "controller.image.pullPolicy=IfNotPresent" \
    --set "postgresql.host=postgres" \
    --set "postgresql.port=5432" \
    --set "postgresql.user=llmsafespace" \
    --set "postgresql.database=llmsafespace" \
    --set "redis.host=redis-master" \
    --set "redis.port=6379" \
    --set "externalSecret.create=true" \
    --set "externalSecret.postgresPassword=changeme" \
    --set "api.config.logging.development=true" \
    --set "networkPolicy.enabled=true" \
    --wait --timeout 5m

kubectl --context "${KUBE_CONTEXT}" -n "${NS}" rollout status \
    deployment/llmsafespace-api --timeout=180s
kubectl --context "${KUBE_CONTEXT}" -n "${NS}" rollout status \
    deployment/llmsafespace-controller --timeout=180s
ok "LLMSafeSpace installed"

# -----------------------------------------------------------------------------
# Phase 7: assert post-install invariants relevant to Phase 0.
# These are NOT pentest findings; they are smoke checks that the kit
# itself is in a known-good state.
# -----------------------------------------------------------------------------
log "Phase 7/7 — post-install invariants"

# 7a. NetworkPolicies from G16 must be applied.
NETPOL_COUNT=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS}" get netpol \
    -l app.kubernetes.io/component=workspace-network-policy \
    -o name 2>/dev/null | wc -l)
if (( NETPOL_COUNT < 2 )); then
    die "expected ≥2 workspace NetworkPolicies (G16); found ${NETPOL_COUNT}. Helm install regressed?"
fi
ok "workspace NetworkPolicies applied (${NETPOL_COUNT} policies)"

# 7b. Sandbox PodSpec template — verify the controller would render
# AutomountServiceAccountToken=false. We can't probe the controller's
# template directly without creating a workspace, so we defer this to
# 02-verify-control-fixture.sh which reads a real sandbox pod.
ok "G17 verification deferred to control fixture step"

# 7c. Audit logs are flowing.
if [[ -d "${AUDIT_DIR}" ]] && find "${AUDIT_DIR}" -name 'audit.log*' -newer "${SCRIPT_DIR}/00-bootstrap.sh" 2>/dev/null | grep -q .; then
    ok "audit log present"
else
    warn "no audit log seen yet — first events may not have flushed; check in 04-verify-logging-baseline.sh"
fi

# -----------------------------------------------------------------------------
# Done — print next steps.
# -----------------------------------------------------------------------------
cat <<EOF

${BOLD}${GREEN}Phase 0 RT-0.1 + RT-0.2 complete.${RESET}

Cluster:    ${KUBE_CONTEXT}
Namespace:  ${NS}
Release:    ${RELEASE_NAME}

${BOLD}Next steps (in this directory):${RESET}

  ./01-record-image-shas.sh
  ./02-deploy-control-fixture.sh
  ./02-verify-control-fixture.sh
  ./03-provision-accounts.sh
  ./04-verify-logging-baseline.sh
  ./05-snapshot-baseline.sh
  ./exit-check.sh

To tear down: ./teardown.sh

EOF
