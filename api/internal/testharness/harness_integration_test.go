// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

package testharness_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/testharness"
)

// newHarness is the single entry point these contract tests use. If the test
// Postgres is unreachable, New skips the test — so this suite runs in CI
// (where TEST_DATABASE_URL is provisioned) and skips locally without Docker.
func newHarness(t *testing.T) *testharness.Harness {
	t.Helper()
	return testharness.New(t)
}

// TestHarness_New_ReturnsWorkingDB — harness smoke test. Value: if New()
// returns a dead pool, every dependent integration test is broken, so this
// must fail loudly and first. Expected: SELECT 1 → 1.
func TestHarness_New_ReturnsWorkingDB(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var got int
	err := h.Pool().QueryRow(context.Background(), "SELECT 1").Scan(&got)
	require.NoError(t, err, "SELECT 1 must succeed on the harness pool")
	assert.Equal(t, 1, got)
}

// TestHarness_SQLDB_ReturnsWorkingDB — the database/sql handle (used by stores
// like the email-token store) is wired to the same Postgres. Value: a stale or
// nil *sql.DB would make every sql-based store silently skip. Expected: SELECT
// 1 via *sql.DB → 1.
func TestHarness_SQLDB_ReturnsWorkingDB(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var got int
	err := h.SQLDB().QueryRowContext(context.Background(), "SELECT 1").Scan(&got)
	require.NoError(t, err, "SELECT 1 must succeed on the harness *sql.DB")
	assert.Equal(t, 1, got)
}

// TestHarness_New_ReturnsWorkingRedis — miniredis-backed client round-trips.
// Value: tests that depend on Redis (rate limit, cache, msgqueue) get a real,
// isolated client. Expected: SET then GET returns the value.
func TestHarness_New_ReturnsWorkingRedis(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	ctx := h.NewContext()
	require.NoError(t, h.Redis().Set(ctx, "k", "v", 0).Err())
	got, err := h.Redis().Get(ctx, "k").Result()
	require.NoError(t, err)
	assert.Equal(t, "v", got)
}

