#!/usr/bin/env bash
# hack/migration-orphan-org-id-backfill.sh
#
# Tests that migration 000044 correctly backfills:
#   1. workspaces.org_id for workspaces whose owner is now in an org but
#      whose org_id is NULL — the deployment-timing orphan from PR #209
#      auto-attribution and PR #228 D4 owner-migration.
#   2. workspace_credential_bindings for org credentials that exist but
#      were never auto-bound to those orphan workspaces because the
#      previous BindCredentialToAllOrgWorkspaces query filters on
#      w.org_id = orgID and skipped NULL-org_id rows.
#
# Strategy: apply migrations through 000043, seed orphan rows that
# reproduce the production state observed on 2026-06-25 (workspace
# d95b6751 had org_id but a847faa5 did not despite the same owner
# being in the same org), apply migration 000044, verify the backfill
# happened.
#
# Required env vars: PGHOST, PGUSER, PGPASSWORD, PGDATABASE.

set -euo pipefail

cd "$(dirname "$0")/.."

PSQL="psql -v ON_ERROR_STOP=1"

echo "== migration-orphan-org-id-backfill: resetting public schema =="
$PSQL -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

echo "== applying migrations 000001–000043 =="
for f in $(ls api/migrations/00000[1-9]*.up.sql api/migrations/00001[0-9]*.up.sql api/migrations/0000[2-3][0-9]*.up.sql api/migrations/000040*.up.sql api/migrations/000041*.up.sql api/migrations/000042*.up.sql api/migrations/000043*.up.sql 2>/dev/null | sort); do
    echo "  $f"
    $PSQL -f "$f" >/dev/null
done

echo "== seeding orphan + control rows =="

# Three users: owner of orphan workspace, control user not in any org,
# and a user who's in a soft-deleted org.
$PSQL -c "INSERT INTO users (id, username, email, password_hash, role) VALUES
  ('user-org-owner', 'owner', 'owner@test.example', 'h', 'user'),
  ('user-no-org',    'solo',  'solo@test.example',  'h', 'user'),
  ('user-dead-org',  'ghost', 'ghost@test.example', 'h', 'user')
ON CONFLICT (id) DO NOTHING;"

# Org with the owner as admin.
ORG_ID='10000000-0000-0000-0000-000000000001'
$PSQL -c "INSERT INTO organizations (id, name, slug, created_by, status, plan_id, subscription_status)
  VALUES ('$ORG_ID', 'Test Org', 'test-org', 'user-org-owner', 'active', 'free', 'inactive')
  ON CONFLICT DO NOTHING;"
$PSQL -c "INSERT INTO org_memberships (org_id, user_id, role)
  VALUES ('$ORG_ID', 'user-org-owner', 'admin')
  ON CONFLICT DO NOTHING;"

# Soft-deleted org with the ghost user as a stale member (memberships
# survive soft-delete; the migration must NOT attribute their workspace
# to a dead org).
DEAD_ORG_ID='20000000-0000-0000-0000-000000000002'
$PSQL -c "INSERT INTO organizations (id, name, slug, created_by, status, plan_id, subscription_status, deleted_at)
  VALUES ('$DEAD_ORG_ID', 'Dead Org', 'dead-org', 'user-dead-org', 'active', 'free', 'inactive', NOW())
  ON CONFLICT DO NOTHING;"
$PSQL -c "INSERT INTO org_memberships (org_id, user_id, role)
  VALUES ('$DEAD_ORG_ID', 'user-dead-org', 'admin')
  ON CONFLICT DO NOTHING;"

# Orphan workspace: owner is in org, but org_id is NULL (the bug).
ORPHAN_WS='a0000000-0000-0000-0000-000000000001'
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version, org_id)
  VALUES ('$ORPHAN_WS', 'ws-orphan-pre-d4', 'user-org-owner', 'latest', '1.0', NULL);"

# Healthy workspace: same owner, org_id correctly set (created post-D4).
HEALTHY_WS='a0000000-0000-0000-0000-000000000002'
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version, org_id)
  VALUES ('$HEALTHY_WS', 'ws-healthy', 'user-org-owner', 'latest', '1.0', '$ORG_ID');"

# Control: solo user's personal workspace. Must NOT be touched (no org).
SOLO_WS='b0000000-0000-0000-0000-000000000001'
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version, org_id)
  VALUES ('$SOLO_WS', 'ws-solo', 'user-no-org', 'latest', '1.0', NULL);"

