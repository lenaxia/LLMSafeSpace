// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// NewObserved returns a Logger backed by an in-memory observer core and
// the ObservedLogs sink for assertions. The observer captures all entries
// at WarnLevel and above.
//
// Intended for unit tests only. Use in production is not meaningful —
// the observer only retains entries in memory with no output.
//
// Usage:
//
//	log, logs := logger.NewObserved()
//	svc, _ := auth.New(cfg, log, db, cache)
//	// ... exercise svc ...
//	require.Equal(t, 1, logs.FilterMessage("auth: rememberMeDuration is shorter...").Len())
func NewObserved() (*Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.WarnLevel)
	return &Logger{logger: zap.New(core)}, logs
}
