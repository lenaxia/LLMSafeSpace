// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// ---- deriveServerKey tests ----

func TestDeriveServerKey_AbsentEnv_ReturnsNil(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	if deriveServerKey("test") != nil {
		t.Error("expected nil when env var absent")
	}
}

func TestDeriveServerKey_EmptyEnv_ReturnsNil(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	if deriveServerKey("test") != nil {
		t.Error("expected nil for empty string")
	}
}

func TestDeriveServerKey_ShortRawBytes_ReturnsNil(t *testing.T) {
	// 31 non-hex chars → 31 raw bytes → below 32-byte minimum
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "abcdefghijklmnopqrstuvwxyz01234")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	if deriveServerKey("test") != nil {
		t.Error("expected nil for 31-char raw bytes")
	}
}

func TestDeriveServerKey_Exactly32RawBytes_Returns32ByteKey(t *testing.T) {
	// 32 non-hex chars → 32 raw bytes → meets minimum
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "abcdefghijklmnopqrstuvwxyz012345")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	key := deriveServerKey("test")
	if key == nil {
		t.Fatal("expected non-nil key for 32-char raw bytes")
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(key))
	}
}

func TestDeriveServerKey_AlphanumericHelmFormat_Returns32ByteKey(t *testing.T) {
	// Helm randAlphaNum 64 — alphanumeric, not hex, 64 chars = 64 raw bytes
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "Abc123DefGhi456JklMno789PqrStu0VwxYz1Abc123DefGhi456JklMno789Pq")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	key := deriveServerKey("test")
	if key == nil {
		t.Fatal("expected non-nil key for 64-char alphanumeric (Helm format)")
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte derived key, got %d", len(key))
	}
}

func TestDeriveServerKey_ValidHex64Chars_Returns32ByteKey(t *testing.T) {
	// 64 lowercase hex chars → 32 decoded bytes
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	key := deriveServerKey("test")
	if key == nil {
		t.Fatal("expected non-nil key for 64-char hex")
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte derived key, got %d", len(key))
	}
}

func TestDeriveServerKey_ShortHex_ReturnsNil(t *testing.T) {
	// 60 hex chars → 30 decoded bytes → below 32-byte minimum
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	if deriveServerKey("test") != nil {
		t.Error("expected nil for 60-char hex (30 decoded bytes)")
	}
}

func TestDeriveServerKey_InvalidHexLongEnough_FallsBackToRawBytes(t *testing.T) {
	// Non-hex but 32+ chars → raw bytes path → should succeed
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ") // 32 uppercase Z — not valid hex
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	key := deriveServerKey("test")
	if key == nil {
		t.Fatal("expected non-nil key for 32-char non-hex string (raw bytes path)")
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte derived key, got %d", len(key))
	}
}

func TestDeriveServerKey_LegacyEnvVar_Accepted(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "abcdefghijklmnopqrstuvwxyz012345")
	key := deriveServerKey("test")
	if key == nil {
		t.Fatal("expected non-nil key via legacy env var LLMSAFESPACE_DEK_MASTER_KEY")
	}
}

func TestDeriveServerKey_PrimaryEnvTakesPrecedence(t *testing.T) {
	primary := "abcdefghijklmnopqrstuvwxyz012345" // 32 chars
	legacy := "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"  // different 32 chars
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", primary)
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", legacy)

	primaryKey := deriveServerKey("test")

	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	legacyKey := deriveServerKey("test")

	if primaryKey == nil || legacyKey == nil {
		t.Fatal("both should produce non-nil keys")
	}
	for i := range primaryKey {
		if primaryKey[i] != legacyKey[i] {
			return // different keys — primary took precedence ✓
		}
	}
	t.Error("primary and legacy produced identical keys — primary should take precedence")
}

func TestDeriveServerKey_NoSideEffects(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "abcdefghijklmnopqrstuvwxyz012345")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	k1 := deriveServerKey("test")
	k2 := deriveServerKey("test")
	if len(k1) != len(k2) {
		t.Error("repeated calls produced different-length keys — side effect suspected")
	}
	for i := range k1 {
		if k1[i] != k2[i] {
			t.Error("repeated calls produced different keys — not deterministic")
			break
		}
	}
}

// ---- validateMasterSecret tests ----

func TestValidateMasterSecret_AbsentEnv_ReturnsError(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, logs := logger.NewObserved()
	err := validateMasterSecret(log)
	if err == nil {
		t.Fatal("expected error when master secret absent")
	}
	if !strings.Contains(err.Error(), "LLMSAFESPACE_MASTER_SECRET") {
		t.Errorf("error should mention env var name, got: %v", err)
	}
	if logs.Len() != 0 {
		t.Errorf("expected no Warn for absent secret (nothing to diagnose), got %d entries", logs.Len())
	}
}

