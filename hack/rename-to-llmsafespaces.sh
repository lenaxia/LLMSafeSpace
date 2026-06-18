#!/usr/bin/env bash
# =============================================================================
# llmsafespace → llmsafespaces rename: dry-run reporter + executor.
#
# Usage:
#   DRY_RUN=1 ./rename.sh   # default — report only, no edits
#   DRY_RUN=0 ./rename.sh   # execute the rename
#
# Policy (per user decision, 2026-06-18):
#   * K8s API group: llmsafespace.dev → llmsafespaces.dev     (rename)
#   * GitHub repo : lenaxia/LLMSafeSpace → LLMSafeSpaces      (rename)
#   * History docs: worklogs/, design/                        (LEAVE ALONE)
#
# Excludes (no edits): .git/, worklogs/, design/, bin/, node_modules/,
#   root binaries (workspace-agentd, redact, tools), go.sum (regenerated),
#   lockfiles (regenerated).
# =============================================================================

set -euo pipefail

ROOT="/workspace/llmsafespace"
cd "$ROOT"

DRY_RUN="${DRY_RUN:-1}"

# ---------- Phase 1: directory renames (git mv) ------------------------------
# Three directories whose name is part of an import path / chart name / pkg id.
DIR_RENAMES=(
  "pkg/apis/llmsafespace:pkg/apis/llmsafespaces"
  "charts/llmsafespace:charts/llmsafespaces"
  "sdks/vscode-llmsafespace:sdks/vscode-llmsafespaces"
)

# ---------- Phase 2: content rewrite rules -----------------------------------
# Case-sensitive. The three case variants are disjoint (verified: zero existing
# 'llmsafespaces' / 'LLMSafeSpaces' in tree), so order is irrelevant and no
# word-boundary guarding needed.
#
# Each rule: PATTERN:REPLACEMENT
RULES=(
  "llmsafespace:llmsafespaces"     # lowercase: module path, CRD group, metrics,
                                   #   env-var snake prefix, PG role, image repo,
                                   #   binary name, dir paths, npm scope, etc.
  "LLMSAFESPACE:LLMSAFESPACES"     # ALL_CAPS: env vars (LLMSAFESPACE_*).
  "LLMSafeSpace:LLMSafeSpaces"     # MixedCase: repo name (URLs), prose headers.
)

# ---------- Files to skip entirely -------------------------------------------
SKIP_PATH_RE='(^|/)(\.git|worklogs|design|bin|node_modules)/'
SKIP_FILES_RE='(^|/)(go\.sum|package-lock\.json|workspace-agentd|redact|tools)$'

# Files whose name itself contains llmsafespace (caught by Phase 1 dir renames
# already for the 3 known dirs; any stray files flagged here for manual review).
NAMED_FILES_RE='llmsafespace'

# ============================================================================
# Helpers
# ============================================================================

is_skipped() {
  local p="$1"
  [[ "$p" =~ $SKIP_PATH_RE ]] && return 0
  [[ "$p" =~ $SKIP_FILES_RE ]] && return 0
  return 1
}

# Count matches of all three case-variants in a file (case-sensitive).
count_hits() {
  local f="$1" total=0 n
  for pat in llmsafespace LLMSAFESPACE LLMSafeSpace; do
    n=$(grep -c -- "$pat" "$f" 2>/dev/null || true)
    total=$((total + n))
  done
  echo "$total"
}

# ============================================================================
# Main
# ============================================================================

echo "=================================================================="
echo " llmsafespace → llmsafespaces rename"
echo " mode: $([ "$DRY_RUN" = "1" ] && echo 'DRY-RUN (no edits)' || echo 'EXECUTE')"
echo " root: $ROOT"
echo "=================================================================="
echo

