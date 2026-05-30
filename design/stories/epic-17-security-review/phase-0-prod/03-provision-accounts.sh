#!/usr/bin/env bash
# Phase 0 (prod) — provision three test accounts via the deployed API.
#
# Each account uses an `@pentest.local` email so cleanup can DELETE
# WHERE email LIKE '%@pentest.local' without risk to real accounts.
#
# In addition to the JWT, this script records the user_id from the
# users table — Phase 2 (RT-2.7 first-user-admin race) and Phase 4
# (RT-4.1 IDOR) need user IDs as test inputs.
#
# Output: phase-0-prod-artefacts/accounts.json (mode 0600, gitignored).
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

LOCAL_PORT="${LOCAL_PORT:-19090}"
API_BASE="http://127.0.0.1:${LOCAL_PORT}"

echo "==> Port-forwarding API service"
kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    port-forward svc/llmsafespace-api "${LOCAL_PORT}:8080" \
    >/dev/null 2>&1 &
PF_PID=$!
trap 'kill ${PF_PID} 2>/dev/null || true' EXIT

for _ in {1..30}; do
    curl -fsS "${API_BASE}/livez" >/dev/null 2>&1 && break
    sleep 0.5
done
if ! curl -fsS "${API_BASE}/livez" >/dev/null 2>&1; then
    echo "ERROR: API server not reachable at ${API_BASE}" >&2
    exit 1
fi
echo "✓ API reachable at ${API_BASE}"

# Note: registration may be admin-gated by the deployed instance settings.
# We attempt register first; fall back to login if user already exists.
register_or_login() {
    local user="$1" email="$2" pw="$3"
    local resp
    resp=$(curl -fsS -X POST "${API_BASE}/api/v1/auth/register" \
        -H 'Content-Type: application/json' \
        -d "$(jq -n --arg u "$user" --arg e "$email" --arg p "$pw" \
            '{username:$u,email:$e,password:$p}')" 2>/dev/null \
        || true)
    if echo "${resp}" | jq -e '.token' >/dev/null 2>&1; then
        echo "${resp}" | jq -r '.token'
        return 0
    fi
    resp=$(curl -fsS -X POST "${API_BASE}/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "$(jq -n --arg e "$email" --arg p "$pw" \
            '{email:$e,password:$p}')")
    echo "${resp}" | jq -r '.token'
}

# Stable but unguessable password — derived from the cluster context name.
PW="${PENTEST_PASSWORD:-pentest-$(echo "${KUBE_CONTEXT}" | sha256sum | cut -c1-32)}"

provision() {
    local user="$1" email="$2"
    local token
    # progress messages MUST go to stderr, not stdout — stdout is captured
    # by the caller as the JWT.
    echo "==> Provisioning ${user}" >&2
    token=$(register_or_login "$user" "$email" "$PW")
    if [[ -z "${token}" || "${token}" == "null" ]]; then
        echo "ERROR: empty JWT for ${user}" >&2
        exit 1
    fi
    echo "${token}"
}

ADMIN_TOKEN=$(provision "admin" "admin@pentest.local")
REG_A_TOKEN=$(provision "regular-a" "regular-a@pentest.local")
REG_B_TOKEN=$(provision "regular-b" "regular-b@pentest.local")

# Resolve user IDs from Postgres for downstream phases.
fetch_user_id() {
    local email="$1"
    kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
        exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -t -A \
        -c "SELECT id FROM users WHERE email = '${email}'" 2>/dev/null | tr -d '[:space:]'
}
ADMIN_ID=$(fetch_user_id "admin@pentest.local")
A_ID=$(fetch_user_id "regular-a@pentest.local")
B_ID=$(fetch_user_id "regular-b@pentest.local")

ADMIN_ROLE=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    exec deploy/postgres -- psql -U llmsafespace -d llmsafespace -t -A \
    -c "SELECT role FROM users WHERE email = 'admin@pentest.local'" 2>/dev/null | tr -d '[:space:]')

if [[ "${ADMIN_ROLE}" != "admin" ]]; then
    echo "ⓘ admin user has role='${ADMIN_ROLE}' (DB already had users; G8 first-user-admin auto-promote does NOT fire)"
    echo "  Phase 2 RT-2.7 first-user-admin race must be tested against a clean DB instead."
fi

OUT="${ARTEFACTS_DIR}/accounts.json"
jq -n \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg base "${API_BASE}" \
    --arg pw "${PW}" \
    --arg admin_t "${ADMIN_TOKEN}" --arg admin_id "${ADMIN_ID}" --arg admin_role "${ADMIN_ROLE}" \
    --arg a_t "${REG_A_TOKEN}" --arg a_id "${A_ID}" \
    --arg b_t "${REG_B_TOKEN}" --arg b_id "${B_ID}" \
    '{
        recorded_at: $ts,
        api_base: $base,
        password: $pw,
        accounts: {
            admin:       { email: "admin@pentest.local",       username: "admin",     user_id: $admin_id, role: $admin_role, token: $admin_t },
            "regular-a": { email: "regular-a@pentest.local",   username: "regular-a", user_id: $a_id,     role: "user",       token: $a_t },
            "regular-b": { email: "regular-b@pentest.local",   username: "regular-b", user_id: $b_id,     role: "user",       token: $b_t }
        }
    }' > "${OUT}"

chmod 600 "${OUT}"
echo "✓ accounts written to ${OUT} (mode 0600, gitignored)"
jq -r '.accounts | to_entries[] | "  \(.key): id=\(.value.user_id) role=\(.value.role) token=\(.value.token[:24])..."' "${OUT}"
