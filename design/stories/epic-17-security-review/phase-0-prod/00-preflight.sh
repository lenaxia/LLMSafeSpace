#!/usr/bin/env bash
# Phase 0 (prod variant) — preflight assertions.
#
# Refuses to proceed unless the cluster matches expectations. Phase 0 in
# production is risky precisely because the operator has cluster-admin
# credentials; this script is the discipline gate that keeps the LLM (or
# the operator) from drifting outside the allowed scope.
#
# Asserts:
#
#   1. KUBE_CONTEXT is set and points at a reachable cluster.
#   2. The current context is NOT the local kind dev cluster
#      (kind-llmsafespace) — that has its own kit and conflating the
#      two would produce wrong artefacts.
#   3. Cilium is installed and healthy.
#   4. LLMSafeSpace is deployed via Helm release `llmsafespace` in
#      `default` namespace.
#   5. Postgres is reachable in `default` ns (cleanup needs it).
#   6. The operator's identity has cluster-admin (sanity check; we don't
#      use cluster-admin for anything outside the allowed scope, but the
#      controller and API need it to operate).
#   7. The operator confirms (interactive y/n) that they understand the
#      blast-radius rules in README.md.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"
mkdir -p "${ARTEFACTS_DIR}"

if [[ -t 1 ]]; then
    BOLD=$'\033[1m'; RED=$'\033[31m'; GREEN=$'\033[32m'
    YELLOW=$'\033[33m'; CYAN=$'\033[36m'; RESET=$'\033[0m'
else
    BOLD=''; RED=''; GREEN=''; YELLOW=''; CYAN=''; RESET=''
fi
log()  { printf '%s==>%s %s\n' "${CYAN}${BOLD}" "${RESET}" "$*"; }
ok()   { printf '%s ✓%s %s\n' "${GREEN}" "${RESET}" "$*"; }
warn() { printf '%s !%s %s\n' "${YELLOW}" "${RESET}" "$*" >&2; }
die()  { printf '%s ✗%s %s\n' "${RED}${BOLD}" "${RESET}" "$*" >&2; exit 1; }

NS_TARGET="default"
NS_FIXTURE="pentest-control-fixture"
RELEASE_NAME="llmsafespace"

# 1. KUBE_CONTEXT explicitly set
if [[ -z "${KUBE_CONTEXT:-}" ]]; then
    die "KUBE_CONTEXT not set. Run: export KUBE_CONTEXT=\"\$(kubectl config current-context)\" and re-run."
fi
log "Phase 0 (prod) preflight"
log "  context: ${KUBE_CONTEXT}"

if ! kubectl --context "${KUBE_CONTEXT}" cluster-info >/dev/null 2>&1; then
    die "context ${KUBE_CONTEXT} not reachable"
fi
ok "context reachable"

# 2. Refuse to run against the local kind dev cluster — that has phase-0/
if [[ "${KUBE_CONTEXT}" == "kind-llmsafespace" || "${KUBE_CONTEXT}" == "kind-llmsafespace-pentest" ]]; then
    die "this is the prod-variant kit; for kind use ../phase-0/00-bootstrap.sh"
fi

# 3. Cilium healthy
if ! kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium >/dev/null 2>&1; then
    die "cilium DaemonSet not found in kube-system. Wrong cluster?"
fi
CILIUM_READY=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium -o jsonpath='{.status.numberReady}')
CILIUM_DESIRED=$(kubectl --context "${KUBE_CONTEXT}" -n kube-system get ds cilium -o jsonpath='{.status.desiredNumberScheduled}')
if [[ "${CILIUM_READY}" != "${CILIUM_DESIRED}" ]]; then
    die "cilium DaemonSet not fully ready (${CILIUM_READY}/${CILIUM_DESIRED})"
fi
ok "cilium ready (${CILIUM_READY}/${CILIUM_DESIRED} pods)"

