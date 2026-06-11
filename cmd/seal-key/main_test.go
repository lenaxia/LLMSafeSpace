package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	assert.GreaterOrEqual(t, len(data), 92)
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
	assert.GreaterOrEqual(t, len(data), 92)
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
