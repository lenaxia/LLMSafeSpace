// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package migrations embeds the SQL migration files so that test tooling (the
// integration-test harness) and any in-process caller can apply migrations
// without the external `migrate` CLI being on PATH.
//
// Only the golang-migrate-format files are embedded: NNNNNN_name.up.sql and
// NNNNNN_name.down.sql. Legacy files (001_initial_schema.sql,
// 001_initial_schema_rollback.sql) and README.md are deliberately excluded so
// that golang-migrate's source driver sees an unambiguous migration set.
package migrations

import "embed"

// FS holds the embedded migration files at its root. Use it with
// golang-migrate's iofs source:
//
//	src, err := iofs.New(migrations.FS, ".")
//
//go:embed *.up.sql *.down.sql
var FS embed.FS
