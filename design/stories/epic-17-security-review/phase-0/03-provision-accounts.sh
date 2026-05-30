#!/usr/bin/env bash
# Phase 0 RT-0.4: provision three test accounts and record JWTs.
#
# Accounts:
#   - admin:     first user → auto-promoted to admin role (G8 — also a
#                pentest target, so we want the admin to actually be an
#                admin).
#   - regular-a: ordinary user. Owns a baseline workspace.
#   - regular-b: the attacker persona. Used by Phase 2/3/7 IDOR / cross-
#                tenant tests.
#
# JWTs are stored in phase-0-artefacts/accounts.json (gitignored).
# DO NOT commit this file. The .gitignore in the artefacts directory
# enforces this.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"
mkdir -p "${ARTEFACTS_DIR}"

# Make sure the artefacts dir is gitignored. We write a fresh .gitignore
# every run because the dir is intentionally throwaway state.
cat > "${ARTEFACTS_DIR}/.gitignore" <<'EOF'
# Phase 0 artefacts contain JWTs and cluster snapshots — never commit.
*
!.gitignore
EOF

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
NS="llmsafespace"

# We talk to the API via port-forward to avoid depending on the kind
# extraPortMapping. The fixed local port 19090 is high enough not to
# collide with anything common.
LOCAL_PORT=19090
API_BASE="http://127.0.0.1:${LOCAL_PORT}"

echo "==> Port-forwarding API service"
kubectl --context "${KUBE_CONTEXT}" -n "${NS}" \
    port-forward svc/llmsafespace-api "${LOCAL_PORT}:8080" \
    >/dev/null 2>&1 &
PF_PID=$!
trap 'kill ${PF_PID} 2>/dev/null || true' EXIT

# Wait for the port-forward to be alive.
for i in {1..30}; do
    if curl -fsS "${API_BASE}/livez" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -fsS "${API_BASE}/livez" >/dev/null 2>&1; then
    echo "ERROR: API server not reachable at ${API_BASE}" >&2
    exit 1
fi
echo "✓ API reachable at ${API_BASE}"

# register_or_login <username> <email> <password>
# Tries POST /auth/register first; if 409 (user exists), falls back to
# POST /auth/login. Returns the JWT on stdout.
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
    # Registration failed — try login.
    resp=$(curl -fsS -X POST "${API_BASE}/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "$(jq -n --arg e "$email" --arg p "$pw" \
            '{email:$e,password:$p}')")
    echo "${resp}" | jq -r '.token'
}

# Use a stable but unguessable password so the artefacts file alone is
# the secret, not "password123".
PW="${PENTEST_PASSWORD:-pentest-$(echo "${KUBE_CONTEXT}" | sha256sum | cut -c1-32)}"

echo "==> Provisioning admin"
ADMIN_TOKEN=$(register_or_login "admin" "admin@pentest.local" "${PW}")

echo "==> Provisioning regular-a"
REG_A_TOKEN=$(register_or_login "regular-a" "regular-a@pentest.local" "${PW}")

echo "==> Provisioning regular-b (attacker persona)"
REG_B_TOKEN=$(register_or_login "regular-b" "regular-b@pentest.local" "${PW}")

# Verify admin role on the first account (G8 first-user-admin auto-promotion).
ADMIN_ME=$(curl -fsS "${API_BASE}/api/v1/auth/me" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}")
ADMIN_ROLE=$(echo "${ADMIN_ME}" | jq -r '.role // empty')
if [[ "${ADMIN_ROLE}" != "admin" ]]; then
    echo "WARNING: admin user has role='${ADMIN_ROLE}', not 'admin'." >&2
    echo "  This is fine if there were already users in the DB before this script ran." >&2
    echo "  Phase 2 RT-2.7 (first-user-admin race) will need a clean DB to reproduce." >&2
fi

OUT="${ARTEFACTS_DIR}/accounts.json"
jq -n \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg base "${API_BASE}" \
    --arg pw "${PW}" \
    --arg admin_t "${ADMIN_TOKEN}" --arg admin_role "${ADMIN_ROLE}" \
    --arg a_t "${REG_A_TOKEN}" --arg b_t "${REG_B_TOKEN}" \
    '{
        recorded_at: $ts,
        api_base: $base,
        password: $pw,
        accounts: {
            admin:     { email: "admin@pentest.local",     username: "admin",     role: $admin_role, token: $admin_t },
            "regular-a": { email: "regular-a@pentest.local", username: "regular-a", role: "user",  token: $a_t },
            "regular-b": { email: "regular-b@pentest.local", username: "regular-b", role: "user",  token: $b_t }
        }
    }' > "${OUT}"

chmod 600 "${OUT}"
echo "✓ accounts written to ${OUT} (mode 0600, gitignored)"
jq '.accounts | to_entries[] | "\(.key): role=\(.value.role) token=\(.value.token[:24])..."' "${OUT}" -r
