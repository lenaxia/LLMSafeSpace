// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package makefile_test

// Regression tests for migrate connection strings in the api/ dev tooling.
//
// Same class of bug as issue #424 (and PR #437): a connection string built as
// a postgres:// URL with the password interpolated via shell/make variable
// substitution. Both make's $(VAR) and bash's ${VAR} are literal string
// replaces with no URL-encoding, so a password containing URL-reserved chars
// (/ ? # @ : % + =) breaks the migrate CLI URL parser. The repro is common:
// a developer exports DB_PASSWORD from `openssl rand -base64 32` (which
// produces slashes) and runs `make migrate-up` or `./scripts/migrate.sh`.
//
// Two live sites had the bug:
//   - api/Makefile migrate-up / migrate-down targets (make $(VAR))
//   - api/scripts/migrate.sh (bash ${VAR})
//
// Fix for both: use the libpq KV connection-string form, which splits on
// whitespace and '=' and never needs encoding — the same fix applied to the
// Helm chart's migration Job in PR #437.
//
// These tests parse each file and assert the connection string uses the KV
// form, not a password-bearing URL. They fail against the original URL-form.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractConnectionStrings scans file content for migrate -database arguments
// (quoted strings) and returns the set of arguments found.
func extractConnectionStrings(t *testing.T, content string) []string {
	t.Helper()
	// Matches: -database "..."  (Makefile and shell both quote the value).
	dbArgRe := regexp.MustCompile("-database\\s+\"([^\"]*)\"")
	var out []string
	for _, m := range dbArgRe.FindAllStringSubmatch(content, -1) {
		out = append(out, m[1])
	}
	return out
}

// assertNoURLForm requires that none of the given connection strings is a
// postgres:// URL (the password-bearing form that breaks on URL-reserved chars).
func assertNoURLForm(t *testing.T, source string, args []string) {
	t.Helper()
	var urlArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "postgres://") || strings.HasPrefix(a, "postgresql://") {
			urlArgs = append(urlArgs, a)
		}
	}
	require.Emptyf(t, urlArgs,
		"%s must NOT use a postgres:// URL with the password interpolated "+
			"(variable substitution is literal with no URL-encoding; a password "+
			"with a URL-reserved char breaks the migrate CLI — same class as "+
			"issue #424). Found URL-form args: %v", source, urlArgs)
}

// assertKVFormWithPassword requires that every connection string uses the
// libpq KV form and interpolates the password (no hard-coded value).
func assertKVFormWithPassword(t *testing.T, source, passwordVar string, args []string) {
	t.Helper()
	require.NotEmptyf(t, args, "%s must contain at least one migrate -database argument", source)
	for _, arg := range args {
		assert.Containsf(t, arg, "password="+passwordVar,
			"%s KV database arg must interpolate the password from %s: %q", source, passwordVar, arg)
		assert.Containsf(t, arg, "sslmode=",
			"%s KV database arg must include sslmode: %q", source, arg)
	}
}

func absPath(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, rel)
}

// TestMigrateMakefileTargets_UseLibpqKVNotURL parses api/Makefile, extracts
// the migrate-up and migrate-down recipe lines, and asserts each uses the
// libpq KV connection-string form rather than a password-bearing URL.
func TestMigrateMakefileTargets_UseLibpqKVNotURL(t *testing.T) {
	data, err := os.ReadFile(absPath(t, "Makefile"))
	require.NoError(t, err)

	args := extractConnectionStrings(t, string(data))
	assertNoURLForm(t, "api/Makefile", args)
	assertKVFormWithPassword(t, "api/Makefile", "$(DB_PASSWORD)", args)
}

// TestMigrateShellScript_UseLibpqKVNotURL parses api/scripts/migrate.sh and
// asserts its connection string uses the libpq KV form rather than a
// password-bearing URL. Same class of bug as the Makefile targets.
//
// migrate.sh builds the connection string in an intermediate CONNECTION_STRING
// variable, so this test scans the file content directly for the bug signature
// (postgres:// with interpolated password) and the fix signature (KV form).
func TestMigrateShellScript_UseLibpqKVNotURL(t *testing.T) {
	data, err := os.ReadFile(absPath(t, filepath.Join("scripts", "migrate.sh")))
	require.NoError(t, err)
	content := string(data)

	// Bug signature: a postgres:// URL with the password interpolated.
	assert.NotContainsf(t, content, "postgres://${DB_USER}:${DB_PASSWORD}",
		"api/scripts/migrate.sh must NOT build a postgres:// URL with the password "+
			"interpolated (bash ${VAR} substitution is literal with no URL-encoding; "+
			"a password with a URL-reserved char breaks the migrate CLI — same class "+
			"as issue #424)")

	// Fix signature: the libpq KV form interpolating the password.
	assert.Containsf(t, content, "password=${DB_PASSWORD}",
		"api/scripts/migrate.sh must use the libpq KV form with password=${DB_PASSWORD}")
	assert.Containsf(t, content, "host=${DB_HOST}",
		"api/scripts/migrate.sh must use the libpq KV form with host=${DB_HOST}")
	assert.Containsf(t, content, "sslmode=",
		"api/scripts/migrate.sh KV connection string must include sslmode")
}
