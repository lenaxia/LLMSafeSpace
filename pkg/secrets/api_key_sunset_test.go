// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// withSunset swaps the package-level isAPIKeySunset predicate for the
// duration of a test and restores the original on cleanup. Tests swap
// the predicate rather than the sunset DATE itself, which is a fixed
// constant per US-44.9. Restoration is deferred so nested subtests and
// parallel-caller scenarios always see the production value again.
func withSunset(t *testing.T, after bool) {
	t.Helper()
	original := isAPIKeySunset
	isAPIKeySunset = func() bool { return after }
	t.Cleanup(func() { isAPIKeySunset = original })
}

func TestSunset_APIKey_CreateSucceedsBeforeSunset(t *testing.T) {
	withSunset(t, false)
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	resp, err := svc.CreateSecret(ctx, "user-1", sessionID, nil, CreateSecretRequest{
		Name:     "legacy-before-sunset",
		Type:     SecretTypeAPIKey,
		Value:    "sk-legacy",
		Metadata: json.RawMessage(`{"kind":"anthropic","slug":"anthropic"}`),
	})
	if err != nil {
		t.Fatalf("api-key must be creatable before sunset, got %v", err)
	}
	if resp.Type != SecretTypeAPIKey {
		t.Errorf("type: got %q, want %q", resp.Type, SecretTypeAPIKey)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestSunset_APIKey_CreateFailsAfterSunset(t *testing.T) {
	withSunset(t, true)
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, nil, CreateSecretRequest{
		Name:     "legacy-after-sunset",
		Type:     SecretTypeAPIKey,
		Value:    "sk-legacy",
		Metadata: json.RawMessage(`{"kind":"anthropic","slug":"anthropic"}`),
	})
	if err == nil {
		t.Fatal("api-key creation must fail after sunset, got nil")
	}
	if !errors.Is(err, ErrInvalidSecretType) {
		t.Errorf("error must wrap ErrInvalidSecretType so the handler maps it to 400, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, APIKeySunsetDate) {
		t.Errorf("error %q must name the sunset date %q", msg, APIKeySunsetDate)
	}
	if !strings.Contains(msg, "llm-provider") || !strings.Contains(msg, "env-secret") {
		t.Errorf("error %q must direct users to llm-provider and env-secret", msg)
	}
	if !strings.Contains(msg, "api-key-to-llm-provider.md") {
		t.Errorf("error %q must link the migration guide", msg)
	}
}

func TestSunset_NonAPIKeyTypes_UnaffectedAfterSunset(t *testing.T) {
	withSunset(t, true)
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		secret   string
		typ      SecretType
		value    string
		metadata json.RawMessage
	}{
		{
			name:     "llm-provider",
			secret:   "lp-1",
			typ:      SecretTypeLLMProvider,
			value:    `{"kind":"anthropic","slug":"anthropic","apiKey":"sk-ant-x"}`,
			metadata: json.RawMessage(`{}`),
		},
		{
			name:     "env-secret",
			secret:   "env-1",
			typ:      SecretTypeEnvSecret,
			value:    "postgres://db",
			metadata: json.RawMessage(`{"var_name":"DATABASE_URL"}`),
		},
		{
			name:     "ssh-key",
			secret:   "ssh-1",
			typ:      SecretTypeSSHKey,
			value:    "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
			metadata: json.RawMessage(`{"key_type":"ed25519"}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.CreateSecret(ctx, "user-1", sessionID, nil, CreateSecretRequest{
				Name: tc.secret, Type: tc.typ, Value: tc.value, Metadata: tc.metadata,
			})
			if err != nil {
				t.Fatalf("non-api-key type %s must be unaffected by the sunset gate, got %v", tc.typ, err)
			}
			if resp.Type != tc.typ {
				t.Errorf("type: got %q, want %q", resp.Type, tc.typ)
			}
		})
	}
}

// TestSunset_PredicateDefaultMatchesFixedDate verifies the production
// default predicate and the APIKeySunsetDate constant stay coupled. The
// expected result is recomputed from the same fixed date so the test is
// stable regardless of when it runs (it will keep passing after the
// sunset date fires); it would only fail if the constant drifts from
// "2026-12-19" or the predicate's polarity/logic is broken.
func TestSunset_PredicateDefaultMatchesFixedDate(t *testing.T) {
	if APIKeySunsetDate != "2026-12-19" {
		t.Errorf("APIKeySunsetDate constant: got %q, want %q", APIKeySunsetDate, "2026-12-19")
	}
	sunset := time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	want := time.Now().After(sunset)
	if got := isAPIKeySunset(); got != want {
		t.Errorf("isAPIKeySunset: got %v, want %v (relative to %s)", got, want, APIKeySunsetDate)
	}
}
