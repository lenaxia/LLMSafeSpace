// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/lenaxia/llmsafespace/pkg/agentd/secrets"
)

// US-44.7: Restart Reason Logging.
//
// agentd writes a restart-reason marker file BEFORE every opencode
// restart, covering reasons: env_secrets_changed, api_key_changed, crash,
// oom. (user_requested is deferred — no trigger exists today; see
// design/stories/epic-44-session-reliability-transparency/README.md.)
//
// The reason is logged at WRITE time (real-time visibility) AND on the
// next pod boot (secondary, one-shot). The boot path distinguishes fresh
// vs stale markers so a pod that boots for an unrelated reason (node
// drain) does not misattribute an old crash to itself.
//
// opencode is third-party and cannot be modified — agentd handles all
// reason recording.

// testObserver builds a zap logger core backed by an in-memory observer so
// tests can assert on emitted fields/levels/messages. Returns the recorded
// logs and the core (which is passed to the production functions that take
// a zapcore.Core, e.g. logRestartReason).
func testObserver(t *testing.T) (*observer.ObservedLogs, zapcore.Core) {
	t.Helper()
	core, recorded := observer.New(zapcore.DebugLevel)
	return recorded, core
}

// nopCore is a silent core for read paths that do not assert logging.
func nopCore() zapcore.Core {
	return zapcore.NewNopCore()
}

// toStringSlice coerces a zap field value ([]interface{} holding strings)
// back to []string for assertion.
func toStringSlice(t *testing.T, v any) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", v)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("expected string element, got %T", e)
		}
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// restartReason struct shape (F6: exactly {Reason, Timestamp, SecretNames})
// ---------------------------------------------------------------------------

func TestRestartReasonStruct_HasNoExitCodeOrMemoryLimitFields(t *testing.T) {
	r := restartReason{
		Reason:      "oom",
		Timestamp:   "2026-06-18T00:00:00Z",
		SecretNames: []string{"A"},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	for k := range raw {
		switch k {
		case "reason", "timestamp", "secretNames":
			// allowed
		default:
			t.Errorf("struct must not emit field %q (US-44.7 spec shape is exactly {reason, timestamp, secretNames})", k)
		}
	}
}

// ---------------------------------------------------------------------------
// writeRestartReasonMarker
// ---------------------------------------------------------------------------

func TestWriteRestartReasonMarker_CreatesFileWithCorrectJSON(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	err := writeRestartReasonMarker(markerPath, "env_secrets_changed", []string{"GH_TOKEN"})
	require.NoError(t, err)

	data, err := os.ReadFile(markerPath)
	require.NoError(t, err)

	var r restartReason
	require.NoError(t, json.Unmarshal(data, &r), "marker must be valid JSON")
	assert.Equal(t, "env_secrets_changed", r.Reason)
	assert.Equal(t, []string{"GH_TOKEN"}, r.SecretNames)
	assert.NotEmpty(t, r.Timestamp, "timestamp must be set")

	_, perr := time.Parse(time.RFC3339, r.Timestamp)
	assert.NoError(t, perr, "timestamp must be RFC3339-parseable")
}

func TestWriteRestartReasonMarker_OverwritesExistingMarker(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	require.NoError(t, writeRestartReasonMarker(markerPath, "env_secrets_changed", []string{"A"}))
	first, err := os.ReadFile(markerPath)
	require.NoError(t, err)

	require.NoError(t, writeRestartReasonMarker(markerPath, "api_key_changed", []string{"B"}))
	second, err := os.ReadFile(markerPath)
	require.NoError(t, err)

	var r restartReason
	require.NoError(t, json.Unmarshal(second, &r))
	assert.Equal(t, "api_key_changed", r.Reason, "second write must overwrite the first")
	assert.Equal(t, []string{"B"}, r.SecretNames)

	var r1 restartReason
	require.NoError(t, json.Unmarshal(first, &r1))
	assert.Equal(t, "env_secrets_changed", r1.Reason, "sanity: first read captured original")
}

func TestWriteRestartReasonMarker_CreatesParentDirIfMissing(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "nested", "deeper", ".opencode-restart-reason")

	err := writeRestartReasonMarker(markerPath, "crash", nil)
	require.NoError(t, err, "MkdirAll must create missing parent dirs")

	_, err = os.Stat(markerPath)
	assert.NoError(t, err, "marker file must exist after write")
}

func TestWriteRestartReasonMarker_EmptySecretNames_OmitsField(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	require.NoError(t, writeRestartReasonMarker(markerPath, "oom", nil))

	data, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "secretNames",
		"empty secretNames must be omitted via omitempty")
}

// ---------------------------------------------------------------------------
// readRestartReasonMarker
// ---------------------------------------------------------------------------

