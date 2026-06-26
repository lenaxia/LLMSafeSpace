// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespaces/pkg/interfaces"
)

// DefaultJWTSessionJanitorInterval is the period between expiry-pruning
// passes on the jwt_sessions table. 60s is small relative to the JWT
// lifetimes the table holds (24h default, 30d remember-me), so a row
// pruned a minute late costs nothing observable. The DELETE WHERE
// expires_at < NOW() query is O(log N) thanks to idx_jwt_sessions_expires_at.
const DefaultJWTSessionJanitorInterval = 60 * time.Second

// JWTSessionJanitor periodically prunes expired rows from jwt_sessions
// so the table stays bounded as login traffic accrues. Mirrors the
// pattern in handlers.PendingOrgCleaner.
//
// Failure handling: a single tick failure is logged and the next tick
// retries. We do NOT bail on persistent errors — recovering PG that
// was briefly unavailable is the same recovery path as a transient
// network blip, and surfacing the error any other way (panic, channel
// emit) adds complexity without benefit.
type JWTSessionJanitor struct {
	store    JWTSessionStore
	interval time.Duration
	logger   pkginterfaces.LoggerInterface
}

// NewJWTSessionJanitor builds a janitor with the given store and
// interval. Pass interval=0 to use DefaultJWTSessionJanitorInterval.
func NewJWTSessionJanitor(store JWTSessionStore, interval time.Duration, logger pkginterfaces.LoggerInterface) *JWTSessionJanitor {
	if interval <= 0 {
		interval = DefaultJWTSessionJanitorInterval
	}
	return &JWTSessionJanitor{store: store, interval: interval, logger: logger}
}

// Run blocks until ctx is canceled. Safe to call exactly once per
// janitor; mirror PendingOrgCleaner.Run.
func (j *JWTSessionJanitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.runOnce(ctx)
		}
	}
}

// runOnce performs a single pruning pass. Exported-by-fact (called from
// Run) so tests can invoke a single pass deterministically without
// waiting on a ticker. Returns the number of rows deleted so tests can
// assert on a precise expected count.
func (j *JWTSessionJanitor) runOnce(ctx context.Context) int64 {
	n, err := j.store.DeleteExpiredJWTSessions(ctx, time.Now())
	if err != nil {
		if j.logger != nil {
			j.logger.Warn("JWTSessionJanitor: prune pass failed (will retry next tick)",
				"error", err.Error())
		}
		return 0
	}
	if n > 0 && j.logger != nil {
		// Only log non-zero counts so the logs aren't noisy on idle
		// clusters; a steady stream of "pruned 0" entries serves no
		// operator. A spike in deletes is interesting (clock skew,
		// bulk revocation, schema migration) and the count exposes it.
		j.logger.Info("JWTSessionJanitor: pruned expired jwt_sessions rows", "count", n)
	}
	return n
}
