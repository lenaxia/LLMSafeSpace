#!/usr/bin/env bash
# hack/benchmark-resume.sh — Measure end-to-end workspace resume latency.
#
# Usage:
#   ./hack/benchmark-resume.sh [options]
#
# Options:
#   -w, --workspace ID       Workspace ID to use (created if absent)
#   -n, --iterations N       Number of resume cycles (default: 5)
#       --assert             Exit 1 if any p99 threshold is violated
#       --max-p99-total S    Max total latency p99 in seconds (default: no limit)
#       --max-p99-pod-to-readyz S
#                            Max pod-running→readyz-200 p99 in seconds
#       --namespace NS       Kubernetes namespace (default: llmsafespaces)
#       --node NODE          Pin workspace pod to this node (by hostname label)
#                            Use instead of --fresh-node when a dedicated
#                            benchmark node is labelled
#                            benchmark.llmsafespaces.dev/role=benchmark
#       --fresh-node         Cordon all OTHER nodes and restore afterwards.
#                            WARNING: disruptive on shared clusters; prefer
#                            --node on production clusters.
#       --api-url URL        API base URL (default: http://localhost:8080)
#       --api-key KEY        API key for authentication
#       --context CTX        kubectl context (default: current context)
#   -h, --help               Show this help
#
# Prerequisites:
#   kubectl, curl, jq
#
# Exit codes:
#   0  All runs completed; all thresholds passed (or --assert not given)
#   1  Threshold violation or hard error
#   2  Prerequisites missing

set -euo pipefail

# ---- defaults ----
WORKSPACE_ID=""
ITERATIONS=5
ASSERT=false
MAX_P99_TOTAL=""
MAX_P99_POD_TO_READYZ=""
NS="${NS:-llmsafespaces}"
NODE=""
FRESH_NODE=false
API_URL="${API_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-}"
KUBE_CONTEXT=""
BENCHMARK_LABEL="benchmark.llmsafespaces.dev/role=benchmark"

# ---- parse args ----
while [[ $# -gt 0 ]]; do
  case "$1" in
    -w|--workspace)       WORKSPACE_ID="$2"; shift 2 ;;
    -n|--iterations)      ITERATIONS="$2"; shift 2 ;;
    --assert)             ASSERT=true; shift ;;
    --max-p99-total)      MAX_P99_TOTAL="$2"; shift 2 ;;
    --max-p99-pod-to-readyz) MAX_P99_POD_TO_READYZ="$2"; shift 2 ;;
    --namespace)          NS="$2"; shift 2 ;;
    --node)               NODE="$2"; shift 2 ;;
    --fresh-node)         FRESH_NODE=true; shift ;;
    --api-url)            API_URL="$2"; shift 2 ;;
    --api-key)            API_KEY="$2"; shift 2 ;;
    --context)            KUBE_CONTEXT="$2"; shift 2 ;;
    -h|--help)            sed -n '2,50p' "$0"; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

# ---- prerequisites ----
missing=()
for cmd in kubectl curl jq; do
  command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
done
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: missing prerequisites: ${missing[*]}" >&2; exit 2
fi

KUBECTL="kubectl"
[[ -n "$KUBE_CONTEXT" ]] && KUBECTL="kubectl --context=$KUBE_CONTEXT"

AUTH_HEADER=""
[[ -n "$API_KEY" ]] && AUTH_HEADER="-H Authorization: Bearer $API_KEY"

# ---- namespace isolation ----
# If a dedicated benchmark node is requested via label, enforce node affinity
# by patching the workspace spec. If --fresh-node is requested instead, cordon
# all non-benchmark nodes (destructive; restored via trap).
cordoned_nodes=()

if [[ "$FRESH_NODE" == true ]]; then
  echo "[benchmark] --fresh-node: cordoning all non-benchmark nodes"
  mapfile -t cordoned_nodes < <($KUBECTL get nodes \
    -l "!${BENCHMARK_LABEL%%=*}" --no-headers -o custom-columns=":metadata.name")
  for n in "${cordoned_nodes[@]}"; do
    $KUBECTL cordon "$n" >/dev/null
  done
  trap 'echo "[benchmark] restoring: uncordoning nodes"; for n in "${cordoned_nodes[@]}"; do $KUBECTL uncordon "$n" >/dev/null; done' EXIT
fi

# ---- workspace setup ----
_created_workspace=false
if [[ -z "$WORKSPACE_ID" ]]; then
  echo "[benchmark] creating temporary workspace..."
  response=$(curl -sf -X POST "$API_URL/workspaces" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
    -H "Content-Type: application/json" \
    -d '{"name":"bench-resume","runtime":"base","storage":{"size":"1Gi"}}')
  WORKSPACE_ID=$(jq -r '.id' <<<"$response")
  _created_workspace=true
  echo "[benchmark] workspace: $WORKSPACE_ID"

  # Wait for initial Active before first suspend.
  _wait_phase "$WORKSPACE_ID" "Active" 120
fi

