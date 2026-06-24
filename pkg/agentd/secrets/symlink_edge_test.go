// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Level 4: Adversarial Edge Cases
//
// Edge cases that are unlikely but would cause silent credential exposure
// or pod boot failures if not handled correctly.
// =============================================================================

// TestReset_Idempotent verifies that calling reset() twice doesn't error
// or corrupt state. This happens on rapid credential reloads.
func TestReset_Idempotent(t *testing.T) {
	sim := newSymlinkFarmSim(t)
	m := &Materializer{FS: RealFS(), Paths: sim.paths}

	require.NoError(t, m.reset())
	require.NoError(t, m.reset(),
		"second reset() must be a no-op, not an error")

	// Directories must still exist.
	_, err := os.Stat(sim.paths.SSHDir)
	require.NoError(t, err)
	_, err = os.Stat(sim.paths.SecretsBaseDir)
	require.NoError(t, err)

	// PVC symlinks must survive both resets.
	assertPVCPathsAreSymlinks(t, sim)
}

// TestReset_TmpfsNotYetCreated verifies reset() works when the tmpfs
// directories don't exist yet (first-ever reset before init container
// has created /sandbox-runtime/rt/*). This can happen if materialize
// runs before the init script's mkdir completes (race on fast systems).
func TestReset_TmpfsNotYetCreated(t *testing.T) {
	tmpfsDir := t.TempDir()
	pvcDir := t.TempDir()
	homeDir := filepath.Join(pvcDir, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))

	// Point paths at tmpfs targets that DON'T exist yet.
	paths := Paths{
		Home:            homeDir,
		SecretsBaseDir:  filepath.Join(tmpfsDir, "rt", "secrets"),
		SSHDir:          filepath.Join(tmpfsDir, "rt", "ssh"),
		AgentConfigPath: filepath.Join(tmpfsDir, "agent-config.json"),
		SecretsEnvPath:  filepath.Join(tmpfsDir, "secrets-env"),
		GitCredsPath:    filepath.Join(tmpfsDir, "rt", "git-credentials"),
	}

	m := &Materializer{FS: RealFS(), Paths: paths}

	// reset must not panic or error — RemoveAll handles ENOENT, MkdirAll
	// creates the path.
	require.NoError(t, m.reset())

	// Directories must now exist.
	require.NoError(t, os.MkdirAll(filepath.Dir(paths.SSHDir), 0o755))
	_, err := os.Stat(paths.SSHDir)
	require.NoError(t, err, "SSH dir must exist after reset created it")
}

// TestMaterialize_EmptyBatch_LeavesNoFiles verifies that materializing an
// empty secret batch after reset leaves no credential files behind. This
// is the unbind-all path: a user removes all credentials, the reload pushes
// an empty batch, and the workspace must not retain stale plaintext.
func TestMaterialize_EmptyBatch_LeavesNoFiles(t *testing.T) {
	sim := newSymlinkFarmSim(t)
	m := &Materializer{FS: RealFS(), Paths: sim.paths}

	// First materialize with real secrets.
	_, err := m.Materialize([]Secret{
		{Type: "ssh-key", Name: "github", Plaintext: "secret-key-data",
			Metadata: map[string]string{"key_type": "ed25519"}},
	})
	require.NoError(t, err)

	// Verify file exists.
	keyPath := filepath.Join(sim.paths.SSHDir, "id_ed25519_github")
	_, err = os.Stat(keyPath)
	require.NoError(t, err, "key must exist after first materialize")

	// Second materialize with empty batch (unbind-all).
	result, err := m.Materialize([]Secret{})
	require.NoError(t, err)
	require.False(t, result.HasFailures())

	// Old key must be gone (reset wiped it, empty batch didn't re-create it).
	_, err = os.Stat(keyPath)
	assert.True(t, os.IsNotExist(err),
		"stale SSH key must not survive an empty materialize (unbind-all)")

	// PVC symlinks must survive.
	assertPVCPathsAreSymlinks(t, sim)
}

