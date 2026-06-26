#!/usr/bin/env bash
# hack/migration-jwt-sessions.sh
#
# Tests migration 000045 (Epic 56 — durable DEK for JWT sessions):
#   1. The jwt_sessions table is created with the expected schema.
#   2. PRIMARY KEY (jti) prevents duplicate session rows.
#   3. FK on user_id is ON DELETE CASCADE — deleting a user cleans
#      up their durable DEK rows.
#   4. The expires_at index is present (used by the janitor to prune
#      rows past JWT expiration).
#   5. The down migration drops the table cleanly.
#
# Strategy: apply migrations through 000045, seed two users and two
# sessions, verify constraints + index presence, exercise the FK
# cascade, then apply the down to verify clean removal.
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.
#
# Design doc: design/stories/epic-56-durable-dek-session/README.md

set -euo pipefail

cd "$(dirname "$0")/.."

PSQL="psql -v ON_ERROR_STOP=1"

echo "== migration-jwt-sessions: resetting public schema =="
$PSQL -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

echo "== applying migrations 000001-000045 =="
for f in $(ls api/migrations/00000[1-9]*.up.sql api/migrations/00001[0-9]*.up.sql api/migrations/0000[2-3][0-9]*.up.sql api/migrations/000040*.up.sql api/migrations/000041*.up.sql api/migrations/000042*.up.sql api/migrations/000043*.up.sql api/migrations/000044*.up.sql api/migrations/000045*.up.sql 2>/dev/null | sort); do
    echo "  $f"
    $PSQL -f "$f" >/dev/null
done

echo "== verifying jwt_sessions schema =="

COLS=$($PSQL -tAc "SELECT count(*) FROM information_schema.columns WHERE table_name = 'jwt_sessions' AND column_name IN ('jti','user_id','wrapped_dek','kek_salt','created_at','expires_at');")
if [ "$COLS" != "6" ]; then
    echo "FAIL: jwt_sessions missing columns (expected 6, got $COLS)"
    exit 1
fi
echo "  OK: 6 expected columns present"

# Verify PRIMARY KEY on jti.
PK=$($PSQL -tAc "SELECT count(*) FROM information_schema.table_constraints WHERE table_name = 'jwt_sessions' AND constraint_type = 'PRIMARY KEY';")
if [ "$PK" != "1" ]; then
    echo "FAIL: jwt_sessions missing PRIMARY KEY"
    exit 1
fi
echo "  OK: PRIMARY KEY present"

# Verify FK on user_id with ON DELETE CASCADE.
FK_DELETE_RULE=$($PSQL -tAc "SELECT rc.delete_rule FROM information_schema.referential_constraints rc JOIN information_schema.key_column_usage kcu ON rc.constraint_name = kcu.constraint_name WHERE kcu.table_name = 'jwt_sessions' AND kcu.column_name = 'user_id';")
if [ "$FK_DELETE_RULE" != "CASCADE" ]; then
    echo "FAIL: jwt_sessions.user_id FK delete rule is '$FK_DELETE_RULE' (expected CASCADE)"
    exit 1
fi
echo "  OK: FK ON DELETE CASCADE on user_id"

# Verify expires_at index exists (the janitor scans on this).
EXP_IDX=$($PSQL -tAc "SELECT count(*) FROM pg_indexes WHERE tablename = 'jwt_sessions' AND indexname = 'idx_jwt_sessions_expires_at';")
if [ "$EXP_IDX" != "1" ]; then
    echo "FAIL: idx_jwt_sessions_expires_at missing"
    exit 1
fi
echo "  OK: expires_at index present"

# Verify user_id index exists (used by RevokeAllUserSessions to
# delete a user's rows efficiently).
USER_IDX=$($PSQL -tAc "SELECT count(*) FROM pg_indexes WHERE tablename = 'jwt_sessions' AND indexname = 'idx_jwt_sessions_user_id';")
if [ "$USER_IDX" != "1" ]; then
    echo "FAIL: idx_jwt_sessions_user_id missing"
    exit 1
fi
echo "  OK: user_id index present"

echo "== exercising constraints =="

# Seed two users.
$PSQL -c "INSERT INTO users (id, username, email, password_hash, role) VALUES
  ('user-jwt-1', 'jwt1', 'jwt1@test.example', 'h', 'user'),
  ('user-jwt-2', 'jwt2', 'jwt2@test.example', 'h', 'user');"

# Seed a jwt_sessions row.
JTI_1='11111111-1111-1111-1111-111111111111'
$PSQL -c "INSERT INTO jwt_sessions (jti, user_id, wrapped_dek, kek_salt, expires_at)
  VALUES ('$JTI_1', 'user-jwt-1', '\\x00', '\\x00', NOW() + INTERVAL '24 hours');"

# Duplicate jti must be rejected.
set +e
$PSQL -c "INSERT INTO jwt_sessions (jti, user_id, wrapped_dek, kek_salt, expires_at)
  VALUES ('$JTI_1', 'user-jwt-2', '\\x00', '\\x00', NOW() + INTERVAL '24 hours');" 2>/dev/null
DUP_RESULT=$?
set -e
if [ "$DUP_RESULT" = "0" ]; then
    echo "FAIL: duplicate jti was accepted (expected unique-violation)"
    exit 1
fi
echo "  OK: duplicate jti rejected by PRIMARY KEY"

# Insert another for user-jwt-2 (different jti — should succeed).
JTI_2='22222222-2222-2222-2222-222222222222'
$PSQL -c "INSERT INTO jwt_sessions (jti, user_id, wrapped_dek, kek_salt, expires_at)
  VALUES ('$JTI_2', 'user-jwt-2', '\\x00', '\\x00', NOW() + INTERVAL '24 hours');"

# Verify FK CASCADE: deleting user-jwt-1 deletes their session row.
$PSQL -c "DELETE FROM users WHERE id = 'user-jwt-1';"
REMAINING=$($PSQL -tAc "SELECT count(*) FROM jwt_sessions WHERE user_id = 'user-jwt-1';")
if [ "$REMAINING" != "0" ]; then
    echo "FAIL: FK CASCADE did not remove user-jwt-1's session rows ($REMAINING remain)"
    exit 1
fi
echo "  OK: FK CASCADE removes session rows on user delete"

# user-jwt-2's row must survive.
REMAINING_2=$($PSQL -tAc "SELECT count(*) FROM jwt_sessions WHERE user_id = 'user-jwt-2';")
if [ "$REMAINING_2" != "1" ]; then
    echo "FAIL: unrelated user-jwt-2's session row was disturbed ($REMAINING_2 rows)"
    exit 1
fi
echo "  OK: unrelated user's session row survives"

echo "== exercising down migration =="
$PSQL -f api/migrations/000045_jwt_sessions.down.sql

TABLE_GONE=$($PSQL -tAc "SELECT count(*) FROM information_schema.tables WHERE table_name = 'jwt_sessions';")
if [ "$TABLE_GONE" != "0" ]; then
    echo "FAIL: jwt_sessions table still exists after down migration"
    exit 1
fi
echo "  OK: down migration drops the table cleanly"

# Re-applying the up after down must succeed (idempotency in the
# sense that the migration can run on a fresh schema).
$PSQL -f api/migrations/000045_jwt_sessions.up.sql
TABLE_BACK=$($PSQL -tAc "SELECT count(*) FROM information_schema.tables WHERE table_name = 'jwt_sessions';")
if [ "$TABLE_BACK" != "1" ]; then
    echo "FAIL: up migration could not re-create after down"
    exit 1
fi
echo "  OK: up + down + up cycle succeeds"

echo "== ALL CHECKS PASS =="
