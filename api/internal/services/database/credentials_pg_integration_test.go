// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration
// +build integration

package database

// credentials_pg_integration_test.go — Integration tests against real
// Postgres for the credential_sets table.
//
// Why this file exists
// --------------------
// The unit tests in pkg/credentials/service_test.go use a mock store
// and never exercise the database/sql ↔ Postgres array driver path.
// The unit tests in api/internal/services/database/database_test.go
// use go-sqlmock, which also does not enforce native driver array
// decoding. As a result, this real driver-level bug shipped to
// production:
//
//   sql: Scan error on column index 5, name "model_allowlist":
//   unsupported Scan, storing driver.Value type string into type *[]string
//
// Cause: model_allowlist is TEXT[] in Postgres (migration 000006), but
// pgx-stdlib (database/sql interface) doesn't natively scan/bind a Go
// []string to/from Postgres arrays. The fix uses pq.Array to wrap
// every []string at the SQL boundary. This test confirms the fix
// against a real Postgres instance — go-sqlmock cannot.
//
// How to run locally
// ------------------
//   POSTGRES_HOST=localhost POSTGRES_PORT=5432 \
//   POSTGRES_USER=llmsafespace POSTGRES_PASSWORD=integration-test \
//   POSTGRES_DB=llmsafespace \
//     go test -tags=integration -run TestPg.*Credential ./api/internal/services/database/
//
// CI runs this via .github/workflows/secrets-integration.yml.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lenaxia/llmsafespace/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// openTestDB connects to the integration Postgres instance configured
// via POSTGRES_* env vars. Skips the test (rather than failing) when
// no DB is reachable, so `go test ./...` without integration tag
// stays green; CI sets the env vars and the test runs for real.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	host := envOrDefault("POSTGRES_HOST", "localhost")
	port := envOrDefault("POSTGRES_PORT", "5432")
	user := envOrDefault("POSTGRES_USER", "llmsafespace")
	pw := envOrDefault("POSTGRES_PASSWORD", "integration-test")
	dbName := envOrDefault("POSTGRES_DB", "llmsafespace")

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, pw, dbName)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Skipf("Skipping PG integration test: open failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("Skipping PG integration test: ping failed: %v", err)
	}
	return db
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newTestService wraps the Service with a Logger and Config that the
// production constructor would normally provide. We only exercise the
// methods that touch credential_sets, so a minimal struct is fine.
func newTestService(db *sql.DB) *Service {
	return &Service{DB: db}
}

// cleanupCredentialSets removes test rows so re-runs are deterministic.
// Filters by name prefix used by these tests; spares any rows the
// operator inserted manually for debugging.
func cleanupCredentialSets(t *testing.T, db *sql.DB, namePrefix string) {
	t.Helper()
	_, _ = db.ExecContext(context.Background(),
		`DELETE FROM credential_sets WHERE name LIKE $1`, namePrefix+"%")
}

// TestPgCredentialSet_CreateAndGet_RoundTripsModelAllowlist is the
// direct regression guard for the user-reported scan error. Pre-fix
// `pq.Array(&r.ModelAllowlist)` was missing, so Get returned:
//
//	sql: Scan error on column index 5, name "model_allowlist":
//	unsupported Scan, storing driver.Value type string into type *[]string
//
// Post-fix this round-trips a 3-element array cleanly.
func TestPgCredentialSet_CreateAndGet_RoundTripsModelAllowlist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-")

	svc := newTestService(db)
	ctx := context.Background()

	want := []string{"gpt-4", "gpt-4o", "claude-3"}
	id, err := svc.CreateCredentialSet(ctx,
		"pg-test-roundtrip",
		[]byte("encrypted-blob"),
		1,
		want,
		json.RawMessage(`"all"`),
		false,
	)
	require.NoError(t, err, "Create should bind []string via pq.Array; pre-fix this fails on the INSERT")
	require.NotEmpty(t, id)

	got, err := svc.GetCredentialSet(ctx, id)
	require.NoError(t, err, "Get should scan TEXT[] via pq.Array; pre-fix this is the user-reported error")
	require.NotNil(t, got)
	require.Equal(t, want, got.ModelAllowlist,
		"model_allowlist must round-trip exactly through Postgres")
}

// TestPgCredentialSet_CreateAndGet_EmptyAllowlist guards the empty-
// array case. Postgres distinguishes empty arrays (`{}`) from NULL;
// pq.Array(nil) marshals to `{}` (after the service.go nil→[] default).
func TestPgCredentialSet_CreateAndGet_EmptyAllowlist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-")

	svc := newTestService(db)
	ctx := context.Background()

	id, err := svc.CreateCredentialSet(ctx,
		"pg-test-empty",
		[]byte("blob"),
		1,
		[]string{}, // explicitly empty
		json.RawMessage(`"all"`),
		false,
	)
	require.NoError(t, err)

	got, err := svc.GetCredentialSet(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.ModelAllowlist, "empty array must scan as []string{}, not nil")
	require.Empty(t, got.ModelAllowlist)
}