# Soft-deleted workspace: owner in org, org_id NULL, but deleted. Must NOT be touched.
DELETED_WS='c0000000-0000-0000-0000-000000000001'
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version, org_id, deleted_at)
  VALUES ('$DELETED_WS', 'ws-deleted', 'user-org-owner', 'latest', '1.0', NULL, NOW());"

# Ghost-org workspace: ghost user in soft-deleted org, NULL org_id.
# The migration must NOT attribute this to the dead org.
GHOST_WS='c0000000-0000-0000-0000-000000000002'
$PSQL -c "INSERT INTO workspaces (id, name, user_id, image_tag, agent_version, org_id)
  VALUES ('$GHOST_WS', 'ws-ghost', 'user-dead-org', 'latest', '1.0', NULL);"

# Org credential. Pre-fix this would be auto-bound to the healthy
# workspace (via SeedWorkspaceCredentials at creation) but not to the
# orphan (because BindCredentialToAllOrgWorkspaces filters w.org_id).
ORG_CRED='d0000000-0000-0000-0000-000000000001'
$PSQL -c "INSERT INTO provider_credentials (id, owner_type, owner_id, name, provider, ciphertext, key_version)
  VALUES ('$ORG_CRED', 'org', '$ORG_ID', 'org-shared', 'custom', '\x00', 1);"

# Second org credential for the SAME org. Multi-credential orgs exercise
# the JOIN provider_credentials across multiple rows; without this seed
# a regression like an accidental LIMIT 1 / DISTINCT ON would not be
# caught (review pass 1 finding).
ORG_CRED_2='d0000000-0000-0000-0000-000000000002'
$PSQL -c "INSERT INTO provider_credentials (id, owner_type, owner_id, name, provider, ciphertext, key_version)
  VALUES ('$ORG_CRED_2', 'org', '$ORG_ID', 'org-shared-2', 'opencode-zen', '\x00', 1);"

# Org cred is bound to the healthy ws but NOT to the orphan ws (the
# downstream consequence of the org_id NULL state).
$PSQL -c "INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
  VALUES ('$ORG_CRED', '$HEALTHY_WS', 'auto', 5);"

# ORG_CRED_2 is also bound to the healthy ws (mirrors how the app
# auto-binds every org credential at workspace-create) but NOT the
# orphan. After the migration, BOTH org creds must end up bound to
# the (formerly-)orphan workspace — that's the multi-credential
# invariant.
$PSQL -c "INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
  VALUES ('$ORG_CRED_2', '$HEALTHY_WS', 'auto', 5);"

echo "== verifying seed state matches the bug =="
ORPHAN_NULL=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$ORPHAN_WS' AND org_id IS NULL;")
HEALTHY_OK=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$HEALTHY_WS' AND org_id='$ORG_ID';")
ORPHAN_HAS_BINDING=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$ORPHAN_WS' AND credential_id='$ORG_CRED';")
HEALTHY_HAS_BINDING=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$HEALTHY_WS' AND credential_id='$ORG_CRED';")
echo "  orphan org_id IS NULL: $ORPHAN_NULL (expect 1)"
echo "  healthy org_id set:    $HEALTHY_OK (expect 1)"
echo "  orphan binding exists: $ORPHAN_HAS_BINDING (expect 0 — the bug)"
echo "  healthy binding exists: $HEALTHY_HAS_BINDING (expect 1)"
if [ "$ORPHAN_NULL" != "1" ] || [ "$HEALTHY_OK" != "1" ] || [ "$ORPHAN_HAS_BINDING" != "0" ] || [ "$HEALTHY_HAS_BINDING" != "1" ]; then
    echo "FAIL: seed state does not reproduce the bug"
    exit 1
fi

echo "== applying migration 000044 =="
$PSQL -f api/migrations/000044_backfill_workspace_org_id_orphans.up.sql

echo "== verifying backfill =="

# Orphan workspace should now have org_id set.
AFTER_ORPHAN=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$ORPHAN_WS' AND org_id='$ORG_ID';")
if [ "$AFTER_ORPHAN" != "1" ]; then
    echo "FAIL: orphan workspace org_id not backfilled"
    exit 1
fi
echo "  OK: orphan workspace org_id backfilled to $ORG_ID"

