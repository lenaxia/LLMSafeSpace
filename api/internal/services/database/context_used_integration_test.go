//go:build integration
// +build integration

package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:testpass@localhost:5433/llmsafespace_test?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("Skipping PG integration test: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("Skipping PG integration test: %v", err)
	}
	return pool
}

func TestIntegration_UpsertContextUsed_RoundTrip(t *testing.T) {
	pool := getIntegrationPool(t)
	defer pool.Close()

	ctx := context.Background()
	wsID := "int-test-ws-context"
	sesID := "int-test-ses-context"

	_, _ = pool.Exec(ctx, "DELETE FROM session_index WHERE workspace_id = $1", wsID)

	_, err := pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, $2, 'Test Session', 0, NOW(), NULL)
	`, wsID, sesID)
	require.NoError(t, err, "insert seed session")

	_, err = pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, $2, 'Test Session', 0, NOW(), $3)
		ON CONFLICT (workspace_id, session_id) DO UPDATE SET context_used = $3, updated_at = NOW()
	`, wsID, sesID, int64(45000))
	require.NoError(t, err, "upsert context_used=45000")

	var contextUsed *int64
	err = pool.QueryRow(ctx, "SELECT context_used FROM session_index WHERE workspace_id = $1 AND session_id = $2", wsID, sesID).Scan(&contextUsed)
	require.NoError(t, err, "select context_used")
	require.NotNil(t, contextUsed, "context_used must not be NULL after upsert")
	assert.Equal(t, int64(45000), *contextUsed, "context_used must match upserted value")

	_, err = pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, $2, 'Test Session', 0, NOW(), $3)
		ON CONFLICT (workspace_id, session_id) DO UPDATE SET context_used = $3, updated_at = NOW()
	`, wsID, sesID, int64(95000))
	require.NoError(t, err, "upsert context_used=95000 (overwrite)")

	err = pool.QueryRow(ctx, "SELECT context_used FROM session_index WHERE workspace_id = $1 AND session_id = $2", wsID, sesID).Scan(&contextUsed)
	require.NoError(t, err)
	require.NotNil(t, contextUsed)
	assert.Equal(t, int64(95000), *contextUsed, "context_used must reflect latest value")

	_, err = pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, $2, 'Test Session', 0, NOW(), $3)
		ON CONFLICT (workspace_id, session_id) DO UPDATE SET context_used = $3, updated_at = NOW()
	`, wsID, sesID, int64(0))
	require.NoError(t, err, "upsert context_used=0")

	err = pool.QueryRow(ctx, "SELECT context_used FROM session_index WHERE workspace_id = $1 AND session_id = $2", wsID, sesID).Scan(&contextUsed)
	require.NoError(t, err)
	require.NotNil(t, contextUsed, "context_used=0 must be stored as 0, not NULL")
	assert.Equal(t, int64(0), *contextUsed, "zero context_used must round-trip as 0")

	_, _ = pool.Exec(ctx, "DELETE FROM session_index WHERE workspace_id = $1", wsID)
}

func TestIntegration_ListSessionIndex_ReturnsContextUsed(t *testing.T) {
	pool := getIntegrationPool(t)
	defer pool.Close()

	ctx := context.Background()
	wsID := "int-test-ws-list"
	now := time.Now()

	_, _ = pool.Exec(ctx, "DELETE FROM session_index WHERE workspace_id = $1", wsID)

	_, err := pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, 'ses_with_ctx', 'Has Context', 5, $2, 42000)
	`, wsID, now)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO session_index (workspace_id, session_id, title, message_count, last_message_at, context_used)
		VALUES ($1, 'ses_no_ctx', 'No Context', 2, $2, NULL)
	`, wsID, now)
	require.NoError(t, err)

	rows, err := pool.Query(ctx, `
		SELECT session_id, context_used FROM session_index
		WHERE workspace_id = $1 ORDER BY session_id
	`, wsID)
	require.NoError(t, err)
	defer rows.Close()

	var results []struct {
		id          string
		contextUsed *int64
	}
	for rows.Next() {
		var r struct {
			id          string
			contextUsed *int64
		}
		require.NoError(t, rows.Scan(&r.id, &r.contextUsed))
		results = append(results, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, results, 2)

	assert.Equal(t, "ses_no_ctx", results[0].id)
	assert.Nil(t, results[0].contextUsed, "NULL context_used must come back as nil")

	assert.Equal(t, "ses_with_ctx", results[1].id)
	require.NotNil(t, results[1].contextUsed, "non-NULL context_used must come back as non-nil")
	assert.Equal(t, int64(42000), *results[1].contextUsed)

	_, _ = pool.Exec(ctx, "DELETE FROM session_index WHERE workspace_id = $1", wsID)
}