func TestValidateMasterSecret_TooShort_LogsWarnAndReturnsError(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "shortkey") // 8 chars = 8 bytes
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, logs := logger.NewObserved()
	err := validateMasterSecret(log)
	if err == nil {
		t.Fatal("expected error for too-short secret")
	}
	if logs.Len() == 0 {
		t.Fatal("expected Warn log for present-but-short secret")
	}
	entry := logs.All()[0]
	found := false
	for _, f := range entry.Context {
		if f.Key == "decoded_bytes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Warn entry should include decoded_bytes field, fields: %v", entry.Context)
	}
}

func TestValidateMasterSecret_TooShort_DoesNotLogSecret(t *testing.T) {
	secret := "shortkey"
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", secret)
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, logs := logger.NewObserved()
	_ = validateMasterSecret(log)
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, secret) {
			t.Error("Warn message must not contain the secret value")
		}
		for _, f := range entry.Context {
			if strings.Contains(f.String, secret) {
				t.Errorf("Warn field %q must not contain the secret value", f.Key)
			}
		}
	}
}

func TestValidateMasterSecret_AlphanumericHelmFormat_Succeeds(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "Abc123DefGhi456JklMno789PqrStu0VwxYz1Abc123DefGhi456JklMno789Pq")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, _ := logger.NewObserved()
	if err := validateMasterSecret(log); err != nil {
		t.Errorf("64-char alphanumeric should succeed, got: %v", err)
	}
}

func TestValidateMasterSecret_HexFormat_Succeeds(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, _ := logger.NewObserved()
	if err := validateMasterSecret(log); err != nil {
		t.Errorf("64-char hex should succeed, got: %v", err)
	}
}

func TestValidateMasterSecret_LegacyEnvVar_Accepted(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "abcdefghijklmnopqrstuvwxyz012345") // 32 chars
	log, _ := logger.NewObserved()
	if err := validateMasterSecret(log); err != nil {
		t.Errorf("LLMSAFESPACE_DEK_MASTER_KEY should satisfy validation, got: %v", err)
	}
}

// ---- app.New wiring tests ----

// minimalCfg returns a *config.Config that makes kubernetes.New fail
// deterministically with a file-not-found error, without touching any real
// infrastructure. Used by app.New wiring tests.
func minimalCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Kubernetes.InCluster = false
	cfg.Kubernetes.ConfigPath = "/nonexistent/kubeconfig-epic34-test"
	return cfg
}

func TestApp_New_FailsWithoutMasterSecret(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "")
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, _ := logger.NewObserved()
	_, err := New(minimalCfg(), log)
	if err == nil {
		t.Fatal("app.New should fail when master secret is absent")
	}
	if !strings.Contains(err.Error(), "LLMSAFESPACE_MASTER_SECRET") {
		t.Errorf("error should mention LLMSAFESPACE_MASTER_SECRET, got: %v", err)
	}
	// Must NOT contain kubernetes-related error — validateMasterSecret fires first
	if strings.Contains(err.Error(), "kubernetes") || strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("should not reach kubernetes step, but got: %v", err)
	}
}

func TestApp_New_FailsWithTooShortMasterSecret(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "tooshort") // 8 chars
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, _ := logger.NewObserved()
	_, err := New(minimalCfg(), log)
	if err == nil {
		t.Fatal("app.New should fail when master secret is too short")
	}
	if strings.Contains(err.Error(), "kubernetes") || strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("should not reach kubernetes step, but got: %v", err)
	}
}

func TestApp_New_WithValidMasterSecret_FailsAtInfra(t *testing.T) {
	t.Setenv("LLMSAFESPACE_MASTER_SECRET", "abcdefghijklmnopqrstuvwxyz012345") // 32 raw bytes
	t.Setenv("LLMSAFESPACE_DEK_MASTER_KEY", "")
	log, _ := logger.NewObserved()
	_, err := New(minimalCfg(), log)
	if err == nil {
		t.Fatal("app.New should fail (no real infra available)")
	}
	// Must NOT be the master-secret error — validation passed and infra was attempted
	if strings.Contains(err.Error(), "LLMSAFESPACE_MASTER_SECRET") {
		t.Errorf("master secret validation should pass, but got master-secret error: %v", err)
	}
	// Must be a kubernetes/infra error — confirming validateMasterSecret passed
	if !strings.Contains(err.Error(), "kubernetes") && !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("expected kubernetes/infra error after validation passes, got: %v", err)
	}
}