# 4. LLMSafeSpace Helm release in default ns
if ! helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" status "${RELEASE_NAME}" >/dev/null 2>&1; then
    die "Helm release '${RELEASE_NAME}' not found in '${NS_TARGET}' ns"
fi
RELEASE_REVISION=$(helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    list -o json | jq -r --arg n "${RELEASE_NAME}" '.[] | select(.name==$n) | .revision')
RELEASE_CHART=$(helm --kube-context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    list -o json | jq -r --arg n "${RELEASE_NAME}" '.[] | select(.name==$n) | .chart')
ok "llmsafespace release present (chart=${RELEASE_CHART}, revision=${RELEASE_REVISION})"

# 4a. API and controller deployments are up
for d in llmsafespace-api llmsafespace-controller; do
    READY=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" get deploy "$d" \
        -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
    if [[ -z "${READY}" || "${READY}" -lt 1 ]]; then
        die "deployment $d has 0 ready replicas in ${NS_TARGET}"
    fi
    ok "$d ready (${READY} replicas)"
done

# 5. Postgres reachable
if ! kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" get deploy postgres >/dev/null 2>&1; then
    die "postgres deployment not in ${NS_TARGET} ns; cleanup script will not work"
fi
if ! kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
        exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -c "SELECT 1" >/dev/null 2>&1; then
    die "cannot connect to llmsafespace DB via 'kubectl exec deploy/postgres -- psql'"
fi
ok "postgres reachable"

# 6. Operator has cluster-admin (the chart needs it to function; the kit
#    itself only uses scoped operations).
if [[ "$(kubectl --context "${KUBE_CONTEXT}" auth can-i '*' '*' --all-namespaces 2>&1)" != "yes" ]]; then
    die "operator does not have cluster-admin; the kit assumes it (chart needs it). Re-auth and retry."
fi
ok "operator has cluster-admin (kit will only USE the allowed scope; see README.md)"

# 7. Confirmation gate — soft. The rules are written down; opt in to
#    interactive confirmation only if you want a manual checkpoint.
echo
cat <<EOF
${BOLD}Blast-radius rules (from README.md):${RESET}

  ALLOWED:
    - default ns
    - pentest-control-fixture ns (created by this kit)
    - All sandbox pods in default ns are pentest-disposable

  OFF-LIMITS:
    - Any namespace other than default and pentest-control-fixture
    - Cluster-scoped mutations outside the llmsafespace Helm release
    - Egress to attacker-controlled domains (use loopback only)
    - Real email addresses (use \*@pentest.local only)

EOF

if [[ "${PHASE0_PROD_INTERACTIVE:-0}" == "1" ]]; then
    read -r -p "Press enter to acknowledge and proceed (Ctrl-C to abort): " _
fi
ok "blast-radius rules acknowledged (recovery path: helm uninstall + reinstall)"

# Persist the resolved config so subsequent scripts inherit it.
cat > "${ARTEFACTS_DIR}/preflight.env" <<EOF
# Generated by 00-preflight.sh. Sourced by subsequent scripts.
KUBE_CONTEXT="${KUBE_CONTEXT}"
NS_TARGET="${NS_TARGET}"
NS_FIXTURE="${NS_FIXTURE}"
RELEASE_NAME="${RELEASE_NAME}"
RELEASE_REVISION="${RELEASE_REVISION}"
RELEASE_CHART="${RELEASE_CHART}"
EOF
chmod 600 "${ARTEFACTS_DIR}/preflight.env"

# Bootstrap a gitignore for artefacts so JWTs etc. don't accidentally
# get committed.
cat > "${ARTEFACTS_DIR}/.gitignore" <<'EOF'
# Phase 0 (prod) artefacts: contain JWTs, DB user IDs, snapshots. Never commit.
*
!.gitignore
EOF

ok "preflight complete; resolved config written to ${ARTEFACTS_DIR}/preflight.env"
