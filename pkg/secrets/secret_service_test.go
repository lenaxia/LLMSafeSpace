package secrets

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// --- In-memory mock SecretStore ---

type mockSecretStore struct {
	mu       sync.Mutex
	secrets  map[string]*UserSecret // keyed by ID
	bindings map[string][]string    // workspace_id -> []secret_id
	audit    []*AuditEntry
}

func newMockSecretStore() *mockSecretStore {
	return &mockSecretStore{
		secrets:  make(map[string]*UserSecret),
		bindings: make(map[string][]string),
	}
}

func (m *mockSecretStore) CreateSecret(_ context.Context, secret *UserSecret) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check unique constraint
	for _, s := range m.secrets {
		if s.UserID == secret.UserID && s.Name == secret.Name {
			return &duplicateError{name: secret.Name}
		}
	}
	if secret.ID == "" {
		secret.ID = "sec-" + secret.Name // deterministic for tests
	}
	cp := *secret
	m.secrets[secret.ID] = &cp
	return nil
}

func (m *mockSecretStore) GetSecret(_ context.Context, userID, secretID string) (*UserSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.secrets[secretID]
	if !ok || s.UserID != userID {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (m *mockSecretStore) GetSecretByName(_ context.Context, userID, name string) (*UserSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.secrets {
		if s.UserID == userID && s.Name == name {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockSecretStore) ListSecrets(_ context.Context, userID string) ([]*UserSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*UserSecret
	for _, s := range m.secrets {
		if s.UserID == userID {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *mockSecretStore) UpdateSecret(_ context.Context, secret *UserSecret) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.secrets[secret.ID]; !ok {
		return &notFoundError{id: secret.ID}
	}
	cp := *secret
	m.secrets[secret.ID] = &cp
	return nil
}

func (m *mockSecretStore) DeleteSecret(_ context.Context, userID, secretID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.secrets[secretID]
	if !ok || s.UserID != userID {
		return &notFoundError{id: secretID}
	}
	delete(m.secrets, secretID)
	// Cascade bindings
	for wsID, sids := range m.bindings {
		var filtered []string
		for _, sid := range sids {
			if sid != secretID {
				filtered = append(filtered, sid)
			}
		}
		m.bindings[wsID] = filtered
	}
	return nil
}

func (m *mockSecretStore) SetBindings(_ context.Context, workspaceID string, secretIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindings[workspaceID] = secretIDs
	return nil
}

func (m *mockSecretStore) GetBindings(_ context.Context, workspaceID string) ([]*UserSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sids := m.bindings[workspaceID]
	var result []*UserSecret
	for _, sid := range sids {
		if s, ok := m.secrets[sid]; ok {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *mockSecretStore) GetBindingsForSecret(_ context.Context, secretID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var workspaces []string
	for wsID, sids := range m.bindings {
		for _, sid := range sids {
			if sid == secretID {
				workspaces = append(workspaces, wsID)
			}
		}
	}
	return workspaces, nil
}

func (m *mockSecretStore) LogAudit(_ context.Context, entry *AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, entry)
	return nil
}

func (m *mockSecretStore) QueryAudit(_ context.Context, userID string, _ AuditQuery) ([]*AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*AuditEntry
	for _, e := range m.audit {
		if e.UserID == userID {
			result = append(result, e)
		}
	}
	return result, nil
}

type duplicateError struct{ name string }

func (e *duplicateError) Error() string { return "duplicate secret: " + e.name }

type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return "not found: " + e.id }

// --- Helper to set up a test SecretService with unlocked DEK ---

func setupSecretService(t *testing.T) (*SecretService, *mockSecretStore, string) {
	t.Helper()
	keyStore := newMockKeyStore()
	dekCache := newMockDEKCache()
	keySvc := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()
	svc := NewSecretService(keySvc, secretStore)

	ctx := context.Background()
	userID := "user-1"
	password := []byte("test-password")
	sessionID := "session-1"

	_, err := keySvc.InitializeUserKeys(ctx, userID, password)
	if err != nil {
		t.Fatalf("InitializeUserKeys failed: %v", err)
	}
	err = keySvc.UnlockDEK(ctx, userID, password, sessionID, time.Hour)
	if err != nil {
		t.Fatalf("UnlockDEK failed: %v", err)
	}

	return svc, secretStore, sessionID
}

// --- Tests ---

func TestSecretService_CreateSecret_LLMProvider(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	resp, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "my-anthropic-key",
		Type:     SecretTypeLLMProvider,
		Value:    "sk-ant-api03-secret-key",
		Metadata: json.RawMessage(`{"provider": "anthropic"}`),
	})
	if err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}

	if resp.Name != "my-anthropic-key" {
		t.Errorf("Expected name 'my-anthropic-key', got '%s'", resp.Name)
	}
	if resp.Type != SecretTypeLLMProvider {
		t.Errorf("Expected type llm-provider, got %s", resp.Type)
	}
	if resp.ID == "" {
		t.Error("Expected non-empty ID")
	}
}

func TestSecretService_CreateSecret_SSHKey(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	resp, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "github-ssh",
		Type:     SecretTypeSSHKey,
		Value:    "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
		Metadata: json.RawMessage(`{"key_type": "ed25519", "host": "github.com"}`),
	})
	if err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}
	if resp.Type != SecretTypeSSHKey {
		t.Errorf("Expected type ssh-key, got %s", resp.Type)
	}
}

