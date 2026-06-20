// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package testharness

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// resolveDSN honors TEST_DATABASE_URL and falls back to the project's standard
// test DSN. Value: tests that run anywhere (CI, local) resolve the right
// Postgres without per-file env plumbing. Failure mode: silent default masking
// a misconfigured CI; this test forces the fallback to be the documented one.
func TestResolveDSN_DefaultWhenEnvUnset(t *testing.T) {
	t.Setenv(envTestDatabaseURL, "")
	got := resolveDSN()
	assert.Equal(t, defaultTestDSN, got, "unset env must fall back to default test DSN")
}

func TestResolveDSN_HonorsEnv(t *testing.T) {
	const want = "postgres://ci:secret@db.svc:5432/cidb?sslmode=require"
	t.Setenv(envTestDatabaseURL, want)
	assert.Equal(t, want, resolveDSN(), "TEST_DATABASE_URL must win over the default")
}

// MigrationFiles embeds the on-disk migration set. Value: the harness asserts
// it is applying the real schema, not a stale copy. Failure mode: a missing or
// mis-embedded migration silently leaves the schema incomplete.
func TestMigrationFiles_EmbeddedAndSorted(t *testing.T) {
	files, err := MigrationFiles()
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected embedded migration files")

	// The initial schema must always be present.
	assert.Contains(t, files, "000001_initial_schema.up.sql",
		"initial schema migration must be embedded")

	// Sorted ascending so golang-migrate applies them in version order.
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	assert.Equal(t, sorted, files, "MigrationFiles must return versions ascending")

	// Only up files are returned (down files are for MigrateDown, listed separately).
	for _, f := range files {
		assert.True(t, strings.HasSuffix(f, ".up.sql"), "want only .up.sql, got %q", f)
	}
}

// filterUserTables keeps golang-migrate's bookkeeping out of TRUNCATE. Value:
// Reset() must not wipe schema_migrations, or every subsequent New() would
// re-run all migrations (slow) or mark the DB dirty. Failure mode: data
// corruption of the migration version table.
func TestFilterUserTables_DropsSchemaMigrations(t *testing.T) {
	in := []string{"users", "schema_migrations", "workspaces", "SCHEMA_MIGRATIONS"}
	got := filterUserTables(in)
	assert.Equal(t, []string{"users", "workspaces"}, got,
		"schema_migrations (any case) must never be truncated")
}

// buildTruncateSQL emits a single CASCADE statement so foreign keys are
// handled in one round-trip. Value: correctness over FK graphs; speed.
func TestBuildTruncateSQL_SingleAndMultiple(t *testing.T) {
	got, err := buildTruncateSQL([]string{"users"})
	require.NoError(t, err)
	assert.Equal(t, `TRUNCATE TABLE "users" RESTART IDENTITY CASCADE`, got)

	got, err = buildTruncateSQL([]string{"users", "workspaces"})
	require.NoError(t, err)
	assert.Equal(t, `TRUNCATE TABLE "users", "workspaces" RESTART IDENTITY CASCADE`, got)
}

func TestBuildTruncateSQL_EmptyIsError(t *testing.T) {
	_, err := buildTruncateSQL(nil)
	require.Error(t, err, "truncating nothing is a programming error, not a silent no-op")
}

// buildSeedSQL deterministically orders columns so generated SQL is stable
// across map iteration. Value: reproducible, debuggable test data. Failure
// mode: non-deterministic SQL makes failing tests unreproducible.
func TestBuildSeedSQL_OrderedAndStable(t *testing.T) {
	row := map[string]any{"email": "a@b", "id": "u1", "name": "n"}
	q, args, err := buildSeedSQL("users", row)
	require.NoError(t, err)

	// Two independent builds must yield identical SQL (determinism proof).
	q2, args2, _ := buildSeedSQL("users", row)
	assert.Equal(t, q, q2, "SQL must be deterministic despite map iteration")
	assert.Equal(t, args, args2)

	assert.Equal(t, `INSERT INTO "users" ("email", "id", "name") VALUES ($1, $2, $3) RETURNING "id"`, q)
	assert.Equal(t, []any{"a@b", "u1", "n"}, args, "args must follow sorted column order")
}

func TestBuildSeedSQL_RejectsEmptyTable(t *testing.T) {
	_, _, err := buildSeedSQL("", map[string]any{"a": 1})
	require.Error(t, err, "empty table name would inject malformed SQL")
}

// newLogger wires a captured zap core so tests assert on emitted log lines.
// Value: "no ERROR was logged" / "this warning fired" assertions work without
// scraping stderr. Failure mode: log assertions silently always pass.
func TestNewLogger_Captures(t *testing.T) {
	logger, logs := newLogger()
	logger.Error("boom", zap.String("k", "v"))

	require.Len(t, logs.All(), 1, "log entry must be captured")
	e := logs.All()[0]
	assert.Equal(t, "boom", e.Message)
	assert.Equal(t, "v", e.ContextMap()["k"])
	assert.Equal(t, zap.ErrorLevel, e.Level)
}
