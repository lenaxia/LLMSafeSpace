// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
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

// TestCredentialPrecedence_ModelAllowlistFiltering verifies that model_allowlist
// restricts which models appear in the injected LLMProviderData.
func TestCredentialPrecedence_ModelAllowlistFiltering(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newTestDEKCache()
	keyService := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()

	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	adminPlaintext, _ := json.Marshal(LLMProviderData{
		Provider: "anthropic", APIKey: "key",
		Models: []LLMModelConfig{
			{ID: "claude-3", Label: "Claude 3"},
			{ID: "claude-2", Label: "Claude 2"},
			{ID: "claude-1", Label: "Claude 1"},
		},
	})
	adminCipher, err := EncryptSecret(adminKEK, adminPlaintext)
	require.NoError(t, err)

	mockCredStore := &mockCredentialStore{
		bindings: []CredentialBinding{{
			ID: "cred-filtered", OwnerType: "admin", OwnerID: "_platform",
			Provider: "anthropic", Ciphertext: adminCipher,
			SourceType: "auto", ModelAllowlist: []string{"claude-3", "claude-1"},
		}},
	}

	combinedStore := &combinedTestStore{SecretStore: secretStore, CredentialStore: mockCredStore}
	svc := NewSecretService(keyService, combinedStore)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	result, err := svc.PrepareSecretsForInjection(context.Background(), "user-1", "no-session", "ws-1")
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))
	llm := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llm, 1)

	var pd LLMProviderData
	require.NoError(t, json.Unmarshal([]byte(llm[0].Plaintext), &pd))
	assert.Len(t, pd.Models, 2)
	assert.Equal(t, "claude-3", pd.Models[0].ID)
	assert.Equal(t, "claude-1", pd.Models[1].ID)
}

// TestCredentialPrecedence_ModelAllowlistFallback verifies that when no models
// match the allowlist, synthetic stubs are created from the allowlist IDs.
func TestCredentialPrecedence_ModelAllowlistFallback(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newTestDEKCache()
	keyService := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()

	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	adminPlaintext, _ := json.Marshal(LLMProviderData{Provider: "openai", APIKey: "key"})
	adminCipher, err := EncryptSecret(adminKEK, adminPlaintext)
	require.NoError(t, err)

	mockCredStore := &mockCredentialStore{
		bindings: []CredentialBinding{{
			ID: "cred-stub", OwnerType: "admin", OwnerID: "_platform",
			Provider: "openai", Ciphertext: adminCipher,
			SourceType: "auto", ModelAllowlist: []string{"gpt-4o", "gpt-4o-mini"},
		}},
	}

	combinedStore := &combinedTestStore{SecretStore: secretStore, CredentialStore: mockCredStore}
	svc := NewSecretService(keyService, combinedStore)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	result, err := svc.PrepareSecretsForInjection(context.Background(), "user-1", "no-session", "ws-1")
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))
	llm := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llm, 1)

	var pd LLMProviderData
	require.NoError(t, json.Unmarshal([]byte(llm[0].Plaintext), &pd))
	assert.Len(t, pd.Models, 2)
	assert.Equal(t, "gpt-4o", pd.Models[0].ID)
	assert.Equal(t, "gpt-4o-mini", pd.Models[1].ID)
}

