// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package chart_test

// Regression tests for the migration Job connection string.
//
// History (two bugs, opposite directions):
//
//  1. #424 — the Job built a `postgres://` URL with `$(DB_PASSWORD)`
//     interpolated raw. K8s env-var substitution has no URL-encoding, so a
//     password with URL-reserved chars broke the migrate CLI's URL parser.
//     The first fix (#437) switched to the libpq KV connection-string form
//     (`host=... password=...`), which never needs encoding.
//
//  2. #455 — the KV form is accepted by golang-migrate AS A GO LIBRARY but
//     NOT by the standalone `migrate` CLI shipped in the `migrate/migrate`
//     Docker image. The CLI requires `driver://url`; the KV form dies with
//     `error: no scheme`. The KV fix was latent-broken from #437 until PR
//     #451 added the first real migration after the baseline; the deploy
//     then hard-failed and rolled back in a loop.
//
// The correct fix (#455) keeps the password out of the rendered YAML (it
// stays in a Secret, read at runtime via the `DB_PASSWORD` env var) AND
// produces a `postgres://` URL the CLI accepts. A shell wrapper
// (`command: ["/bin/sh", "-ec"]`) percent-encodes every byte of both the
// user and the password at runtime, then `exec`s the migrate binary with a
// fully-encoded URL. Encoding every byte is wasteful but unconditionally
// correct for any password content (including the full URL-reserved set
// `/ ? # @ : % + = &` and control chars).
//
// These tests assert the rendered Job uses the shell-wrapper form and that
// the wrapper produces a valid, correctly-encoded URL when executed. The
// earlier assertion (KV form, NOT a URL) is inverted: that property WAS the
// bug.

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

// migrationScript returns the migrate container's shell-wrapper script
// (the single element of args) plus the command vector. It fails the test
// if the container is not shaped as a shell wrapper.
func migrationScript(t *testing.T, job map[string]any) (command []string, script string) {
	t.Helper()
	c := findContainer(t, job, "migrate")

	rawCmd, _ := c["command"].([]any)
	require.NotEmpty(t, rawCmd,
		"migrate container must set command (shell wrapper); bare args are rejected by the CLI in KV form (#455)")
	for _, x := range rawCmd {
		s, ok := x.(string)
		require.True(t, ok, "command entries must be strings")
		command = append(command, s)
	}

	rawArgs, _ := c["args"].([]any)
	require.Len(t, rawArgs, 1,
		"migrate container args must be a single shell script (the wrapper body)")
	script, ok := rawArgs[0].(string)
	require.True(t, ok, "the wrapper script must be a string")
	return command, script
}

// TestMigrationJob_UsesShellWrapperWithEncodedURL verifies the migrate
// container overrides the image entrypoint with a `/bin/sh -ec` wrapper that
// builds a `postgres://` URL — the form the standalone migrate CLI requires
// (#455). The previous libpq-KV-form args are gone.
func TestMigrationJob_UsesShellWrapperWithEncodedURL(t *testing.T) {
	docs := helmTemplate(t, "")
	job := findMigrationJob(t, docs)
	require.NotNil(t, job, "migration Job must render by default (migrations.enabled=true)")

	command, script := migrationScript(t, job)

	require.Equal(t, []string{"/bin/sh", "-ec"}, command,
		"migrate command must be a shell wrapper (/bin/sh -ec) so the password can be URL-encoded at runtime")

	// The wrapper must produce a postgres:// URL — the only form the CLI accepts.
	assert.Contains(t, script, "postgres://",
		"wrapper script must build a postgres:// URL (KV form is rejected by the migrate CLI; #455)")

	// The migrate binary must be exec'd (replaces the shell so signals/Kubelet
	// lifecycle attach to migrate directly).
	assert.Regexp(t, `(^|\n)[[:space:]]*exec[[:space:]]+(/migrate|migrate)\b`, script,
		"wrapper must exec the migrate binary (image entrypoint is overridden by command)")

	// The DB connection parameters must be sourced from env vars at runtime,
	// not interpolated at render time (secrets stay out-of-band). Accept both
	// braced (${VAR}) and unbraced ($VAR) shell forms.
	for _, name := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE"} {
		assert.True(t, strings.Contains(script, "${"+name+"}") || strings.Contains(script, "$"+name),
			"wrapper must read connection parameter %s from an env var at runtime", name)
	}
}