// TestHarness_MigrationsApplied — New() guarantees the schema is current.
// Value: tests assume tables (users, workspaces, …) exist; if migrations were
// silently skipped, every query would fail with "relation does not exist".
// Expected: a known table from the initial schema is present.
func TestHarness_MigrationsApplied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var n int
	err := h.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = 'users'`).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the 'users' table from the initial migration must exist")
}

// TestHarness_Reset_ClearsData — explicit Reset() empties user tables for tests
// that need a clean slate. Value: deterministic ordering within a package's
// non-parallel tests. Failure mode: leftover rows from a prior test bleed into
// the next. Expected: after Reset, a count is zero.
//
// Note: Reset is NOT called automatically and is NOT safe to combine with
// t.Parallel on the shared test DB. The project convention for parallel
// integration tests is per-test unique IDs (see TestHarness_UniqueIDIsolation).
func TestHarness_Reset_ClearsData(t *testing.T) {
	h := newHarness(t)

	// Seed a user, confirm it landed, reset, confirm it's gone.
	_, err := h.Pool().Exec(context.Background(),
		`INSERT INTO users (id, username, email, password_hash, active, role)
		 VALUES ('reset-probe', 'reset_probe', 'reset@probe', 'h', true, 'user')`)
	require.NoError(t, err)

	var n int
	require.NoError(t, h.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = 'reset-probe'`).Scan(&n))
	require.Equal(t, 1, n, "seed must land before reset")

	require.NoError(t, h.Reset(), "Reset must truncate user tables")

	require.NoError(t, h.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = 'reset-probe'`).Scan(&n))
	assert.Equal(t, 0, n, "Reset must clear the seeded row")
}

// TestHarness_Reset_PreservesMigrations — Reset must never touch
// schema_migrations, or the next New() would re-apply migrations or mark the DB
// dirty. Expected: after Reset, a known table still exists.
func TestHarness_Reset_PreservesMigrations(t *testing.T) {
	h := newHarness(t)

	require.NoError(t, h.Reset())

	var n int
	require.NoError(t, h.Pool().QueryRow(context.Background(),
		`SELECT COUNT(*) FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = 'users'`).Scan(&n))
	assert.Equal(t, 1, n, "schema must survive Reset")
}

// TestHarness_NewContext_HasTimeout — contexts from the harness carry a
// deadline so a stuck query fails the test instead of hanging the suite.
// Expected: Deadline() is non-zero and in the future.
func TestHarness_NewContext_HasTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	ctx := h.NewContext()
	_, ok := ctx.Deadline()
	require.True(t, ok, "NewContext must attach a deadline")
	assert.False(t, ctx.Err() != nil, "context must not be already canceled")
}

// TestHarness_Seed_InsertsAndReturnsID — Seed is a thin typed convenience over
// raw SQL. Value: less boilerplate for the common "insert a user, get its id"
// shape. Expected: row is inserted and the id is returned.
func TestHarness_Seed_InsertsAndReturnsID(t *testing.T) {
	h := newHarness(t)

	id := h.Seed("users", map[string]any{
		"id":            "seed-probe",
		"username":      "seed_probe",
		"email":         "seed@probe",
		"password_hash": "h",
		"active":        true,
		"role":          "user",
	})
	require.NotEmpty(t, id, "Seed must return the row id")
	assert.Equal(t, "seed-probe", id)

	// Cleanup so the test is self-contained: without it, re-running this test
	// in isolation (-run TestHarness_Seed) against a persistent DB fails on the
	// second run with a primary-key violation.
	t.Cleanup(func() {
		_, _ = h.Pool().Exec(context.Background(), "DELETE FROM users WHERE id = $1", id)
	})

	var email string
	require.NoError(t, h.Pool().QueryRow(context.Background(),
		`SELECT email FROM users WHERE id = $1`, id).Scan(&email))
	assert.Equal(t, "seed@probe", email)
}

// TestHarness_UniqueIDIsolation — the project's convention for parallel
// integration tests on the shared DB: each test uses a unique marker so its
// reads see only its own writes. Value: parallel tests don't corrupt each
// other without needing per-instance database isolation. Expected: two
// concurrent writers each see exactly their own marker, never the other's.
//
// newHarness is called once, in the test goroutine, so the skip-if-no-DB
// signal propagates correctly (t.Skip from a spawned goroutine would not).
func TestHarness_UniqueIDIsolation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := h.NewContext()

	const writers = 2
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			marker := fmt.Sprintf("iso-%d-%s", i, h.ID())
			if _, err := h.Pool().Exec(ctx,
				`INSERT INTO users (id, username, email, password_hash, active, role)
				 VALUES ($1, $2, $3, 'h', true, 'user')`,
				marker, "u_"+marker, marker+"@iso"); err != nil {
				done <- err
				return
			}
			var n int
			if err := h.Pool().QueryRow(ctx,
				`SELECT COUNT(*) FROM users WHERE id = $1`, marker).Scan(&n); err != nil {
				done <- err
				return
			}
			if n != 1 {
				done <- fmt.Errorf("marker %s saw %d rows, want 1", marker, n)
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < writers; i++ {
		require.NoError(t, <-done, "parallel writers must not corrupt each other's writes")
	}
}

// TestHarness_Teardown_ClosesConnections — Close() (registered via t.Cleanup
// by New) releases the pool, *sql.DB, redis client, and miniredis. Value: no
// fd / goroutine leaks across long CI runs. Expected: after Close, Redis Ping
// and a pool query both error (handles torn down).
func TestHarness_Teardown_ClosesConnections(t *testing.T) {
	h := newHarness(t)

	h.Close()

	// Redis client is closed → Ping must error.
	if err := h.Redis().Ping(context.Background()).Err(); err == nil {
		t.Error("Redis Ping must fail after Close (client should be closed)")
	}
	// Pool is closed → query must error.
	if _, err := h.Pool().Exec(context.Background(), "SELECT 1"); err == nil {
		t.Error("pool query must fail after Close (pool should be closed)")
	}
}
