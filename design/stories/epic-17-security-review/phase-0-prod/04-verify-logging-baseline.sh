#!/usr/bin/env bash
# Phase 0 (prod) — verify logging baseline.
#
# Unlike the kind kit, audit logs in prod are handled by the cloud's K8s
# offering (cloud audit logs / centralised aggregation). We can't peek
# at /var/log/kubernetes/audit. Instead we verify:
#
#   1. API server logs are flowing (kubectl logs, last 60s).
#   2. Controller logs are flowing.
#   3. We synthesise a benign /livez probe and confirm a fresh log line
#      appears within 30s.
#
# K8s audit log validation against the cloud dashboard is OPERATOR work:
# this script prints the kubectl impersonation command that produces a
# verifiable audit trail event, and the operator confirms it in the
# cloud dashboard.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

FAIL=0
ok()   { echo "✓ $*"; }
fail() { echo "✗ $*" >&2; FAIL=$((FAIL+1)); }

# 1. API server logs
echo "==> Verifying API server logs"
PORT=19191
kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    port-forward svc/llmsafespace-api "${PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill ${PF_PID} 2>/dev/null || true' EXIT
sleep 1
curl -fsS "http://127.0.0.1:${PORT}/livez" >/dev/null
sleep 2
LOG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    logs deployment/llmsafespace-api --since=10s 2>&1 || true)
if [[ -z "${LOG}" ]]; then
    fail "API logs empty for the last 10s after a /livez probe"
else
    LINES=$(echo "${LOG}" | wc -l)
    ok "API logs flowing (${LINES} lines in last 10s)"
fi
kill ${PF_PID} 2>/dev/null || true

# 2. Controller logs
echo "==> Verifying controller logs"
LOG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    logs deployment/llmsafespace-controller --since=60s 2>&1 || true)
if [[ -z "${LOG}" ]]; then
    fail "controller logs empty for the last 60s"
else
    LINES=$(echo "${LOG}" | wc -l)
    ok "controller logs flowing (${LINES} lines in last 60s)"
fi

# 3. Cloud audit log canary — print, don't verify.
echo
echo "==> K8s audit canary"
CANARY_NAME="phase0-canary-$(date -u +%s)"
echo "  Synthesising a benign canary event (a Get on a non-existent secret)"
kubectl --context "${KUBE_CONTEXT}" -n "${NS_TARGET}" \
    get secret "${CANARY_NAME}" 2>&1 | head -1 || true
cat <<EOF

  ⓘ K8s audit log verification is an OPERATOR step on this prod cluster.
  Open the cluster's audit log destination (cloud-provider dashboard)
  and search for: ${CANARY_NAME}

  An audit entry of this name within the last 60s confirms the audit
  pipeline is alive. If absent, the cluster's audit policy does NOT
  cover the verb 'get on secrets', and Phase 4 RT-4.x findings cannot
  rely on audit-log evidence.

EOF

echo "----------------------------------------"
if (( FAIL == 0 )); then
    ok "logging baseline verified (operator must independently confirm cloud audit canary)"
else
    echo "✗ ${FAIL} log stream(s) failed verification" >&2
    exit 1
fi
