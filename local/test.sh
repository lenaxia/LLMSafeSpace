#!/usr/bin/env bash
# End-to-end smoke test for an LLMSafeSpace install on a kind cluster.
#
# Assumes ./bootstrap.sh has been run (or equivalent: cluster up, namespace
# llmsafespace exists, API + controller deployments are running).
#
# What it tests:
#   1. /livez and /readyz return expected codes
#   2. CRDs are installed (workspaces, sandboxes, sandboxprofiles, runtimeenvironments)
#   3. A Workspace can be created and reaches Phase=Active (PVC binds)
#   4. A Sandbox can be created and reaches Phase=Running (pod schedules,
#      opencode serve responds to /global/health on port 4096 inside the pod)
#   5. Sandbox suspend / resume work
#   6. Cleanup
#
# Each assertion is structured: log + run + assert. Failures print the
# relevant kubectl describe / events for debugging and exit non-zero.
set -Eeuo pipefail

# Pretty logging
if [[ -t 1 ]]; then
    BOLD=$'\033[1m'; RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; CYAN=$'\033[36m'; RESET=$'\033[0m'
else
    BOLD=''; RED=''; GREEN=''; YELLOW=''; CYAN=''; RESET=''
fi
log()  { printf '%s==>%s %s\n' "${CYAN}${BOLD}" "${RESET}" "$*"; }
ok()   { printf '%s ✓%s %s\n' "${GREEN}" "${RESET}" "$*"; }
warn() { printf '%s !%s %s\n' "${YELLOW}" "${RESET}" "$*" >&2; }
die()  { printf '%s ✗%s %s\n' "${RED}${BOLD}" "${RESET}" "$*" >&2; exit 1; }

CLUSTER_NAME="${CLUSTER_NAME:-llmsafespace}"
CTX="kind-${CLUSTER_NAME}"
NS="llmsafespace"
SANDBOX_NAME="e2e-sandbox"
WORKSPACE_NAME="e2e-workspace"
USER_ID="e2e-user"
PORTFWD_PORT="${PORTFWD_PORT:-18080}"

# Cleanup local processes on exit (port-forward, etc.)
cleanup() {
    if [[ -n "${PF_PID:-}" ]]; then
        kill "${PF_PID}" 2>/dev/null || true
        wait "${PF_PID}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

kc() { kubectl --context "${CTX}" "$@"; }

# -----------------------------------------------------------------------------
# Test 1: probes
# -----------------------------------------------------------------------------
log "Test 1/7 — API probes via port-forward"

kc -n "${NS}" port-forward svc/llmsafespace-api "${PORTFWD_PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!

# Wait up to 10s for port-forward to be live
for _ in $(seq 1 10); do
    if curl -sfm 1 "http://127.0.0.1:${PORTFWD_PORT}/livez" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

LIVE_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PORTFWD_PORT}/livez" || true)
[[ "${LIVE_CODE}" == "200" ]] || die "/livez returned ${LIVE_CODE}, expected 200"
ok "/livez returns 200"

READY_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PORTFWD_PORT}/readyz" || true)
[[ "${READY_CODE}" == "200" ]] || die "/readyz returned ${READY_CODE}, expected 200 (deps may be unhealthy)"
ok "/readyz returns 200 (Postgres + Redis reachable)"

# -----------------------------------------------------------------------------
# Test 2: CRDs installed
# -----------------------------------------------------------------------------
log "Test 2/7 — CRDs registered"
for crd in workspaces.llmsafespace.dev sandboxes.llmsafespace.dev \
           sandboxprofiles.llmsafespace.dev runtimeenvironments.llmsafespace.dev; do
    kc get crd "${crd}" >/dev/null \
        || die "CRD ${crd} not installed"
done
ok "all 4 CRDs installed"

# -----------------------------------------------------------------------------
# Test 3: RuntimeEnvironment available for python:3.11
# -----------------------------------------------------------------------------
# The sandbox controller maps Sandbox.spec.runtime → container image via
# cluster-scoped RuntimeEnvironment lookup. For the e2e we ensure a
# RuntimeEnvironment named "python-3.11" maps to the runtime-base image we
# loaded into kind. Idempotent: re-runs apply.
log "Test 3/7 — RuntimeEnvironment for python:3.11"
RUNTIME_IMAGE_REF="${RUNTIME_IMAGE_REF:-llmsafespace/runtime-base:dev}"
cat <<EOF | kc apply -f -
apiVersion: llmsafespace.dev/v1
kind: RuntimeEnvironment
metadata:
  name: python-3.11
spec:
  image: ${RUNTIME_IMAGE_REF}
  language: python
  version: "3.11"
EOF
ok "RuntimeEnvironment python-3.11 → ${RUNTIME_IMAGE_REF}"

# -----------------------------------------------------------------------------
# Test 3: Workspace creation reaches Active
# -----------------------------------------------------------------------------
log "Test 4/7 — Workspace lifecycle (create → Active)"

# Clean slate
kc -n "${NS}" delete workspace "${WORKSPACE_NAME}" --ignore-not-found >/dev/null 2>&1 || true

cat <<EOF | kc -n "${NS}" apply -f -
apiVersion: llmsafespace.dev/v1
kind: Workspace
metadata:
  name: ${WORKSPACE_NAME}
  labels:
    user-id: ${USER_ID}
spec:
  owner:
    userID: ${USER_ID}
  storage:
    size: 1Gi
    accessMode: ReadWriteOnce
  defaultRuntime: python:3.11
EOF
ok "Workspace ${WORKSPACE_NAME} created"

