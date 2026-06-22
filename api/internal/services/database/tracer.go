// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package database — query tracer.
//
// queryTracer implements the pgx v5 QueryTracer interface and records two
// metrics for every query the driver executes:
//
//   - llmsafespaces_db_query_duration_seconds{operation}
//   - llmsafespaces_db_errors_total{operation,error_type}
//
// It is attached to both the *sql.DB pool (via stdlib.OpenDB +
// pgx.ConnConfig.Tracer) and the *pgxpool.Pool used by the secrets store
// (via pgxpool.Config.ConnConfig.Tracer). Every SQL statement issued by
// the API binary therefore flows through one tracer and one metric API,
// which is the long-term-correct alternative to wrapping every call site
// or registering a database/sql wrapper driver.
package database

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

// queryStartCtxKey scopes the tracer's per-query state so multiple in-flight
// queries on the same goroutine (rare with pgx but possible via
// pgx.Conn.SendBatch) do not stomp each other.
type queryStartCtxKey struct{}

type queryStartState struct {
	operation string
	startTime time.Time
}

type queryTracer struct{}

// newQueryTracer constructs a tracer. Stateless — the same instance is
// safe to share across both pools.
func newQueryTracer() *queryTracer { return &queryTracer{} }

// NewQueryTracer returns the QueryTracer used by the API's *sql.DB pool.
// Exported so callers (e.g. the secrets pgxpool initialized in
// internal/app) can attach the same tracer to their own pool, ensuring
// every query — regardless of which pool issues it — flows through one
// metrics path. The returned value implements pgx.QueryTracer.
func NewQueryTracer() pgx.QueryTracer { return newQueryTracer() }

// Compile-time assertion: queryTracer must implement pgx.QueryTracer.
var _ pgx.QueryTracer = (*queryTracer)(nil)

// TraceQueryStart is called by pgx before each query. It returns a derived
// context that carries the operation classification and the start time;
// TraceQueryEnd reads them back to compute duration and emit metrics.
func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryStartCtxKey{}, queryStartState{
		operation: classifyOperation(data.SQL),
		startTime: time.Now(),
	})
}

// TraceQueryEnd is called by pgx after each query. It records the duration
// histogram unconditionally and the error counter when the query failed
// with anything other than pgx.ErrNoRows (which is a control-flow signal,
// not a fault).
func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(queryStartCtxKey{}).(queryStartState)
	if !ok {
		// No start state — should not happen, but if it does we'd
		// rather record nothing than emit a misleading 0-duration
		// observation.
		return
	}
	metrics.RecordDBQueryDuration(st.operation, time.Since(st.startTime))
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		metrics.RecordDBError(st.operation, classifyError(data.Err))
	}
}

// classifyOperation extracts the leading SQL verb (SELECT, INSERT, …) and
// folds it to a low-cardinality bucket label. It strips leading whitespace
// and SQL comments (line and block) so query-tag annotations (e.g. pgx's
// "/* tagged: GetUser */") do not poison the classifier.
//
// CTEs ("WITH … SELECT …") are folded to the trailing operation when the
// classifier can detect it cheaply; otherwise they fall to "other". The
// goal is bounded label cardinality, not perfect SQL parsing — operators
// can pivot from the duration histogram into pg_stat_statements when they
// need statement-level detail.
func classifyOperation(sql string) string {
	sql = stripLeadingNoise(sql)
	if sql == "" {
		return "other"
	}
	upper := strings.ToUpper(sql)
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return "select"
	case strings.HasPrefix(upper, "INSERT"):
		return "insert"
	case strings.HasPrefix(upper, "UPDATE"):
		return "update"
	case strings.HasPrefix(upper, "DELETE"):
		return "delete"
	case strings.HasPrefix(upper, "BEGIN") || strings.HasPrefix(upper, "START TRANSACTION"):
		return "begin"
	case strings.HasPrefix(upper, "COMMIT"):
		return "commit"
	case strings.HasPrefix(upper, "ROLLBACK"):
		return "rollback"
	case strings.HasPrefix(upper, "WITH"):
		// Cheap CTE classification: scan for the first SELECT/INSERT/
		// UPDATE/DELETE token after the WITH clause. Bounded scan
		// limits worst-case cost on pathological SQL.
		if op := classifyAfterWith(upper); op != "" {
			return op
		}
		return "other"
	}
	return "other"
}

