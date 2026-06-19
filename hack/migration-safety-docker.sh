#!/usr/bin/env bash
# hack/migration-safety-docker.sh
#
# Spin up a throwaway postgres:16 container, run the full migration-safety
# suite (round-trip + idempotency + FK cascade + data cleanup) against it,
# then tear it down. Requires Docker + host-side PostgreSQL client tools
# (psql, pg_dump, pg_isready) — the underlying migration scripts invoke
# these directly against the container's published port.
#
# This is the local equivalent of .github/workflows/migration-safety.yml.
# It exists so the pre-commit hook (and developers) can validate that
# migrations actually apply cleanly against a real Postgres without
# remembering to start a database and export PG* env vars.
#
# Usage:
#   make migration-safety-docker
#   bash hack/migration-safety-docker.sh
#
# Exit codes:
#   0  all checks passed
#   1  a migration-safety check failed (see output for which one)
#   2  prerequisites missing (docker / psql / pg_isready not available)
#
# Escape hatch: set LSS_SKIP_MIGRATION_GATE=1 in the environment to make
# this script exit 0 immediately. Used by the pre-commit hook when a
# contributor wants a fast commit and has already validated locally.

set -euo pipefail

# Opt-out: the pre-commit gate sets this when the contributor requests a
# skip. Standalone invocations are unaffected (the var defaults to unset).
if [ "${LSS_SKIP_MIGRATION_GATE:-0}" = "1" ]; then
    echo "== migration-safety-docker: skipped (LSS_SKIP_MIGRATION_GATE=1) =="
    exit 0
fi

cd "$(dirname "$0")/.."

if ! command -v docker >/dev/null 2>&1; then
    echo "migration-safety-docker: docker not found on PATH." >&2
    echo "  Install Docker, or run the suite manually against a Postgres" >&2
    echo "  you manage: see the 'Migration safety' section of the Makefile." >&2
    exit 2
fi

if ! docker info >/dev/null 2>&1; then
    echo "migration-safety-docker: Docker daemon is not reachable." >&2
    echo "  Start Docker (or run 'make migration-safety' against a Postgres" >&2
    echo "  you supply via PG* env vars)." >&2
    exit 2
fi

# The underlying migration scripts (hack/migration-roundtrip.sh etc.) invoke
# psql, pg_dump, and pg_isready directly against the container's published
# port. Fail fast before starting the container if these are missing.
for cmd in psql pg_isready; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "migration-safety-docker: $cmd not found on PATH." >&2
        echo "  Install PostgreSQL client tools (e.g. 'brew install libpq' or" >&2
        echo "  'apt install postgresql-client'), then re-run." >&2
        exit 2
    fi
done

PGIMAGE="${LSS_PGIMAGE:-postgres:16}"
PGUSER_VAL="llmsafespaces"
PGPASSWORD_VAL="migration-test"
PGDATABASE_VAL="llmsafespaces"

# Unique container name per invocation (PID + timestamp) so two concurrent
# runs (e.g. two terminals) don't collide.
CID="lss-migration-test-$$-$(date +%s)"

cleanup() {
    if [ -n "${CID:-}" ]; then
        docker rm -f "$CID" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT INT TERM

echo "== migration-safety-docker: starting $PGIMAGE =="
# -P publishes 5432 to a random host port (avoids clashing with a dev's
#   local Postgres on 5432). --rm is belt-and-braces; cleanup() also force-removes.
CID_OUT=$(docker run -d --rm \
    -e POSTGRES_USER="$PGUSER_VAL" \
    -e POSTGRES_PASSWORD="$PGPASSWORD_VAL" \
    -e POSTGRES_DB="$PGDATABASE_VAL" \
    -P \
    --name "$CID" \
    "$PGIMAGE") || {
    echo "migration-safety-docker: failed to start container (image pull?)." >&2
    exit 2
}
CID="$CID_OUT"

# Resolve the random host port docker assigned to 5432.
PGPORT=$(docker port "$CID" 5432/tcp | sed -n 's/.*:\([0-9]\{1,5\}\)$/\1/p' | head -n1)
if [ -z "$PGPORT" ]; then
    echo "migration-safety-docker: could not determine published port." >&2
    exit 2
fi

echo "== migration-safety-docker: waiting for Postgres on localhost:$PGPORT =="
for i in $(seq 1 30); do
    if pg_isready -h localhost -p "$PGPORT" -U "$PGUSER_VAL" >/dev/null 2>&1; then
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "migration-safety-docker: Postgres did not become ready in 30s." >&2
        exit 2
    fi
    sleep 1
done

export PGHOST=localhost
export PGPORT="$PGPORT"
export PGUSER="$PGUSER_VAL"
export PGPASSWORD="$PGPASSWORD_VAL"
export PGDATABASE="$PGDATABASE_VAL"

echo "== migration-safety-docker: running suite (this takes ~20-40s) =="
echo ""
if make -s migration-safety; then
    echo ""
    echo "== migration-safety-docker: ALL CHECKS PASSED =="
    exit 0
else
    echo "" >&2
    echo "migration-safety-docker: a migration-safety check FAILED (see above)." >&2
    echo "  Fix the migration, then re-run. To bypass in the pre-commit hook:" >&2
    echo "    LSS_SKIP_MIGRATION_GATE=1 git commit ..." >&2
    echo "  or in an emergency: git commit --no-verify" >&2
    exit 1
fi
