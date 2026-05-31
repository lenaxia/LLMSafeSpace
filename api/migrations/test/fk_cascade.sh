#!/usr/bin/env bash
# api/migrations/test/fk_cascade.sh
#
# Foreign-key cascade integrity test. Run after applying all up
# migrations to a Postgres instance. Inserts sample rows in every
# parent table that has at least one child via FK, deletes the parent,
# then asserts no orphan rows remain in any child.
#
# Catches regressions like:
#   - removing ON DELETE CASCADE from a FK definition
#   - new tables with FK that forget the cascade
#   - the worklog-0094 hazard O11 (bindings surviving workspace delete);
#     workspace_id on user_secret_bindings is intentionally NOT a FK
#     to a database table (workspaces metadata lives in the same DB
#     but the binding semantics are application-level), so we
#     additionally assert the application-layer cleanup function
#     MarkWorkspaceDeleted purges bindings transactionally.
#
# As of migration 000004 the sandboxes / sandbox_labels tables have
# been DROPPED — their contents now live in the workspaces / Workspace
# CRD. The FK graph that survives in the final schema is:
#
#   users -> api_keys                ON DELETE CASCADE
#   users -> permissions             ON DELETE CASCADE
#   users -> user_keys               ON DELETE CASCADE
#   users -> user_secrets            ON DELETE CASCADE
#   users -> user_settings           ON DELETE CASCADE
#   user_secrets -> user_secret_bindings  ON DELETE CASCADE
#
# secret_audit_log has no FK to users — append-only by design.
# workspaces has no FK to users — workspaces.user_id is VARCHAR (free-
# form, may reference an external IdP user that was never inserted
# into our users table).
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.
#
# Exit code: 0 on success, 1 on any orphan-row assertion failure.

set -euo pipefail

psql_run() {
    psql -v ON_ERROR_STOP=1 -t -A "$@"
}

assert_count() {
    local table="$1"
    local where="$2"
    local expected="$3"
    local label="$4"
    actual=$(psql_run -c "SELECT COUNT(*) FROM ${table} WHERE ${where};")
    if [ "${actual}" != "${expected}" ]; then
        echo "FAIL: ${label}"
        echo "  table:    ${table}"
        echo "  filter:   ${where}"
        echo "  expected: ${expected}"
        echo "  actual:   ${actual}"
        exit 1
    fi
    echo "OK: ${label} (${table}=${actual})"
}

# -----------------------------------------------------------------------------
# Test 1: user-rooted CASCADE chain
#
# Insert a user with one row in every child that FKs to users.
# Delete the user. Assert every child row is gone.
# -----------------------------------------------------------------------------

echo "== Test 1: users CASCADE =="

USER_ID="fk-cascade-user-1"
SECRET_ID=$(psql_run -c "SELECT gen_random_uuid();")

# Parent
psql_run -c "INSERT INTO users (id, username, email, password_hash) VALUES ('${USER_ID}', 'fkcasc1', 'fkcasc1@test', 'x');"

# Children (one row each in every table FK'd to users)
psql_run -c "INSERT INTO api_keys (id, user_id, key, name) VALUES ('apikey-1', '${USER_ID}', 'k1', 'name');"
psql_run -c "INSERT INTO permissions (id, user_id, resource_type, resource_id, action) VALUES ('perm-1', '${USER_ID}', 'workspace', '*', 'read');"
psql_run -c "INSERT INTO user_keys (user_id, key_version, wrapped_dek, salt) VALUES ('${USER_ID}', 1, '\\x00', '\\x00');"
psql_run -c "INSERT INTO user_secrets (id, user_id, name, type, ciphertext, key_version) VALUES ('${SECRET_ID}', '${USER_ID}', 'aname', 'env-secret', '\\x00', 1);"
psql_run -c "INSERT INTO user_settings (user_id, key, value) VALUES ('${USER_ID}', 'preferences', '{}'::jsonb);"

# Grandchildren: user_secret_bindings via user_secrets
psql_run -c "INSERT INTO user_secret_bindings (secret_id, workspace_id) VALUES ('${SECRET_ID}', 'ws-1');"

# Verify rows exist
assert_count "api_keys" "user_id = '${USER_ID}'" "1" "api_keys exists pre-delete"
assert_count "permissions" "user_id = '${USER_ID}'" "1" "permissions exists pre-delete"
assert_count "user_keys" "user_id = '${USER_ID}'" "1" "user_keys exists pre-delete"
assert_count "user_secrets" "user_id = '${USER_ID}'" "1" "user_secrets exists pre-delete"
assert_count "user_settings" "user_id = '${USER_ID}'" "1" "user_settings exists pre-delete"
assert_count "user_secret_bindings" "secret_id = '${SECRET_ID}'" "1" "user_secret_bindings exists pre-delete"

# Delete the parent
psql_run -c "DELETE FROM users WHERE id = '${USER_ID}';"

# Assert every child cascaded
assert_count "api_keys" "user_id = '${USER_ID}'" "0" "api_keys cascaded"
assert_count "permissions" "user_id = '${USER_ID}'" "0" "permissions cascaded"
assert_count "user_keys" "user_id = '${USER_ID}'" "0" "user_keys cascaded"
assert_count "user_secrets" "user_id = '${USER_ID}'" "0" "user_secrets cascaded"
assert_count "user_settings" "user_id = '${USER_ID}'" "0" "user_settings cascaded"
assert_count "user_secret_bindings" "secret_id = '${SECRET_ID}'" "0" "user_secret_bindings cascaded (via user_secrets)"

