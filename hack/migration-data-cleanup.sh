#!/usr/bin/env bash
# hack/migration-data-cleanup.sh
#
# Test that migration 000014 correctly handles edge-case data:
#   1. Non-UUID workspace_id values in user_secret_bindings
#   2. Orphan workspaces whose user_id is absent from users
#
# Strategy: apply all migrations through 000013, seed edge-case rows,
# apply migration 000014, verify the cleanup happened correctly.
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.

set -euo pipefail

cd "$(dirname "$0")/.."

PSQL="psql -v ON_ERROR_STOP=1"

echo "== migration-data-cleanup: resetting public schema =="
$PSQL -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

echo "== applying migrations 000001–000013 =="
for f in $(ls api/migrations/00000[1-9]*.up.sql api/migrations/00001[0-3]*.up.sql 2>/dev/null | sort); do
    echo "  $f"
    $PSQL -f "$f" >/dev/null
done

echo "== seeding edge-case data =="

# Create a user so workspaces have a valid owner.
$PSQL -c "INSERT INTO users (id, username, email, password_hash, role)
          VALUES ('user-test-0001', 'testuser', 'test@example.com', 'hash', 'user')
          ON CONFLICT (id) DO NOTHING;"

# Create a workspace owned by the test user.
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version)
          VALUES ('a0000000-0000-0000-0000-000000000001', 'ws-legit', 'user-test-0001', 'latest', '1.0');"

# Create an orphan workspace (user_id references a user that doesn't exist).
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version)
          VALUES ('b0000000-0000-0000-0000-000000000002', 'ws-orphan', 'dead-user-9999', 'latest', '1.0');"

# Create a secret so we can bind it.
$PSQL -c "INSERT INTO user_secrets (id, user_id, name, type, ciphertext, key_version)
          VALUES ('c0000000-0000-0000-0000-000000000001', 'user-test-0001', 'test-key', 'llm-provider', '\x00', 1);"

# Create a valid binding (UUID workspace_id).
$PSQL -c "INSERT INTO user_secret_bindings (secret_id, workspace_id)
          VALUES ('c0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001');"

# Create an invalid binding (non-UUID workspace_id — test data).
$PSQL -c "INSERT INTO user_secret_bindings (secret_id, workspace_id)
          VALUES ('c0000000-0000-0000-0000-000000000001', 'ws-validation');"

echo "== verifying seed data =="
BINDINGS=$($PSQL -tAc "SELECT count(*) FROM user_secret_bindings WHERE workspace_id = 'ws-validation';")
ORPHANS=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE user_id = 'dead-user-9999' AND deleted_at IS NULL;")
echo "  non-UUID bindings: $BINDINGS (expect 1)"
echo "  orphan workspaces: $ORPHANS (expect 1)"

if [ "$BINDINGS" != "1" ] || [ "$ORPHANS" != "1" ]; then
    echo "FAIL: seed data did not match expectations"
    exit 1
fi

echo "== applying migration 000014 =="
$PSQL -f api/migrations/000014_workspace_agent_state_and_bug11_fix.up.sql

echo "== verifying cleanup =="

# Non-UUID binding should be deleted.
# Cast workspace_id::text because migration 000014 changes the column type
# to UUID, after which the literal 'ws-validation' cannot be implicitly
# cast on the right side of the comparison.
BINDINGS_AFTER=$($PSQL -tAc "SELECT count(*) FROM user_secret_bindings WHERE workspace_id::text = 'ws-validation';")
if [ "$BINDINGS_AFTER" != "0" ]; then
    echo "FAIL: non-UUID binding still exists after migration"
    exit 1
fi
echo "  OK: non-UUID bindings deleted"

# Valid binding should survive.
VALID_BINDING=$($PSQL -tAc "SELECT count(*) FROM user_secret_bindings WHERE workspace_id = 'a0000000-0000-0000-0000-000000000001';")
if [ "$VALID_BINDING" != "1" ]; then
    echo "FAIL: valid binding was deleted (overly broad DELETE)"
    exit 1
fi
echo "  OK: valid binding survives"

# Orphan workspace should be soft-deleted.
ORPHAN_DELETED=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id = 'b0000000-0000-0000-0000-000000000002' AND deleted_at IS NOT NULL;")
if [ "$ORPHAN_DELETED" != "1" ]; then
    echo "FAIL: orphan workspace was not soft-deleted"
    exit 1
fi
echo "  OK: orphan workspace soft-deleted"

# Legit workspace should be untouched.
LEGIT=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id = 'a0000000-0000-0000-0000-000000000001' AND deleted_at IS NULL;")
if [ "$LEGIT" != "1" ]; then
    echo "FAIL: legitimate workspace was affected by orphan cleanup"
    exit 1
fi
echo "  OK: legitimate workspace untouched"

# workspace_agent_state table should exist.
TABLE_EXISTS=$($PSQL -tAc "SELECT count(*) FROM information_schema.tables WHERE table_name = 'workspace_agent_state';")
if [ "$TABLE_EXISTS" != "1" ]; then
    echo "FAIL: workspace_agent_state table does not exist"
    exit 1
fi
echo "  OK: workspace_agent_state table created"

# Backfill: the valid binding's workspace should have a pending_refresh row.
BACKFILL=$($PSQL -tAc "SELECT count(*) FROM workspace_agent_state WHERE workspace_id = 'a0000000-0000-0000-0000-000000000001' AND pending_refresh = true;")
if [ "$BACKFILL" != "1" ]; then
    echo "FAIL: backfill did not create pending_refresh row for workspace with llm-provider binding"
    exit 1
fi
echo "  OK: backfill created pending_refresh row"

# FK constraint: user_secret_bindings_workspace_id_fkey should exist.
FK_BINDINGS=$($PSQL -tAc "SELECT count(*) FROM information_schema.table_constraints WHERE constraint_name = 'user_secret_bindings_workspace_id_fkey';")
if [ "$FK_BINDINGS" != "1" ]; then
    echo "WARN: user_secret_bindings_workspace_id_fkey not created (orphan data may have prevented it)"
else
    echo "  OK: user_secret_bindings_workspace_id_fkey created"
fi

# FK constraint: workspaces_user_id_fkey — may not exist if orphan workspaces remain.
FK_WORKSPACES=$($PSQL -tAc "SELECT count(*) FROM information_schema.table_constraints WHERE constraint_name = 'workspaces_user_id_fkey';")
if [ "$FK_WORKSPACES" != "1" ]; then
    echo "WARN: workspaces_user_id_fkey not created (orphan workspaces prevented FK — expected with test data)"
else
    echo "  OK: workspaces_user_id_fkey created"
fi

echo ""
echo "migration-data-cleanup: all checks passed."
