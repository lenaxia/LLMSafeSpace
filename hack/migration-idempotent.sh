#!/usr/bin/env bash
# hack/migration-idempotent.sh
#
# Apply all up migrations twice. The second apply MUST succeed: a
# migration using CREATE TABLE without IF NOT EXISTS, or ALTER TABLE
# without IF EXISTS, will fail and that's a real bug — operators
# retry migrations on transient failure.
#
# Exception: 000001_initial_schema is a pg_dump snapshot post-collapse
# (Epic 55) and is NOT expected to be idempotent on its own. The
# golang-migrate CLI tracks applied versions in `schema_migrations` and
# will not re-run it. The idempotency check applies to migrations
# 000002+ which MUST use IF NOT EXISTS / IF EXISTS clauses per
# api/migrations/README.md.
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.

set -euo pipefail

cd "$(dirname "$0")/.."

# Reset schema to start clean.
echo "== Resetting public schema =="
psql -v ON_ERROR_STOP=1 -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

echo "== First apply (all migrations) =="
for f in $(ls api/migrations/*.up.sql | sort); do
    psql -v ON_ERROR_STOP=1 -f "$f" >/dev/null
done
echo "  OK: first apply succeeded"

# Re-apply migrations 000002+ only. The 000001 pg_dump baseline is not
# idempotent by design; later incremental migrations MUST be.
incremental=$(ls api/migrations/*.up.sql | sort | grep -v '^api/migrations/000001_' || true)
if [ -z "$incremental" ]; then
    echo "== Skipping second-apply check: only baseline migration present =="
    echo ""
    echo "Idempotency OK (vacuously): no incremental migrations to re-apply."
    exit 0
fi

echo "== Second apply (incremental migrations must succeed without errors) =="
for f in $incremental; do
    echo "  re-applying $f"
    psql -v ON_ERROR_STOP=1 -f "$f" >/dev/null
done
echo "  OK: second apply succeeded"

echo ""
echo "Idempotency OK: ups can be applied repeatedly."
