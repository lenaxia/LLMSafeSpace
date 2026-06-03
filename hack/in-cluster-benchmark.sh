#!/bin/sh
# in-cluster-benchmark.sh — Self-contained workspace startup latency benchmark.
# Runs inside the cluster where kubectl and the API are both reachable.
# Measures resume and create latency with per-gate breakdown.
#
# Usage: sh /tmp/bench.sh [resume|create|both] [iterations]

set -eu

MODE="${1:-both}"
ITERATIONS="${2:-3}"
# RESUME_WS_ID: prefer env var (RESUME_WS_ID=xxx sh bench.sh), then $3 positional arg
RESUME_WS_ID="${RESUME_WS_ID:-${3:-}}"
API="http://llmsafespace-api:8080"
NS="default"
HDR="X-Forwarded-Proto: https"
APIKEY="${BENCH_APIKEY:-}"

# ---- colour helpers ----
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'

log()  { printf "${BOLD}[bench]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[warn]${NC}  %s\n" "$*"; }
die()  { printf "${RED}[fail]${NC}  %s\n" "$*" >&2; exit 1; }

# ---- millisecond timestamp (python3 required - BusyBox date lacks %3N) ----
ts_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

# ---- ms to seconds string ----
ms2s() { awk "BEGIN{printf \"%.2f\", $1/1000}"; }

# ---- API call wrapper (always adds required headers) ----
api() {
  local method="$1"; shift
  local path="$1"; shift
  curl -sf -X "$method" "$API$path" \
    -H "$HDR" \
    ${APIKEY:+-H "Authorization: Bearer $APIKEY"} \
    "$@"
}

# ---- wait for workspace phase ----
wait_phase() {
  local ws_id="$1" target="$2" timeout_s="${3:-180}"
  local deadline=$(( $(ts_ms) + timeout_s * 1000 ))
  while [ $(ts_ms) -lt $deadline ]; do
    phase=$(api GET "/api/v1/workspaces/$ws_id" 2>/dev/null | jq -r '.phase // empty' 2>/dev/null || echo "")
    [ "$phase" = "$target" ] && return 0
    sleep 0.5
  done
  warn "workspace $ws_id never reached $target within ${timeout_s}s (last: $phase)"
  return 1
}

# ---- get pod name for workspace ----
pod_name() {
  kubectl -n "$NS" get pods \
    -l "llmsafespace.dev/workspace=$1" \
    --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1
}

# ---- poll for a NEW pod (created at or after t0_iso) reaching Running ----
# t0_iso: ISO8601 UTC timestamp string (date -u '+%Y-%m-%dT%H:%M:%SZ')
# Returns pod name on stdout.
wait_new_pod_running() {
  local ws_id="$1" t0_iso="$2" timeout_s="${3:-120}"
  local deadline=$(( $(ts_ms) + timeout_s * 1000 ))
  while [ $(ts_ms) -lt $deadline ]; do
    # List pods with name, creationTimestamp, phase on one line each
    local line pod_name pod_ts pod_phase
    kubectl -n "$NS" get pods \
      -l "llmsafespace.dev/workspace=$ws_id" \
      -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.creationTimestamp} {.status.phase}{"\n"}{end}' \
      2>/dev/null | \
    while IFS= read -r line; do
      pod_name=$(echo "$line" | awk '{print $1}')
      pod_ts=$(echo "$line"   | awk '{print $2}')
      pod_phase=$(echo "$line" | awk '{print $3}')
      # ISO8601 strings sort lexicographically — pod_ts >= t0_iso means new pod
      if [ "$pod_ts" \> "$t0_iso" ] || [ "$pod_ts" = "$t0_iso" ]; then
        if [ "$pod_phase" = "Running" ]; then
          echo "$pod_name"
          return 0
        fi
      fi
    done | head -1 | grep -q . && {
      # Found a running new pod — re-fetch the name and return
      kubectl -n "$NS" get pods \
        -l "llmsafespace.dev/workspace=$ws_id" \
        -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.creationTimestamp} {.status.phase}{"\n"}{end}' \
        2>/dev/null | awk -v t0="$t0_iso" '
          $2 >= t0 && $3 == "Running" {print $1; exit}
        ' && return 0
    }
    sleep 0.5
  done
  return 1
}

