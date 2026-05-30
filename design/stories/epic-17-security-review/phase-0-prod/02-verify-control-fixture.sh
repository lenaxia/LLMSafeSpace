#!/usr/bin/env bash
# Phase 0 (prod) — verify control fixture findings.
#
# Identical contract to phase-0/02-verify-control-fixture.sh (tools must
# detect the planted bugs); only difference is the kubectl context comes
# from preflight.env rather than CLUSTER_NAME inference.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-prod-artefacts"

if [[ ! -f "${ARTEFACTS_DIR}/preflight.env" ]]; then
    echo "ERROR: run ./00-preflight.sh first" >&2
    exit 1
fi
# shellcheck disable=SC1091
source "${ARTEFACTS_DIR}/preflight.env"

mkdir -p "${ARTEFACTS_DIR}/control-fixture-runs"

FAIL=0
PASS=0
SKIP=0
ok()   { echo "✓ $*"; PASS=$((PASS+1)); }
fail() { echo "✗ $*" >&2; FAIL=$((FAIL+1)); }
skip() { echo "- $*  (skipped — tool absent)"; SKIP=$((SKIP+1)); }

# --- kubeaudit: privileged + hostNetwork + hostPID -----------------------
echo
echo "==> kubeaudit: bug 1 (privileged + hostNetwork + hostPID)"
if command -v kubeaudit >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/kubeaudit.json"
    kubeaudit all --context "${KUBE_CONTEXT}" --namespace "${NS_FIXTURE}" \
        --format json > "${OUT}" 2>&1 || true
    EXPECTED='HostNetworkTrue HostPIDTrue PrivilegedTrue'
    MISSED=""
    for code in ${EXPECTED}; do
        if ! grep -qi "${code}" "${OUT}" 2>/dev/null; then
            MISSED="${MISSED} ${code}"
        fi
    done
    if [[ -z "${MISSED}" ]]; then
        ok "kubeaudit found bug-1 indicators"
    else
        fail "kubeaudit MISSED:${MISSED}  (see ${OUT})"
    fi
else
    skip "kubeaudit"
fi

# --- trivy: HIGH+ CVEs on busybox:1.30 -----------------------------------
echo
echo "==> trivy: bug 2 (HIGH+ CVEs on busybox:1.30)"
if command -v trivy >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/trivy-busybox.json"
    trivy image --severity HIGH,CRITICAL --format json -q busybox:1.30 \
        > "${OUT}" 2>&1 || true
    HIGH=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity=="HIGH" or .Severity=="CRITICAL")] | length' \
        "${OUT}" 2>/dev/null || echo 0)
    if (( HIGH > 0 )); then
        ok "trivy found ${HIGH} HIGH+ CVEs on busybox:1.30"
    else
        fail "trivy MISSED — expected ≥1 HIGH+ CVE (output: ${OUT})"
    fi
else
    skip "trivy"
fi

# --- kubeaudit asat: SA token automount ---------------------------------
echo
echo "==> kubeaudit asat: bug 3 (SA token automount on privileged SA)"
if command -v kubeaudit >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/kubeaudit-asat.json"
    kubeaudit asat --context "${KUBE_CONTEXT}" --namespace "${NS_FIXTURE}" \
        --format json > "${OUT}" 2>&1 || true
    if grep -qi "automount\|ServiceAccount\|AutomountServiceAccountToken" "${OUT}" 2>/dev/null; then
        ok "kubeaudit asat flagged the fixture pod"
    else
        fail "kubeaudit asat MISSED — expected SA-token finding (output: ${OUT})"
    fi
else
    skip "kubeaudit asat"
fi

# --- kube-hunter -----------------------------------------------------------
# In prod we do NOT run kube-hunter against the apiserver because cloud
# alerting may treat it as an attack. Operator-discretion only.
echo
echo "==> kube-hunter: skipped on prod cluster (would trigger cloud alerting)"
skip "kube-hunter (prod-target safety)"

echo
echo "----------------------------------------"
echo "Control fixture verification: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped"
echo "Artefacts: ${ARTEFACTS_DIR}/control-fixture-runs/"
echo "----------------------------------------"

if (( FAIL > 0 )); then
    cat <<'EOF' >&2

✗ One or more tools FAILED to detect a planted bug.

Phase 1 cannot proceed with broken tooling. Either fix the tool, install
a different scanner, or document the false-negative as a known
limitation in the worklog.
EOF
    exit 1
fi

if (( PASS == 0 )); then
    echo "✗ No tools were available to verify. Install at least kubeaudit + trivy." >&2
    exit 1
fi

echo "✓ Control fixture verification passed."