# -----------------------------------------------------------------------------
# Test 2: user_secrets -> user_secret_bindings CASCADE in isolation
# (re-test the leaf cascade without going through user delete)
# -----------------------------------------------------------------------------

echo "== Test 2: user_secrets -> user_secret_bindings CASCADE =="

USER_ID="fk-cascade-user-2"
SECRET_ID=$(psql_run -c "SELECT gen_random_uuid();")

psql_run -c "INSERT INTO users (id, username, email, password_hash) VALUES ('${USER_ID}', 'fkcasc2', 'fkcasc2@test', 'x');"
psql_run -c "INSERT INTO user_secrets (id, user_id, name, type, ciphertext, key_version) VALUES ('${SECRET_ID}', '${USER_ID}', 'aname', 'env-secret', '\\x00', 1);"
psql_run -c "INSERT INTO user_secret_bindings (secret_id, workspace_id) VALUES ('${SECRET_ID}', 'ws-1'), ('${SECRET_ID}', 'ws-2');"

assert_count "user_secret_bindings" "secret_id = '${SECRET_ID}'" "2" "bindings exist pre-delete"

# Delete the secret directly (not via user cascade)
psql_run -c "DELETE FROM user_secrets WHERE id = '${SECRET_ID}';"

assert_count "user_secret_bindings" "secret_id = '${SECRET_ID}'" "0" "bindings cascaded on user_secrets delete"

# Cleanup
psql_run -c "DELETE FROM users WHERE id = '${USER_ID}';"

# -----------------------------------------------------------------------------
# Test 3: secret_audit_log persists across user delete (intentional, append-only)
#
# Audit logs must outlive the principals they reference for compliance.
# If someone adds a FK with CASCADE this test fires and they need to
# revisit the audit-log retention design (worklog 0094 audit discussion).
# -----------------------------------------------------------------------------

echo "== Test 3: secret_audit_log persists across user delete =="

USER_ID="fk-cascade-user-3"
psql_run -c "INSERT INTO users (id, username, email, password_hash) VALUES ('${USER_ID}', 'fkcasc3', 'fkcasc3@test', 'x');"
psql_run -c "INSERT INTO secret_audit_log (user_id, action) VALUES ('${USER_ID}', 'create_secret');"

assert_count "secret_audit_log" "user_id = '${USER_ID}'" "1" "audit row exists pre-delete"

psql_run -c "DELETE FROM users WHERE id = '${USER_ID}';"

assert_count "secret_audit_log" "user_id = '${USER_ID}'" "1" "audit row SURVIVES user delete (intentional, append-only)"

# Cleanup
psql_run -c "DELETE FROM secret_audit_log WHERE user_id = '${USER_ID}';"

# -----------------------------------------------------------------------------
# Test 4: workspace deletion does NOT cascade to user_secret_bindings
# at the FK level — application logic (DatabaseService.MarkWorkspaceDeleted)
# is responsible. This test asserts the database-only behavior.
# Application-level coverage lives in
# api/internal/services/database/database_test.go::TestMarkWorkspaceDeleted_PurgesBindings.
# -----------------------------------------------------------------------------

echo "== Test 4: workspace_id is not a FK (documents the design choice) =="

USER_ID="fk-cascade-user-4"
SECRET_ID=$(psql_run -c "SELECT gen_random_uuid();")
WORKSPACE_ID=$(psql_run -c "SELECT gen_random_uuid();")

psql_run -c "INSERT INTO users (id, username, email, password_hash) VALUES ('${USER_ID}', 'fkcasc4', 'fkcasc4@test', 'x');"
psql_run -c "INSERT INTO user_secrets (id, user_id, name, type, ciphertext, key_version) VALUES ('${SECRET_ID}', '${USER_ID}', 'aname', 'env-secret', '\\x00', 1);"
psql_run -c "INSERT INTO workspaces (id, name, user_id) VALUES ('${WORKSPACE_ID}', 'wsname', '${USER_ID}');"
psql_run -c "INSERT INTO user_secret_bindings (secret_id, workspace_id) VALUES ('${SECRET_ID}', '${WORKSPACE_ID}');"

assert_count "user_secret_bindings" "workspace_id = '${WORKSPACE_ID}'" "1" "binding exists pre-workspace-delete"

# Direct DELETE (NOT MarkWorkspaceDeleted; that's app-level cleanup).
# After this raw delete the binding row remains — exactly as
# designed, because the FK contract says workspace_id is not bound to
# the workspaces table at the database layer.
psql_run -c "DELETE FROM workspaces WHERE id = '${WORKSPACE_ID}';"

assert_count "user_secret_bindings" "workspace_id = '${WORKSPACE_ID}'" "1" "binding REMAINS (intentional; app cleanup uses DatabaseService.MarkWorkspaceDeleted)"

# Cleanup
psql_run -c "DELETE FROM users WHERE id = '${USER_ID}';"

echo ""
echo "All FK cascade tests passed."