// TestCredentialPrecedence_DecryptionFailureFallback verifies that when a
// higher-priority credential fails to decrypt, the system falls through to
// a lower-priority one for the same provider.
func TestCredentialPrecedence_DecryptionFailureFallback(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newTestDEKCache()
	keyService := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()

	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	goodPlaintext, _ := json.Marshal(LLMProviderData{Provider: "anthropic", APIKey: "good-key"})
	goodCipher, err := EncryptSecret(adminKEK, goodPlaintext)
	require.NoError(t, err)

	mockCredStore := &mockCredentialStore{
		bindings: []CredentialBinding{
			{ID: "bad", OwnerType: "admin", OwnerID: "_platform", Provider: "anthropic",
				Ciphertext: []byte("corrupt"), SourceType: "explicit", WithinPriority: 10},
			{ID: "good", OwnerType: "admin", OwnerID: "_platform", Provider: "anthropic",
				Ciphertext: goodCipher, SourceType: "auto", WithinPriority: 0},
		},
	}

	combinedStore := &combinedTestStore{SecretStore: secretStore, CredentialStore: mockCredStore}
	svc := NewSecretService(keyService, combinedStore)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	result, err := svc.PrepareSecretsForInjection(context.Background(), "user-1", "no-session", "ws-1")
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))
	llm := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llm, 1, "should fall through to good credential")

	var pd LLMProviderData
	require.NoError(t, json.Unmarshal([]byte(llm[0].Plaintext), &pd))
	assert.Equal(t, "good-key", pd.APIKey)
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
func (m *mockCredentialStore) BindCredentialToAllUserWorkspaces(_ context.Context, _, _ string) error {
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

// TestAsyncAuditLogger_ImplementsCredentialStore verifies that the type assertion
// in PrepareSecretsForInjection succeeds when store is *AsyncAuditLogger.
func TestAsyncAuditLogger_ImplementsCredentialStore(t *testing.T) {
	inner := newMockSecretStore()
	logger := &asyncAuditTestLogger{}
	audit := NewAsyncAuditLogger(inner, 16, logger)
	defer audit.Stop()

	// Type assertion must succeed.
	_, ok := interface{}(audit).(CredentialStore)
	require.True(t, ok, "AsyncAuditLogger must implement CredentialStore for injection path to activate")
}

// TestPrepareSecretsForInjection_ViaAsyncAuditLogger ensures the new path
// activates when store is wrapped in AsyncAuditLogger (production configuration).
func TestPrepareSecretsForInjection_ViaAsyncAuditLogger(t *testing.T) {
	// Inner store implements both SecretStore and CredentialStore.
	innerSecret := newMockSecretStore()
	adminKEK := make([]byte, 32)
	for i := range adminKEK {
		adminKEK[i] = byte(i + 1)
	}

	plaintext, _ := json.Marshal(LLMProviderData{Provider: "opencode", APIKey: "public"})
	cipher, err := EncryptSecret(adminKEK, plaintext)
	require.NoError(t, err)

	// AsyncAuditLogger wraps the inner store (which is both SecretStore and doesn't implement CredentialStore).
	// We need to use a combined store that implements both.
	mockCred := &mockCredentialStore{
		bindings: []CredentialBinding{
			{ID: "c1", OwnerType: "admin", OwnerID: "_platform", Provider: "opencode", Ciphertext: cipher, SourceType: "auto"},
		},
	}
	combined := &combinedTestStore{SecretStore: innerSecret, CredentialStore: mockCred}

	logger := &asyncAuditTestLogger{}
	audit := NewAsyncAuditLogger(combined, 16, logger)
	defer audit.Stop()

	dekCache := newTestDEKCache()
	keyService := NewKeyService(newMockKeyStore(), dekCache)

	svc := NewSecretService(keyService, audit)
	svc.SetAdminKeyDeriver(func(label string) []byte { return adminKEK })

	ctx := context.Background()
	result, err := svc.PrepareSecretsForInjection(ctx, "user-1", "sess-1", "ws-1")
	require.NoError(t, err)

	var injected []InjectedSecret
	require.NoError(t, json.Unmarshal(result, &injected))

	llm := filterByType(injected, SecretTypeLLMProvider)
	require.Len(t, llm, 1, "new path must activate through AsyncAuditLogger")
	assert.Contains(t, llm[0].Plaintext, `"public"`)
}

type asyncAuditTestLogger struct{}

func (l *asyncAuditTestLogger) Info(_ string, _ ...interface{})           {}
func (l *asyncAuditTestLogger) Warn(_ string, _ ...interface{})           {}
func (l *asyncAuditTestLogger) Error(_ string, _ error, _ ...interface{}) {}
func (l *asyncAuditTestLogger) Debug(_ string, _ ...interface{})          {}
func (l *asyncAuditTestLogger) Fatal(_ string, _ error, _ ...interface{}) {}
func (l *asyncAuditTestLogger) Sync() error                               { return nil }
func (l *asyncAuditTestLogger) With(_ ...interface{}) pkginterfaces.LoggerInterface {
	return l
}
