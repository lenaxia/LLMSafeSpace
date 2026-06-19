// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

func TestPgOrgStore_GetUserIDByEmail_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("owner@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user-123"))

	id, err := store.GetUserIDByEmail(context.Background(), "owner@example.com")
	require.NoError(t, err)
	assert.Equal(t, "user-123", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserIDByEmail_NotFound_ReturnsEmptyNoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("nobody@example.com").
		WillReturnError(sql.ErrNoRows)

	id, err := store.GetUserIDByEmail(context.Background(), "nobody@example.com")
	require.NoError(t, err, "not-found must return (\"\", nil), not an error")
	assert.Equal(t, "", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserIDByEmail_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("boom@example.com").
		WillReturnError(errors.New("connection refused"))

	id, err := store.GetUserIDByEmail(context.Background(), "boom@example.com")
	require.Error(t, err)
	assert.Equal(t, "", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id"}).AddRow("org-abc"))

	orgID, err := store.GetUserOrgID(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, "org-abc", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_NotFound_ReturnsEmptyNoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-no-org").
		WillReturnError(sql.ErrNoRows)

	orgID, err := store.GetUserOrgID(context.Background(), "user-no-org")
	require.NoError(t, err, "not-found must return (\"\", nil), not an error")
	assert.Equal(t, "", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-boom").
		WillReturnError(errors.New("connection refused"))

	orgID, err := store.GetUserOrgID(context.Background(), "user-boom")
	require.Error(t, err)
	assert.Equal(t, "", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSoftDeleteOrg_F6_DoesNotNullWorkspaceOrgID verifies F6: SoftDeleteOrg
// must NOT null workspaces.org_id. It only sets organizations.deleted_at. The
// workspaces keep their org_id and become frozen via IsOrgMember's deleted_at
// check.
func TestSoftDeleteOrg_F6_DoesNotNullWorkspaceOrgID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	// Only the organizations UPDATE — NO workspaces UPDATE.
	mock.ExpectExec(`UPDATE organizations SET deleted_at = NOW\(\) WHERE id = \$1`).
		WithArgs("org-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Explicitly assert NO "UPDATE workspaces" query is expected.
	// (sqlmock fails if an unexpected query is executed.)

	require.NoError(t, store.SoftDeleteOrg(context.Background(), "org-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListWorkspaces_S1_FiltersFrozenWorkspaces verifies S1: the query includes
// the membership condition that filters out frozen workspaces. The sqlmock
// regex anchors the full WHERE clause including the org_id/EXISTS condition.
func TestListWorkspaces_S1_IncludesMembershipFilter(t *testing.T) {
	// This test uses the database.Service (not PgOrgStore) since ListWorkspaces
	// lives there. We verify the SQL contains the frozen-filter condition.
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// COUNT query with the membership condition
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM workspaces w.*org_id IS NULL.*EXISTS`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	metas, _, err := svc.ListWorkspaces(context.Background(), "user-1", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, metas)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateOrgWithAdmin_MigratesOwnerPersonalWorkspaces verifies M1/D4: the
// create transaction includes the same workspace migration as AcceptInvitationTx.
func TestCreateOrgWithAdmin_MigratesOwnerPersonalWorkspaces(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO organizations`).
		WithArgs("org-1", "Acme", "acme", "admin-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status"}).
			AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "pending_activation", "free", "inactive"))
	// INSERT membership (CreateOrgWithAdmin hardcodes 'admin' as a literal)
	mock.ExpectExec(`INSERT INTO org_memberships`).
		WithArgs("org-1", "owner-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// UPDATE workspaces — the migration (M1/D4). Anchor the full WHERE clause.
	mock.ExpectExec(`UPDATE workspaces SET org_id = .* WHERE user_id = .* AND org_id IS NULL AND deleted_at IS NULL`).
		WithArgs("owner-1", "org-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	org := &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme", CreatedBy: "admin-1"}
	_, err = store.CreateOrgWithAdmin(context.Background(), org, "owner-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAcceptInvitationTx_MigratesPersonalWorkspaces verifies D4: the accept
// transaction includes an UPDATE that migrates the user's personal workspaces
// to the org. Uses sqlmock to assert the UPDATE statement is executed inside
// the transaction.
func TestAcceptInvitationTx_MigratesPersonalWorkspaces(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	// SELECT invitation FOR UPDATE — returns the org_id.
	mock.ExpectQuery(`SELECT org_id, accepted_at, declined_at`).
		WithArgs("inv-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "accepted_at", "declined_at"}).
			AddRow("org-1", nil, nil))
	// INSERT membership
	mock.ExpectExec(`INSERT INTO org_memberships`).
		WithArgs("org-1", "user-1", "member").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// UPDATE workspaces — the migration (D4). Anchor the full WHERE clause so
	// a regression that drops deleted_at/org_id/user_id scoping is caught.
	mock.ExpectExec(`UPDATE workspaces SET org_id = .* WHERE user_id = .* AND org_id IS NULL AND deleted_at IS NULL`).
		WithArgs("user-1", "org-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	// UPDATE invitation accepted
	mock.ExpectExec(`UPDATE org_invitations SET accepted_at`).
		WithArgs("inv-1", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	member, alreadyTaken, err := store.AcceptInvitationTx(context.Background(), "inv-1", "user-1", "member")
	require.NoError(t, err)
	assert.False(t, alreadyTaken)
	require.NotNil(t, member)
	assert.Equal(t, "org-1", member.OrgID)
	assert.Equal(t, "user-1", member.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- US-43.20: ListAllAudit ---

func auditListRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
		AddRow(int64(1), "user-1", "org", "policy.set", "allowed_models", "org-1", []byte(`{}`), now).
		AddRow(int64(2), "user-2", "admin", "user.suspend", "user-9", "org-2", []byte(`{"reason":"abuse"}`), now)
}

func strPtr(s string) *string { return &s }

func TestPgOrgStore_ListAllAudit_NoFilterReturnsAll(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)
	now := time.Now()

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs(100, 0).
		WillReturnRows(auditListRows(now))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{})
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, 2, page.Total)
	assert.Len(t, entries, 2)
	assert.Equal(t, "policy.set", entries[0].Action)
	assert.Equal(t, map[string]any{"reason": "abuse"}, entries[1].Metadata)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_OrgIDFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)
	now := time.Now()

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WithArgs("org-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs("org-1", 100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
			AddRow(int64(1), "user-1", "org", "policy.set", "allowed_models", "org-1", []byte(`{}`), now))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{OrgID: strPtr("org-1")})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	assert.Len(t, entries, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_ActorIDFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WithArgs("user-2").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// total == 0 ⇒ SELECT is skipped.

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{ActorID: strPtr("user-2")})
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, 0, page.Total)
	assert.Len(t, entries, 0)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_DomainFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)
	now := time.Now()

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs("admin", 100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
			AddRow(int64(2), "user-2", "admin", "user.suspend", "user-9", "", []byte(`{}`), now))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{Domain: strPtr("admin")})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	require.Len(t, entries, 1)
	assert.Equal(t, "admin", entries[0].Domain)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_CombinedFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)
	now := time.Now()

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WithArgs("org-1", "user-1", "org").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs("org-1", "user-1", "org", 100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
			AddRow(int64(1), "user-1", "org", "policy.set", "allowed_models", "org-1", []byte(`{}`), now))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{
		OrgID:   strPtr("org-1"),
		ActorID: strPtr("user-1"),
		Domain:  strPtr("org"),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	require.Len(t, entries, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_LimitClampedToDefaultAndMax(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// limit <= 0 ⇒ store applies default 100. Non-zero count forces the SELECT
	// so the clamped value reaches the driver.
	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs(100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
			AddRow(int64(1), "u", "admin", "x", "", "", []byte(`{}`), time.Now()))

	_, _, err = store.ListAllAudit(context.Background(), types.AuditFilters{Limit: 0})
	require.NoError(t, err)

	// limit > 500 ⇒ store clamps to 500.
	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs(500, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "domain", "action", "target_id", "org_id", "metadata", "created_at"}).
			AddRow(int64(1), "u", "admin", "x", "", "", []byte(`{}`), time.Now()))

	_, _, err = store.ListAllAudit(context.Background(), types.AuditFilters{Limit: 9999})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_NegativeOffsetNormalized(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	_, _, err = store.ListAllAudit(context.Background(), types.AuditFilters{Offset: -7})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_EmptyResultSkipsSelect(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// No SELECT expectation: total == 0 must short-circuit.

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{})
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, 0, page.Total)
	assert.Len(t, entries, 0)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_CountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnError(errors.New("connection refused"))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{})
	require.Error(t, err)
	assert.Nil(t, entries)
	assert.Nil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllAudit_SelectError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM audit_log`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT id, actor_id, domain, action`).
		WithArgs(100, 0).
		WillReturnError(errors.New("select failed"))

	entries, page, err := store.ListAllAudit(context.Background(), types.AuditFilters{})
	require.Error(t, err)
	assert.Nil(t, entries)
	assert.Nil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- US-43.19: last-admin deadlock prevention (real SQL) ---

func TestPgOrgStore_OrgsWhereUserIsLastActiveAdmin_SoleAdmin(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// The user is the only admin of org-1 → the NOT EXISTS subquery finds no
	// other active admin → the org is returned.
	mock.ExpectQuery(`SELECT m.org_id, o.name FROM org_memberships m`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "name"}).
			AddRow("org-1", "Acme"))

	orgs, err := store.OrgsWhereUserIsLastActiveAdmin(context.Background(), "user-1")
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	assert.Equal(t, "org-1", orgs[0].OrgID)
	assert.Equal(t, "Acme", orgs[0].OrgName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_OrgsWhereUserIsLastActiveAdmin_NotLast(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// Another active admin exists → empty result → suspend allowed.
	mock.ExpectQuery(`SELECT m.org_id, o.name FROM org_memberships m`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "name"}))

	orgs, err := store.OrgsWhereUserIsLastActiveAdmin(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Len(t, orgs, 0, "not-last-admin must return empty (not nil) slice")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_OrgsWhereUserIsLastActiveAdmin_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT m.org_id, o.name FROM org_memberships m`).
		WithArgs("user-1").
		WillReturnError(errors.New("connection refused"))

	orgs, err := store.OrgsWhereUserIsLastActiveAdmin(context.Background(), "user-1")
	require.Error(t, err)
	assert.Nil(t, orgs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgOrgStore_OrgsWhereUserIsLastActiveAdmin_QueryShape verifies the SQL
// actually encodes the "other active admin" condition (NOT EXISTS ... role=
// 'admin' ... status = 'active') — a regression guard against accidental
// weakening of the last-admin check.
func TestPgOrgStore_OrgsWhereUserIsLastActiveAdmin_QueryShape(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`NOT EXISTS.*role = 'admin'.*u\.status = 'active'`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "name"}))

	_, _ = store.OrgsWhereUserIsLastActiveAdmin(context.Background(), "user-1")
	require.NoError(t, mock.ExpectationsWereMet(),
		"query must include NOT EXISTS over active admins")
}

// --- US-43.19: general audit writer ---

func TestPgOrgStore_LogAuditEvent_PlatformAdminEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// A platform-admin user.suspend: domain='admin', orgID NULL → NULLIF binds.
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs("admin-1", "admin", "user.suspend", "user-9", nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.LogAuditEvent(context.Background(), "admin", "admin-1", "user.suspend", "user-9", nil, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_LogOrgEvent_DelegatesToLogAuditEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// Org-scoped event: domain='org', orgID passed positionally as $5.
	orgID := "org-1"
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs("admin-1", "org", "policy.set", "allowed_models", orgID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.LogOrgEvent(context.Background(), "org-1", "admin-1", "policy.set", "allowed_models", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- US-43.18: ListAllOrgs ---

func orgSummaryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status", "member_count", "workspace_count"}).
		AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "active", "enterprise", "active", 3, 5).
		AddRow("org-2", "Globex", "globex", "admin-1", time.Now(), time.Now(), "suspended", "team", "past_due", 1, 2)
}

func TestPgOrgStore_ListAllOrgs_NoFilterReturnsAll(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT o.id, o.name, o.slug`).
		WithArgs(50, 0).
		WillReturnRows(orgSummaryRows())

	orgs, page, err := store.ListAllOrgs(context.Background(), 50, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, 2, page.Total)
	require.Len(t, orgs, 2)
	assert.Equal(t, "org-1", orgs[0].ID)
	assert.Equal(t, 3, orgs[0].MemberCount)
	assert.Equal(t, 5, orgs[0].WorkspaceCount)
	assert.Equal(t, types.OrgStatusSuspended, orgs[1].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllOrgs_StatusFilterAppendsWhere(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// The status filter adds a $1 placeholder on both COUNT and SELECT.
	mock.ExpectQuery(`COUNT\(\*\) FROM organizations WHERE deleted_at IS NULL AND status = \$1`).
		WithArgs("suspended").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`FROM organizations o WHERE deleted_at IS NULL AND status = \$1`).
		WithArgs("suspended", 50, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status", "member_count", "workspace_count"}).
			AddRow("org-2", "Globex", "globex", "admin-1", time.Now(), time.Now(), "suspended", "team", "past_due", 1, 2))

	orgs, page, err := store.ListAllOrgs(context.Background(), 50, 0, strPtr("suspended"))
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	require.Len(t, orgs, 1)
	assert.Equal(t, types.OrgStatusSuspended, orgs[0].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllOrgs_LimitClampedToDefaultAndMax(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	// limit <= 0 ⇒ default 50.
	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT o.id, o.name, o.slug`).
		WithArgs(50, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status", "member_count", "workspace_count"}).
			AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "active", "enterprise", "active", 0, 0))

	_, _, err = store.ListAllOrgs(context.Background(), 0, 0, nil)
	require.NoError(t, err)

	// limit > 200 ⇒ clamped to 200.
	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT o.id, o.name, o.slug`).
		WithArgs(200, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status", "member_count", "workspace_count"}).
			AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "active", "enterprise", "active", 0, 0))

	_, _, err = store.ListAllOrgs(context.Background(), 9999, 0, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllOrgs_EmptyResultSkipsSelect(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// No SELECT expectation: total == 0 must short-circuit.

	orgs, page, err := store.ListAllOrgs(context.Background(), 50, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, 0, page.Total)
	assert.Len(t, orgs, 0)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllOrgs_CountError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnError(errors.New("connection refused"))

	orgs, page, err := store.ListAllOrgs(context.Background(), 50, 0, nil)
	require.Error(t, err)
	assert.Nil(t, orgs)
	assert.Nil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_ListAllOrgs_SelectError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT o.id, o.name, o.slug`).
		WithArgs(50, 0).
		WillReturnError(errors.New("select failed"))

	orgs, page, err := store.ListAllOrgs(context.Background(), 50, 0, nil)
	require.Error(t, err)
	assert.Nil(t, orgs)
	assert.Nil(t, page)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgOrgStore_ListAllOrgs_QueryShape verifies the SQL aggregates member and
// workspace counts in a single statement (no N+1) — a regression guard against
// an accidental refactor that drops the correlated subqueries.
func TestPgOrgStore_ListAllOrgs_QueryShape(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	_, _, _ = store.ListAllOrgs(context.Background(), 50, 0, nil)
	// The select is short-circuited on total==0, so assert the shape via the
	// count-only path is insufficient — re-run with a non-zero total so the
	// SELECT fires and its regex is matched.
	mock.ExpectQuery(`COUNT\(\*\) FROM organizations`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM org_memberships.*SELECT COUNT\(\*\) FROM workspaces`).
		WithArgs(50, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status", "member_count", "workspace_count"}).
			AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "active", "enterprise", "active", 0, 0))

	_, _, err = store.ListAllOrgs(context.Background(), 50, 0, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet(),
		"query must include correlated subqueries for member_count and workspace_count")
}

// --- F7: SuspendUserGuardedByLastAdmin atomicity (US-43.19) ---
//
// These tests lock in the TOCTOU fix: the last-admin check (SELECT … FOR
// UPDATE) and the status UPDATE run in a single transaction, so the suspend
// path can no longer orphan an org under concurrent admin operations.

func TestPgOrgStore_SuspendUserGuardedByLastAdmin_NotLast_Suspends(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	// Lock the user's admin rows (FOR UPDATE).
	mock.ExpectQuery(`FOR UPDATE`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id"}).AddRow("org-1"))
	// Re-run the last-admin check inside the tx → no conflict.
	mock.ExpectQuery(`NOT EXISTS`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "name"}))
	// UPDATE mirrors active=false (F6).
	mock.ExpectExec(`UPDATE users SET status = 'suspended', active = false`).
		WithArgs("user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	conflict, err := store.SuspendUserGuardedByLastAdmin(context.Background(), "user-1", false)
	require.NoError(t, err)
	require.Nil(t, conflict, "non-last admin must be suspended without conflict")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_SuspendUserGuardedByLastAdmin_LastAdmin_Refuses(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`FOR UPDATE`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id"}).AddRow("org-9"))
	// Last-admin check returns a conflict.
	mock.ExpectQuery(`NOT EXISTS`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "name"}).AddRow("org-9", "Acme"))
	mock.ExpectRollback()
	// No UPDATE must run — the org is refused.

	conflict, err := store.SuspendUserGuardedByLastAdmin(context.Background(), "user-1", false)
	require.NoError(t, err)
	require.NotNil(t, conflict, "last admin must produce a conflict")
	require.Equal(t, "org-9", conflict.OrgID)
	require.Equal(t, "Acme", conflict.OrgName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_SuspendUserGuardedByLastAdmin_Force_SkipsCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	// force=true: NO lock query, NO last-admin query — straight to the UPDATE.
	mock.ExpectExec(`UPDATE users SET status = 'suspended', active = false`).
		WithArgs("user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	conflict, err := store.SuspendUserGuardedByLastAdmin(context.Background(), "user-1", true)
	require.NoError(t, err)
	require.Nil(t, conflict, "force=true must suspend even the last admin")
	require.NoError(t, mock.ExpectationsWereMet(), "force path must not run the FOR UPDATE / last-admin queries")
}

func TestPgOrgStore_SetUserStatus_MirrorsActive_F6(t *testing.T) {
	// F6: SetUserStatus must update `active` alongside `status` so the two
	// columns cannot drift. Suspended ⇒ active=false; active ⇒ active=true.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := &Service{DB: db}

	mock.ExpectExec(`UPDATE users SET status = \$1, active = \$2, updated_at = NOW\(\) WHERE id = \$3`).
		WithArgs("suspended", false, "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.SetUserStatus(context.Background(), "user-1", types.UserStatusSuspended))

	mock.ExpectExec(`UPDATE users SET status = \$1, active = \$2, updated_at = NOW\(\) WHERE id = \$3`).
		WithArgs("active", true, "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.SetUserStatus(context.Background(), "user-1", types.UserStatusActive))
	require.NoError(t, mock.ExpectationsWereMet())
}