func TestSecretService_CreateSecret_SSHKey_MissingMetadata(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:  "github-ssh",
		Type:  SecretTypeSSHKey,
		Value: "key-data",
	})
	if err == nil {
		t.Error("SSH key without key_type metadata should fail")
	}
}

func TestSecretService_CreateSecret_SecretFile_MissingMountPath(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:  "cert",
		Type:  SecretTypeSecretFile,
		Value: "cert-data",
	})
	if err == nil {
		t.Error("Secret file without mount_path metadata should fail")
	}
}

func TestSecretService_CreateSecret_EnvSecret_MissingVarName(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:  "db-url",
		Type:  SecretTypeEnvSecret,
		Value: "postgres://...",
	})
	if err == nil {
		t.Error("Env secret without var_name metadata should fail")
	}
}

func TestSecretService_CreateSecret_InvalidType(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:  "test",
		Type:  "invalid-type",
		Value: "data",
	})
	if err == nil {
		t.Error("Invalid secret type should fail")
	}
}

func TestSecretService_CreateSecret_DuplicateName(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "my-key",
		Type:     SecretTypeLLMProvider,
		Value:    "value1",
		Metadata: json.RawMessage(`{"provider": "openai"}`),
	})
	if err != nil {
		t.Fatalf("First create failed: %v", err)
	}

	_, err = svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "my-key",
		Type:     SecretTypeLLMProvider,
		Value:    "value2",
		Metadata: json.RawMessage(`{"provider": "openai"}`),
	})
	if err == nil {
		t.Error("Duplicate name should fail")
	}
}

func TestSecretService_CreateSecret_NoSession(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newMockDEKCache()
	keySvc := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()
	svc := NewSecretService(keySvc, secretStore)
	ctx := context.Background()

	// Initialize keys but don't unlock
	_, _ = keySvc.InitializeUserKeys(ctx, "user-1", []byte("pw"))

	_, err := svc.CreateSecret(ctx, "user-1", "no-session", CreateSecretRequest{
		Name:  "test",
		Type:  SecretTypeLLMProvider,
		Value: "val",
	})
	if err == nil {
		t.Error("CreateSecret without active session should fail")
	}
}

func TestSecretService_GetSecret(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "test-secret",
		Type:     SecretTypeLLMProvider,
		Value:    "secret-value",
		Metadata: json.RawMessage(`{"provider": "openai"}`),
	})

	resp, err := svc.GetSecret(ctx, "user-1", created.ID)
	if err != nil {
		t.Fatalf("GetSecret failed: %v", err)
	}
	if resp.Name != "test-secret" {
		t.Errorf("Expected name 'test-secret', got '%s'", resp.Name)
	}
}

func TestSecretService_GetSecret_NotFound(t *testing.T) {
	svc, _, _ := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.GetSecret(ctx, "user-1", "nonexistent")
	if err == nil {
		t.Error("GetSecret for nonexistent ID should fail")
	}
}

func TestSecretService_ListSecrets(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "key-1", Type: SecretTypeLLMProvider, Value: "v1", Metadata: json.RawMessage(`{"provider":"a"}`),
	})
	svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "key-2", Type: SecretTypeEnvSecret, Value: "v2", Metadata: json.RawMessage(`{"var_name":"DB_URL"}`),
	})

	list, err := svc.ListSecrets(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListSecrets failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("Expected 2 secrets, got %d", len(list))
	}
}

func TestSecretService_UpdateSecret(t *testing.T) {
	svc, store, sessionID := setupSecretService(t)
	ctx := context.Background()

	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "updatable", Type: SecretTypeLLMProvider, Value: "old-value", Metadata: json.RawMessage(`{"provider":"x"}`),
	})

	err := svc.UpdateSecret(ctx, "user-1", sessionID, created.ID, UpdateSecretRequest{
		Value: "new-value",
	})
	if err != nil {
		t.Fatalf("UpdateSecret failed: %v", err)
	}

	// Verify ciphertext changed
	secret, _ := store.GetSecret(ctx, "user-1", created.ID)
	if secret == nil {
		t.Fatal("Secret should still exist")
	}
	// Decrypt and verify
	dek, _ := svc.keys.GetDEK(ctx, sessionID)
	plaintext, _ := DecryptSecret(dek, secret.Ciphertext)
	if string(plaintext) != "new-value" {
		t.Errorf("Expected decrypted value 'new-value', got '%s'", string(plaintext))
	}
}

