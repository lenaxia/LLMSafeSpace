// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/testharness"
)

// TestIntegration_ListAllUsers_UserWithoutOrgMembership is the regression test
// for the "failed to list users" 500 in the platform-admin Users tab.
//
// ListAllUsers LEFT JOINs org_memberships and does COALESCE(m.org_id, ”).
// org_memberships.org_id is a UUID column, so COALESCE(UUID, ”) forces
// Postgres to coerce the ” text literal to uuid — which fails with
// "invalid input syntax for type uuid" whenever the LEFT JOIN yields a NULL
// org_id (i.e. any user with no org membership). This test seeds exactly
// that shape: one user in an org, one user with no membership. Without the
// ::text cast on org_id the query errors; with it, both rows return.
func TestIntegration_ListAllUsers_UserWithoutOrgMembership(t *testing.T) {
	h := testharness.New(t)
	pool := h.Pool()
	ctx := h.NewContext()
	svc := &Service{DB: h.SQLDB()}

	marker := h.ID()
	userInOrg := "int-" + marker + "-in-org"
	userNoOrg := "int-" + marker + "-no-org"
	orgSlug := "intorg-" + marker

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM org_memberships WHERE user_id IN ($1, $2)", userInOrg, userNoOrg)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id IN ($1, $2)", userInOrg, userNoOrg)
		_, _ = pool.Exec(ctx, "DELETE FROM organizations WHERE slug = $1", orgSlug)
	}
	cleanup()
	t.Cleanup(cleanup)

	const fakeHash = "$2a$12$placeholderhashplaceholderhashplaceholderhash"
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password_hash, status) VALUES
		($1, $1, $2, $3, 'active'),
		($4, $4, $5, $6, 'active')
	`,
		userInOrg, userInOrg+"@example.test", fakeHash,
		userNoOrg, userNoOrg+"@example.test", fakeHash,
	)
	require.NoError(t, err, "seed two users")

	var orgID string
	err = pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, status, plan_id, subscription_status)
		VALUES ('Int Test Org', $1, 'active', 'free', 'active')
		RETURNING id
	`, orgSlug).Scan(&orgID)
	require.NoError(t, err, "seed one organization")

	_, err = pool.Exec(ctx, `
		INSERT INTO org_memberships (org_id, user_id, role)
		VALUES ($1, $2, 'admin')
	`, orgID, userInOrg)
	require.NoError(t, err, "seed membership for the in-org user only")

	users, _, err := svc.ListAllUsers(ctx, 200, 0, nil)
	require.NoError(t, err, "ListAllUsers must not error when a user has no org membership")

	var foundInOrg, foundNoOrg bool
	for _, u := range users {
		if u.ID == userInOrg {
			foundInOrg = true
			assert.Equal(t, orgID, u.OrgID, "in-org user should carry org id")
			assert.Equal(t, 1, u.OrgCount)
		}
		if u.ID == userNoOrg {
			foundNoOrg = true
			assert.Equal(t, "", u.OrgID, "no-org user should have empty OrgID")
			assert.Equal(t, 0, u.OrgCount)
		}
	}
	assert.True(t, foundInOrg, "seeded in-org user must appear in list")
	assert.True(t, foundNoOrg, "seeded no-org user must appear in list")
}