# Healthy workspace org_id unchanged.
AFTER_HEALTHY=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$HEALTHY_WS' AND org_id='$ORG_ID';")
if [ "$AFTER_HEALTHY" != "1" ]; then
    echo "FAIL: healthy workspace org_id was disturbed"
    exit 1
fi
echo "  OK: healthy workspace untouched"

# Solo user's workspace untouched (still NULL — they're not in any org).
AFTER_SOLO=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$SOLO_WS' AND org_id IS NULL;")
if [ "$AFTER_SOLO" != "1" ]; then
    echo "FAIL: solo user's personal workspace was incorrectly attributed to an org"
    exit 1
fi
echo "  OK: non-org user's personal workspace remains personal"

# Soft-deleted workspace untouched.
AFTER_DELETED=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$DELETED_WS' AND org_id IS NULL;")
if [ "$AFTER_DELETED" != "1" ]; then
    echo "FAIL: soft-deleted workspace was modified"
    exit 1
fi
echo "  OK: soft-deleted workspace untouched"

# Ghost-org workspace: user is a member of a SOFT-DELETED org. Migration
# must NOT attribute the workspace to a dead org. This catches Attack 14
# from the stress-test pass: org_memberships rows survive when an org
# is soft-deleted, so a naive UPDATE join would happily pick the dead
# org_id and write it back into a live workspace.
AFTER_GHOST=$($PSQL -tAc "SELECT count(*) FROM workspaces WHERE id='$GHOST_WS' AND org_id IS NULL;")
if [ "$AFTER_GHOST" != "1" ]; then
    echo "FAIL: workspace owned by user-in-soft-deleted-org was attributed to the dead org"
    exit 1
fi
echo "  OK: stale membership in soft-deleted org did not leak into a workspace"

# Orphan workspace now has the org credential binding.
AFTER_ORPHAN_BINDING=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$ORPHAN_WS' AND credential_id='$ORG_CRED';")
if [ "$AFTER_ORPHAN_BINDING" != "1" ]; then
    echo "FAIL: org credential not bound to orphan workspace after backfill"
    exit 1
fi
echo "  OK: org credential bound to (formerly-)orphan workspace"

# Orphan also receives the SECOND org credential's binding. Multi-
# credential orgs need every org cred bound to every formerly-orphan
# workspace — a regression like accidental LIMIT 1 / DISTINCT ON
# would only land one of the two.
AFTER_ORPHAN_BINDING_2=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$ORPHAN_WS' AND credential_id='$ORG_CRED_2';")
if [ "$AFTER_ORPHAN_BINDING_2" != "1" ]; then
    echo "FAIL: second org credential not bound to orphan workspace (multi-credential org regression)"
    exit 1
fi
echo "  OK: second org credential also bound to orphan workspace"

# Healthy binding unchanged (no duplicate from idempotent re-run).
AFTER_HEALTHY_BINDING=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$HEALTHY_WS' AND credential_id='$ORG_CRED';")
if [ "$AFTER_HEALTHY_BINDING" != "1" ]; then
    echo "FAIL: healthy binding count is $AFTER_HEALTHY_BINDING (expected 1)"
    exit 1
fi
echo "  OK: healthy binding survives without duplication"

# Solo user's workspace got NO org bindings.
SOLO_BINDING_COUNT=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$SOLO_WS';")
if [ "$SOLO_BINDING_COUNT" != "0" ]; then
    echo "FAIL: solo workspace received bindings ($SOLO_BINDING_COUNT)"
    exit 1
fi
echo "  OK: solo workspace received no spurious bindings"

# Soft-deleted workspace got NO bindings.
DELETED_BINDING_COUNT=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$DELETED_WS';")
if [ "$DELETED_BINDING_COUNT" != "0" ]; then
    echo "FAIL: soft-deleted workspace received bindings ($DELETED_BINDING_COUNT)"
    exit 1
fi
echo "  OK: soft-deleted workspace received no bindings"

echo "== verifying idempotency =="
$PSQL -f api/migrations/000044_backfill_workspace_org_id_orphans.up.sql

# Running again must not duplicate bindings or change rows.
RERUN_BINDING=$($PSQL -tAc "SELECT count(*) FROM workspace_credential_bindings WHERE workspace_id='$ORPHAN_WS' AND credential_id='$ORG_CRED';")
if [ "$RERUN_BINDING" != "1" ]; then
    echo "FAIL: re-running migration produced duplicates or removed the binding"
    exit 1
fi
echo "  OK: migration is idempotent"

echo "== ALL CHECKS PASS =="