# ---- poll agentd /v1/readyz via pod IP ----
wait_readyz() {
  local pod="$1" timeout_s="${2:-180}"
  local deadline=$(( $(ts_ms) + timeout_s * 1000 ))

  # get admin token from workspace password secret
  local ws_id="$3"
  local admin_token
  admin_token=$(kubectl -n "$NS" get secret "workspace-pw-$ws_id" \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || echo "")

  local pod_ip
  pod_ip=$(kubectl -n "$NS" get pod "$pod" \
    -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")

  if [ -z "$pod_ip" ]; then
    warn "could not get pod IP for $pod"
    return 1
  fi

  while [ $(ts_ms) -lt $deadline ]; do
    local code
    if [ -n "$admin_token" ]; then
      code=$(curl -sf -o /dev/null -w "%{http_code}" \
        -H "Authorization: Bearer $admin_token" \
        "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
    else
      code=$(curl -sf -o /dev/null -w "%{http_code}" \
        "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
    fi
    [ "$code" = "200" ] && return 0
    sleep 0.5
  done
  warn "readyz never 200 on $pod_ip:4098"
  return 1
}

# ---- get init container duration from pod status ----
init_container_duration_ms() {
  local pod="$1"
  local started finished
  started=$(kubectl -n "$NS" get pod "$pod" \
    -o jsonpath='{.status.initContainerStatuses[?(@.name=="workspace-setup")].state.terminated.startedAt}' \
    2>/dev/null || echo "")
  finished=$(kubectl -n "$NS" get pod "$pod" \
    -o jsonpath='{.status.initContainerStatuses[?(@.name=="workspace-setup")].state.terminated.finishedAt}' \
    2>/dev/null || echo "")

  [ -z "$started" ] || [ -z "$finished" ] && echo "0" && return

  # Convert ISO8601 to epoch ms using awk (no date -d on alpine)
  local s_epoch f_epoch
  s_epoch=$(echo "$started" | awk '{
    gsub(/[-:TZ]/, " "); split($0, a, " ");
    printf "%d", mktime(a[1]" "a[2]" "a[3]" "a[4]" "a[5]" "a[6])
  }')
  f_epoch=$(echo "$finished" | awk '{
    gsub(/[-:TZ]/, " "); split($0, a, " ");
    printf "%d", mktime(a[1]" "a[2]" "a[3]" "a[4]" "a[5]" "a[6])
  }')
  echo $(( (f_epoch - s_epoch) * 1000 ))
}

# ---- percentile from space-separated list ----
percentile() {
  local p="$1"; shift
  local vals="$*"
  local n
  n=$(echo "$vals" | wc -w | tr -d ' ')
  [ "$n" -eq 0 ] && echo "0" && return
  local sorted
  sorted=$(echo "$vals" | tr ' ' '\n' | sort -n | tr '\n' ' ')
  local idx
  idx=$(awk "BEGIN{printf \"%d\", ($p * $n + 99) / 100 - 1}")
  [ "$idx" -lt 0 ] && idx=0
  [ "$idx" -ge "$n" ] && idx=$(( n - 1 ))
  echo "$sorted" | awk "{print \$$(( idx + 1 ))}"
}

# ---- print summary row ----
print_row() {
  local label="$1"; shift
  local vals="$*"
  [ -z "$vals" ] && return
  local p50 p90 p99
  p50=$(percentile 50 $vals)
  p90=$(percentile 90 $vals)
  p99=$(percentile 99 $vals)
  printf "  %-42s %7ss %7ss %7ss\n" \
    "$label" "$(ms2s $p50)" "$(ms2s $p90)" "$(ms2s $p99)"
}

# ======================================================================
# RESUME BENCHMARK
# ======================================================================
run_resume() {
  log "=== RESUME BENCHMARK ($ITERATIONS iterations) ==="
  echo ""

  # Find or use explicit workspace
  local ws_id
  ws_id="${RESUME_WS_ID:-}"

  if [ -z "$ws_id" ]; then
    # Try API listing for a suspended workspace owned by this user
    ws_id=$(api GET "/api/v1/workspaces" 2>/dev/null \
      | jq -r '.workspaces[]? | select(.phase=="Suspended") | .id' 2>/dev/null \
      | head -1)
  fi

  if [ -z "$ws_id" ]; then
    log "No suspended workspace — creating one..."
    ws_id=$(api POST "/api/v1/workspaces" \
      -H "Content-Type: application/json" \
      -d '{"name":"bench-resume","runtime":"base","storage":{"size":"1Gi"}}' \
      | jq -r '.id')
    log "Created: $ws_id — waiting for Active..."
    wait_phase "$ws_id" "Active" 180
    log "Suspending..."
    api POST "/api/v1/workspaces/$ws_id/suspend" >/dev/null
    wait_phase "$ws_id" "Suspended" 60
  fi
  log "Using workspace: $ws_id"

  local g_api_creating=""
  local g_creating_pod=""
  local g_pod_readyz=""
  local g_readyz_active=""
  local g_total=""

  local i=1
  while [ "$i" -le "$ITERATIONS" ]; do
    echo ""
    printf "${BOLD}--- Resume run %d/%d ---${NC}\n" "$i" "$ITERATIONS"

    # Ensure workspace is Suspended and its pod is fully gone before timing.
    # Without this, the old running pod is mistaken for the new one and all
    # gate times collapse to near-zero.
    local cur_phase
    cur_phase=$(api GET "/api/v1/workspaces/$ws_id" | jq -r '.phase')
    if [ "$cur_phase" != "Suspended" ]; then
      if [ "$cur_phase" = "Active" ] || [ "$cur_phase" = "Suspending" ]; then
        api POST "/api/v1/workspaces/$ws_id/suspend" >/dev/null 2>/dev/null || true
        wait_phase "$ws_id" "Suspended" 60
      else
        warn "Unexpected phase $cur_phase — waiting for Suspended..."
        wait_phase "$ws_id" "Suspended" 90 || { warn "skip run $i"; i=$(( i + 1 )); continue; }
      fi
    fi

    # Wait until the workspace pod is fully terminated.
    local pod_gone_deadline=$(( $(ts_ms) + 30000 ))
    while [ $(ts_ms) -lt $pod_gone_deadline ]; do
      local existing_pod
      existing_pod=$(kubectl -n "$NS" get pods \
        -l "llmsafespace.dev/workspace=$ws_id" \
        --no-headers -o custom-columns=":metadata.name" 2>/dev/null | head -1)
      [ -z "$existing_pod" ] && break
      sleep 1
    done

    local t0 t0_iso
    t0=$(ts_ms)
    t0_iso=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

    # Resume
    api POST "/api/v1/workspaces/$ws_id/resume" >/dev/null

    # Gate 1: API → Creating/Active (timestamp recorded at first observation)
    local t_creating=0
    local deadline=$(( t0 + 30000 ))
    while [ $(ts_ms) -lt $deadline ]; do
      local ph
      ph=$(api GET "/api/v1/workspaces/$ws_id" | jq -r '.phase' 2>/dev/null || echo "")
      if [ "$ph" = "Creating" ] || [ "$ph" = "Active" ]; then
        t_creating=$(ts_ms); break
      fi
      sleep 0.3
    done
    if [ "$t_creating" = "0" ]; then warn "never reached Creating"; i=$(( i + 1 )); continue; fi

    # Gate 2: → New pod Running (timestamp recorded at first observation inside loop)
    local t_pod_running=0
    local new_pod=""
    local deadline2=$(( t0 + 120000 ))
    while [ $(ts_ms) -lt $deadline2 ]; do
      local candidate
      candidate=$(kubectl -n "$NS" get pods \
        -l "llmsafespace.dev/workspace=$ws_id" \
        -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.creationTimestamp} {.status.phase}{"\n"}{end}' \
        2>/dev/null | awk -v t0="$t0_iso" '$2 >= t0 && $3 == "Running" {print $1; exit}')
      if [ -n "$candidate" ]; then
        t_pod_running=$(ts_ms)
        new_pod="$candidate"
        break
      fi
      sleep 0.5
    done
    if [ "$t_pod_running" = "0" ]; then warn "no new Running pod within 120s"; i=$(( i + 1 )); continue; fi

    # Gate 3: → readyz 200 (timestamp recorded at first 200)
    local t_readyz=0
    local pod_ip
    pod_ip=$(kubectl -n "$NS" get pod "$new_pod" -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
    local admin_token
    admin_token=$(kubectl -n "$NS" get secret "workspace-pw-$ws_id" \
      -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
    if [ -z "$pod_ip" ]; then
      warn "could not get pod IP for $new_pod"; i=$(( i + 1 )); continue
    fi
    local deadline3=$(( t0 + 300000 ))
    while [ $(ts_ms) -lt $deadline3 ]; do
      local code
      if [ -n "$admin_token" ]; then
        code=$(curl -sf -o /dev/null -w "%{http_code}" \
          -H "Authorization: Bearer $admin_token" \
          "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
      else
        code=$(curl -sf -o /dev/null -w "%{http_code}" \
          "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
      fi
      if [ "$code" = "200" ]; then
        t_readyz=$(ts_ms); break
      fi
      sleep 0.5
    done
    if [ "$t_readyz" = "0" ]; then warn "readyz never 200 within 300s"; i=$(( i + 1 )); continue; fi

    # Gate 4: → workspace Active (timestamp recorded at first observation)
    local t_active=0
    local deadline4=$(( t0 + 330000 ))
    while [ $(ts_ms) -lt $deadline4 ]; do
      local ph4
      ph4=$(api GET "/api/v1/workspaces/$ws_id" | jq -r '.phase' 2>/dev/null || echo "")
      if [ "$ph4" = "Active" ]; then
        t_active=$(ts_ms); break
      fi
      sleep 0.5
    done
    if [ "$t_active" = "0" ]; then warn "never Active within 330s"; i=$(( i + 1 )); continue; fi

    local g1=$(( t_creating - t0 ))
    local g2=$(( t_pod_running - t_creating ))
    local g3=$(( t_readyz - t_pod_running ))
    local g4=$(( t_active - t_readyz ))
    local total=$(( t_active - t0 ))

    g_api_creating="$g_api_creating $g1"
    g_creating_pod="$g_creating_pod $g2"
    g_pod_readyz="$g_pod_readyz $g3"
    g_readyz_active="$g_readyz_active $g4"
    g_total="$g_total $total"

    printf "  API → Creating:            %7ss\n" "$(ms2s $g1)"
    printf "  Creating → Pod Running:    %7ss\n" "$(ms2s $g2)"
    printf "  Pod Running → readyz 200:  %7ss  ← primary gate\n" "$(ms2s $g3)"
    printf "  readyz 200 → Active:       %7ss\n" "$(ms2s $g4)"
    printf "  ${BOLD}TOTAL:${NC}                     %7ss\n" "$(ms2s $total)"

    i=$(( i + 1 ))
  done

  echo ""
  printf "${BOLD}=== Resume Summary (%d runs) ===${NC}\n" "$ITERATIONS"
  printf "  %-42s %7s  %7s  %7s\n" "Gate" "p50" "p90" "p99"
  printf "  %-42s %7s  %7s  %7s\n" "----" "---" "---" "---"
  print_row "API → Creating"          $g_api_creating
  print_row "Creating → Pod Running"  $g_creating_pod
  print_row "Pod Running → readyz 200 (PRIMARY)" $g_pod_readyz
  print_row "readyz 200 → Active"     $g_readyz_active
  print_row "TOTAL"                   $g_total

  RESUME_P99_TOTAL=$(percentile 99 $g_total)
  RESUME_P99_POD_READYZ=$(percentile 99 $g_pod_readyz)
}

# ======================================================================
# CREATE BENCHMARK
# ======================================================================
run_create() {
  log "=== CREATE BENCHMARK ($ITERATIONS iterations) ==="
  echo ""

  local g_api_creating=""
  local g_creating_pod=""
  local g_pod_readyz=""
  local g_readyz_active=""
  local g_total=""

  local i=1
  while [ "$i" -le "$ITERATIONS" ]; do
    echo ""
    printf "${BOLD}--- Create run %d/%d ---${NC}\n" "$i" "$ITERATIONS"

    local t0 t0_iso
    t0=$(ts_ms)
    t0_iso=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

    local resp ws_id
    resp=$(api POST "/api/v1/workspaces" \
      -H "Content-Type: application/json" \
      -d '{"name":"bench-create","runtime":"base","storage":{"size":"1Gi"}}') || {
        warn "create failed"; i=$(( i + 1 )); continue; }
    ws_id=$(echo "$resp" | jq -r '.id')
    log "  workspace: $ws_id"

    # Gate 1: API → Creating (timestamp at first observation)
    local t_creating=0
    local deadline=$(( t0 + 60000 ))
    while [ $(ts_ms) -lt $deadline ]; do
      local ph
      ph=$(api GET "/api/v1/workspaces/$ws_id" | jq -r '.phase' 2>/dev/null || echo "")
      if [ "$ph" = "Creating" ] || [ "$ph" = "Active" ]; then
        t_creating=$(ts_ms); break
      fi
      sleep 0.3
    done
    if [ "$t_creating" = "0" ]; then
      warn "never reached Creating"
      api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true
      i=$(( i + 1 )); continue
    fi

    # Gate 2: → New pod Running (timestamp at first observation inside loop)
    local t_pod_running=0
    local new_pod=""
    local deadline2=$(( t0 + 180000 ))
    while [ $(ts_ms) -lt $deadline2 ]; do
      local candidate
      candidate=$(kubectl -n "$NS" get pods \
        -l "llmsafespace.dev/workspace=$ws_id" \
        -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.creationTimestamp} {.status.phase}{"\n"}{end}' \
        2>/dev/null | awk -v t0="$t0_iso" '$2 >= t0 && $3 == "Running" {print $1; exit}')
      if [ -n "$candidate" ]; then
        t_pod_running=$(ts_ms)
        new_pod="$candidate"
        break
      fi
      sleep 0.5
    done
    if [ "$t_pod_running" = "0" ]; then
      warn "no new Running pod within 180s"
      api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true
      i=$(( i + 1 )); continue
    fi

    # Gate 3: → readyz 200 (timestamp at first 200)
    local t_readyz=0
    local pod_ip
    pod_ip=$(kubectl -n "$NS" get pod "$new_pod" -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
    local admin_token
    admin_token=$(kubectl -n "$NS" get secret "workspace-pw-$ws_id" \
      -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
    if [ -z "$pod_ip" ]; then
      warn "could not get pod IP for $new_pod"
      api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true
      i=$(( i + 1 )); continue
    fi
    local deadline3=$(( t0 + 360000 ))
    while [ $(ts_ms) -lt $deadline3 ]; do
      local code
      if [ -n "$admin_token" ]; then
        code=$(curl -sf -o /dev/null -w "%{http_code}" \
          -H "Authorization: Bearer $admin_token" \
          "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
      else
        code=$(curl -sf -o /dev/null -w "%{http_code}" \
          "http://$pod_ip:4098/v1/readyz" 2>/dev/null || echo "000")
      fi
      if [ "$code" = "200" ]; then
        t_readyz=$(ts_ms); break
      fi
      sleep 0.5
    done
    if [ "$t_readyz" = "0" ]; then
      warn "readyz never 200 within 360s"
      api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true
      i=$(( i + 1 )); continue
    fi

    # Gate 4: → workspace Active (timestamp at first observation)
    local t_active=0
    local deadline4=$(( t0 + 390000 ))
    while [ $(ts_ms) -lt $deadline4 ]; do
      local ph4
      ph4=$(api GET "/api/v1/workspaces/$ws_id" | jq -r '.phase' 2>/dev/null || echo "")
      if [ "$ph4" = "Active" ]; then
        t_active=$(ts_ms); break
      fi
      sleep 0.5
    done
    if [ "$t_active" = "0" ]; then
      warn "never Active within 390s"
      api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true
      i=$(( i + 1 )); continue
    fi

    # Cleanup
    api DELETE "/api/v1/workspaces/$ws_id" >/dev/null 2>/dev/null || true

    local g1=$(( t_creating - t0 ))
    local g2=$(( t_pod_running - t_creating ))
    local g3=$(( t_readyz - t_pod_running ))
    local g4=$(( t_active - t_readyz ))
    local total=$(( t_active - t0 ))

    g_api_creating="$g_api_creating $g1"
    g_creating_pod="$g_creating_pod $g2"
    g_pod_readyz="$g_pod_readyz $g3"
    g_readyz_active="$g_readyz_active $g4"
    g_total="$g_total $total"

    printf "  API → Creating (PVC):      %7ss\n" "$(ms2s $g1)"
    printf "  Creating → Pod Running:    %7ss\n" "$(ms2s $g2)"
    printf "  Pod Running → readyz 200:  %7ss  ← primary gate\n" "$(ms2s $g3)"
    printf "  readyz 200 → Active:       %7ss\n" "$(ms2s $g4)"
    printf "  ${BOLD}TOTAL:${NC}                     %7ss\n" "$(ms2s $total)"

    i=$(( i + 1 ))
  done

  echo ""
  printf "${BOLD}=== Create Summary (%d runs) ===${NC}\n" "$ITERATIONS"
  printf "  %-42s %7s  %7s  %7s\n" "Gate" "p50" "p90" "p99"
  printf "  %-42s %7s  %7s  %7s\n" "----" "---" "---" "---"
  print_row "API → Creating (PVC)"       $g_api_creating
  print_row "Creating → Pod Running"     $g_creating_pod
  print_row "Pod Running → readyz 200 (PRIMARY)" $g_pod_readyz
  print_row "readyz 200 → Active"        $g_readyz_active
  print_row "TOTAL"                      $g_total

  CREATE_P99_TOTAL=$(percentile 99 $g_total)
  CREATE_P99_POD_READYZ=$(percentile 99 $g_pod_readyz)
}

# ======================================================================
# MAIN
# ======================================================================
RESUME_P99_TOTAL=0
RESUME_P99_POD_READYZ=0
CREATE_P99_TOTAL=0
CREATE_P99_POD_READYZ=0

[ "$MODE" = "resume" ] || [ "$MODE" = "both" ] && run_resume
[ "$MODE" = "create" ] || [ "$MODE" = "both" ] && run_create

echo ""
printf "${BOLD}=====================================================${NC}\n"
printf "${BOLD}  Final Baseline Numbers${NC}\n"
printf "${BOLD}=====================================================${NC}\n"
[ "$RESUME_P99_TOTAL" -gt 0 ] && \
  printf "  Resume  p99 total:              %ss\n" "$(ms2s $RESUME_P99_TOTAL)"
[ "$RESUME_P99_POD_READYZ" -gt 0 ] && \
  printf "  Resume  p99 pod→readyz:         %ss\n" "$(ms2s $RESUME_P99_POD_READYZ)"
[ "$CREATE_P99_TOTAL" -gt 0 ] && \
  printf "  Create  p99 total:              %ss\n" "$(ms2s $CREATE_P99_TOTAL)"
[ "$CREATE_P99_POD_READYZ" -gt 0 ] && \
  printf "  Create  p99 pod→readyz:         %ss\n" "$(ms2s $CREATE_P99_POD_READYZ)"
printf "${BOLD}=====================================================${NC}\n"