// TestMigrationJob_PasswordURLEncodedNotRaw is the #455 core regression:
// the password must NOT appear raw inside the postgres:// URL. It must flow
// through a percent-encoding pipeline (od | tr | sed) into a separate
// variable, and only that encoded variable is interpolated into the URL.
// A raw `$DB_PASSWORD` / `$(DB_PASSWORD)` inside the URL is the #424 bug
// re-introduced.
func TestMigrationJob_PasswordURLEncodedNotRaw(t *testing.T) {
	docs := helmTemplate(t, "")
	job := findMigrationJob(t, docs)
	require.NotNil(t, job)
	_, script := migrationScript(t, job)

	// There must be a percent-encoding pipeline that consumes DB_PASSWORD and
	// produces an encoded value. The canonical pipeline encodes every byte:
	//   printf '%s' "$DB_PASSWORD" | od -An -tx1 | tr -d ' \n' | sed 's/../%&/g'
	assert.Contains(t, script, `od -An -tx1`,
		"wrapper must percent-encode via od (od -An -tx1 | tr -d ' \\n' | sed 's/../%&/g')")
	assert.Contains(t, script, `tr -d ' \n'`,
		"wrapper must strip od's whitespace before re-pairing bytes")
	assert.Contains(t, script, `s/../%&/g`,
		"wrapper must prefix each byte pair with %% (sed 's/../%%&/g')")

	// Extract the postgres:// URL substring and assert the raw password never
	// reaches it. The URL is on the line containing `-database "postgres://`.
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "postgres://") {
			continue
		}
		assert.NotContains(t, line, "DB_PASSWORD",
			"the raw password env var must NOT appear inside the postgres:// URL; it must be percent-encoded first (#455/#424)")
		assert.NotContains(t, line, "$(DB_PASSWORD)",
			"K8s $(DB_PASSWORD) substitution must NOT appear inside the URL (no URL-encoding; #424)")
		// The encoded variable must be what's interpolated.
		assert.Contains(t, line, "enc_pwd",
			"the postgres:// URL must interpolate the percent-encoded password variable (enc_pwd)")
		break
	}
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

// TestMigrationJob_ScriptProducesValidURLOnReservedCharPassword is the
// integration-level proof for #455. It renders the chart, extracts the
// exact shell-wrapper bytes, then EXECUTES the wrapper against a `migrate`
// shim (a script that records its argv) with a password containing every
// URL-reserved character. It then asserts the migrate binary received a
// syntactically-valid postgres:// URL whose decoded userinfo matches the
// original password and user byte-for-byte.
//
// This closes the test gap identified in #455: the previous chart test only
// asserted on rendered YAML shape and never exercised the arg-construction
// logic. The only remaining uncovered piece is the migrate binary actually
// connecting to a live Postgres, which is production-cluster validation.
func TestMigrationJob_ScriptProducesValidURLOnReservedCharPassword(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH; cannot execute the wrapper script")
	}
	docs := helmTemplate(t, "")
	job := findMigrationJob(t, docs)
	require.NotNil(t, job)
	_, script := migrationScript(t, job)

	// Build a `migrate` shim that records its argv to a file. The wrapper
	// `exec`s `migrate`, so the shim must be found on PATH.
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.txt")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "migrate"), []byte(shim), 0o755))

	// Password + user containing every URL-reserved char (the #424/#455 set)
	// plus whitespace and a control char.
	const (
		password = "P@ss/w0rd?+#%=& a:b\tq"
		user     = "u@ser:name"
	)

	cmd := exec.Command("/bin/sh", "-ec", script)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"DB_HOST=pg-host",
		"DB_PORT=5432",
		"DB_USER="+user,
		"DB_PASSWORD="+password,
		"DB_NAME=appdb",
		"DB_SSLMODE=require",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(),
		"wrapper script must execute cleanly against the shim; stderr:\n%s", stderr.String())

	argvBytes, err := os.ReadFile(argvFile)
	require.NoError(t, err, "shim must have recorded argv (wrapper must have exec'd migrate)")
	argv := strings.Split(strings.TrimRight(string(argvBytes), "\n"), "\n")

	// Locate the -database value (CLI accepts `-database X` or `-database=X`).
	var dbArg string
	for i, a := range argv {
		if a == "-database" && i+1 < len(argv) {
			dbArg = argv[i+1]
			break
		}
		if strings.HasPrefix(a, "-database=") {
			dbArg = strings.TrimPrefix(a, "-database=")
		}
	}
	require.NotEmpty(t, dbArg, "migrate must receive a -database argument")

	u, err := url.Parse(dbArg)
	require.NoError(t, err, "-database value must be a parseable URL; got %q", dbArg)
	assert.Equal(t, "postgres", u.Scheme,
		"-database URL scheme must be postgres (CLI rejects schemes-less KV form; #455)")
	assert.Equal(t, "pg-host:5432", u.Host, "host:port must round-trip unencoded")
	assert.Equal(t, "appdb", strings.TrimPrefix(u.Path, "/"), "dbname must round-trip unencoded")
	assert.Equal(t, "require", u.Query().Get("sslmode"), "sslmode must round-trip unencoded")

	// The decisive assertion: the password decodes back to the exact original
	// (reserved-char-laden) value. url.URL decodes percent-encoding in userinfo.
	gotUser := u.User.Username()
	gotPass, hasPass := u.User.Password()
	require.True(t, hasPass, "decoded URL must carry a password in userinfo")
	assert.Equal(t, user, gotUser,
		"decoded user must match the reserved-char input byte-for-byte")
	assert.Equal(t, password, gotPass,
		"decoded password must match the reserved-char input byte-for-byte — "+
			"this is the #455 regression guard: any raw/missing encoding breaks here")
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
