package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func getSealKeyBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		binPath = filepath.Join(os.TempDir(), "seal-key-test-bin")
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Env = append(os.Environ(), "GOPROXY=direct", "GONOSUMCHECK=*", "GONOSUMDB=*")
		var out []byte
		out, binErr = cmd.CombinedOutput()
		if binErr != nil {
			binErr = fmt.Errorf("build failed: %s: %w", out, binErr)
		}
	})
	require.NoError(t, binErr)
	return binPath
}

// hexKeyRe matches a 32-byte root key hex-encoded (64 lowercase hex chars).
// A randomly-generated root key is exactly this; nothing else in seal-key's
// output (paths, passphrases, status text) reaches 64 consecutive hex chars,
// so a match is a strong signal that the secret was leaked to the stream.
var hexKeyRe = regexp.MustCompile("[0-9a-f]{64}")

func TestSealKey_RoundTrip(t *testing.T) {
	bin := getSealKeyBin(t)
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")
	passPath := filepath.Join(tmpDir, "passphrase")
	require.NoError(t, os.WriteFile(passPath, []byte("test-pass"), 0600))

	cmd := exec.Command(bin, "-out", sealedPath, "-passphrase-file", passPath)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	data, err := os.ReadFile(sealedPath)
	require.NoError(t, err)
	// V1 sealed format: magic(6)+salt(32)+nonce(12)+ct(32+16) = 98 min (US-50.11).
	assert.GreaterOrEqual(t, len(data), 98)
}

func TestSealKey_WithExplicitKey(t *testing.T) {
	bin := getSealKeyBin(t)
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")

	key := hex.EncodeToString(make([]byte, 32))
	cmd := exec.Command(bin, "-out", sealedPath, "-passphrase", "pass", "-key", key)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	data, err := os.ReadFile(sealedPath)
	require.NoError(t, err)
	// V1 sealed format: magic(6)+salt(32)+nonce(12)+ct(32+16) = 98 min (US-50.11).
	assert.GreaterOrEqual(t, len(data), 98)
}

func TestSealKey_MissingOutFlag(t *testing.T) {
	bin := getSealKeyBin(t)
	cmd := exec.Command(bin, "-passphrase", "pass")
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "-out is required")
}

func TestSealKey_MissingPassphrase(t *testing.T) {
	bin := getSealKeyBin(t)
	cmd := exec.Command(bin, "-out", "/tmp/test-sealed")
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "passphrase")
}

func TestSealKey_InvalidKeyHex(t *testing.T) {
	bin := getSealKeyBin(t)
	cmd := exec.Command(bin, "-out", "/tmp/test-sealed", "-passphrase", "pass", "-key", "not-hex")
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "invalid hex")
}

func TestSealKey_KeyWrongSize(t *testing.T) {
	bin := getSealKeyBin(t)
	cmd := exec.Command(bin, "-out", "/tmp/test-sealed", "-passphrase", "pass", "-key", "aabb")
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "32 bytes")
}

// US-50.10: the root key must never be emitted to stdout or stderr by default.
func TestSealKey_Default_NoKeyInOutput(t *testing.T) {
	bin := getSealKeyBin(t)
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")
	passPath := filepath.Join(tmpDir, "passphrase")
	require.NoError(t, os.WriteFile(passPath, []byte("test-pass"), 0600))

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "-out", sealedPath, "-passphrase-file", passPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "seal-key should succeed; stderr=%q", stderr.String())

	if hexKeyRe.MatchString(stdout.String()) {
		t.Errorf("stdout must not contain the root key, got: %q", stdout.String())
	}
	if hexKeyRe.MatchString(stderr.String()) {
		t.Errorf("stderr must not contain the root key, got: %q", stderr.String())
	}
}

// US-50.10: -print-key opts in to emitting the key, and it must go to stdout
// (pipeable) with a risk warning, not stderr.
func TestSealKey_PrintKey_OutputsToStdout(t *testing.T) {
	bin := getSealKeyBin(t)
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "-out", sealedPath, "-passphrase", "pass", "-print-key")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "seal-key -print-key should succeed; stderr=%q", stderr.String())

	stdoutStr := stdout.String()
	require.Regexp(t, hexKeyRe, stdoutStr, "stdout must contain the hex-encoded root key")
	assert.Contains(t, stdoutStr, "WARNING", "stdout must include a risk warning above the key")

	_, err := os.Stat(sealedPath)
	require.NoError(t, err, "sealed file must still be written")
}

// US-50.10: even when -print-key is set, the key must not leak to stderr.
func TestSealKey_PrintKey_NotOnStderr(t *testing.T) {
	bin := getSealKeyBin(t)
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "-out", sealedPath, "-passphrase", "pass", "-print-key")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run())

	if hexKeyRe.MatchString(stderr.String()) {
		t.Errorf("root key must not appear on stderr, got: %q", stderr.String())
	}
}
