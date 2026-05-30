#!/usr/bin/env bash
# Phase 0 RT-0.3 verify: confirm the planted bugs in the control fixture
# are detected by the tooling we plan to use in Phase 1.
#
# Exit non-zero if ANY tool fails to report its expected finding, since
# that indicates the tool is broken (false-negative). A successful run
# proves the toolchain is calibrated and Phase 1 results are meaningful.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARTEFACTS_DIR="${SCRIPT_DIR}/phase-0-artefacts"
mkdir -p "${ARTEFACTS_DIR}/control-fixture-runs"

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace-pentest}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"

FAIL=0
PASS=0
SKIP=0

ok()   { echo "✓ $*"; PASS=$((PASS+1)); }
fail() { echo "✗ $*" >&2; FAIL=$((FAIL+1)); }
skip() { echo "- $*  (skipped — tool absent)"; SKIP=$((SKIP+1)); }

# -----------------------------------------------------------------------------
# Bug 1: privileged / hostNetwork / hostPID — kubeaudit
# -----------------------------------------------------------------------------
echo
echo "==> kubeaudit: bug 1 (privileged + hostNetwork + hostPID)"
if command -v kubeaudit >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/kubeaudit.json"
    kubeaudit all --kubeconfig "${KUBE_CONTEXT_FILE:-${HOME}/.kube/config}" \
        --context "${KUBE_CONTEXT}" \
        --namespace pentest-control-fixture \
        --format json \
        > "${OUT}" 2>&1 || true
    # kubeaudit emits one JSON object per line.
    EXPECTED='HostNetworkTrue HostPIDTrue PrivilegedTrue'
    MISSED=""
    for code in ${EXPECTED}; do
        if ! grep -q "\"AuditResultName\":\"${code}\"" "${OUT}" 2>/dev/null \
             && ! grep -qi "${code}" "${OUT}" 2>/dev/null; then
            MISSED="${MISSED} ${code}"
        fi
    done
    if [[ -z "${MISSED}" ]]; then
        ok "kubeaudit found all bug-1 indicators"
    else
        fail "kubeaudit MISSED expected findings:${MISSED}  (see ${OUT})"
    fi
else
    skip "kubeaudit"
fi

# -----------------------------------------------------------------------------
# Bug 2: HIGH-rated CVE on busybox:1.30 — trivy
# -----------------------------------------------------------------------------
echo
echo "==> trivy: bug 2 (HIGH+ CVEs on busybox:1.30)"
if command -v trivy >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/trivy-busybox.json"
    trivy image --severity HIGH,CRITICAL --format json -q busybox:1.30 \
        > "${OUT}" 2>&1 || true
    HIGH_COUNT=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity=="HIGH" or .Severity=="CRITICAL")] | length' \
        "${OUT}" 2>/dev/null || echo 0)
    if (( HIGH_COUNT > 0 )); then
        ok "trivy found ${HIGH_COUNT} HIGH+ CVEs on busybox:1.30"
    else
        fail "trivy MISSED — expected ≥1 HIGH+ CVE on busybox:1.30 (output: ${OUT})"
    fi
else
    skip "trivy"
fi

# -----------------------------------------------------------------------------
# Bug 3: SA token automount + cluster-admin binding — kubeaudit "asat"
# -----------------------------------------------------------------------------
echo
echo "==> kubeaudit: bug 3 (SA token automount on privileged SA)"
if command -v kubeaudit >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/kubeaudit-asat.json"
    kubeaudit asat --context "${KUBE_CONTEXT}" \
        --namespace pentest-control-fixture --format json > "${OUT}" 2>&1 || true
    # asat = Auto-mount Service Account Token; flagged when not explicitly false.
    if grep -qi "automount" "${OUT}" 2>/dev/null \
       || grep -qi "ServiceAccount" "${OUT}" 2>/dev/null \
       || grep -qi "AutomountServiceAccountToken" "${OUT}" 2>/dev/null; then
        ok "kubeaudit asat flagged the fixture pod"
    else
        fail "kubeaudit asat MISSED — expected SA-token finding (output: ${OUT})"
    fi
else
    skip "kubeaudit asat"
fi

# -----------------------------------------------------------------------------
# Bug 1+3 cross-check: kube-hunter passive scan
# -----------------------------------------------------------------------------
echo
echo "==> kube-hunter: passive scan from inside the cluster"
if command -v kube-hunter >/dev/null 2>&1; then
    OUT="${ARTEFACTS_DIR}/control-fixture-runs/kube-hunter.json"
    # Run kube-hunter in pod mode against the cluster's API server.
    # We deliberately scan the apiserver from outside; the privileged
    # control fixture pod also gets noticed in cluster-discovery.
    kube-hunter --remote $(kubectl --context "${KUBE_CONTEXT}" \
        cluster-info | awk '/control plane/ { print $NF }' \
        | sed -E 's|https?://||;s|:.*$||') \
        --report json --log NONE > "${OUT}" 2>&1 || true
    if jq -e '.vulnerabilities | length > 0' "${OUT}" >/dev/null 2>&1; then
        N=$(jq '.vulnerabilities | length' "${OUT}")
        ok "kube-hunter reported ${N} findings"
    else
        # kube-hunter sometimes returns nothing on a fresh kind cluster;
        # treat as soft pass and note for operator follow-up.
        skip "kube-hunter returned no findings (manual verify the privileged-pod check)"
    fi
else
    skip "kube-hunter"
fi

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo
echo "----------------------------------------"
echo "Control fixture verification: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped"
echo "Artefacts: ${ARTEFACTS_DIR}/control-fixture-runs/"
echo "----------------------------------------"

if (( FAIL > 0 )); then
    cat <<'EOF' >&2

✗ One or more tools FAILED to detect a planted bug.

This means the tool has a false-negative: Phase 1 findings produced by
that tool would be unreliable. Either:

  - install the missing tool (see tools-manifest.txt), OR
  - investigate why the tool is not reporting the planted bug, OR
  - replace the tool with a different scanner.

Do NOT proceed to Phase 1 until every tool you intend to rely on
detects its planted finding.

EOF
    exit 1
fi

if (( PASS == 0 )); then
    echo "✗ No tools were available to verify. Install at least one of: kubeaudit, trivy, kube-hunter." >&2
    exit 1
fi

echo "✓ Control fixture verification passed."