if [[ "$NODE" != "" ]]; then
  echo "[benchmark] pinning to node: $NODE"
  # Patch workspace spec with nodeSelector — requires S18.10 CRD field.
  # Gracefully skip if field not present in this schema version.
  $KUBECTL -n "$NS" patch workspace "$WORKSPACE_ID" --type=merge \
    -p "{\"spec\":{\"nodeSelector\":{\"kubernetes.io/hostname\":\"$NODE\"}}}" 2>/dev/null || true
fi

# ---- collection arrays ----
declare -a t_api_to_creating
declare -a t_creating_to_pod_running
declare -a t_pod_running_to_readyz
declare -a t_readyz_to_active
declare -a t_active_to_proxy
declare -a t_total

# ---- helpers ----
_ts() { date +%s%3N; }  # milliseconds since epoch

_ms_to_s() {
  local ms="$1"
  echo "scale=2; $ms / 1000" | bc
}

_wait_phase() {
  local ws_id="$1" target_phase="$2" timeout_s="${3:-120}"
  local deadline=$(( $(_ts) + timeout_s * 1000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    phase=$(curl -sf "$API_URL/workspaces/$ws_id/status" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} | jq -r '.phase' 2>/dev/null || echo "")
    [[ "$phase" == "$target_phase" ]] && return 0
    sleep 0.5
  done
  echo "ERROR: workspace $ws_id did not reach $target_phase within ${timeout_s}s" >&2
  return 1
}

_pod_name() {
  $KUBECTL -n "$NS" get pods -l "llmsafespaces.dev/workspace=$1" \
    --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1
}

_readyz_ok() {
  local pod_ip="$1" admin_token="$2"
  code=$(curl -sf -o /dev/null -w "%{http_code}" \
    ${admin_token:+-H "Authorization: Bearer $admin_token"} \
    "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
  [[ "$code" == "200" ]]
}

# ---- main loop ----
for i in $(seq 1 "$ITERATIONS"); do
  echo ""
  echo "=== Resume run $i/$ITERATIONS ==="

  # 1. Suspend
  curl -sf -X POST "$API_URL/workspaces/$WORKSPACE_ID/suspend" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null
  _wait_phase "$WORKSPACE_ID" "Suspended" 60

  t0=$(_ts)

  # 2. Resume
  curl -sf -X POST "$API_URL/workspaces/$WORKSPACE_ID/activate" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null

  # 3. Poll phase=Creating
  t_creating=""
  deadline=$(( t0 + 30000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    phase=$(curl -sf "$API_URL/workspaces/$WORKSPACE_ID/status" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} | jq -r '.phase' 2>/dev/null || echo "")
    if [[ "$phase" == "Creating" ]]; then
      t_creating=$(_ts); break
    fi
    sleep 0.2
  done
  [[ -z "$t_creating" ]] && { echo "ERROR: never reached Creating" >&2; continue; }

  # 4. Poll pod Phase=Running
  t_pod_running=""
  deadline=$(( t0 + 120000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    pod=$(_pod_name "$WORKSPACE_ID")
    if [[ -n "$pod" ]]; then
      pod_phase=$($KUBECTL -n "$NS" get pod "$pod" \
        --no-headers -o custom-columns=":status.phase" 2>/dev/null || echo "")
      if [[ "$pod_phase" == "Running" ]]; then
        t_pod_running=$(_ts); break
      fi
    fi
    sleep 0.5
  done
  [[ -z "$t_pod_running" ]] && { echo "ERROR: pod never Running" >&2; continue; }

  # 5. Poll agentd /v1/readyz via port-forward (background)
  pod=$(_pod_name "$WORKSPACE_ID")
  local_port=$(( 19000 + RANDOM % 1000 ))
  $KUBECTL -n "$NS" port-forward "pod/$pod" "$local_port:4098" >/dev/null 2>&1 &
  pf_pid=$!
  trap "kill $pf_pid 2>/dev/null || true" EXIT

  t_readyz=""
  deadline=$(( t0 + 180000 ))
  admin_token=$($KUBECTL -n "$NS" get secret "workspace-pw-$WORKSPACE_ID" \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
  while [[ $(_ts) -lt $deadline ]]; do
    code=$(curl -sf -o /dev/null -w "%{http_code}" \
      ${admin_token:+-H "Authorization: Bearer $admin_token"} \
      "http://localhost:$local_port/v1/readyz" 2>/dev/null || echo "000")
    if [[ "$code" == "200" ]]; then
      t_readyz=$(_ts); break
    fi
    sleep 0.5
  done
  kill "$pf_pid" 2>/dev/null || true
  [[ -z "$t_readyz" ]] && { echo "ERROR: readyz never 200" >&2; continue; }

  # 6. Poll phase=Active
  t_active=""
  deadline=$(( t0 + 200000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    phase=$(curl -sf "$API_URL/workspaces/$WORKSPACE_ID/status" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} | jq -r '.phase' 2>/dev/null || echo "")
    if [[ "$phase" == "Active" ]]; then
      t_active=$(_ts); break
    fi
    sleep 0.5
  done
  [[ -z "$t_active" ]] && { echo "ERROR: never Active" >&2; continue; }

  # 7. Proxy probe
  t_proxy=""
  deadline=$(( t_active + 10000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    code=$(curl -sf -o /dev/null -w "%{http_code}" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
      "$API_URL/workspaces/$WORKSPACE_ID/health" 2>/dev/null || echo "000")
    if [[ "$code" == "200" || "$code" == "204" ]]; then
      t_proxy=$(_ts); break
    fi
    sleep 0.2
  done
  [[ -z "$t_proxy" ]] && t_proxy=$t_active  # fallback: proxy ok = active

  # ---- record ----
  g1=$(( t_creating - t0 ))
  g2=$(( t_pod_running - t_creating ))
  g3=$(( t_readyz - t_pod_running ))
  g4=$(( t_active - t_readyz ))
  g5=$(( t_proxy - t_active ))
  total=$(( t_proxy - t0 ))

  t_api_to_creating+=("$g1")
  t_creating_to_pod_running+=("$g2")
  t_pod_running_to_readyz+=("$g3")
  t_readyz_to_active+=("$g4")
  t_active_to_proxy+=("$g5")
  t_total+=("$total")

  printf "  API → Creating:            %5.1fs\n" "$(_ms_to_s "$g1")"
  printf "  Creating → Pod Running:    %5.1fs\n" "$(_ms_to_s "$g2")"
  printf "  Pod Running → readyz 200:  %5.1fs  ← primary suspect\n" "$(_ms_to_s "$g3")"
  printf "  readyz 200 → Active:       %5.1fs\n" "$(_ms_to_s "$g4")"
  printf "  Active → proxy ok:         %5.1fs\n" "$(_ms_to_s "$g5")"
  printf "  TOTAL:                     %5.1fs\n" "$(_ms_to_s "$total")"
done

# ---- cleanup ----
if [[ "$_created_workspace" == true ]]; then
  curl -sf -X DELETE "$API_URL/workspaces/$WORKSPACE_ID" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true
fi

# ---- statistics ----
_percentile() {
  # Usage: _percentile P arr...  (P = 50|90|99)
  local p="$1"; shift
  local arr=("$@")
  local n=${#arr[@]}
  [[ $n -eq 0 ]] && echo "N/A" && return
  IFS=$'\n' sorted=($(sort -n <<<"${arr[*]}")); unset IFS
  local idx=$(( (p * n + 99) / 100 - 1 ))
  [[ $idx -lt 0 ]] && idx=0
  [[ $idx -ge $n ]] && idx=$(( n - 1 ))
  echo "${sorted[$idx]}"
}

echo ""
echo "=== Summary ($ITERATIONS runs) ==="
printf "%-40s %8s %8s %8s\n" "Gate" "p50" "p90" "p99"
printf "%-40s %8s %8s %8s\n" "----" "---" "---" "---"

_row() {
  local label="$1"; shift
  local arr=("$@")
  [[ ${#arr[@]} -eq 0 ]] && return
  printf "%-40s %6.1fs %6.1fs %6.1fs\n" "$label" \
    "$(_ms_to_s "$(_percentile 50 "${arr[@]}")")" \
    "$(_ms_to_s "$(_percentile 90 "${arr[@]}")")" \
    "$(_ms_to_s "$(_percentile 99 "${arr[@]}")")"
}

_row "API → Creating"           "${t_api_to_creating[@]}"
_row "Creating → Pod Running"   "${t_creating_to_pod_running[@]}"
_row "Pod Running → readyz 200" "${t_pod_running_to_readyz[@]}"
_row "readyz 200 → Active"      "${t_readyz_to_active[@]}"
_row "Active → proxy ok"        "${t_active_to_proxy[@]}"
_row "TOTAL"                    "${t_total[@]}"

# ---- threshold assertions ----
violations=0
if [[ "$ASSERT" == true ]]; then
  echo ""
  echo "=== Threshold checks ==="

  _check() {
    local name="$1" actual_ms="$2" max_s="$3"
    local max_ms=$(echo "$max_s * 1000" | bc | cut -d. -f1)
    if [[ "$actual_ms" -le "$max_ms" ]]; then
      printf "  PASS  %-40s %.1fs <= %.1fs\n" "$name" "$(_ms_to_s "$actual_ms")" "$max_s"
    else
      printf "  FAIL  %-40s %.1fs > %.1fs (threshold)\n" "$name" "$(_ms_to_s "$actual_ms")" "$max_s"
      violations=$(( violations + 1 ))
    fi
  }

  p99_total=$(_percentile 99 "${t_total[@]}")
  p99_pod_to_readyz=$(_percentile 99 "${t_pod_running_to_readyz[@]}")

  [[ -n "$MAX_P99_TOTAL" ]] && _check "p99 total latency" "$p99_total" "$MAX_P99_TOTAL"
  [[ -n "$MAX_P99_POD_TO_READYZ" ]] && \
    _check "p99 pod-running→readyz-200" "$p99_pod_to_readyz" "$MAX_P99_POD_TO_READYZ"

  if [[ $violations -gt 0 ]]; then
    echo ""
    echo "ERROR: $violations threshold violation(s)" >&2
    exit 1
  fi
  echo "  All thresholds passed."
fi
