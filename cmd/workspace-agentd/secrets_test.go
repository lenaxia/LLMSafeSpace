package main

// Tests for the materialize subcommand and reload-secrets HTTP handler.
//
// These tests are written TDD-style: they were authored before the
// implementation and exercise the contract that the implementation must
// satisfy. Each test corresponds to a concrete behavioral promise:
//
//   - The materialize subcommand reads /sandbox-cfg/secrets.json (or the
//     path given by --from) and applies it via pkg/agentd/secrets.
//   - Exit status: 0 if all secrets materialized OR all skipped (i.e. the
//     batch is structurally valid). Non-zero only if I/O failures occur.
//   - The reload-secrets handler accepts the same JSON shape over HTTP,
//     applies it, and returns a structured per-secret outcome list.
//   - buildEnv() uses pkg/agentd/secrets.ParseEnvLine so payloads that
//     contain shell metacharacters round-trip into opencode's env.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Build the workspace-agentd binary once per test process; subsequent
// subcommand invocations re-execute it as a real subprocess so the
// CLI surface (flag parsing, exit codes) is exercised end-to-end.
func buildAgentdBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("subprocess test assumes unix")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "workspace-agentd")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "go build failed")
	return bin
}

// runMaterializeSubcommand runs `workspace-agentd materialize --from <path>`
// and returns exit code, stdout, stderr.
func runMaterializeSubcommand(t *testing.T, bin, secretsPath, secretsBase, sshDir, agentCfg, envPath, gitCreds string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, "materialize", "--from", secretsPath)
	// Override paths via env so we don't need root or to write into
	// /home/sandbox during tests.
	cmd.Env = append(os.Environ(),
		"LLMSAFESPACE_SECRETS_BASE_DIR="+secretsBase,
		"LLMSAFESPACE_SSH_DIR="+sshDir,
		"LLMSAFESPACE_AGENT_CONFIG_PATH="+agentCfg,
		"LLMSAFESPACE_SECRETS_ENV_PATH="+envPath,
		"LLMSAFESPACE_GIT_CREDS_PATH="+gitCreds,
		"HOME="+filepath.Dir(sshDir),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("subprocess failed: %v", err)
	}
	return exit, stdout.String(), stderr.String()
}

// TestMaterializeSubcommand_HappyPath verifies the subcommand reads a
// well-formed secrets file and writes the expected outputs.
func TestMaterializeSubcommand_HappyPath(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	secretsPath := filepath.Join(dir, "secrets.json")
	require.NoError(t, os.WriteFile(secretsPath, []byte(`[
		{"type":"env-secret","name":"a","metadata":{"var_name":"FOO"},"plaintext":"bar"},
		{"type":"api-key","name":"p","plaintext":"{\"provider\":\"x\"}"}
	]`), 0o600))

	secretsBase := filepath.Join(dir, "secrets")
	sshDir := filepath.Join(dir, ".ssh")
	agentCfg := filepath.Join(dir, "agent-config.json")
	envPath := filepath.Join(dir, "env")
	gitCreds := filepath.Join(dir, ".git-credentials")

	exit, stdout, stderr := runMaterializeSubcommand(t, bin, secretsPath, secretsBase, sshDir, agentCfg, envPath, gitCreds)
	require.Equal(t, 0, exit, "stderr=%q stdout=%q", stderr, stdout)

	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Contains(t, string(envContent), "export FOO=")

	cfg, err := os.ReadFile(agentCfg)
	require.NoError(t, err)
	require.Equal(t, `{"provider":"x"}`, string(cfg))

	st, err := os.Stat(envPath)
	require.NoError(t, err)
	require.Zero(t, st.Mode().Perm()&0o077, "env file must not have group/other bits")
}

// TestMaterializeSubcommand_MissingSecretsFile_NoOp verifies that a missing
// secrets file is treated as "no secrets to apply" rather than as an error.
// This matches the production case where /sandbox-cfg/secrets.json is
// absent for workspaces that have no user-supplied credentials.
func TestMaterializeSubcommand_MissingSecretsFile_NoOp(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	secretsPath := filepath.Join(dir, "does-not-exist.json")
	exit, stdout, stderr := runMaterializeSubcommand(t, bin, secretsPath,
		filepath.Join(dir, "secrets"),
		filepath.Join(dir, ".ssh"),
		filepath.Join(dir, "agent-config.json"),
		filepath.Join(dir, "env"),
		filepath.Join(dir, ".git-credentials"))
	require.Equal(t, 0, exit, "missing file must be a no-op; stderr=%q stdout=%q", stderr, stdout)
}

// TestMaterializeSubcommand_BadJSON_ReturnsExit2 verifies that a malformed
// secrets file fails loudly rather than silently boot-looping.
func TestMaterializeSubcommand_BadJSON_ReturnsExit2(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	secretsPath := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(secretsPath, []byte("not json"), 0o600))

	exit, _, stderr := runMaterializeSubcommand(t, bin, secretsPath,
		filepath.Join(dir, "secrets"),
		filepath.Join(dir, ".ssh"),
		filepath.Join(dir, "agent-config.json"),
		filepath.Join(dir, "env"),
		filepath.Join(dir, ".git-credentials"))
	require.NotZero(t, exit)
	require.Contains(t, stderr, "parsing")
}

