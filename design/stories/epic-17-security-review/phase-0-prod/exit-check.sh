#!/usr/bin/env bash
# Phase 0 (prod) — exit gate.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "✗ ${ARTEFACTS_DIR}/preflight.env missing — preflight not yet run" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

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

echo "==> Phase 0 (prod) exit-criteria check"
echo

# 1. Cluster reachable
check "context ${KUBE_CONTEXT} reachable" \
    kubectl --context "${KUBE_CONTEXT}" cluster-info

# 2. Cilium present + ready
check "cilium present" \
    kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium

# 3. LLMSafeSpace deployed
check "llmsafespace Helm release deployed" \
    helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" status "${RELEASE_NAME}"

# 4. Image SHAs recorded
check "image SHAs recorded (state-latest)" \
    test -s "${ARTEFACTS_DIR}/state-latest/image-shas.json"

# 5. Control fixture deployed
check "control fixture pod present in ${NS_FIXTURE}" \
    kubectl --context "${KUBE_CONTEXT}" -n "${NS_FIXTURE}" get pod control-fixture-pod

# 6. Test accounts file present and gitignored
check "accounts.json present" \
    test -s "${ARTEFACTS_DIR}/accounts.json"
check "accounts.json mode 0600" \
    bash -c '[[ "$(stat -c %a "'"${ARTEFACTS_DIR}"'/accounts.json")" == "600" ]]'
check "artefacts gitignored" \
    test -f "${ARTEFACTS_DIR}/.gitignore"

# 7. All three accounts have user_id resolved
if [[ -s "${ARTEFACTS_DIR}/accounts.json" ]]; then
    EMPTY=$(jq -r '[.accounts[] | select(.user_id == "" or .user_id == null)] | length' "${ARTEFACTS_DIR}/accounts.json")
    if [[ "${EMPTY}" == "0" ]]; then
        echo "✓ all accounts have user_id"
    else
        echo "✗ ${EMPTY} accounts missing user_id" >&2
        FAIL=$((FAIL+1))
    fi
fi

# 8. Baseline snapshot present
check "baseline-default snapshot present" \
    bash -c 'ls "'"${ARTEFACTS_DIR}"'"/baseline-default-*.tar.gz >/dev/null 2>&1'

echo
if (( FAIL == 0 )); then
    cat <<'EOF'

✓ Phase 0 (prod) exit criteria met.

Phase 1 may begin. Reminders:
  - blast-radius rules in README.md must be respected
  - cleanup.sh removes all kit artefacts when the pentest is done

EOF
    exit 0
else
    echo "✗ ${FAIL} exit criteria failed. Phase 1 blocked." >&2
    exit 1
fi
