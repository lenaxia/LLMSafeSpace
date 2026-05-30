package secrets

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSecretService_PrepareSecretsForInjection_Empty(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	data, err := svc.PrepareSecretsForInjection(ctx, "user-1", sessionID, "ws-empty")
	if err != nil {
		t.Fatalf("PrepareSecretsForInjection failed: %v", err)
	}

	var injected []InjectedSecret
	json.Unmarshal(data, &injected)
	if len(injected) != 0 {
		t.Errorf("Expected 0 injected secrets, got %d", len(injected))
	}
}

func TestSecretService_PrepareSecretsForInjection_MultipleTypes(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	// Create secrets of different types
	s1, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "anthropic", Type: SecretTypeAPIKey, Value: `{"apiKey":"sk-ant-123","provider":"anthropic"}`,
		Metadata: json.RawMessage(`{"provider":"anthropic"}`),
	})
	s2, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "gh-ssh", Type: SecretTypeSSHKey, Value: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake",
		Metadata: json.RawMessage(`{"key_type":"ed25519","host":"github.com"}`),
	})
	s3, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "db-url", Type: SecretTypeEnvSecret, Value: "postgres://user:pass@host/db",
		Metadata: json.RawMessage(`{"var_name":"DATABASE_URL"}`),
	})

	// Bind all to workspace
	svc.SetBindings(ctx, "user-1", "ws-1", []string{s1.ID, s2.ID, s3.ID})

	// Prepare injection
	data, err := svc.PrepareSecretsForInjection(ctx, "user-1", sessionID, "ws-1")
	if err != nil {
		t.Fatalf("PrepareSecretsForInjection failed: %v", err)
	}

	var injected []InjectedSecret
	if err := json.Unmarshal(data, &injected); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(injected) != 3 {
		t.Fatalf("Expected 3 injected secrets, got %d", len(injected))
	}

	// Verify types and plaintext
	typeMap := make(map[SecretType]string)
	for _, s := range injected {
		typeMap[s.Type] = s.Plaintext
	}

	if v, ok := typeMap[SecretTypeAPIKey]; !ok || v == "" {
		t.Error("LLM provider secret missing or empty")
	}
	if v, ok := typeMap[SecretTypeSSHKey]; !ok || v == "" {
		t.Error("SSH key secret missing or empty")
	}
	if v, ok := typeMap[SecretTypeEnvSecret]; !ok || v != "postgres://user:pass@host/db" {
		t.Errorf("Env secret wrong: %s", v)
	}
}

func TestSecretService_PrepareSecretsForInjection_NoSession(t *testing.T) {
	keyStore := newMockKeyStore()
	dekCache := newMockDEKCache()
	keySvc := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()
	svc := NewSecretService(keySvc, secretStore)
	ctx := context.Background()

	// Initialize but don't unlock
	_, _ = keySvc.InitializeUserKeys(ctx, "user-1", []byte("pw"))

	_, err := svc.PrepareSecretsForInjection(ctx, "user-1", "no-session", "ws-1")
	// Should succeed with empty result (no bindings)
	if err != nil {
		t.Fatalf("Should succeed with no bindings: %v", err)
	}
}

func TestSecretService_PrepareSecretsForInjection_AuditLogged(t *testing.T) {
	svc, store, sessionID := setupSecretService(t)
	ctx := context.Background()

	s1, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "audited-inject", Type: SecretTypeAPIKey, Value: "val",
		Metadata: json.RawMessage(`{"provider":"x"}`),
	})
	svc.SetBindings(ctx, "user-1", "ws-1", []string{s1.ID})

	// Clear audit from create/bind
	store.mu.Lock()
	store.audit = nil
	store.mu.Unlock()

	// Inject
	svc.PrepareSecretsForInjection(ctx, "user-1", sessionID, "ws-1")

	// Should have a "read" audit entry
	entries, _ := svc.QueryAudit(ctx, "user-1", AuditQuery{})
	found := false
	for _, e := range entries {
		if e.Action == "read" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'read' audit entry for pod injection")
	}
}

func TestSecretService_PrepareSecretsForInjection_PreservesMetadata(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	s1, _ := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "file-secret", Type: SecretTypeSecretFile, Value: "cert-content",
		Metadata: json.RawMessage(`{"mount_path":"cert.pem"}`),
	})
	svc.SetBindings(ctx, "user-1", "ws-1", []string{s1.ID})

	data, _ := svc.PrepareSecretsForInjection(ctx, "user-1", sessionID, "ws-1")

	var injected []InjectedSecret
	json.Unmarshal(data, &injected)

	if len(injected) != 1 {
		t.Fatalf("Expected 1, got %d", len(injected))
	}

	var meta map[string]string
	json.Unmarshal(injected[0].Metadata, &meta)
	if meta["mount_path"] != "cert.pem" {
		t.Errorf("Metadata mount_path not preserved: %v", meta)
	}
}

// setupSecretServiceWithTwoUsers creates a service with two users for isolation tests
func setupSecretServiceWithTwoUsers(t *testing.T) (*SecretService, string, string) {
	t.Helper()
	keyStore := newMockKeyStore()
	dekCache := newMockDEKCache()
	keySvc := NewKeyService(keyStore, dekCache)
	secretStore := newMockSecretStore()
	svc := NewSecretService(keySvc, secretStore)
	ctx := context.Background()

	_, _ = keySvc.InitializeUserKeys(ctx, "user-1", []byte("pw1"))
	_ = keySvc.UnlockDEK(ctx, "user-1", []byte("pw1"), "sess-1", time.Hour)

	_, _ = keySvc.InitializeUserKeys(ctx, "user-2", []byte("pw2"))
	_ = keySvc.UnlockDEK(ctx, "user-2", []byte("pw2"), "sess-2", time.Hour)

	return svc, "sess-1", "sess-2"
}

func TestSecretService_PrepareSecretsForInjection_CrossTenantIsolation(t *testing.T) {
	svc, sess1, _ := setupSecretServiceWithTwoUsers(t)
	ctx := context.Background()

	// User 1 creates and binds a secret
	s1, _ := svc.CreateSecret(ctx, "user-1", sess1, CreateSecretRequest{
		Name: "private", Type: SecretTypeAPIKey, Value: "user1-key",
		Metadata: json.RawMessage(`{"provider":"x"}`),
	})
	svc.SetBindings(ctx, "user-1", "ws-user1", []string{s1.ID})

	// User 2 tries to inject from user 1's workspace (should get nothing)
	data, err := svc.PrepareSecretsForInjection(ctx, "user-2", "sess-2", "ws-user1")
	if err != nil {
		t.Fatalf("Should not error: %v", err)
	}

	var injected []InjectedSecret
	json.Unmarshal(data, &injected)
	// The bindings exist but secrets belong to user-1, so user-2 gets filtered out
	for _, s := range injected {
		if s.Plaintext == "user1-key" {
			t.Error("User 2 should NOT see user 1's decrypted secrets")
		}
	}
}