// stripLeadingNoise removes leading whitespace, line comments (-- …\n) and
// block comments (/* … */) so the classifier sees the actual SQL verb.
func stripLeadingNoise(sql string) string {
	for {
		// Trim whitespace.
		sql = strings.TrimLeftFunc(sql, unicode.IsSpace)
		if strings.HasPrefix(sql, "--") {
			if i := strings.IndexByte(sql, '\n'); i >= 0 {
				sql = sql[i+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(sql, "/*") {
			if i := strings.Index(sql, "*/"); i >= 0 {
				sql = sql[i+2:]
				continue
			}
			return ""
		}
		return sql
	}
}

// classifyAfterWith finds the DML verb that owns the statement when the
// query starts with a CTE (`WITH … <DML>`). It is a best-effort scan,
// not a SQL parser:
//
//   - Tokenization treats ANY whitespace (space, tab, newline) as a word
//     boundary. The earlier space-only match missed multi-line queries.
//
//   - It tracks parenthesis depth. SELECT/INSERT/UPDATE/DELETE keywords
//     inside a parenthesised CTE body (depth > 0) are CTE-internal and
//     ignored. The first keyword observed at depth 0 AFTER the CTE list
//     is the real operation. This correctly handles INSERT…SELECT,
//     UPDATE…FROM, and DELETE…WHERE…IN(SELECT…) shapes — earlier
//     positional heuristics (first-keyword-wins / last-keyword-wins) got
//     these wrong because they ignored the CTE structure.
//
//   - The returned bucket is "select" / "insert" / "update" / "delete"
//     or empty (caller falls back to "other") if no DML verb is found
//     at the outer depth — e.g. a CTE that ends in a TRUNCATE or a
//     malformed string.
func classifyAfterWith(upperSQL string) string {
	// Cap the scan to the first 4096 bytes; CTEs longer than that are
	// vanishingly rare and the cost of a full scan is not worth the
	// bucket precision. If the trailing verb falls beyond the cap, the
	// caller falls back to "other" — safe but less precise.
	if len(upperSQL) > 4096 {
		upperSQL = upperSQL[:4096]
	}

	// Walk the string once. Track parenthesis depth. At depth 0 (outside
	// any CTE body), the first DML keyword bounded by whitespace is the
	// owning operation. We must skip the leading "WITH" itself, which
	// lives at depth 0.
	depth := 0
	i := 0
	// Skip past the leading WITH token.
	if strings.HasPrefix(upperSQL, "WITH") {
		i = len("WITH")
	}
	for i < len(upperSQL) {
		c := upperSQL[i]
		switch c {
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth > 0 {
			i++
			continue
		}
		// At depth 0: check for a DML keyword starting at i, with a
		// left whitespace boundary (i==0 or s[i-1] whitespace) and a
		// right whitespace boundary (or EOF).
		if i != 0 && !isASCIISpace(upperSQL[i-1]) {
			i++
			continue
		}
		for _, kw := range [...]string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			end := i + len(kw)
			if end > len(upperSQL) {
				continue
			}
			if upperSQL[i:end] != kw {
				continue
			}
			if end != len(upperSQL) && !isASCIISpace(upperSQL[end]) {
				continue
			}
			return strings.ToLower(kw)
		}
		i++
	}
	return ""
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// classifyError buckets driver/server errors into a small set of operational
// categories. The buckets are chosen to be actionable for an oncall:
// "connection" / "timeout" indicate fleet-level health, "constraint" /
// "deadlock" indicate application-level bugs, "syntax" indicates a code
// regression. Anything else falls to "other".
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "eof"):
		return "connection"
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "context canceled"):
		return "timeout"
	case strings.Contains(msg, "duplicate key"),
		strings.Contains(msg, "violates unique constraint"),
		strings.Contains(msg, "violates foreign key"),
		strings.Contains(msg, "violates check constraint"),
		strings.Contains(msg, "violates not-null"),
		strings.Contains(msg, "constraint"):
		return "constraint"
	case strings.Contains(msg, "deadlock"):
		return "deadlock"
	case strings.Contains(msg, "syntax error"):
		return "syntax"
	}
	return "other"
}
