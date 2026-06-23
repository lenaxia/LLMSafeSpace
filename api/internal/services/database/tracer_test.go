// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	dto "github.com/prometheus/client_model/go"

	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

func gatherMetricTracer(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	mfs, err := metrics.GatherDefault()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func sumCounterByLabelTracer(t *testing.T, mf *dto.MetricFamily, labelKey, labelVal string) float64 {
	t.Helper()
	if mf == nil {
		return 0
	}
	var total float64
	for _, m := range mf.GetMetric() {
		match := false
		for _, l := range m.GetLabel() {
			if l.GetName() == labelKey && l.GetValue() == labelVal {
				match = true
				break
			}
		}
		if match {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func histogramCountByLabelTracer(t *testing.T, mf *dto.MetricFamily, labelKey, labelVal string) uint64 {
	t.Helper()
	if mf == nil {
		return 0
	}
	var total uint64
	for _, m := range mf.GetMetric() {
		match := false
		for _, l := range m.GetLabel() {
			if l.GetName() == labelKey && l.GetValue() == labelVal {
				match = true
				break
			}
		}
		if match {
			total += m.GetHistogram().GetSampleCount()
		}
	}
	return total
}

func TestQueryTracer_RecordsSuccessDuration(t *testing.T) {
	tr := newQueryTracer()
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL: "SELECT id FROM users WHERE id = $1",
	})
	time.Sleep(2 * time.Millisecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: nil})

	mf := gatherMetricTracer(t, "llmsafespaces_db_query_duration_seconds")
	if mf == nil {
		t.Fatal("llmsafespaces_db_query_duration_seconds not emitted")
	}
	if c := histogramCountByLabelTracer(t, mf, "operation", "select"); c == 0 {
		t.Fatalf("expected at least 1 select observation; got 0")
	}
}

func TestClassifyOperation_ClassifiesAllVerbs(t *testing.T) {
	cases := map[string]string{
		"  SELECT 1":                    "select",
		"insert into x values (1)":      "insert",
		"UPDATE users SET name = $1":    "update",
		"DELETE FROM users WHERE id=$1": "delete",
		"BEGIN":                         "begin",
		"COMMIT":                        "commit",
		"ROLLBACK":                      "rollback",
		"WITH x AS (SELECT 1) SELECT 1": "select",
		"":                              "other",
		"VACUUM users":                  "other",
	}
	for sql, want := range cases {
		got := classifyOperation(sql)
		if got != want {
			t.Errorf("classifyOperation(%q): got %q want %q", sql, got, want)
		}
	}
}

// CTE classifier must fold to the trailing DML operation, not the first
// keyword it encounters. Production examples (CreateUser, DeleteSessionTree
// in api/internal/services/database/database.go) wrap an INSERT or DELETE
// in a CTE that contains a SELECT — those queries must classify as INSERT
// / DELETE, not SELECT, otherwise the per-operation buckets are wrong.
func TestClassifyOperation_CTEFoldsToTrailingDML(t *testing.T) {
	cases := map[string]string{
		// Simple CTE-INSERT: SELECT inside parens (does not match
		// a space-bounded ` SELECT `), trailing INSERT does.
		"WITH existing AS (SELECT count(*) FROM users) INSERT INTO users VALUES ($1)": "insert",
		// Simple CTE-DELETE: same pattern.
		"WITH RECURSIVE d AS (SELECT id FROM s) DELETE FROM s WHERE id IN (SELECT id FROM d)": "delete",
		// Update via CTE.
		"WITH x AS (SELECT 1) UPDATE users SET name = 'a'": "update",
		// CTE wrapping only SELECTs still classifies as select.
		"WITH x AS (SELECT 1), y AS (SELECT 2) SELECT * FROM x, y": "select",
		// PRODUCTION REGRESSION: CreateUser query (database.go:194).
		// The INSERT statement uses an INSERT … SELECT form, so
		// after the CTE there are TWO un-parenthesised tokens:
		// ` INSERT ` AND ` SELECT $1 …`. The first-keyword loop
		// would return "select" — wrong; this is an INSERT.
		`
		WITH existing AS (
			SELECT COUNT(*) AS n FROM users
		)
		INSERT INTO users (id, username, email, password_hash, created_at, updated_at, active, role)
		SELECT $1, $2, $3, $4, $5, $6, $7,
		       CASE WHEN existing.n = 0 THEN 'admin' ELSE $8 END
		FROM existing
		RETURNING role`: "insert",
	}
	for sql, want := range cases {
		got := classifyOperation(sql)
		if got != want {
			t.Errorf("classifyOperation(%q):\n  got  %q\n  want %q", sql, got, want)
		}
	}
}

func TestClassifyOperation_LeadingComments(t *testing.T) {
	cases := map[string]string{
		// Block comment (pgx-style query tag).
		"/* tagged: GetUser */\n  SELECT 1": "select",
		// Line comment — must skip past the trailing newline so the
		// classifier sees the SQL verb on the next line. A regression
		// that mis-advanced past `--` (e.g. dropping the +1) would
		// either return "other" or hang the loop.
		"-- name: GetUser\nSELECT 1":                            "select",
		"-- audit: trace-id=abc\nINSERT INTO users VALUES ($1)": "insert",
		// Mixed comments: line comment followed by block comment then
		// the verb. Exercises the loop in stripLeadingNoise.
		"-- header\n/* tag */ DELETE FROM s WHERE id=$1": "delete",
		// Line comment with no trailing newline — content is entirely
		// commented out, classifier must return "other".
		"-- this is the whole query": "other",
	}
	for sql, want := range cases {
		got := classifyOperation(sql)
		if !strings.EqualFold(got, want) {
			t.Errorf("classifyOperation(%q): got %q want %q", sql, got, want)
		}
	}
}

func TestQueryTracer_RecordsErrorWithType(t *testing.T) {
	tr := newQueryTracer()
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL: "INSERT INTO users (id) VALUES ($1)",
	})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{
		Err: errors.New("ERROR: duplicate key value violates unique constraint"),
	})

	mf := gatherMetricTracer(t, "llmsafespaces_db_errors_total")
	if mf == nil {
		t.Fatal("llmsafespaces_db_errors_total not emitted")
	}
	if c := sumCounterByLabelTracer(t, mf, "operation", "insert"); c < 1 {
		t.Fatalf("expected insert error counter >= 1; got %v", c)
	}
}