log "  waiting up to 90s for Workspace phase=Active"
for i in $(seq 1 30); do
    PHASE=$(kc -n "${NS}" get workspace "${WORKSPACE_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "${PHASE}" == "Active" ]]; then
        ok "Workspace reached phase=Active after ~$((i*3))s"
        break
    fi
    if (( i == 30 )); then
        warn "Workspace did not reach Active. Current phase=${PHASE:-<empty>}"
        kc -n "${NS}" describe workspace "${WORKSPACE_NAME}" || true
        kc -n "${NS}" get events --field-selector involvedObject.name="${WORKSPACE_NAME}" || true
        die "Workspace timeout"
    fi
    sleep 3
done

# Verify PVC was created
PVC_NAME=$(kc -n "${NS}" get workspace "${WORKSPACE_NAME}" -o jsonpath='{.status.pvcName}')
[[ -n "${PVC_NAME}" ]] || die "Workspace.status.pvcName is empty"
kc -n "${NS}" get pvc "${PVC_NAME}" >/dev/null \
    || die "PVC ${PVC_NAME} (referenced by workspace) does not exist"
ok "Workspace PVC ${PVC_NAME} bound"

# -----------------------------------------------------------------------------
# Test 4: Sandbox creation reaches Running, opencode serve responds
# -----------------------------------------------------------------------------
log "Test 5/7 — Sandbox lifecycle (create → Running → opencode responds)"

kc -n "${NS}" delete sandbox "${SANDBOX_NAME}" --ignore-not-found >/dev/null 2>&1 || true

cat <<EOF | kc -n "${NS}" apply -f -
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: ${SANDBOX_NAME}
  labels:
    user-id: ${USER_ID}
spec:
  runtime: python:3.11
  workspaceRef: ${WORKSPACE_NAME}
  securityLevel: standard
  resources:
    cpu: "500m"
    memory: "512Mi"
EOF
ok "Sandbox ${SANDBOX_NAME} created"

log "  waiting up to 180s for Sandbox phase=Running"
for i in $(seq 1 60); do
    PHASE=$(kc -n "${NS}" get sandbox "${SANDBOX_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "${PHASE}" == "Running" ]]; then
        ok "Sandbox reached phase=Running after ~$((i*3))s"
        break
    fi
    if (( i == 60 )); then
        warn "Sandbox did not reach Running. Current phase=${PHASE:-<empty>}"
        kc -n "${NS}" describe sandbox "${SANDBOX_NAME}" || true
        POD=$(kc -n "${NS}" get sandbox "${SANDBOX_NAME}" -o jsonpath='{.status.podName}' 2>/dev/null)
        if [[ -n "${POD}" ]]; then
            warn "Pod ${POD}:"
            kc -n "${NS}" describe pod "${POD}" || true
            kc -n "${NS}" logs "${POD}" --all-containers=true --tail=50 || true
        fi
        die "Sandbox timeout"
    fi
    sleep 3
done

POD=$(kc -n "${NS}" get sandbox "${SANDBOX_NAME}" -o jsonpath='{.status.podName}')
[[ -n "${POD}" ]] || die "Sandbox.status.podName is empty"
ok "Sandbox pod: ${POD}"

# Hit /global/health on the sandbox pod's opencode server (port 4096) via
# kubectl exec + curl — avoids needing a Service or another port-forward.
log "  verifying opencode serve responds inside the sandbox pod"
HEALTH=$(kc -n "${NS}" exec "${POD}" -c sandbox -- \
    curl -sfm 5 http://127.0.0.1:4096/global/health 2>&1 || true)
case "${HEALTH}" in
    *healthy*true*)
        ok "opencode /global/health: ${HEALTH}"
        ;;
    *)
        warn "opencode /global/health unexpected response: ${HEALTH}"
        kc -n "${NS}" logs "${POD}" -c sandbox --tail=30 || true
        die "opencode serve did not respond healthy"
        ;;
esac

# -----------------------------------------------------------------------------
# Test 5: Sandbox suspend / resume
# -----------------------------------------------------------------------------
log "Test 6/7 — Sandbox suspend / resume"

# Suspend by adding the suspend annotation (the way the API service does it)
kc -n "${NS}" annotate sandbox "${SANDBOX_NAME}" \
    llmsafespace.dev/suspend=true --overwrite

log "  waiting up to 60s for Sandbox phase=Suspended"
for i in $(seq 1 20); do
    PHASE=$(kc -n "${NS}" get sandbox "${SANDBOX_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "${PHASE}" == "Suspended" ]]; then
        ok "Sandbox suspended after ~$((i*3))s"
        break
    fi
    if (( i == 20 )); then
        warn "Sandbox did not suspend. Current phase=${PHASE}"
        kc -n "${NS}" describe sandbox "${SANDBOX_NAME}" || true
        die "Suspend timeout"
    fi
    sleep 3
done

# Resume
kc -n "${NS}" annotate sandbox "${SANDBOX_NAME}" \
    llmsafespace.dev/suspend- --overwrite >/dev/null 2>&1 || true

log "  waiting up to 90s for Sandbox phase=Running again"
for i in $(seq 1 30); do
    PHASE=$(kc -n "${NS}" get sandbox "${SANDBOX_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "${PHASE}" == "Running" ]]; then
        ok "Sandbox resumed to Running after ~$((i*3))s"
        break
    fi
    if (( i == 30 )); then
        warn "Sandbox did not resume. Current phase=${PHASE}"
        die "Resume timeout"
    fi
    sleep 3
done

# -----------------------------------------------------------------------------
# Test 6: cleanup
# -----------------------------------------------------------------------------
log "Test 7/7 — cleanup"
kc -n "${NS}" delete sandbox "${SANDBOX_NAME}" --wait=false >/dev/null
kc -n "${NS}" delete workspace "${WORKSPACE_NAME}" --wait=false >/dev/null
ok "delete requested"

cat <<EOF

${BOLD}${GREEN}All e2e tests passed.${RESET}

EOF
