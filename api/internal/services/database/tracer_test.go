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

func TestClassifyOperation_LeadingComments(t *testing.T) {
	got := classifyOperation("/* tagged: GetUser */\n  SELECT 1")
	if !strings.EqualFold(got, "select") {
		t.Fatalf("got %q want select", got)
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
