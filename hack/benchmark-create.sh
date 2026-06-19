#!/usr/bin/env bash
# hack/benchmark-create.sh — Measure end-to-end workspace first-create latency.
#
# Usage:
#   ./hack/benchmark-create.sh [options]
#
# Options:
#   -n, --iterations N       Number of create/delete cycles (default: 5)
#       --runtime R          Runtime to use (default: base)
#       --packages "a b c"   Space-separated package names to install
#                            (activates workspace-setup init container)
#       --init-script FILE   Path to shell script to run as initScript
#       --assert             Exit 1 if any p99 threshold is violated
#       --max-p99-total S    Max total latency p99 in seconds (default: no limit)
#       --max-p99-pod-to-readyz S
#                            Max pod-running→readyz-200 p99 in seconds
#       --max-p99-init S     Max init container p99 in seconds (requires packages
#                            or init-script; skipped otherwise)
#       --namespace NS       Kubernetes namespace (default: llmsafespaces)
#       --node NODE          Pin workspace to this node (by hostname label)
#       --fresh-node         Cordon all OTHER nodes (destructive; prefer --node).
#                            Only safe on a dedicated benchmark cluster.
#       --api-url URL        API base URL (default: http://localhost:8080)
#       --api-key KEY        API key for authentication
#       --context CTX        kubectl context (default: current context)
#   -h, --help               Show this help
#
# Exit codes:
#   0  All runs completed; all thresholds passed (or --assert not given)
#   1  Threshold violation or hard error
#   2  Prerequisites missing

set -euo pipefail

# ---- defaults ----
ITERATIONS=5
RUNTIME="base"
PACKAGES=""
INIT_SCRIPT_FILE=""
ASSERT=false
MAX_P99_TOTAL=""
MAX_P99_POD_TO_READYZ=""
MAX_P99_INIT=""
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
    -n|--iterations)      ITERATIONS="$2"; shift 2 ;;
    --runtime)            RUNTIME="$2"; shift 2 ;;
    --packages)           PACKAGES="$2"; shift 2 ;;
    --init-script)        INIT_SCRIPT_FILE="$2"; shift 2 ;;
    --assert)             ASSERT=true; shift ;;
    --max-p99-total)      MAX_P99_TOTAL="$2"; shift 2 ;;
    --max-p99-pod-to-readyz) MAX_P99_POD_TO_READYZ="$2"; shift 2 ;;
    --max-p99-init)       MAX_P99_INIT="$2"; shift 2 ;;
    --namespace)          NS="$2"; shift 2 ;;
    --node)               NODE="$2"; shift 2 ;;
    --fresh-node)         FRESH_NODE=true; shift ;;
    --api-url)            API_URL="$2"; shift 2 ;;
    --api-key)            API_KEY="$2"; shift 2 ;;
    --context)            KUBE_CONTEXT="$2"; shift 2 ;;
    -h|--help)            sed -n '2,60p' "$0"; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

# ---- prerequisites ----
missing=()
for cmd in kubectl curl jq bc; do
  command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
done
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: missing prerequisites: ${missing[*]}" >&2; exit 2
fi

KUBECTL="kubectl"
[[ -n "$KUBE_CONTEXT" ]] && KUBECTL="kubectl --context=$KUBE_CONTEXT"

# ---- node isolation ----
cordoned_nodes=()
if [[ "$FRESH_NODE" == true ]]; then
  echo "[benchmark] --fresh-node: cordoning all non-benchmark nodes"
  mapfile -t cordoned_nodes < <($KUBECTL get nodes \
    -l "!${BENCHMARK_LABEL%%=*}" --no-headers -o custom-columns=":metadata.name")
  for n in "${cordoned_nodes[@]}"; do
    $KUBECTL cordon "$n" >/dev/null
  done
  trap 'echo "[benchmark] restoring nodes"; for n in "${cordoned_nodes[@]}"; do $KUBECTL uncordon "$n" >/dev/null; done' EXIT
fi

