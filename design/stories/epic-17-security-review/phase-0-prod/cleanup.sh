#!/usr/bin/env bash
# Phase 0 (prod) — cleanup. Removes ONLY the artefacts this kit created.
#
# Cleanup steps, in order:
#
#   1. Delete K8s Workspace CRDs labelled with our test users' user-ids.
#      The controller's finalizer cleans up sandbox pods + PVCs.
#   2. Wait for the controller to finalize them (up to 90s).
#   3. DELETE FROM users WHERE email LIKE '%@pentest.local'. CASCADE FK
#      removes api_keys, sandboxes, permissions, user_keys, user_secrets,
#      user_settings rows.
#   4. Delete the pentest-control-fixture namespace (cascades to pod, SA,
#      ClusterRoleBinding).
#   5. Remove local phase-0-prod-artefacts/ directory (gitignored, but
#      contains JWTs and DB user IDs — best to wipe explicitly).
#
# Idempotent: safe to re-run on partial state.
#
# This script does NOT touch:
#   - Any namespace other than default and pentest-control-fixture.
#   - Any LLMSafeSpace-owned resource not tied to the test accounts.
#   - The Helm release.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ -t 1 ]]; then
    BOLD=$'\033[1m'; YELLOW=$'\033[33m'; GREEN=$'\033[32m'; RESET=$'\033[0m'
else
    BOLD=''; YELLOW=''; GREEN=''; RESET=''
fi
log()  { printf '%s==>%s %s\n' "${BOLD}" "${RESET}" "$*"; }
ok()   { printf '%s ✓%s %s\n' "${GREEN}" "${RESET}" "$*"; }
warn() { printf '%s !%s %s\n' "${YELLOW}" "${RESET}" "$*" >&2; }

# Cleanup is non-interactive by default. Mistakes are recoverable: if
# state ends up wedged, `helm uninstall llmsafespace && helm install ...`
# restores the namespace from chart. Set PHASE0_PROD_CLEANUP_INTERACTIVE=1
# if you want a yes/no gate.
if [[ "${PHASE0_PROD_CLEANUP_INTERACTIVE:-0}" == "1" ]]; then
    echo
    cat <<'EOF'
This will:
  1. Delete every Workspace CRD whose user-id label matches a test user.
  2. Wait for the controller to clean up associated sandbox pods + PVCs.
  3. DELETE rows from the users table WHERE email LIKE '%@pentest.local'.
     (CASCADE removes api_keys, sandboxes, permissions, user_keys,
      user_secrets, user_settings rows.)
  4. Delete the pentest-control-fixture namespace.
  5. Wipe the local phase-0-prod-artefacts/ directory.

EOF
    read -r -p "Type 'cleanup' to proceed: " ANSWER
    if [[ "${ANSWER}" != "cleanup" ]]; then
        echo "aborted"; exit 1
    fi
fi

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    warn "preflight.env missing — cluster context unknown; using current kubectl context"
    KUBE_CONTEXT="$(kubectl config current-context)"
    NS_TARGET="default"
    NS_FIXTURE="pentest-control-fixture"
else
    # shellcheck disable=SC1091
    source "${ARTEFACTS_DIR}/preflight.env"
fi

# 1+2. Delete workspaces owned by test users via DB lookup of user IDs.
log "1) Deleting Workspace CRDs owned by test accounts"
USER_IDS=()
if [[ -s "${ARTEFACTS_DIR}/accounts.json" ]]; then
    while IFS= read -r id; do
        [[ -n "${id}" && "${id}" != "null" ]] && USER_IDS+=("${id}")
    done < <(jq -r '.accounts[].user_id' "${ARTEFACTS_DIR}/accounts.json")
fi
# Also resolve directly from the DB in case accounts.json was wiped.
DB_IDS=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -t -A \
    -c "SELECT id FROM users WHERE email LIKE '%@pentest.local'" 2>/dev/null \
    | tr '\n' ' ' || true)
for id in ${DB_IDS}; do
    USER_IDS+=("${id}")
done

if [[ ${#USER_IDS[@]} -eq 0 ]]; then
    ok "no test-user IDs found — nothing to delete"
else
    # dedupe
    UNIQUE_IDS=$(printf "%s\n" "${USER_IDS[@]}" | sort -u)
    for uid in ${UNIQUE_IDS}; do
        DELETED=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
            delete workspace -l "user-id=${uid}" --ignore-not-found=true \
            -o name 2>&1 | wc -l || true)
        ok "user ${uid}: ${DELETED} workspaces deleted"
    done

    log "2) Waiting up to 90s for controller to finalize sandbox pods"
    for _ in {1..18}; do
        REMAINING=0
        for uid in ${UNIQUE_IDS}; do
            COUNT=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
                get workspace -l "user-id=${uid}" --no-headers 2>/dev/null | wc -l)
            REMAINING=$((REMAINING + COUNT))
        done
        [[ ${REMAINING} -eq 0 ]] && break
        sleep 5
    done
    if (( REMAINING > 0 )); then
        warn "${REMAINING} workspace(s) still finalizing; check kubectl get workspace manually"
    else
        ok "all test-user workspaces finalized"
    fi
fi

# 3. Delete user rows. CASCADE handles api_keys, sandboxes,
# permissions, user_keys, user_secrets, user_settings.
log "3) Deleting test-user rows from DB"
DELETED_ROWS=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -t -A \
    -c "DELETE FROM users WHERE email LIKE '%@pentest.local'; SELECT count(*) FROM users WHERE email LIKE '%@pentest.local'" \
    2>/dev/null | tail -1 | tr -d '[:space:]')
if [[ "${DELETED_ROWS}" == "0" ]]; then
    ok "test-user rows removed (0 remain)"
else
    warn "DB delete returned '${DELETED_ROWS}' — manual inspection recommended"
fi

# 4. Delete the pentest-control-fixture namespace. The fixture's
# ClusterRoleBinding is cluster-scoped; we delete it explicitly because
# `kubectl delete ns` does NOT remove cluster-scoped objects.
log "4) Deleting ${NS_FIXTURE} namespace"
kubectl --context "${KUBE_CONTEXT}" delete clusterrolebinding control-fixture-cluster-admin \
    --ignore-not-found=true >/dev/null 2>&1 || true
kubectl --context "${KUBE_CONTEXT}" delete namespace "${NS_FIXTURE}" \
    --ignore-not-found=true --wait=false >/dev/null 2>&1 || true

# Wait briefly for the namespace to be gone (or accept it's still
# Terminating and let the operator follow up).
for _ in {1..12}; do
    if ! kubectl --context "${KUBE_CONTEXT}" get ns "${NS_FIXTURE}" >/dev/null 2>&1; then
        ok "${NS_FIXTURE} ns removed"
        break
    fi
    sleep 5
done
if kubectl --context "${KUBE_CONTEXT}" get ns "${NS_FIXTURE}" >/dev/null 2>&1; then
    warn "${NS_FIXTURE} still terminating after 60s — finalizer stuck"
    warn "  recovery: helm uninstall llmsafespace && reinstall, or"
    warn "            kubectl get ns ${NS_FIXTURE} -o json | jq '.spec.finalizers=[]' | kubectl replace --raw /api/v1/namespaces/${NS_FIXTURE}/finalize -f -"
fi

# 5. Wipe local artefacts.
log "5) Removing local artefacts"
rm -rf "${ARTEFACTS_DIR}"
ok "${ARTEFACTS_DIR} removed"

echo
ok "cleanup complete"