func TestReadRestartReasonMarker_ValidFile_ReturnsReason(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	require.NoError(t, writeRestartReasonMarker(markerPath, "api_key_changed", []string{"KEY"}))

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	require.True(t, ok, "valid marker must return ok=true")
	assert.Equal(t, "api_key_changed", r.Reason)
	assert.Equal(t, []string{"KEY"}, r.SecretNames)
	assert.NotEmpty(t, r.Timestamp)
}

func TestReadRestartReasonMarker_NoFile_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "does-not-exist")

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	assert.False(t, ok, "missing marker must return ok=false")
	assert.Equal(t, restartReason{}, r, "missing marker must return zero value")
}

func TestReadRestartReasonMarker_CorruptJSON_ReturnsFalseNoPanic(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	require.NoError(t, os.WriteFile(markerPath, []byte("{not valid json"), 0o600))

	var r restartReason
	var ok bool
	assert.NotPanics(t, func() {
		r, ok = readRestartReasonMarker(markerPath, nopCore())
	})
	assert.False(t, ok, "corrupt marker must return ok=false (boot must not fail)")
	assert.Equal(t, restartReason{}, r)
}

func TestReadRestartReasonMarker_EmptyFile_ReturnsFalseNoPanic(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	require.NoError(t, os.WriteFile(markerPath, []byte{}, 0o600))

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	assert.False(t, ok, "empty file is not valid JSON → ok=false")
	assert.Equal(t, restartReason{}, r)
}

// ---------------------------------------------------------------------------
// logRestartReasonAtWrite (F1: real-time logging at marker-write time)
// ---------------------------------------------------------------------------

func TestLogRestartReasonAtWrite_EmitsInfoWithReasonAndSecretNames(t *testing.T) {
	recorded, lg := testObserver(t)

	logRestartReasonAtWrite("env_secrets_changed", []string{"GH_TOKEN"}, lg)

	logs := recorded.FilterMessage("opencode restart scheduled").All()
	require.Len(t, logs, 1, "must emit exactly one 'opencode restart scheduled' log")
	entry := logs[0]
	assert.Equal(t, zapcore.InfoLevel, entry.Level, "fresh write-time log must be Info")
	assert.Equal(t, "env_secrets_changed", entry.ContextMap()["reason"])
	// zap.Strings encodes as []interface{} (each element is interface{}),
	// so compare via ElementsMatch on the recovered slice.
	assert.ElementsMatch(t, []string{"GH_TOKEN"}, toStringSlice(t, entry.ContextMap()["secretNames"]))
}

func TestLogRestartReasonAtWrite_CrashReason_NoSecretNames(t *testing.T) {
	recorded, lg := testObserver(t)

	logRestartReasonAtWrite("crash", nil, lg)

	logs := recorded.FilterMessage("opencode restart scheduled").All()
	require.Len(t, logs, 1)
	entry := logs[0]
	assert.Equal(t, "crash", entry.ContextMap()["reason"])
	_, present := entry.ContextMap()["secretNames"]
	assert.False(t, present, "nil secretNames must not appear as a field")
}

func TestLogRestartReasonAtWrite_OOMReason_EmitsAtWarn(t *testing.T) {
	recorded, lg := testObserver(t)

	logRestartReasonAtWrite("oom", nil, lg)

	logs := recorded.FilterMessage("opencode restart scheduled").All()
	require.Len(t, logs, 1)
	assert.Equal(t, zapcore.WarnLevel, logs[0].Level,
		"oom is the most severe reason — log at Warn")
}

// ---------------------------------------------------------------------------
// logRestartReason (boot hook: read → log → delete; F1: stale detection)
// ---------------------------------------------------------------------------

func TestLogRestartReason_FreshMarker_LogsAtInfoAndDeletesFile(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	require.NoError(t, writeRestartReasonMarker(markerPath, "crash", nil))
	require.FileExists(t, markerPath)

	recorded, lg := testObserver(t)
	logRestartReason(markerPath, lg)

	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "marker must be deleted (one-shot)")

	logs := recorded.FilterMessage("opencode restarted").All()
	require.Len(t, logs, 1, "fresh marker must log 'opencode restarted' at Info")
	assert.Equal(t, zapcore.InfoLevel, logs[0].Level)
	assert.Equal(t, "crash", logs[0].ContextMap()["reason"])
}