# ---- build workspace spec ----
_build_spec() {
  local spec="{\"name\":\"bench-create-$RANDOM\",\"runtime\":\"$RUNTIME\",\"storage\":{\"size\":\"1Gi\"}}"

  if [[ -n "$PACKAGES" ]]; then
    local pkgs_json
    pkgs_json=$(printf '"%s",' $PACKAGES | sed 's/,$//') 
    spec=$(jq --argjson reqs "[$pkgs_json]" \
      '.packages=[{"runtime":"python3","requirements":$reqs}]' <<<"$spec")
  fi

  if [[ -n "$INIT_SCRIPT_FILE" ]]; then
    local script_content
    script_content=$(cat "$INIT_SCRIPT_FILE")
    spec=$(jq --arg s "$script_content" '.initScript=$s' <<<"$spec")
  fi

  if [[ -n "$NODE" ]]; then
    spec=$(jq --arg n "$NODE" '.nodeSelector={"kubernetes.io/hostname":$n}' <<<"$spec")
  fi

  echo "$spec"
}

# ---- helpers ----
_ts() { date +%s%3N; }

_ms_to_s() {
  local ms="$1"
  printf "%.1f" "$(echo "scale=2; $ms / 1000" | bc)"
}

_wait_phase() {
  local ws_id="$1" target="$2" timeout_s="${3:-180}"
  local deadline=$(( $(_ts) + timeout_s * 1000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    phase=$(curl -sf "$API_URL/workspaces/$ws_id/status" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} | jq -r '.phase' 2>/dev/null || echo "")
    [[ "$phase" == "$target" ]] && return 0
    sleep 0.5
  done
  echo "ERROR: workspace $ws_id did not reach $target within ${timeout_s}s" >&2
  return 1
}

_pod_name() {
  $KUBECTL -n "$NS" get pods -l "llmsafespaces.dev/workspace=$1" \
    --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1
}

_init_container_duration_ms() {
  local pod="$1"
  local started finished
  started=$($KUBECTL -n "$NS" get pod "$pod" \
    -o jsonpath='{.status.initContainerStatuses[?(@.name=="workspace-setup")].state.terminated.startedAt}' \
    2>/dev/null || echo "")
  finished=$($KUBECTL -n "$NS" get pod "$pod" \
    -o jsonpath='{.status.initContainerStatuses[?(@.name=="workspace-setup")].state.terminated.finishedAt}' \
    2>/dev/null || echo "")
  if [[ -z "$started" || -z "$finished" ]]; then
    echo "0"
    return
  fi
  local s_ts f_ts
  s_ts=$(date -d "$started" +%s%3N 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$started" "+%s" 2>/dev/null || echo "0")
  f_ts=$(date -d "$finished" +%s%3N 2>/dev/null || date -j -f "%Y-%m-%dT%H:%M:%SZ" "$finished" "+%s" 2>/dev/null || echo "0")
  echo $(( f_ts - s_ts ))
}

# ---- collection arrays ----
declare -a t_api_to_pvc_bound
declare -a t_pvc_to_pod_created
declare -a t_init_containers
declare -a t_pod_running_to_readyz
declare -a t_readyz_to_active
declare -a t_total
has_init=$(( [[ -n "$PACKAGES" || -n "$INIT_SCRIPT_FILE" ]] && echo 1 || echo 0 ) || true)
has_init=0
[[ -n "$PACKAGES" || -n "$INIT_SCRIPT_FILE" ]] && has_init=1

# ---- main loop ----
for i in $(seq 1 "$ITERATIONS"); do
  echo ""
  echo "=== Create run $i/$ITERATIONS (runtime=$RUNTIME, packages=$([ -n "$PACKAGES" ] && echo 'yes' || echo 'no')) ==="

  spec=$(_build_spec)
  t0=$(_ts)

  response=$(curl -sf -X POST "$API_URL/workspaces" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} \
    -H "Content-Type: application/json" \
    -d "$spec")
  ws_id=$(jq -r '.id' <<<"$response")
  echo "  workspace: $ws_id"

  # 1. Wait for PVC Bound (Pending → Creating transition)
  t_creating=""
  deadline=$(( t0 + 60000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    phase=$(curl -sf "$API_URL/workspaces/$ws_id/status" \
      ${API_KEY:+-H "Authorization: Bearer $API_KEY"} | jq -r '.phase' 2>/dev/null || echo "")
    if [[ "$phase" == "Creating" ]]; then
      t_creating=$(_ts); break
    fi
    sleep 0.5
  done
  [[ -z "$t_creating" ]] && { echo "ERROR: never reached Creating" >&2
    curl -sf -X DELETE "$API_URL/workspaces/$ws_id" ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true
    continue; }

  # 2. Wait for pod to appear
  t_pod_created=""
  deadline=$(( t0 + 120000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    pod=$(_pod_name "$ws_id")
    if [[ -n "$pod" ]]; then
      t_pod_created=$(_ts); break
    fi
    sleep 0.5
  done
  [[ -z "$t_pod_created" ]] && { echo "ERROR: pod never appeared" >&2
    curl -sf -X DELETE "$API_URL/workspaces/$ws_id" ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true
    continue; }
  pod=$(_pod_name "$ws_id")

  # 3. Wait for pod Running
  t_pod_running=""
  deadline=$(( t0 + 300000 ))
  while [[ $(_ts) -lt $deadline ]]; do
    pod_phase=$($KUBECTL -n "$NS" get pod "$pod" \
      --no-headers -o custom-columns=":status.phase" 2>/dev/null || echo "")
    if [[ "$pod_phase" == "Running" ]]; then
      t_pod_running=$(_ts); break
    fi
    sleep 0.5
  done
  [[ -z "$t_pod_running" ]] && { echo "ERROR: pod never Running" >&2
    curl -sf -X DELETE "$API_URL/workspaces/$ws_id" ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true
    continue; }

  # 3b. Init container duration (from pod timestamps)
  init_ms=0
  if [[ "$has_init" -eq 1 ]]; then
    init_ms=$(_init_container_duration_ms "$pod")
  fi

  # 4. readyz via port-forward
  local_port=$(( 19100 + RANDOM % 1000 ))
  $KUBECTL -n "$NS" port-forward "pod/$pod" "$local_port:4098" >/dev/null 2>&1 &
  pf_pid=$!

  t_readyz=""
  deadline=$(( t0 + 400000 ))
  admin_token=$($KUBECTL -n "$NS" get secret "workspace-pw-$ws_id" \
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
  [[ -z "$t_readyz" ]] && { echo "ERROR: readyz never 200" >&2
    curl -sf -X DELETE "$API_URL/workspaces/$ws_id" ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true
    continue; }

  # 5. Active
  _wait_phase "$ws_id" "Active" 60
  t_active=$(_ts)

  # ---- cleanup ----
  curl -sf -X DELETE "$API_URL/workspaces/$ws_id" \
    ${API_KEY:+-H "Authorization: Bearer $API_KEY"} >/dev/null || true

  # ---- record ----
  g1=$(( t_creating - t0 ))
  g2=$(( t_pod_created - t_creating ))
  g3=$(( t_pod_running - t_pod_created ))
  g4=$(( t_readyz - t_pod_running ))
  g5=$(( t_active - t_readyz ))
  total=$(( t_active - t0 ))

  t_api_to_pvc_bound+=("$g1")
  t_pvc_to_pod_created+=("$g2")
  t_pod_running_to_readyz+=("$g4")
  t_readyz_to_active+=("$g5")
  t_total+=("$total")
  [[ "$has_init" -eq 1 ]] && t_init_containers+=("$init_ms")

  printf "  API → PVC Bound/Creating:  %5ss\n" "$(_ms_to_s "$g1")"
  printf "  Creating → Pod Created:    %5ss\n" "$(_ms_to_s "$g2")"
  printf "  Pod Created → Pod Running: %5ss\n" "$(_ms_to_s "$g3")"
  if [[ "$has_init" -eq 1 ]]; then
    printf "  Init container:            %5ss\n" "$(_ms_to_s "$init_ms")"
  fi
  printf "  Pod Running → readyz 200:  %5ss  ← primary suspect\n" "$(_ms_to_s "$g4")"
  printf "  readyz 200 → Active:       %5ss\n" "$(_ms_to_s "$g5")"
  printf "  TOTAL:                     %5ss\n" "$(_ms_to_s "$total")"
done

# ---- statistics ----
_percentile() {
  local p="$1"; shift
  local arr=("$@")
  local n=${#arr[@]}
  [[ $n -eq 0 ]] && echo "0" && return
  IFS=$'\n' sorted=($(sort -n <<<"${arr[*]}")); unset IFS
  local idx=$(( (p * n + 99) / 100 - 1 ))
  [[ $idx -lt 0 ]] && idx=0
  [[ $idx -ge $n ]] && idx=$(( n - 1 ))
  echo "${sorted[$idx]}"
}

echo ""
echo "=== Summary ($ITERATIONS runs) ==="
printf "%-42s %8s %8s %8s\n" "Gate" "p50" "p90" "p99"
printf "%-42s %8s %8s %8s\n" "----" "---" "---" "---"

_row() {
  local label="$1"; shift
  local arr=("$@")
  [[ ${#arr[@]} -eq 0 ]] && return
  printf "%-42s %6ss %6ss %6ss\n" "$label" \
    "$(_ms_to_s "$(_percentile 50 "${arr[@]}")")" \
    "$(_ms_to_s "$(_percentile 90 "${arr[@]}")")" \
    "$(_ms_to_s "$(_percentile 99 "${arr[@]}")")"
}

_row "API → PVC Bound/Creating"      "${t_api_to_pvc_bound[@]}"
_row "Creating → Pod Created"        "${t_pvc_to_pod_created[@]}"
[[ "$has_init" -eq 1 ]] && _row "Init container (workspace-setup)" "${t_init_containers[@]}"
_row "Pod Running → readyz 200"      "${t_pod_running_to_readyz[@]}"
_row "readyz 200 → Active"           "${t_readyz_to_active[@]}"
_row "TOTAL"                         "${t_total[@]}"

# ---- threshold assertions ----
violations=0
if [[ "$ASSERT" == true ]]; then
  echo ""
  echo "=== Threshold checks ==="

  _check() {
    local name="$1" actual_ms="$2" max_s="$3"
    local max_ms
    max_ms=$(echo "$max_s * 1000" | bc | cut -d. -f1)
    if [[ "$actual_ms" -le "$max_ms" ]]; then
      printf "  PASS  %-42s %ss <= %ss\n" "$name" "$(_ms_to_s "$actual_ms")" "$max_s"
    else
      printf "  FAIL  %-42s %ss > %ss (threshold)\n" "$name" "$(_ms_to_s "$actual_ms")" "$max_s"
      violations=$(( violations + 1 ))
    fi
  }

  p99_total=$(_percentile 99 "${t_total[@]}")
  p99_pod_to_readyz=$(_percentile 99 "${t_pod_running_to_readyz[@]}")

  [[ -n "$MAX_P99_TOTAL" ]] && _check "p99 total create latency" "$p99_total" "$MAX_P99_TOTAL"
  [[ -n "$MAX_P99_POD_TO_READYZ" ]] && \
    _check "p99 pod-running→readyz-200" "$p99_pod_to_readyz" "$MAX_P99_POD_TO_READYZ"
  if [[ -n "$MAX_P99_INIT" && "$has_init" -eq 1 ]]; then
    p99_init=$(_percentile 99 "${t_init_containers[@]}")
    _check "p99 init container" "$p99_init" "$MAX_P99_INIT"
  fi

  if [[ $violations -gt 0 ]]; then
    echo ""
    echo "ERROR: $violations threshold violation(s)" >&2
    exit 1
  fi
  echo "  All thresholds passed."
fi