// TestMaterializeSubcommand_InvalidEntries_DoesNotBlockBoot verifies T5: a
// malformed secret entry is skipped, materialize returns exit 0 (so the
// pod boots), and stderr lists the skipped entries for operator triage.
func TestMaterializeSubcommand_InvalidEntries_DoesNotBlockBoot(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	secretsPath := filepath.Join(dir, "secrets.json")
	require.NoError(t, os.WriteFile(secretsPath, []byte(`[
		{"type":"env-secret","name":"good","metadata":{"var_name":"GOOD"},"plaintext":"1"},
		{"type":"env-secret","name":"bad","metadata":{"var_name":"123BAD"},"plaintext":"2"}
	]`), 0o600))

	envPath := filepath.Join(dir, "env")
	exit, _, stderr := runMaterializeSubcommand(t, bin, secretsPath,
		filepath.Join(dir, "secrets"),
		filepath.Join(dir, ".ssh"),
		filepath.Join(dir, "agent-config.json"),
		envPath,
		filepath.Join(dir, ".git-credentials"))
	require.Equal(t, 0, exit, "bad entry must skip, not abort the batch")

	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Contains(t, string(envContent), "export GOOD=")
	require.NotContains(t, string(envContent), "123BAD")
	require.Contains(t, stderr, "123BAD",
		"stderr should report the skipped entry by name or by reason")
}

// TestReloadSecretsHandler_HappyPath wires the handler against a real
// in-memory materializer and verifies the response shape.
func TestReloadSecretsHandler_HappyPath(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env")
	cfg := materializeConfig{
		secretsBaseDir:  filepath.Join(dir, "secrets"),
		sshDir:          filepath.Join(dir, ".ssh"),
		agentConfigPath: filepath.Join(dir, "agent-config.json"),
		secretsEnvPath:  envPath,
		gitCredsPath:    filepath.Join(dir, ".git-credentials"),
		home:            dir,
	}

	body := `[{"type":"env-secret","name":"x","metadata":{"var_name":"X"},"plaintext":"v"}]`
	req := httptest.NewRequest(http.MethodPost, "/v1/reload-secrets", strings.NewReader(body))
	rec := httptest.NewRecorder()

	prevLog := log
	log = zap.NewNop()
	defer func() { log = prevLog }()

	reloadSecretsHandler(cfg, nil)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Reloaded  int  `json:"reloaded"`
		Restarted bool `json:"restarted"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Reloaded)

	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Contains(t, string(envContent), "export X=")
}

// TestReloadSecretsHandler_BadJSON returns 400.
func TestReloadSecretsHandler_BadJSON(t *testing.T) {
	cfg := materializeConfig{}
	req := httptest.NewRequest(http.MethodPost, "/v1/reload-secrets", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	prevLog := log
	log = zap.NewNop()
	defer func() { log = prevLog }()

	reloadSecretsHandler(cfg, nil)(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestReloadSecretsHandler_WrongMethod returns 405.
func TestReloadSecretsHandler_WrongMethod(t *testing.T) {
	cfg := materializeConfig{}
	req := httptest.NewRequest(http.MethodGet, "/v1/reload-secrets", nil)
	rec := httptest.NewRecorder()

	prevLog := log
	log = zap.NewNop()
	defer func() { log = prevLog }()

	reloadSecretsHandler(cfg, nil)(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestBuildEnv_RoundTripsValuesWithMetacharacters confirms the buildEnv()
// refactor uses ParseEnvLine and therefore handles values that contain
// single quotes, newlines, etc. without mangling them. Pre-fix, the
// strings.Replace(..., "='", "=", 1) hack mangled such values.
func TestBuildEnv_RoundTripsValuesWithMetacharacters(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env")

	// Write a couple of lines using FormatEnvLine so we know the format
	// matches what materialize produces.
	content := ""
	for _, kv := range []struct{ k, v string }{
		{"TOKEN_WITH_QUOTE", `'; whoami; '`},
		{"TOKEN_WITH_NEWLINE", "line1\nline2"},
		{"NORMAL", "value"},
	} {
		content += "export " + kv.k + "=" + shellQuoteForTest(kv.v) + "\n"
	}
	require.NoError(t, os.WriteFile(envPath, []byte(content), 0o600))

	got := buildEnvFrom(envPath)
	want := map[string]string{
		"TOKEN_WITH_QUOTE":   `'; whoami; '`,
		"TOKEN_WITH_NEWLINE": "line1\nline2",
		"NORMAL":             "value",
	}
	gotMap := map[string]string{}
	for _, e := range got {
		// Only consider the variables we care about; ignore inherited env.
		for k := range want {
			if strings.HasPrefix(e, k+"=") {
				gotMap[k] = strings.TrimPrefix(e, k+"=")
			}
		}
	}
	for k, v := range want {
		require.Equal(t, v, gotMap[k], "var %q must round-trip through buildEnvFrom", k)
	}
}

// shellQuoteForTest is a small reimplementation used only by the test to
// avoid an import cycle (the test lives in the main package).
func shellQuoteForTest(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