func TestLogRestartReason_StaleMarker_LogsAtDebugWithNote(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	// Write a marker with a timestamp 1 hour in the past — well past the
	// 10-minute freshness threshold.
	old := restartReason{
		Reason:    "crash",
		Timestamp: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	data, err := json.Marshal(old)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(markerPath, data, 0o600))

	recorded, lg := testObserver(t)
	logRestartReason(markerPath, lg)

	// Fresh Info line must NOT be emitted for a stale marker.
	assert.Empty(t, recorded.FilterMessage("opencode restarted").All(),
		"stale marker must not emit the fresh 'opencode restarted' Info line")

	staleLogs := recorded.FilterMessage("stale restart-reason marker from previous run (may be unrelated to this boot)").All()
	require.Len(t, staleLogs, 1, "stale marker must emit the stale-note line")
	assert.Equal(t, zapcore.DebugLevel, staleLogs[0].Level,
		"stale attribution is low-signal → Debug")
	assert.Equal(t, "crash", staleLogs[0].ContextMap()["reason"])

	// Stale marker is still consumed (deleted) — one-shot semantics.
	_, err = os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "stale marker must still be deleted (one-shot)")
}

func TestLogRestartReason_BoundaryExactly10Minutes_Fresh(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	// 9 min old — comfortably inside the fresh region. (Testing the exact
	// 10-min boundary is inherently flaky because test-execution time
	// pushes a precisely-10-min-old marker across the line.)
	boundary := restartReason{
		Reason:    "oom",
		Timestamp: time.Now().UTC().Add(-9 * time.Minute).Format(time.RFC3339),
	}
	data, err := json.Marshal(boundary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(markerPath, data, 0o600))

	recorded, lg := testObserver(t)
	logRestartReason(markerPath, lg)

	require.Len(t, recorded.FilterMessage("opencode restarted").All(), 1,
		"marker younger than the 10-min threshold must be logged as fresh Info")
	assert.Empty(t, recorded.FilterMessage("stale restart-reason marker from previous run (may be unrelated to this boot)").All())
}

func TestLogRestartReason_BoundaryCorruptTimestamp_TreatedAsStale(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")
	bad := restartReason{
		Reason:    "crash",
		Timestamp: "not-a-timestamp",
	}
	data, err := json.Marshal(bad)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(markerPath, data, 0o600))

	recorded, lg := testObserver(t)
	assert.NotPanics(t, func() { logRestartReason(markerPath, lg) })

	assert.Empty(t, recorded.FilterMessage("opencode restarted").All(),
		"unparseable timestamp must not be treated as fresh (safe fallback → stale)")
	require.Len(t, recorded.FilterMessage("stale restart-reason marker from previous run (may be unrelated to this boot)").All(), 1)

	_, err = os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "marker with bad timestamp must still be deleted")
}

func TestLogRestartReason_NoMarker_SilentNoOp(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "does-not-exist")

	recorded, lg := testObserver(t)
	assert.NotPanics(t, func() { logRestartReason(markerPath, lg) },
		"absent marker must be a silent no-op — no error, no panic")

	assert.Empty(t, recorded.All(), "absent marker must emit no log lines")

	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "no file must be created by the no-op")
}

// ---------------------------------------------------------------------------
// classifySecretRestartReason
// ---------------------------------------------------------------------------