// TestPgCredentialSet_List_ScansAllRows is the multi-row variant.
// Pre-fix every row in List failed at the rows.Scan call with the
// same driver error; the function returned an empty list AND an
// error to the caller (which the API handler logged as 500).
func TestPgCredentialSet_List_ScansAllRows(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-list-")

	svc := newTestService(db)
	ctx := context.Background()

	for i, allow := range [][]string{
		{"a", "b"},
		{},
		{"c", "d", "e", "f"},
	} {
		_, err := svc.CreateCredentialSet(ctx,
			fmt.Sprintf("pg-test-list-%d", i),
			[]byte("blob"),
			1,
			allow,
			json.RawMessage(`"all"`),
			false,
		)
		require.NoError(t, err)
	}

	all, err := svc.ListCredentialSets(ctx)
	require.NoError(t, err)

	// Filter to just our test rows (other operator-created rows may
	// exist in the shared integration DB).
	var ours []*credentials.CredentialSetRow
	for _, r := range all {
		if len(r.Name) > len("pg-test-list-") && r.Name[:len("pg-test-list-")] == "pg-test-list-" {
			ours = append(ours, r)
		}
	}
	require.Len(t, ours, 3)
	for _, r := range ours {
		require.NotNil(t, r.ModelAllowlist, "every row's ModelAllowlist must be non-nil after scan")
	}
}

// TestPgCredentialSet_Update_BindsModelAllowlist guards the UPDATE
// path. The dynamic SET-clause builder has its own pq.Array wrap;
// without it the UPDATE silently fails (or succeeds binding the
// wrong type) and the next SELECT reads stale data.
func TestPgCredentialSet_Update_BindsModelAllowlist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-update-")

	svc := newTestService(db)
	ctx := context.Background()

	id, err := svc.CreateCredentialSet(ctx,
		"pg-test-update",
		[]byte("blob"),
		1,
		[]string{"old"},
		json.RawMessage(`"all"`),
		false,
	)
	require.NoError(t, err)

	newAllow := []string{"new-1", "new-2"}
	err = svc.UpdateCredentialSet(ctx, id, credentials.CredentialSetUpdates{
		ModelAllowlist: &newAllow,
	})
	require.NoError(t, err)

	got, err := svc.GetCredentialSet(ctx, id)
	require.NoError(t, err)
	require.Equal(t, newAllow, got.ModelAllowlist,
		"Update must bind []string via pq.Array; pre-fix this would fail")
}

// TestPgCredentialSet_GetDefault_ScansModelAllowlist guards the
// GetDefault path (used by the default-credential lookup on every
// chat request). Same scan invariant as Get.
func TestPgCredentialSet_GetDefault_ScansModelAllowlist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-default-")

	svc := newTestService(db)
	ctx := context.Background()

	id, err := svc.CreateCredentialSet(ctx,
		"pg-test-default",
		[]byte("blob"),
		1,
		[]string{"x", "y"},
		json.RawMessage(`"all"`),
		true, // is_default
	)
	require.NoError(t, err)
	_ = id

	got, err := svc.GetDefault(ctx)
	require.NoError(t, err, "GetDefault must scan TEXT[] via pq.Array")
	require.NotNil(t, got)
	require.Equal(t, []string{"x", "y"}, got.ModelAllowlist)
}

// TestPgCredentialSet_ListByKeyVersionBelow_ScansModelAllowlist
// guards the rotation path. Same scan invariant; this is the path
// that runs during admin key rotation.
func TestPgCredentialSet_ListByKeyVersionBelow_ScansModelAllowlist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	defer cleanupCredentialSets(t, db, "pg-test-rotate-")

	svc := newTestService(db)
	ctx := context.Background()

	_, err := svc.CreateCredentialSet(ctx,
		"pg-test-rotate-old",
		[]byte("blob"),
		1, // old key version
		[]string{"old-model"},
		json.RawMessage(`"all"`),
		false,
	)
	require.NoError(t, err)

	rows, err := svc.ListByKeyVersionBelow(ctx, 99)
	require.NoError(t, err, "ListByKeyVersionBelow must scan TEXT[] via pq.Array")

	var found bool
	for _, r := range rows {
		if r.Name == "pg-test-rotate-old" {
			require.Equal(t, []string{"old-model"}, r.ModelAllowlist)
			found = true
		}
	}
	require.True(t, found, "test row must appear in rotation candidates")
}
