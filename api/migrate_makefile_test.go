// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package makefile_test

// Regression test for the api/Makefile migrate connection string.
//
// Same class of bug as issue #424 (and PR #437): the migrate-up / migrate-down
// targets built the database argument as a postgres:// URL with the password
// interpolated via make's $(DB_PASSWORD). Make substitution is a literal
// string replace with no URL-encoding, so a password containing URL-reserved
// chars (/ ? # @ : % + =) breaks the migrate CLI URL parser and the target
// fails. The repro is common: a developer exports DB_PASSWORD from
// `openssl rand -base64 32` (which produces slashes) and runs `make migrate-up`.
//
// Fix: use the libpq KV connection-string form, which splits on whitespace
// and '=' and never needs encoding — the same fix applied to the Helm chart's
// migration Job in PR #437.
//
// This test parses the Makefile and asserts the migrate targets use the KV
// form, not a postgres:// URL with the password inline. It fails against the
// original URL-form targets.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makefilePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "Makefile")
}

// TestMigrateTargets_UseLibpqKVNotURL parses api/Makefile, extracts the
// migrate-up and migrate-down recipe lines, and asserts each uses the
// libpq KV connection-string form rather than a password-bearing URL.
func TestMigrateTargets_UseLibpqKVNotURL(t *testing.T) {
	data, err := os.ReadFile(makefilePath(t))
	require.NoError(t, err)

	// Extract the database argument from every migrate recipe line.
	dbArgRe := regexp.MustCompile(`-database\s+"([^"]*)"`)
	urlTargets := []string{}
	kvTargets := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "migrate") {
			continue
		}
		m := dbArgRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dbArg := m[1]
		if strings.HasPrefix(dbArg, "postgres://") || strings.HasPrefix(dbArg, "postgresql://") {
			urlTargets = append(urlTargets, dbArg)
			continue
		}
		kvTargets[dbArg] = true
	}

	require.Empty(t, urlTargets,
		"migrate targets must NOT use a postgres:// URL with the password interpolated "+
			"(make $(VAR) substitution is literal with no URL-encoding; a password with "+
			"a URL-reserved char breaks the migrate CLI — same class as issue #424). "+
			"Found URL-form args: %v", urlTargets)

	// At least one KV-form database arg must exist (both migrate-up and
	// migrate-down). KV form must interpolate the password from $(DB_PASSWORD),
	// not a hard-coded value.
	require.NotEmpty(t, kvTargets,
		"expected at least one migrate target using the libpq KV form")
	for arg := range kvTargets {
		assert.Contains(t, arg, "password=$(DB_PASSWORD)",
			"KV database arg must interpolate the password from $(DB_PASSWORD): %q", arg)
		assert.Contains(t, arg, "sslmode=",
			"KV database arg must include sslmode (use the existing sslmode value): %q", arg)
	}
}