# ----- Phase 1: directory renames --------------------------------------------
echo "### Phase 1: directory renames (git mv)"
echo
for entry in "${DIR_RENAMES[@]}"; do
  src="${entry%%:*}"; dst="${entry##*:}"
  if [ -d "$src" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      files=$(git ls-files "$src" | wc -l)
      printf "  [DRY] git mv '%s' '%s'   (%s tracked files)\n" "$src" "$dst" "$files"
    else
      git mv "$src" "$dst"
      printf "  [OK]  %s → %s\n" "$src" "$dst"
    fi
  else
    printf "  [SKIP] %s (not a directory or already moved)\n" "$src"
  fi
done
echo

# ----- Phase 2a: enumerate files needing content edits -----------------------
echo "### Phase 2: content rewrites"
echo
echo "Patterns (case-sensitive):"
for r in "${RULES[@]}"; do
  printf "   '%s' → '%s'\n" "${r%%:*}" "${r##*:}"
done
echo
echo "Excluded paths: worklogs/, design/, .git/, bin/, node_modules/"
echo "Excluded files: go.sum, package-lock.json, workspace-agentd, redact, tools"
echo

# Build list of candidate files (tracked, text, not skipped).
mapfile -t ALL_FILES < <(git ls-files)

declare -a EDIT_FILES=()
declare -a NAMED_LIKE=()
total_hits=0

for f in "${ALL_FILES[@]}"; do
  if is_skipped "$f"; then continue; fi
  # Flag stray files whose NAME contains the token (won't be auto-renamed).
  base="${f##*/}"
  if [[ "$base" =~ $NAMED_FILES_RE ]] && \
     [[ "$f" != pkg/apis/llmsafespa* ]] && \
     [[ "$f" != charts/llmsafespa* ]] && \
     [[ "$f" != sdks/vscode-llmsafespa* ]]; then
    NAMED_LIKE+=("$f")
  fi
  if [ ! -f "$f" ]; then continue; fi
  # Skip binary files (grep -I would exclude them, but check explicitly).
  if ! grep -qI "" "$f" 2>/dev/null; then continue; fi
  n=$(count_hits "$f")
  if [ "$n" -gt 0 ]; then
    EDIT_FILES+=("$f|$n")
    total_hits=$((total_hits + n))
  fi
done

# ----- Phase 2b: report (or execute) -----------------------------------------
if [ "$DRY_RUN" = "1" ]; then
  printf "Files needing edits: %d\n" "${#EDIT_FILES[@]}"
  printf "Total line-level matches across all files: %d\n" "$total_hits"
  echo
  echo "Top 30 files by match count:"
  echo "-----------------------------------------------"
  printf "%6s  %s\n" "HITS" "FILE"
  printf "%6s  %s\n" "-----" "----------------------------------------"
  printf "%s\n" "${EDIT_FILES[@]}" \
    | sort -t'|' -k2 -nr | head -30 \
    | awk -F'|' '{ printf "%6d  %s\n", $2, $1 }'
  echo

  # Per-pattern totals.
  echo "Per-pattern match counts (entire non-excluded tree):"
  for pat in llmsafespace LLMSAFESPACE LLMSafeSpace; do
    c=$(git grep -Ic -- "$pat" -- . 2>/dev/null \
        | awk -F: '{ for(i=2;i<=NF;i++) s+=$i } END{ print s+0 }')
    # Subtract matches inside excluded dirs.
    excl=$(git grep -Ic -- "$pat" -- worklogs design 2>/dev/null \
           | awk -F: '{ for(i=2;i<=NF;i++) s+=$i } END{ print s+0 }')
    real=$((c - excl))
    printf "   %-15s %5d matches (excluded %d in worklogs/+design/)\n" \
      "$pat" "$real" "$excl"
  done
  echo
else
  printf "Rewriting %d files...\n" "${#EDIT_FILES[@]}"
  for entry in "${EDIT_FILES[@]}"; do
    f="${entry%%|*}"
    # Apply each rule with a perl one-liner (handles all chars safely).
    for r in "${RULES[@]}"; do
      pat="${r%%:*}"; rep="${r##*:}"
      # Escape for perl s/// (paths/patterns are plain ASCII alnum here).
      perl -i -pe "s/\Q$pat\E/$rep/g" "$f"
    done
  done
  printf "  [OK] rewrote %d files (%d total replacements)\n" \
    "${#EDIT_FILES[@]}" "$total_hits"
  echo
fi

# ----- Phase 3: files whose NAME contains the token (manual review) ----------
echo "### Phase 3: stray files whose NAME contains 'llmsafespace'"
echo "            (not under one of the 3 renamed dirs — review individually)"
echo
if [ "${#NAMED_LIKE[@]}" -eq 0 ]; then
  echo "  (none)"
else
  for f in "${NAMED_LIKE[@]}"; do
    if [ "$DRY_RUN" = "1" ]; then
      printf "  [DRY] review: %s\n" "$f"
    else
      printf "  [MANUAL] %s (not auto-renamed)\n" "$f"
    fi
  done
fi
echo

# ----- Phase 4: post-rewrite regeneration commands --------------------------
echo "### Phase 4: regeneration commands to run after edits"
echo "            (run these manually; they regenerate derived artifacts)"
echo
cat <<'EOF'
   go mod edit -module github.com/lenaxia/llmsafespaces
   (cd sdks/go && go mod edit -module github.com/lenaxia/llmsafespaces/sdk/go)
   go mod tidy
   (cd sdks/go && go mod tidy)
   make manifests          # regenerate CRD YAML + zz_generated.deepcopy.go
   make mocks              # regenerate mocks (if a make target exists)
   make test lint          # verify
EOF
echo

# ----- Phase 5: external (manual) steps --------------------------------------
echo "### Phase 5: external manual steps"
echo
cat <<'EOF'
   1. GitHub: Settings → Repository → Rename
        lenaxia/LLMSafeSpace → lenaxia/LLMSafeSpaces
      (GitHub auto-redirects old URL; existing clones keep working.)

   2. Container registry (ghcr.io): future pushes use new repo path.
      Old image tags (ghcr.io/lenaxia/llmsafespace/*) become orphans.

   3. npm: publish @llmsafespaces/sdk and vscode-llmsafespaces
      when cutting next release. Old packages can be deprecated.

   4. PyPI: publish llmsafespaces (sdks/python/pyproject.toml)
      when cutting next release. Old 'llmsafespace' can be yanked.

   5. Cloudflare Worker: rename 'llmsafespace-inference-relay' to
      'llmsafespaces-inference-relay' in wrangler.toml + redeploy.

   6. Local Postgres (dev cluster): drop+recreate DB/role as
      'llmsafespaces' (or update values.yaml + local/bootstrap.sh).
EOF
echo

# ----- Summary ---------------------------------------------------------------
echo "=================================================================="
echo " Summary"
echo "=================================================================="
printf "  Dirs to rename        : %d\n" "${#DIR_RENAMES[@]}"
printf "  Files to edit         : %d\n" "${#EDIT_FILES[@]}"
printf "  Total line matches    : %d\n" "$total_hits"
printf "  Stray-named files     : %d (manual review)\n" "${#NAMED_LIKE[@]}"
if [ "$DRY_RUN" = "1" ]; then
  echo
  echo "  Re-run with DRY_RUN=0 to execute."
fi
echo "=================================================================="
