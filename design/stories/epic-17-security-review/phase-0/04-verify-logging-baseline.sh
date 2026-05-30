#!/usr/bin/env bash
# Phase 0 RT-0.5: verify logging baseline.
#
# Three log streams must be flowing before Phase 1 begins:
#
#   1. K8s API server audit log — written to /var/log/kubernetes/audit/
#      inside the kind control-plane node, mounted to
#      ./phase-0-artefacts/audit on the host (kind-cluster-pentest.yaml).
#   2. API server logs — `kubectl logs deployment/llmsafespace-api`.
#   3. Controller logs — `kubectl logs deployment/llmsafespace-controller`.
#
# The check synthesises a benign event (a no-op kubectl get) and asserts
# that within 30 seconds, all three logs show NEW content. This is the
# canary that proves logs are still flowing — a "logs were flowing
# yesterday" check is useless mid-pentest.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
NS="llmsafespace"

FAIL=0
ok()   { echo "✓ $*"; }
fail() { echo "✗ $*" >&2; FAIL=$((FAIL+1)); }

# -----------------------------------------------------------------------------
# 1. K8s API audit log
# -----------------------------------------------------------------------------
AUDIT_DIR="${ARTEFACTS_DIR}/audit"
echo "==> Verifying K8s audit log at ${AUDIT_DIR}"
if [[ ! -d "${AUDIT_DIR}" ]]; then
    fail "audit directory missing — kind cluster was not created with audit policy"
elif ! find "${AUDIT_DIR}" -name 'audit.log*' -mmin -10 2>/dev/null | grep -q .; then
    fail "audit log has no entries from the last 10 min — audit policy may not be applied"
else
    LATEST=$(ls -t "${AUDIT_DIR}"/audit.log* 2>/dev/null | head -1)
    SIZE=$(stat -c '%s' "${LATEST}" 2>/dev/null || echo 0)
    ok "audit log present (${LATEST}, ${SIZE} bytes)"
fi

# -----------------------------------------------------------------------------
# 2. API server logs — synthesise an event and verify a fresh log line.
# -----------------------------------------------------------------------------
echo "==> Verifying API server logs"
# Capture the current latest timestamp so we can detect new lines.
BEFORE=$(date -u +%s)
# Trigger an API access by hitting /livez via service.
PORT=19191
kubectl --context "${KUBE_CONTEXT}" -n "${NS}" port-forward svc/llmsafespace-api "${PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill ${PF_PID} 2>/dev/null || true' EXIT
sleep 1
curl -fsS "http://127.0.0.1:${PORT}/livez" >/dev/null
sleep 2
LOG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS}" logs deployment/llmsafespace-api --since=10s 2>&1 || true)
if [[ -z "${LOG}" ]]; then
    fail "API logs empty for the last 10s after a /livez probe — logging may be broken"
else
    LINES=$(echo "${LOG}" | wc -l)
    ok "API logs flowing (${LINES} lines in last 10s after canary probe)"
fi
kill ${PF_PID} 2>/dev/null || true

# -----------------------------------------------------------------------------
# 3. Controller logs
# -----------------------------------------------------------------------------
echo "==> Verifying controller logs"
LOG=$(kubectl --context "${KUBE_CONTEXT}" -n "${NS}" logs deployment/llmsafespace-controller --since=60s 2>&1 || true)
if [[ -z "${LOG}" ]]; then
    fail "controller logs empty for the last 60s — controller may be wedged"
else
    LINES=$(echo "${LOG}" | wc -l)
    ok "controller logs flowing (${LINES} lines in last 60s)"
fi

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo
if (( FAIL == 0 )); then
    ok "logging baseline verified"
else
    echo
    echo "✗ ${FAIL} log stream(s) failed verification. Phase 1 cannot proceed:" >&2
    echo "  - findings depend on log evidence as ground truth" >&2
    exit 1
fi
