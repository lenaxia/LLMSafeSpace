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
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// newMockOrgStore builds a PgOrgStore backed by a sqlmock DB for unit testing
// the SSO store methods. Uses the repo-default regex query matcher; each
// expectation is a distinctive SQL fragment with regex metacharacters escaped.
func newMockOrgStore(t *testing.T) (*PgOrgStore, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	return NewPgOrgStore(db), mock, db
}

func ssoColumns() []string {
	return []string{"org_id", "oidc_discovery_url", "oidc_client_id", "oidc_client_secret",
		"claimed_domains", "verified_domains", "verification_token",
		"auto_provision", "group_role_mapping", "created_at", "updated_at"}
}

// Distinctive, escaped regex fragments matching each store query.
const (
	qGetSSO      = `FROM org_sso_configs WHERE org_id = \$1`
	qUpsertSSO   = `INSERT INTO org_sso_configs`
	qDeleteSSO   = `DELETE FROM org_sso_configs WHERE org_id = \$1`
	qFindByDom   = `WHERE \$1 = ANY \(c\.claimed_domains\)`
	qListDomains = `array_length\(c\.verified_domains, 1\) IS NOT NULL`
	qCountSSO    = `COUNT\(\*\) FROM org_sso_configs`
	qSetVerified = `verified_domains = array_append\(verified_domains, \$2\)`
	qRotateToken = `verification_token = \$2`
)