func TestSecretService_UpdateSecret_NotFound(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	err := svc.UpdateSecret(ctx, "user-1", sessionID, "nonexistent", UpdateSecretRequest{Value: "x"})
	if err == nil {
		t.Error("UpdateSecret for nonexistent ID should fail")
	}
}

func TestSecretService_DeleteSecret(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "deletable", Type: SecretTypeLLMProvider, Value: "val", Metadata: json.RawMessage(`{"provider":"x"}`),
	})

	err := svc.DeleteSecret(ctx, "user-1", created.ID)
	if err != nil {
		t.Fatalf("DeleteSecret failed: %v", err)
	}

	_, err = svc.GetSecret(ctx, "user-1", created.ID)
	if err == nil {
		t.Error("Secret should not exist after deletion")
	}
}

func TestSecretService_DeleteSecret_NotFound(t *testing.T) {
	svc, _, _ := setupSecretService(t)
	ctx := context.Background()

	err := svc.DeleteSecret(ctx, "user-1", "nonexistent")
	if err == nil {
		t.Error("DeleteSecret for nonexistent ID should fail")
	}
}

func TestSecretService_DecryptSecretValue(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	originalValue := "sk-super-secret-key-12345"
	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "decrypt-test", Type: SecretTypeLLMProvider, Value: originalValue, Metadata: json.RawMessage(`{"provider":"x"}`),
	})

	plaintext, err := svc.DecryptSecretValue(ctx, "user-1", sessionID, created.ID)
	if err != nil {
		t.Fatalf("DecryptSecretValue failed: %v", err)
	}
	if string(plaintext) != originalValue {
		t.Errorf("Expected '%s', got '%s'", originalValue, string(plaintext))
	}
}

func TestSecretService_SetAndGetBindings(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	s1, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "key-1", Type: SecretTypeLLMProvider, Value: "v1", Metadata: json.RawMessage(`{"provider":"a"}`),
	})
	s2, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "key-2", Type: SecretTypeEnvSecret, Value: "v2", Metadata: json.RawMessage(`{"var_name":"X"}`),
	})

	err := svc.SetBindings(ctx, "user-1", "workspace-1", []string{s1.ID, s2.ID})
	if err != nil {
		t.Fatalf("SetBindings failed: %v", err)
	}

	resp, err := svc.GetBindings(ctx, "user-1", "workspace-1")
	if err != nil {
		t.Fatalf("GetBindings failed: %v", err)
	}
	if len(resp.Bindings) != 2 {
		t.Errorf("Expected 2 bindings, got %d", len(resp.Bindings))
	}
}

func TestSecretService_SetBindings_NonexistentSecret(t *testing.T) {
	svc, _, _ := setupSecretService(t)
	ctx := context.Background()

	err := svc.SetBindings(ctx, "user-1", "workspace-1", []string{"nonexistent-id"})
	if err == nil {
		t.Error("Binding nonexistent secret should fail")
	}
}

func TestSecretService_AuditLogging(t *testing.T) {
	svc, store, sessionID := setupSecretService(t)
	ctx := context.Background()

	// Create a secret (generates audit entry)
	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "audited", Type: SecretTypeLLMProvider, Value: "v", Metadata: json.RawMessage(`{"provider":"x"}`),
	})

	// Update (generates audit entry)
	svc.UpdateSecret(ctx, "user-1", sessionID, created.ID, UpdateSecretRequest{Value: "v2"})

	// Delete (generates audit entry)
	svc.DeleteSecret(ctx, "user-1", created.ID)

	// Check audit entries
	entries, err := svc.QueryAudit(ctx, "user-1", AuditQuery{})
	if err != nil {
		t.Fatalf("QueryAudit failed: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 audit entries, got %d", len(entries))
	}

	// Verify actions
	actions := make([]string, len(store.audit))
	for i, e := range store.audit {
		actions[i] = e.Action
	}
	expected := []string{"create", "update", "delete"}
	for i, exp := range expected {
		if i >= len(actions) || actions[i] != exp {
			t.Errorf("Expected action[%d]='%s', got '%s'", i, exp, actions[i])
		}
	}
}

func TestSecretService_DeleteSecret_CascadesBindings(t *testing.T) {
	svc, secretStore, sessionID := setupSecretService(t)
	ctx := context.Background()

	created, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "bound-secret", Type: SecretTypeLLMProvider, Value: "v", Metadata: json.RawMessage(`{"provider":"x"}`),
	})

	// Bind to workspace
	svc.SetBindings(ctx, "user-1", "ws-1", []string{created.ID})

	// Delete secret
	svc.DeleteSecret(ctx, "user-1", created.ID)

	// Bindings should be gone
	bindings, _ := secretStore.GetBindings(ctx, "ws-1")
	if len(bindings) != 0 {
		t.Errorf("Expected 0 bindings after delete, got %d", len(bindings))
	}
}