// TestGitCredentials_WrittenThroughDanglingSymlink verifies that git-credentials
// is created on first write even when the symlink target doesn't exist yet
// (dangling symlink). This is the production boot path: the init container
// creates the symlink but does NOT pre-create the target file (no `touch` —
// a zero-byte file would break JSON parsers for auth.json).
func TestGitCredentials_WrittenThroughDanglingSymlink(t *testing.T) {
	sim := newSymlinkFarmSim(t)

	// git-credentials target is a dangling symlink at this point (init created
	// the symlink, but not the target file).
	gitCredsSymlink := filepath.Join(sim.paths.Home, ".git-credentials")
	target, err := os.Readlink(gitCredsSymlink)
	require.NoError(t, err)
	_, err = os.Stat(target)
	require.True(t, os.IsNotExist(err),
		"git-credentials target must not exist yet (dangling symlink)")

	// Materialize must create the file through the dangling symlink.
	m := &Materializer{FS: RealFS(), Paths: sim.paths}
	_, err = m.Materialize([]Secret{
		{Type: "git-credential", Name: "github",
			Plaintext: "test_token_abc123",
			Metadata:  map[string]string{"host": "github.com", "protocol": "https"}},
	})
	require.NoError(t, err)

	// File must now exist at the tmpfs target.
	content, err := os.ReadFile(sim.paths.GitCredsPath)
	require.NoError(t, err, "git-credentials must be created through dangling symlink")
	assert.Contains(t, string(content), "test_token_abc123")

	// And must be readable through the PVC-side symlink.
	contentViaSymlink, err := os.ReadFile(gitCredsSymlink)
	require.NoError(t, err)
	assert.Equal(t, string(content), string(contentViaSymlink))
}

// TestAgentConfig_PathIsTmpfsNotPVC verifies agent-config.json is written
// to the tmpfs path and does not exist on the PVC. Unlike Group C paths,
// agent-config.json is a direct tmpfs path (not a symlink) — so this tests
// the direct-redirect mechanism (US-35.7.2).
func TestAgentConfig_PathIsTmpfsNotPVC(t *testing.T) {
	sim := newSymlinkFarmSim(t)

	// AgentConfigPath must point to tmpfs, not PVC.
	assert.True(t, strings.HasPrefix(sim.paths.AgentConfigPath, sim.tmpfsDir),
		"AgentConfigPath must be under tmpfs dir, got %s", sim.paths.AgentConfigPath)

	// Write a file directly at the tmpfs path (simulates FlushProviders).
	require.NoError(t, os.MkdirAll(sim.tmpfsDir, 0o755))
	require.NoError(t, os.WriteFile(sim.paths.AgentConfigPath, []byte(`{"apiKey":"sk-test"}`), 0o600))

	// Walk the PVC: no file must contain agent-config content.
	err := filepath.Walk(sim.pvcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return err
		}
		content, _ := os.ReadFile(path)
		assert.NotContains(t, string(content), "sk-test",
			"PVC file %s must not contain agent-config content", path)
		return nil
	})
	require.NoError(t, err)
}

// TestReset_DoubleMaterializeSimulatesReloadCycle simulates the full reload
// lifecycle: boot materialize → live reload → live reload. Verifies that
// each cycle cleanly replaces credentials without accumulating stale files
// and without touching PVC symlinks.
func TestReset_DoubleMaterializeSimulatesReloadCycle(t *testing.T) {
	sim := newSymlinkFarmSim(t)
	m := &Materializer{FS: RealFS(), Paths: sim.paths}

	// Boot: materialize initial credentials.
	_, err := m.Materialize([]Secret{
		{Type: "ssh-key", Name: "github", Plaintext: "KEY_V1",
			Metadata: map[string]string{"key_type": "ed25519"}},
		{Type: "git-credential", Name: "github", Plaintext: "TOKEN_V1_abc123",
			Metadata: map[string]string{"host": "github.com", "protocol": "https"}},
	})
	require.NoError(t, err)

	// Reload 1: replace credentials with new values.
	_, err = m.Materialize([]Secret{
		{Type: "ssh-key", Name: "github", Plaintext: "KEY_V2",
			Metadata: map[string]string{"key_type": "ed25519"}},
		{Type: "git-credential", Name: "github", Plaintext: "TOKEN_V2_xyz789",
			Metadata: map[string]string{"host": "github.com", "protocol": "https"}},
	})
	require.NoError(t, err)

	// V1 must be gone, V2 must be present.
	sshKey := filepath.Join(sim.paths.SSHDir, "id_ed25519_github")
	content, err := os.ReadFile(sshKey)
	require.NoError(t, err)
	assert.Contains(t, string(content), "KEY_V2")
	assert.NotContains(t, string(content), "KEY_V1",
		"old SSH key must not survive reload")

	gitContent, err := os.ReadFile(sim.paths.GitCredsPath)
	require.NoError(t, err)
	assert.Contains(t, string(gitContent), "TOKEN_V2_xyz789")
	assert.NotContains(t, string(gitContent), "TOKEN_V1_abc123",
		"old git token must not survive reload")

	// PVC symlinks must survive the full reload cycle.
	assertPVCPathsAreSymlinks(t, sim)
}
