// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lenaxia/llmsafespaces/api/internal/config"
	"github.com/lenaxia/llmsafespaces/api/internal/logger"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-50.8: the static root-key-provider deprecation warning must fire on the
// Helm-empty default (""), not only on an explicit "static". Operators who
// accept the risk can suppress it via Security.SkipMasterKeyWarning.
//
// Tests for deriveServerKey/validateMasterSecret live in app_master_key_test.go.

const us508StaticWarnSnippet = "using static root key provider"

// setValidMasterSecretUS508 sets a 32-byte master secret so newRootKeyProvider's
// static path reaches provider construction and the warning (it returns nil
// before warning when the master secret is absent — app.New rejects that
// upstream via validateMasterSecret anyway).
func setValidMasterSecretUS508(t *testing.T) {
	t.Helper()
	t.Setenv("LLMSAFESPACES_MASTER_SECRET", "abcdefghijklmnopqrstuvwxyz012345")
	t.Setenv("LLMSAFESPACES_DEK_MASTER_KEY", "")
}

func TestNewRootKeyProvider_EmptyDefault_LogsWarning(t *testing.T) {
	setValidMasterSecretUS508(t)
	cfg := &config.Config{} // RootKeyProvider == "" is the Helm default
	log, logs := logger.NewObserved()

	p := newRootKeyProvider(cfg, log)
	require.NotNil(t, p, "static provider should be constructed with a valid master secret")
	assert.Equal(t, 1, logs.FilterMessageSnippet(us508StaticWarnSnippet).Len(),
		"US-50.8 M1: empty Helm default must emit the static deprecation warning")
}

func TestNewRootKeyProvider_ExplicitStatic_LogsWarning(t *testing.T) {
	setValidMasterSecretUS508(t)
	cfg := &config.Config{}
	cfg.Security.RootKeyProvider = "static"
	log, logs := logger.NewObserved()

	p := newRootKeyProvider(cfg, log)
	require.NotNil(t, p)
	assert.Equal(t, 1, logs.FilterMessageSnippet(us508StaticWarnSnippet).Len())
}

func TestNewRootKeyProvider_SkipWarning_Suppresses(t *testing.T) {
	setValidMasterSecretUS508(t)
	cfg := &config.Config{} // empty default would normally warn
	cfg.Security.SkipMasterKeyWarning = true
	log, logs := logger.NewObserved()

	p := newRootKeyProvider(cfg, log)
	require.NotNil(t, p)
	assert.Equal(t, 0, logs.FilterMessageSnippet(us508StaticWarnSnippet).Len(),
		"SkipMasterKeyWarning must suppress the static deprecation warning")
}

func TestNewRootKeyProvider_Sealed_NoWarning(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed")
	passPath := filepath.Join(tmpDir, "passphrase")
	passphrase := []byte("correct-horse-battery-staple")
	require.NoError(t, os.WriteFile(passPath, passphrase, 0600))

	rootKey := make([]byte, 32)
	for i := range rootKey {
		rootKey[i] = byte(i)
	}
	require.NoError(t, secrets.SealRootKey(sealedPath, passphrase, rootKey))

	cfg := &config.Config{}
	cfg.Security.RootKeyProvider = "sealed"
	cfg.Security.SealedKeyPath = sealedPath
	cfg.Security.PassphrasePath = passPath
	log, logs := logger.NewObserved()

	p := newRootKeyProvider(cfg, log)
	require.NotNil(t, p, "sealed provider should construct from valid sealed + passphrase files")
	assert.Equal(t, 0, logs.FilterMessageSnippet(us508StaticWarnSnippet).Len(),
		"sealed provider must not emit the static deprecation warning")
}