func TestClassifySecretRestartReason(t *testing.T) {
	tests := []struct {
		name       string
		batch      []secrets.Secret
		wantReason string
		wantNames  []string
	}{
		{
			name:       "empty batch",
			batch:      nil,
			wantReason: "",
			wantNames:  nil,
		},
		{
			name: "env-secret only → env_secrets_changed",
			batch: []secrets.Secret{
				{Type: "env-secret", Name: "github-token", Metadata: map[string]string{"var_name": "GH_TOKEN"}, Plaintext: "x"},
			},
			wantReason: "env_secrets_changed",
			wantNames:  []string{"GH_TOKEN"},
		},
		{
			name: "env-secret without var_name falls back to Name",
			batch: []secrets.Secret{
				{Type: "env-secret", Name: "PLAIN_ENV", Plaintext: "x"},
			},
			wantReason: "env_secrets_changed",
			wantNames:  []string{"PLAIN_ENV"},
		},
		{
			name: "api-key only → api_key_changed",
			batch: []secrets.Secret{
				{Type: "api-key", Name: "my-key", Plaintext: "x"},
			},
			wantReason: "api_key_changed",
			wantNames:  []string{"my-key"},
		},
		{
			name: "mixed api-key + env-secret → api_key_changed takes precedence",
			batch: []secrets.Secret{
				{Type: "env-secret", Name: "gh", Metadata: map[string]string{"var_name": "GH_TOKEN"}, Plaintext: "x"},
				{Type: "api-key", Name: "my-key", Plaintext: "x"},
			},
			wantReason: "api_key_changed",
			wantNames:  []string{"GH_TOKEN", "my-key"},
		},
		{
			name: "llm-provider only → no restart",
			batch: []secrets.Secret{
				{Type: "llm-provider", Name: "anthropic", Plaintext: `{"apiKey":"x"}`},
			},
			wantReason: "",
			wantNames:  nil,
		},
		{
			name: "non-restart types only → no restart",
			batch: []secrets.Secret{
				{Type: "ssh-key", Name: "k", Metadata: map[string]string{"key_type": "ed25519"}, Plaintext: "x"},
				{Type: "secret-file", Name: "f", Metadata: map[string]string{"mount_path": "y"}, Plaintext: "x"},
			},
			wantReason: "",
			wantNames:  nil,
		},
		{
			name: "multiple env-secrets collect all var_names",
			batch: []secrets.Secret{
				{Type: "env-secret", Name: "a", Metadata: map[string]string{"var_name": "VAR_A"}, Plaintext: "x"},
				{Type: "env-secret", Name: "b", Metadata: map[string]string{"var_name": "VAR_B"}, Plaintext: "x"},
			},
			wantReason: "env_secrets_changed",
			wantNames:  []string{"VAR_A", "VAR_B"},
		},
		{
			name: "env-secret with empty var_name string falls back to Name",
			batch: []secrets.Secret{
				{Type: "env-secret", Name: "FALLBACK", Metadata: map[string]string{"var_name": ""}, Plaintext: "x"},
			},
			wantReason: "env_secrets_changed",
			wantNames:  []string{"FALLBACK"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, names := classifySecretRestartReason(tc.batch)
			assert.Equal(t, tc.wantReason, reason)
			if tc.wantNames == nil {
				assert.Nil(t, names)
			} else {
				assert.Equal(t, tc.wantNames, names)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration: classify → write → read (F4: secrets marker-write wiring)
// ---------------------------------------------------------------------------

func TestRestartReason_SecretsBatch_ClassifyWriteRead(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	batch := []secrets.Secret{
		{Type: "env-secret", Name: "gh", Metadata: map[string]string{"var_name": "GH_TOKEN"}, Plaintext: "x"},
		{Type: "env-secret", Name: "db", Metadata: map[string]string{"var_name": "DATABASE_URL"}, Plaintext: "x"},
	}

	reason, names := classifySecretRestartReason(batch)
	require.Equal(t, "env_secrets_changed", reason)

	require.NoError(t, writeRestartReasonMarker(markerPath, reason, names))

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	require.True(t, ok)
	assert.Equal(t, "env_secrets_changed", r.Reason)
	assert.Equal(t, []string{"GH_TOKEN", "DATABASE_URL"}, r.SecretNames,
		"wiring from batch → classifier → marker must preserve var_name ordering")
}

func TestRestartReason_APIKeyBatch_ClassifyWriteRead(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	batch := []secrets.Secret{
		{Type: "api-key", Name: "stripe", Plaintext: "sk-..."},
	}
	reason, names := classifySecretRestartReason(batch)
	require.NoError(t, writeRestartReasonMarker(markerPath, reason, names))

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	require.True(t, ok)
	assert.Equal(t, "api_key_changed", r.Reason)
	assert.Equal(t, []string{"stripe"}, r.SecretNames)
}

// ---------------------------------------------------------------------------
// Integration: crash write → read → logRestartReason deletes
// ---------------------------------------------------------------------------

func TestRestartReason_Crash_WriteReadLogDelete_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-restart-reason")

	require.NoError(t, writeRestartReasonMarker(markerPath, "crash", nil))

	r, ok := readRestartReasonMarker(markerPath, nopCore())
	require.True(t, ok)
	assert.Equal(t, "crash", r.Reason)
	require.FileExists(t, markerPath)

	recorded, lg := testObserver(t)
	logRestartReason(markerPath, lg)

	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "after logRestartReason the marker must be gone")

	r2, ok2 := readRestartReasonMarker(markerPath, nopCore())
	assert.False(t, ok2, "second read after delete must return false")
	assert.Equal(t, restartReason{}, r2)

	require.Len(t, recorded.FilterMessage("opencode restarted").All(), 1)
}

// ---------------------------------------------------------------------------
// Markers are independent: restart-reason marker does not collide with OOM
// marker (different filenames on the same PVC).
// ---------------------------------------------------------------------------

func TestRestartReasonMarker_IndependentOfOOMMarker(t *testing.T) {
	dir := t.TempDir()
	restartPath := filepath.Join(dir, ".opencode-restart-reason")
	oomPath := filepath.Join(dir, ".opencode-oom-marker")

	require.NoError(t, writeRestartReasonMarker(restartPath, "oom", nil))
	require.NoError(t, writeOOMMarker(oomPath, "2Gi"))

	require.FileExists(t, restartPath)
	require.FileExists(t, oomPath)

	_, lg := testObserver(t)
	// logRestartReason on the restart marker must NOT touch the OOM marker.
	logRestartReason(restartPath, lg)

	_, restartErr := os.Stat(restartPath)
	assert.True(t, os.IsNotExist(restartErr), "restart marker must be deleted")
	_, oomErr := os.Stat(oomPath)
	assert.NoError(t, oomErr, "OOM marker must be untouched by restart-reason logging")
}
