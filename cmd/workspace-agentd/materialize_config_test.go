// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadMaterializeConfig_ResolvesTmpfsPaths is the critical regression guard
// for US-35.7. If loadMaterializeConfig() resolves SSH/git/secrets paths to PVC
// paths (home+"/.ssh" etc.) instead of tmpfs targets, Materializer.reset() will
// RemoveAll the PVC-side symlink, then MkdirAll a real directory on the PVC —
// silently landing plaintext credentials on disk on the first reload.
//
// This test must assert the DEFAULTS (no env vars set) because env-var overrides
// are only used in tests. In production, no LLMSAFESPACES_* env vars are set.
func TestLoadMaterializeConfig_ResolvesTmpfsPaths(t *testing.T) {
	t.Setenv("LLMSAFESPACES_SECRETS_BASE_DIR", "")
	t.Setenv("LLMSAFESPACES_SSH_DIR", "")
	t.Setenv("LLMSAFESPACES_AGENT_CONFIG_PATH", "")
	t.Setenv("LLMSAFESPACES_SECRETS_ENV_PATH", "")
	t.Setenv("LLMSAFESPACES_GIT_CREDS_PATH", "")

	cfg := loadMaterializeConfig()

	assert.Equal(t, "/sandbox-runtime/rt/secrets", cfg.secretsBaseDir,
		"secretsBaseDir must resolve to tmpfs (US-35.7) — PVC path causes reset() to destroy symlinks")
	assert.Equal(t, "/sandbox-runtime/rt/ssh", cfg.sshDir,
		"sshDir must resolve to tmpfs (US-35.7) — PVC path causes reset() to destroy symlinks")
	assert.Equal(t, "/sandbox-runtime/rt/git-credentials", cfg.gitCredsPath,
		"gitCredsPath must resolve to tmpfs (US-35.7) — PVC path causes reset() to destroy symlinks")
	assert.Equal(t, "/sandbox-runtime/agent-config.json", cfg.agentConfigPath,
		"agentConfigPath must resolve to tmpfs (US-35.7)")
	assert.Equal(t, "/sandbox-runtime/secrets-env", cfg.secretsEnvPath,
		"secretsEnvPath must resolve to tmpfs (US-35.7)")
}

// TestLoadMaterializeConfig_EnvOverridesStillWork verifies the env-var override
// path (used by tests) still functions — so tests can inject temp dirs.
func TestLoadMaterializeConfig_EnvOverridesStillWork(t *testing.T) {
	t.Setenv("LLMSAFESPACES_SSH_DIR", "/tmp/test-ssh")
	t.Setenv("LLMSAFESPACES_GIT_CREDS_PATH", "/tmp/test-git")

	cfg := loadMaterializeConfig()

	require.Equal(t, "/tmp/test-ssh", cfg.sshDir,
		"env override must take precedence over tmpfs default")
	require.Equal(t, "/tmp/test-git", cfg.gitCredsPath,
		"env override must take precedence over tmpfs default")
}