func TestQueryTracer_NoErrCounterOnSuccess(t *testing.T) {
	tr := newQueryTracer()

	beforeMf := gatherMetricTracer(t, "llmsafespaces_db_errors_total")
	before := sumCounterByLabelTracer(t, beforeMf, "operation", "update")

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "UPDATE users SET x=1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: nil})

	afterMf := gatherMetricTracer(t, "llmsafespaces_db_errors_total")
	after := sumCounterByLabelTracer(t, afterMf, "operation", "update")

	if after != before {
		t.Fatalf("error counter should not increment on success: before=%v after=%v", before, after)
	}
}

func TestQueryTracer_NoRowsIsNotAnError(t *testing.T) {
	tr := newQueryTracer()

	beforeMf := gatherMetricTracer(t, "llmsafespaces_db_errors_total")
	before := sumCounterByLabelTracer(t, beforeMf, "operation", "select")

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: pgx.ErrNoRows})

	afterMf := gatherMetricTracer(t, "llmsafespaces_db_errors_total")
	after := sumCounterByLabelTracer(t, afterMf, "operation", "select")

	if after != before {
		t.Fatalf("ErrNoRows must not increment errors counter: before=%v after=%v", before, after)
	}
}

func TestClassifyError_BucketsKnownTypes(t *testing.T) {
	cases := map[error]string{
		errors.New("connection refused"):                                    "connection",
		errors.New("dial tcp: i/o timeout"):                                 "timeout",
		errors.New("ERROR: duplicate key value violates unique constraint"): "constraint",
		errors.New("ERROR: deadlock detected"):                              "deadlock",
		errors.New("syntax error at or near \"FROM\""):                      "syntax",
		errors.New("something else entirely"):                               "other",
	}
	for err, want := range cases {
		if got := classifyError(err); got != want {
			t.Errorf("classifyError(%q): got %q want %q", err.Error(), got, want)
		}
	}
}
