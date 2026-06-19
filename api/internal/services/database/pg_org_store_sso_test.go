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

	"github.com/lenaxia/llmsafespace/pkg/types"
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
		"claimed_domains", "auto_provision", "group_role_mapping", "created_at", "updated_at"}
}

// Distinctive, escaped regex fragments matching each store query.
const (
	qGetSSO      = `FROM org_sso_configs WHERE org_id = \$1`
	qUpsertSSO   = `INSERT INTO org_sso_configs`
	qDeleteSSO   = `DELETE FROM org_sso_configs WHERE org_id = \$1`
	qFindByDom   = `WHERE \$1 = ANY \(c\.claimed_domains\)`
	qListDomains = `array_length\(c\.claimed_domains, 1\) IS NOT NULL`
	qCountSSO    = `COUNT\(\*\) FROM org_sso_configs`
)

func TestGetSSOConfig_Found(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	now := time.Now()
	rows := sqlmock.NewRows(ssoColumns()).AddRow(
		"org-1", "https://idp/.well-known/openid-configuration", "client-abc", []byte("encrypted"),
		"{acme.com}", true, []byte(`{"admins":"admin"}`), now, now,
	)
	mock.ExpectQuery(qGetSSO).WithArgs("org-1").WillReturnRows(rows)

	cfg, err := store.GetSSOConfig(context.Background(), "org-1")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "org-1", cfg.OrgID)
	require.Equal(t, "client-abc", cfg.ClientID)
	require.Equal(t, []byte("encrypted"), cfg.ClientSecret)
	require.Equal(t, []string{"acme.com"}, cfg.ClaimedDomains)
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
		"org-1", "https://idp", "cid", []byte("enc"), "{}", true,
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
		AutoProvision:    true,
		GroupRoleMapping: map[string]types.OrgRole{"admins": types.OrgRoleAdmin},
	}
	mock.ExpectExec(qUpsertSSO).
		WithArgs("org-1", "https://idp", "client-abc", []byte("encrypted"),
			`{"acme.com"}`, true, []byte(`{"admins":"admin"}`)).
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
		WithArgs("org-1", "https://idp", "cid", []byte("enc"), `{}`, false, []byte(`{}`)).
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
		"org-1", "https://idp", "cid", []byte("enc"), "{acme.com}", true, []byte(`{}`), now, now,
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

	rows := sqlmock.NewRows([]string{"slug", "name", "claimed_domains"}).
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
		WillReturnRows(sqlmock.NewRows([]string{"slug", "name", "claimed_domains"}))

	out, err := store.ListSSODomains(context.Background())
	require.NoError(t, err)
	require.Equal(t, []types.SSODomain{}, out)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSSODomains_NormalizesLeadingAtAndCase(t *testing.T) {
	store, mock, db := newMockOrgStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"slug", "name", "claimed_domains"}).
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