func TestGetSSOConfig_Found(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	now := time.Now()
	rows := sqlmock.NewRows(ssoColumns()).AddRow(
		"org-1", "https://idp/.well-known/openid-configuration", "client-abc", []byte("encrypted"),
		"{acme.com}", "{acme.com}", "tok-123", true, []byte(`{"admins":"admin"}`), now, now,
	)
	mock.ExpectQuery(qGetSSO).WithArgs("org-1").WillReturnRows(rows)

	cfg, err := store.GetSSOConfig(context.Background(), "org-1")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "org-1", cfg.OrgID)
	require.Equal(t, "client-abc", cfg.ClientID)
	require.Equal(t, []byte("encrypted"), cfg.ClientSecret)
	require.Equal(t, []string{"acme.com"}, cfg.ClaimedDomains)
	require.Equal(t, []string{"acme.com"}, cfg.VerifiedDomains)
	require.Equal(t, "tok-123", cfg.VerificationToken)
	require.True(t, cfg.AutoProvision)
	require.Equal(t, map[string]types.OrgRole{"admins": types.OrgRoleAdmin}, cfg.GroupRoleMapping)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSSOConfig_NotFound_ReturnsNil(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectQuery(qGetSSO).WithArgs("nope").WillReturnError(sql.ErrNoRows)

	cfg, err := store.GetSSOConfig(context.Background(), "nope")
	require.NoError(t, err)
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSSOConfig_DBError(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectQuery(qGetSSO).WithArgs("org-1").WillReturnError(errors.New("connection reset"))

	_, err := store.GetSSOConfig(context.Background(), "org-1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSSOConfig_GroupMappingInvalidRolesDropped(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	now := time.Now()
	rows := sqlmock.NewRows(ssoColumns()).AddRow(
		"org-1", "https://idp", "cid", []byte("enc"), "{}", "{}", "", true,
		[]byte(`{"admins":"admin","bogus":"superuser","devs":"member"}`), now, now,
	)
	mock.ExpectQuery(qGetSSO).WithArgs("org-1").WillReturnRows(rows)

	cfg, err := store.GetSSOConfig(context.Background(), "org-1")
	require.NoError(t, err)
	require.Equal(t, map[string]types.OrgRole{
		"admins": types.OrgRoleAdmin,
		"devs":   types.OrgRoleMember,
	}, cfg.GroupRoleMapping)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSSOConfig_Insert(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	cfg := &types.OrgSSOConfig{
		OrgID:            "org-1",
		DiscoveryURL:     "https://idp",
		ClientID:         "client-abc",
		ClientSecret:     []byte("encrypted"),
		ClaimedDomains:   []string{"acme.com"},
		VerifiedDomains:  []string{"acme.com"},
		AutoProvision:    true,
		GroupRoleMapping: map[string]types.OrgRole{"admins": types.OrgRoleAdmin},
		// VerificationToken empty → UpsertSSOConfig generates one (matched via AnyArg)
	}
	mock.ExpectExec(qUpsertSSO).
		WithArgs("org-1", "https://idp", "client-abc", []byte("encrypted"),
			`{"acme.com"}`, `{"acme.com"}`, sqlmock.AnyArg(),
			true, []byte(`{"admins":"admin"}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.UpsertSSOConfig(context.Background(), cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSSOConfig_PreservesExplicitTokenOnInsert(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	cfg := &types.OrgSSOConfig{
		OrgID:             "org-1",
		DiscoveryURL:      "https://idp",
		ClientID:          "cid",
		ClientSecret:      []byte("enc"),
		VerificationToken: "explicit-token-abc",
	}
	mock.ExpectExec(qUpsertSSO).
		WithArgs("org-1", "https://idp", "cid", []byte("enc"),
			`{}`, `{}`, "explicit-token-abc", false, []byte(`{}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.UpsertSSOConfig(context.Background(), cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSSOConfig_NilDomainsBecomesEmptyArray(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	cfg := &types.OrgSSOConfig{
		OrgID:        "org-1",
		DiscoveryURL: "https://idp",
		ClientID:     "cid",
		ClientSecret: []byte("enc"),
	}
	mock.ExpectExec(qUpsertSSO).
		WithArgs("org-1", "https://idp", "cid", []byte("enc"),
			`{}`, `{}`, sqlmock.AnyArg(), false, []byte(`{}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.UpsertSSOConfig(context.Background(), cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSSOConfig_DBError(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qUpsertSSO).WillReturnError(errors.New("write failed"))

	err := store.UpsertSSOConfig(context.Background(), &types.OrgSSOConfig{
		OrgID: "org-1", DiscoveryURL: "u", ClientID: "c", ClientSecret: []byte("x"),
	})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSSOConfig(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qDeleteSSO).WithArgs("org-1").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.DeleteSSOConfig(context.Background(), "org-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindSSOConfigByDomain_Found(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	now := time.Now()
	rows := sqlmock.NewRows(ssoColumns()).AddRow(
		"org-1", "https://idp", "cid", []byte("enc"), "{acme.com}", "{acme.com}", "tok", true, []byte(`{}`), now, now,
	)
	mock.ExpectQuery(qFindByDom).WithArgs("acme.com").WillReturnRows(rows)

	cfg, err := store.FindSSOConfigByDomain(context.Background(), "acme.com")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "org-1", cfg.OrgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindSSOConfigByDomain_NotFound(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectQuery(qFindByDom).WithArgs("unknown.com").WillReturnError(sql.ErrNoRows)

	cfg, err := store.FindSSOConfigByDomain(context.Background(), "unknown.com")
	require.NoError(t, err)
	require.Nil(t, cfg)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSSODomains_Multiple(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"slug", "name", "verified_domains"}).
		AddRow("acme", "Acme Corp", "{acme.com,acme.io}").
		AddRow("globex", "Globex", "{globex.com}")
	mock.ExpectQuery(qListDomains).WillReturnRows(rows)

	out, err := store.ListSSODomains(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, []types.SSODomain{
		{Domain: "@acme.com", OrgSlug: "acme", OrgName: "Acme Corp"},
		{Domain: "@acme.io", OrgSlug: "acme", OrgName: "Acme Corp"},
		{Domain: "@globex.com", OrgSlug: "globex", OrgName: "Globex"},
	}, out)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSSODomains_Empty(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectQuery(qListDomains).
		WillReturnRows(sqlmock.NewRows([]string{"slug", "name", "verified_domains"}))

	out, err := store.ListSSODomains(context.Background())
	require.NoError(t, err)
	require.Equal(t, []types.SSODomain{}, out)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSSODomains_NormalizesLeadingAtAndCase(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"slug", "name", "verified_domains"}).
		AddRow("acme", "Acme", "{@ACME.com}")
	mock.ExpectQuery(qListDomains).WillReturnRows(rows)

	out, err := store.ListSSODomains(context.Background())
	require.NoError(t, err)
	require.Equal(t, "@acme.com", out[0].Domain)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountSSOConfigs(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectQuery(qCountSSO).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	n, err := store.CountSSOConfigs(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- New: SetDomainVerified ---

func TestSetDomainVerified_PromotesClaimedDomain(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qSetVerified).WithArgs("org-1", "acme.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	promoted, err := store.SetDomainVerified(context.Background(), "org-1", "acme.com")
	require.NoError(t, err)
	require.True(t, promoted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetDomainVerified_Idempotent_AlreadyVerified(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	// Domain already in verified_domains → WHERE clause excludes it → 0 rows
	mock.ExpectExec(qSetVerified).WithArgs("org-1", "acme.com").
		WillReturnResult(sqlmock.NewResult(0, 0))

	promoted, err := store.SetDomainVerified(context.Background(), "org-1", "acme.com")
	require.NoError(t, err)
	require.False(t, promoted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetDomainVerified_NotClaimed_NoOp(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	// Domain not in claimed_domains → WHERE clause excludes it → 0 rows
	mock.ExpectExec(qSetVerified).WithArgs("org-1", "unknown.com").
		WillReturnResult(sqlmock.NewResult(0, 0))

	promoted, err := store.SetDomainVerified(context.Background(), "org-1", "unknown.com")
	require.NoError(t, err)
	require.False(t, promoted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetDomainVerified_DBError(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qSetVerified).WillReturnError(errors.New("write failed"))

	_, err := store.SetDomainVerified(context.Background(), "org-1", "acme.com")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- New: RotateVerificationToken ---

func TestRotateVerificationToken_Success(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qRotateToken).WithArgs("org-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	token, err := store.RotateVerificationToken(context.Background(), "org-1")
	require.NoError(t, err)
	require.Len(t, token, 32, "token must be 32 hex chars")
	require.Regexp(t, `^[0-9a-f]{32}$`, token)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateVerificationToken_NoSSOConfig(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	// 0 rows affected → org has no SSO config
	mock.ExpectExec(qRotateToken).WithArgs("ghost-org", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := store.RotateVerificationToken(context.Background(), "ghost-org")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sso config")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateVerificationToken_DBError(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	mock.ExpectExec(qRotateToken).WillReturnError(errors.New("connection lost"))

	_, err := store.RotateVerificationToken(context.Background(), "org-1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateVerificationToken_GeneratesDifferentTokens(t *testing.T) {
	// Tests the helper directly since randomness is a property of the
	// generator, not the SQL path.
	t1 := randomVerificationToken()
	t2 := randomVerificationToken()
	require.NotEqual(t, t1, t2, "consecutive tokens must differ")
	require.Len(t, t1, 32)
	require.Len(t, t2, 32)
	require.Regexp(t, `^[0-9a-f]{32}$`, t1)
}
