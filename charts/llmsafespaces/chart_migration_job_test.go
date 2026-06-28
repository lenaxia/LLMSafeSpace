// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package chart_test

// Regression tests for the migration Job connection string (issue #424).
//
// The migration Job built the migrate CLI database argument as a URL:
//
//	-database=postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSLMODE)
//
// K8s env-var substitution ($(VAR)) is a literal string replacement with no
// URL-encoding. When the postgres password (read from a Secret) contains a
// URL-reserved character (/ ? # @ : % + =), the migrate CLI URL parser
// misinterprets it and the pre-install Helm hook fails, leaving the chart's
// first install stuck in pending-install. The repro is common: operators
// generate passwords with `openssl rand -base64 32`, which produces slashes.
//
// CI missed this because the chart's auto-generated secret uses
// randAlphaNum (alphanumeric only), never exercising the failure path.
//
// Fix: use the libpq KV connection-string form, which splits on whitespace
// and '=' and therefore never needs URL-encoding:
//
//	-database host=$(DB_HOST) port=$(DB_PORT) user=$(DB_USER) password=$(DB_PASSWORD) dbname=$(DB_NAME) sslmode=$(DB_SSLMODE)
//
// These tests assert the rendered Job uses the KV form and never the raw
// URL-with-interpolated-password form.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMigrationJob returns the rendered migrate Job (pre-install hook), or
// nil if absent. The Job is named <release>-llmsafespaces-migrate.
func findMigrationJob(t *testing.T, docs []map[string]any) map[string]any {
	t.Helper()
	jobs := findByKind(docs, "Job")
	for _, j := range jobs {
		if strings.HasSuffix(metaName(j), "-migrate") {
			return j
		}
	}
	return nil
}

// TestMigrationJob_UsesLibpqKVNotURLWithPassword verifies the migrate CLI
// database argument is in libpq KV form, not a postgres:// URL with the
// password interpolated. The URL form breaks on URL-reserved chars in the
// password (issue #424).
func TestMigrationJob_UsesLibpqKVNotURLWithPassword(t *testing.T) {
	docs := helmTemplate(t, "")
	job := findMigrationJob(t, docs)
	require.NotNil(t, job, "migration Job must render by default (migrations.enabled=true)")

	args := containerArgs(t, job, "migrate")

	// Find the -database argument. migrate accepts either:
	//   -database=VALUE        (single arg)
	//   -database VALUE        (two args)
	var dbArg string
	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			continue
		}
		if s == "-database" && i+1 < len(args) {
			if next, ok := args[i+1].(string); ok {
				dbArg = next
			}
			break
		}
		if strings.HasPrefix(s, "-database=") {
			dbArg = strings.TrimPrefix(s, "-database=")
			break
		}
	}
	require.NotEmpty(t, dbArg, "migrate container must receive a -database argument")

	// Must NOT be a postgres:// (or postgresql://) URL. That form embeds the
	// password inline and breaks on URL-reserved chars without URL-encoding.
	assert.False(t, strings.HasPrefix(dbArg, "postgres://") || strings.HasPrefix(dbArg, "postgresql://"),
		"-database must not be a postgres:// URL; the password is interpolated raw and breaks on "+
			"URL-reserved chars (issue #424). Use the libpq KV form instead.")

	// Must use the libpq KV form and interpolate every connection parameter.
	for _, kv := range []string{"host=", "port=", "user=", "password=", "dbname=", "sslmode="} {
		assert.Contains(t, dbArg, kv,
			"-database KV form must include %q (got: %s)", kv, dbArg)
	}
	// The password must come from $(DB_PASSWORD), not from a render-time
	// value (the secret is BYO/external in production).
	assert.Contains(t, dbArg, "$(DB_PASSWORD)",
		"-database must interpolate the password from $(DB_PASSWORD); the secret is out-of-band")
}

// TestMigrationJob_PasswordFromSecret verifies the DB_PASSWORD env var is
// sourced from the credentials Secret via secretKeyRef, never rendered
// inline. This guards against a "fix" that reads the password at render
// time (which would require lookup() and fails under helm --dry-run /
// ArgoCD/Flux with lookup denied).
func TestMigrationJob_PasswordFromSecret(t *testing.T) {
	docs := helmTemplate(t, "")
	job := findMigrationJob(t, docs)
	require.NotNil(t, job, "migration Job must render by default")

	env := containerEnv(t, job, "migrate")
	var dbPwd map[string]any
	for _, e := range env {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := em["name"].(string); name == "DB_PASSWORD" {
			dbPwd = em
			break
		}
	}
	require.NotNil(t, dbPwd, "migrate container must define DB_PASSWORD env var")

	ref, ok := dbPwd["valueFrom"].(map[string]any)
	require.True(t, ok, "DB_PASSWORD must use valueFrom (not an inline value)")

	skr, ok := ref["secretKeyRef"].(map[string]any)
	require.True(t, ok, "DB_PASSWORD must reference a Secret via secretKeyRef")
	assert.Equal(t, "postgres-password", skr["key"],
		"DB_PASSWORD secretKeyRef.key must be postgres-password")
}

// containerArgs extracts the args of the named container in a Job.
func containerArgs(t *testing.T, job map[string]any, name string) []any {
	t.Helper()
	c := findContainer(t, job, name)
	args, ok := c["args"].([]any)
	require.True(t, ok, "container %q must have args", name)
	return args
}

// containerEnv extracts the env of the named container in a Job.
func containerEnv(t *testing.T, job map[string]any, name string) []any {
	t.Helper()
	c := findContainer(t, job, name)
	env, ok := c["env"].([]any)
	require.True(t, ok, "container %q must have env", name)
	return env
}

// findContainer returns the named container from a Job's pod template.
func findContainer(t *testing.T, job map[string]any, name string) map[string]any {
	t.Helper()
	spec, _ := job["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	tspec, _ := tmpl["spec"].(map[string]any)
	containers, _ := tspec["containers"].([]any)
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cn, _ := cm["name"].(string); cn == name {
			return cm
		}
	}
	require.FailNow(t, "container %q not found in Job", name)
	return nil
}
