#!/usr/bin/env bash
# hack/migration-idempotent.sh
#
# Apply all up migrations twice. The second apply MUST succeed: a
# migration using CREATE TABLE without IF NOT EXISTS, or ALTER TABLE
# without IF EXISTS, will fail and that's a real bug — operators
# retry migrations on transient failure.
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.

set -euo pipefail

cd "$(dirname "$0")/.."

# Reset schema to start clean.
echo "== Resetting public schema =="
psql -v ON_ERROR_STOP=1 -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

echo "== First apply =="
for f in $(ls api/migrations/*.up.sql | sort); do
    psql -v ON_ERROR_STOP=1 -f "$f" >/dev/null
done
echo "  OK: first apply succeeded"

echo "== Second apply (must succeed without errors) =="
for f in $(ls api/migrations/*.up.sql | sort); do
    echo "  re-applying $f"
    psql -v ON_ERROR_STOP=1 -f "$f" >/dev/null
done
echo "  OK: second apply succeeded"

echo ""
echo "Idempotency OK: ups can be applied repeatedly."
