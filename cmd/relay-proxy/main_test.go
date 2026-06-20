// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"
)

// TestLoadConfig_UpstreamFlagOverridesEnv verifies the --upstream flag (which
// controller/internal/relay/cloudinit.go renders into the relay VM's systemd
// ExecStart) is actually parsed and takes precedence over the UPSTREAM_URL env
// and the hardcoded default. Before this fix, the binary ignored os.Args
// entirely — the rendered --upstream=<spec.upstreamURL> was silently dropped
// and the VM fell back to the hardcoded default, breaking any per-CR upstream
// override.
func TestLoadConfig_UpstreamFlagOverridesEnv(t *testing.T) {
	t.Setenv("UPSTREAM_URL", "https://from-env.example/v1")

	cfg, err := loadConfig([]string{"--upstream", "https://from-flag.example/v1"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.upstreamURL != "https://from-flag.example/v1" {
		t.Errorf("upstreamURL = %q, want %q (flag must override env)", cfg.upstreamURL, "https://from-flag.example/v1")
	}
}

// TestLoadConfig_UpstreamFlagEqualsForm verifies the --upstream=value form
// (also produced by systemd ExecStart tokenization) is accepted.
func TestLoadConfig_UpstreamFlagEqualsForm(t *testing.T) {
	cfg, err := loadConfig([]string{"--upstream=https://equals-form.example/v1"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.upstreamURL != "https://equals-form.example/v1" {
		t.Errorf("upstreamURL = %q, want equals-form URL", cfg.upstreamURL)
	}
}

// TestLoadConfig_UpstreamEnvWhenNoFlag verifies env is honored when the flag
// is absent (backwards-compatible with any operator running the binary without
// args but with UPSTREAM_URL set).
func TestLoadConfig_UpstreamEnvWhenNoFlag(t *testing.T) {
	t.Setenv("UPSTREAM_URL", "https://from-env.example/v1")

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.upstreamURL != "https://from-env.example/v1" {
		t.Errorf("upstreamURL = %q, want env value when flag absent", cfg.upstreamURL)
	}
}

// TestLoadConfig_UpstreamDefaultWhenNeither verifies the hardcoded default
// (opencode.ai/zen/v1) applies when neither flag nor env is set — so a bare
// `relay-proxy` invocation still targets the free-model upstream.
func TestLoadConfig_UpstreamDefaultWhenNeither(t *testing.T) {
	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.upstreamURL != defaultUpstreamURL {
		t.Errorf("upstreamURL = %q, want default %q", cfg.upstreamURL, defaultUpstreamURL)
	}
}

// TestLoadConfig_ListenAndKeepaliveFlags verifies the other two knobs are also
// flag-parsable for a complete, consistent CLI.
func TestLoadConfig_ListenAndKeepaliveFlags(t *testing.T) {
	cfg, err := loadConfig([]string{
		"--listen", "0.0.0.0:8080",
		"--keepalive-interval", "45s",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.listenAddr != "0.0.0.0:8080" {
		t.Errorf("listenAddr = %q, want 0.0.0.0:8080", cfg.listenAddr)
	}
	if cfg.keepaliveInterval != 45*time.Second {
		t.Errorf("keepaliveInterval = %v, want 45s", cfg.keepaliveInterval)
	}
}

// TestLoadConfig_DefaultListenIsAllInterfaces verifies the default listen
// address is 0.0.0.0:8080, not the old WG-only 10.42.42.2:8080. The WG-only
// bind was defense-in-depth when WG was the transport; with plaintext HTTP +
// token auth (worklog 0442), binding to the WG interface would be
// EADDRNOTAVAIL on every non-OCI VM and there is no WG interface at all.
func TestLoadConfig_DefaultListenIsAllInterfaces(t *testing.T) {
	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.listenAddr != "0.0.0.0:8080" {
		t.Errorf("listenAddr default = %q, want 0.0.0.0:8080 (post-WG-removal)", cfg.listenAddr)
	}
}

// TestLoadConfig_TokenFlagOverridesEnv verifies --token is parsed and beats
// RELAY_TOKEN env (same precedence as --upstream). The controller embeds
// `--token=<per-VM-secret>` into each VM's systemd ExecStart via cloud-init.
func TestLoadConfig_TokenFlagOverridesEnv(t *testing.T) {
	t.Setenv("RELAY_TOKEN", "env-secret")

	cfg, err := loadConfig([]string{"--token", "flag-secret"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.token != "flag-secret" {
		t.Errorf("token = %q, want flag-secret (flag must override env)", cfg.token)
	}
}

// TestLoadConfig_TokenEnvWhenNoFlag verifies the env fallback works.
func TestLoadConfig_TokenEnvWhenNoFlag(t *testing.T) {
	t.Setenv("RELAY_TOKEN", "env-secret")

	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.token != "env-secret" {
		t.Errorf("token = %q, want env-secret", cfg.token)
	}
}

// TestLoadConfig_TokenEmptyByDefault verifies the token defaults to empty
// (auth disabled) when neither flag nor env is set. This is intentional for
// local dev; production relays must set it (the controller always does).
func TestLoadConfig_TokenEmptyByDefault(t *testing.T) {
	cfg, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.token != "" {
		t.Errorf("token default = %q, want empty (auth disabled in dev)", cfg.token)
	}
}

// TestAuthMode verifies the startup log helper renders the auth posture.
func TestAuthMode(t *testing.T) {
	if got := authMode(""); got != "open" {
		t.Errorf("authMode(\"\") = %q, want open", got)
	}
	if got := authMode("anything"); got != "token" {
		t.Errorf("authMode(\"anything\") = %q, want token", got)
	}
}

// TestLoadConfig_InvalidFlagReturnsError verifies an unknown flag is rejected
// rather than silently ignored (defends against typos in the cloud-init
// template going unnoticed).
func TestLoadConfig_InvalidFlagReturnsError(t *testing.T) {
	_, err := loadConfig([]string{"--bogus", "x"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}
