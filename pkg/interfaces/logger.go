// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package interfaces

// LoggerInterface defines the interface for logging operations
type LoggerInterface interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, err error, keysAndValues ...interface{})
	Fatal(msg string, err error, keysAndValues ...interface{})
	With(keysAndValues ...interface{}) LoggerInterface
	Sync() error
}
