// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCredentialPrecedence_AdminFreeTierOverrideByUser verifies that a user
// credential for the same provider overrides an admin credential when both
// are bound to the same workspace.
func TestCredentialPrecedence_AdminFreeTierOverrideByUser(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newTestDEKCache()
	keyService := NewKeyService(keyStore, dekCache)

	secretStore := newMockSecretStore()

	// Simulate admin key deriver returning a fixed key.
	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	// Setup user DEK.
	userID := "user-1"
	sessionID := "sess-1"
	workspaceID := "ws-1"
	userDEK := make([]byte, 32)
	for i := range userDEK {
		userDEK[i] = byte(i + 100)
	}
	dekCache.CacheDEK(context.Background(), sessionID, userDEK, time.Hour) //nolint:errcheck

	// Create encrypted credentials.
	adminPlaintext, _ := json.Marshal(LLMProviderData{Provider: "anthropic", APIKey: "admin-key"})
	adminCipher, err := EncryptSecret(adminKEK, adminPlaintext)
	require.NoError(t, err)

	userPlaintext, _ := json.Marshal(LLMProviderData{Provider: "anthropic", APIKey: "user-key"}) //nolint:gosec
	userCipher, err := EncryptSecret(userDEK, userPlaintext)
	require.NoError(t, err)

	mockCredStore := &mockCredentialStore{
		bindings: []CredentialBinding{
			{ID: "cred-user", OwnerType: "user", OwnerID: userID, Provider: "anthropic", Ciphertext: userCipher, SourceType: "explicit"},
			{ID: "cred-admin", OwnerType: "admin", OwnerID: "_platform", Provider: "anthropic", Ciphertext: adminCipher, SourceType: "auto"},
		},
	}

	combinedStore := &combinedTestStore{SecretStore: secretStore, CredentialStore: mockCredStore}
	svc := NewSecretService(keyService, combinedStore)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	ctx := context.Background()
	result, err := svc.PrepareSecretsForInjection(ctx, userID, sessionID, workspaceID)
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))

	llmSecrets := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llmSecrets, 1, "expected exactly one anthropic credential (user wins)")

	var pd LLMProviderData
	require.NoError(t, json.Unmarshal([]byte(llmSecrets[0].Plaintext), &pd))
	assert.Equal(t, "user-key", pd.APIKey)
	assert.Equal(t, "anthropic", pd.Provider)
}

// TestCredentialPrecedence_AdminOnlyNoSession verifies that admin credentials
// work without an active user session (no DEK needed).
func TestCredentialPrecedence_AdminOnlyNoSession(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newTestDEKCache()
	keyService := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()

	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	adminPlaintext, _ := json.Marshal(LLMProviderData{Provider: "opencode", APIKey: "public"})
	adminCipher, err := EncryptSecret(adminKEK, adminPlaintext)
	require.NoError(t, err)

	mockCredStore := &mockCredentialStore{
		bindings: []CredentialBinding{
			{ID: "cred-free", OwnerType: "admin", OwnerID: "_platform", Provider: "opencode", Ciphertext: adminCipher, SourceType: "auto"},
		},
	}

	combinedStore := &combinedTestStore{SecretStore: secretStore, CredentialStore: mockCredStore}
	svc := NewSecretService(keyService, combinedStore)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	ctx := context.Background()
	result, err := svc.PrepareSecretsForInjection(ctx, "user-1", "no-session", "ws-1")
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))

	llmSecrets := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llmSecrets, 1)

	var pd LLMProviderData
	require.NoError(t, json.Unmarshal([]byte(llmSecrets[0].Plaintext), &pd))
	assert.Equal(t, "public", pd.APIKey)
}

// --- Test helpers ---

func filterByType(secrets []InjectedSecret, typ SecretType) []InjectedSecret {
	var out []InjectedSecret
	for _, s := range secrets {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

type mockCredentialStore struct {
	bindings []CredentialBinding
}

func (m *mockCredentialStore) GetWorkspaceCredentials(_ context.Context, _ string) ([]CredentialBinding, error) {
	return m.bindings, nil
}

func (m *mockCredentialStore) UpsertFreeTierCredential(_ context.Context, _ []byte) error { return nil }
func (m *mockCredentialStore) SeedWorkspaceCredentials(_ context.Context, _, _ string) error {
	return nil
}
func (m *mockCredentialStore) HasUserProviderCredential(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

// combinedTestStore satisfies both SecretStore and CredentialStore via embedding.
type combinedTestStore struct {
	SecretStore
	CredentialStore
}

type testDEKCache struct {
	cache map[string][]byte
}

func newTestDEKCache() *testDEKCache {
	return &testDEKCache{cache: make(map[string][]byte)}
}

func (c *testDEKCache) Set(sessionID string, dek []byte) {
	c.cache[sessionID] = dek
}

func (c *testDEKCache) CacheDEK(_ context.Context, sessionID string, dek []byte, _ time.Duration) error {
	c.cache[sessionID] = dek
	return nil
}

func (c *testDEKCache) GetDEK(_ context.Context, sessionID string) ([]byte, error) {
	dek, ok := c.cache[sessionID]
	if !ok {
		return nil, ErrDEKUnavailable
	}
	return dek, nil
}

func (c *testDEKCache) EvictDEK(_ context.Context, _ string) error { return nil }
