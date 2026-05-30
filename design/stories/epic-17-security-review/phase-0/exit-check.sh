#!/usr/bin/env bash
# Phase 0 exit gate. Runs every Phase-0 invariant check and exits non-
# zero if anything is missing. Phase 1 cannot start until this passes.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
NS="llmsafespace"

FAIL=0
check() {
    local desc="$1"; shift
    if "$@" >/dev/null 2>&1; then
        echo "✓ ${desc}"
    else
        echo "✗ ${desc}" >&2
        FAIL=$((FAIL+1))
    fi
}

echo "==> Phase 0 exit-criteria check"
echo

# 1. Cluster up
check "cluster ${CLUSTER_NAME} reachable" \
    kubectl --context "${KUBE_CONTEXT}" cluster-info

# 2. CNI enforces NetworkPolicy
check "Cilium installed" \
    bash -c 'kubectl --context "'"${KUBE_CONTEXT}"'" -n kube-system get ds cilium'

# 3. LLMSafeSpace deployed
check "API deployment ready" \
    bash -c 'kubectl --context "'"${KUBE_CONTEXT}"'" -n "'"${NS}"'" get deploy llmsafespace-api -o jsonpath="{.status.readyReplicas}" | grep -q "^[1-9]"'
check "controller deployment ready" \
    bash -c 'kubectl --context "'"${KUBE_CONTEXT}"'" -n "'"${NS}"'" get deploy llmsafespace-controller -o jsonpath="{.status.readyReplicas}" | grep -q "^[1-9]"'

# 4. NetworkPolicies applied
check "G16 NetworkPolicies present" \
    bash -c 'count=$(kubectl --context "'"${KUBE_CONTEXT}"'" -n "'"${NS}"'" get netpol -l app.kubernetes.io/component=workspace-network-policy -o name | wc -l); [[ ${count} -ge 2 ]]'

# 5. Image SHAs recorded
check "image SHAs file exists" \
    test -s "${ARTEFACTS_DIR}/image-shas.json"

# 6. Control fixture deployed
check "control fixture pod present" \
    kubectl --context "${KUBE_CONTEXT}" -n pentest-control-fixture get pod control-fixture-pod

# 7. Test accounts file present and gitignored
check "accounts.json present" \
    test -s "${ARTEFACTS_DIR}/accounts.json"
check "accounts.json mode 0600" \
    bash -c '[[ "$(stat -c %a "'"${ARTEFACTS_DIR}"'/accounts.json")" == "600" ]]'
check "artefacts gitignored" \
    test -f "${ARTEFACTS_DIR}/.gitignore"

# 8. Audit logs flowing
check "audit log directory non-empty" \
    bash -c 'find "'"${ARTEFACTS_DIR}"'/audit" -name "audit.log*" -mmin -10 2>/dev/null | grep -q .'

# 9. Baseline snapshot present
check "baseline snapshot present" \
    bash -c 'ls "'"${ARTEFACTS_DIR}"'"/baseline-*.tar.gz >/dev/null 2>&1'

echo
if (( FAIL == 0 )); then
    cat <<'EOF'

✓ Phase 0 exit criteria met.

Phase 1 may begin. Recommended next reading:

  ../README.md  §Phase 1: Reconnaissance & Attack Surface Mapping

EOF
    exit 0
else
    echo "✗ ${FAIL} exit criteria failed. Phase 1 blocked." >&2
    exit 1
fi
