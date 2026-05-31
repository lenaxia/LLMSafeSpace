package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func (m *mockSecretStore) ReEncryptUserSecrets(ctx context.Context, userID string, newKeyVersion int, transform func([]byte) ([]byte, error), commit func(context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Two-pass: compute every new ciphertext first; if any transform
	// fails, abort without modifying state. This mirrors the
	// transactional contract the SecretStore interface promises.
	updates := make(map[string][]byte)
	for id, s := range m.secrets {
		if s.UserID != userID {
			continue
		}
		newCT, err := transform(s.Ciphertext)
		if err != nil {
			return err
		}
		updates[id] = newCT
	}
	// Run commit hook before any state change so failures roll back.
	if commit != nil {
		if err := commit(ctx); err != nil {
			return err
		}
	}
	for id, newCT := range updates {
		s := m.secrets[id]
		s.Ciphertext = newCT
		s.KeyVersion = newKeyVersion
	}
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

func (m *mockSecretStore) AddBindings(_ context.Context, workspaceID string, secretIDs []string) error {
	if len(secretIDs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing := m.bindings[workspaceID]
	seen := make(map[string]struct{}, len(existing)+len(secretIDs))
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	for _, id := range secretIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		existing = append(existing, id)
	}
	m.bindings[workspaceID] = existing
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

// duplicateError wraps the package's ErrDuplicateSecret sentinel so
// errors.Is on the result of mockSecretStore.CreateSecret correctly
// classifies the error in handler tests. Without the Unwrap method,
// the handler's errors.Is(err, ErrDuplicateSecret) would not match.
type duplicateError struct{ name string }

func (e *duplicateError) Error() string { return "duplicate secret: " + e.name }
func (e *duplicateError) Unwrap() error { return ErrDuplicateSecret }

type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return "not found: " + e.id }
func (e *notFoundError) Unwrap() error { return ErrSecretNotFound }

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
		Type:     SecretTypeAPIKey,
		Value:    "sk-ant-api03-secret-key",
		Metadata: json.RawMessage(`{"provider": "anthropic"}`),
	})
	if err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}

	if resp.Name != "my-anthropic-key" {
		t.Errorf("Expected name 'my-anthropic-key', got '%s'", resp.Name)
	}
	if resp.Type != SecretTypeAPIKey {
		t.Errorf("Expected type api-key, got %s", resp.Type)
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

// TestSecretService_CreateSecret_InvalidType_ListsValidTypes is the
// regression test for Bug 7 in worklog 0085: the error message must
// enumerate the valid secret types so callers can fix the request
// without consulting external docs.
func TestSecretService_CreateSecret_InvalidType_ListsValidTypes(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "test", Type: "bogus", Value: "data",
	})
	if err == nil {
		t.Fatal("Invalid type must error")
	}
	msg := err.Error()
	for _, want := range []string{"api-key", "ssh-key", "git-credential", "secret-file", "env-secret"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must list valid type %q", msg, want)
		}
	}
}

// TestSecretService_CreateSecret_InvalidMetadata_NamesField is the
// regression test for Bug 7: when metadata is missing a required key,
// the error names the exact field expected (e.g. "var_name") so callers
// don't have to reverse-engineer the schema.
func TestSecretService_CreateSecret_InvalidMetadata_NamesField(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	cases := []struct {
		secretType SecretType
		wantField  string
	}{
		{SecretTypeSSHKey, "key_type"},
		{SecretTypeSecretFile, "mount_path"},
		{SecretTypeEnvSecret, "var_name"},
	}
	for _, tc := range cases {
		_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
			Name: "n-" + string(tc.secretType), Type: tc.secretType, Value: "v",
			Metadata: json.RawMessage(`{}`),
		})
		if err == nil {
			t.Errorf("%s: missing metadata must error", tc.secretType)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantField) {
			t.Errorf("%s: error %q must name field %q", tc.secretType, err.Error(), tc.wantField)
		}
	}
}

// TestSecretService_CreateSecret_RejectsAdversarialMountPath is the
// regression test for Bug 13 in worklog 0085: API-layer defence-in-depth
// against path-traversal in secret-file mount_path. The materializer's
// resolveMountPath catches these too, but accepting them at the API
// layer means adversarial input lives in the database long enough for a
// future bug or migration to mishandle it.
func TestSecretService_CreateSecret_RejectsAdversarialMountPath(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	bad := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"/../../etc/shadow",
		"../escaped",
		".../traversal",
		"./valid/../../escape",
		"foo/../../bar",
		"", // empty
	}
	for _, mp := range bad {
		_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
			Name: "f-" + mp, Type: SecretTypeSecretFile, Value: "x",
			Metadata: json.RawMessage(`{"mount_path":"` + mp + `"}`),
		})
		if err == nil {
			t.Errorf("mount_path %q must be rejected at the API layer", mp)
		}
	}

	// Sanity: a safe path is still accepted.
	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "safe-secret-file", Type: SecretTypeSecretFile, Value: "x",
		Metadata: json.RawMessage(`{"mount_path":"config/app.yaml"}`),
	})
	if err != nil {
		t.Errorf("safe mount_path was rejected: %v", err)
	}
}

// fakeWorkspaceOwnerVerifier returns ErrWorkspaceNotOwned for any
// (userID, workspaceID) pair not in the allowedPairs map. Used to
// exercise the cross-tenant binding-pollution defence (validator
// pass-3 finding SO-1).
type fakeWorkspaceOwnerVerifier struct {
	allowedPairs map[string]map[string]struct{} // userID -> set of workspaceIDs
}

func (f *fakeWorkspaceOwnerVerifier) VerifyWorkspaceOwner(_ context.Context, userID, workspaceID string) error {
	if pairs, ok := f.allowedPairs[userID]; ok {
		if _, ok := pairs[workspaceID]; ok {
			return nil
		}
	}
	return ErrWorkspaceNotOwned
}

// TestSecretService_SetBindings_RejectsForeignWorkspace is the
// regression test for SO-1: SetBindings/AddBindings must verify the
// caller owns the workspace, not just the secrets being bound.
// Pre-fix any user with a leaked workspaceID could pollute another
// user's binding rows.
func TestSecretService_SetBindings_RejectsForeignWorkspace(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	verifier := &fakeWorkspaceOwnerVerifier{
		allowedPairs: map[string]map[string]struct{}{
			"user-1": {"ws-mine": {}},
		},
	}
	svc.SetWorkspaceOwnerVerifier(verifier)

	created, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "mine", Type: SecretTypeEnvSecret, Value: "v",
		Metadata: []byte(`{"var_name":"X"}`),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// Owned workspace: SetBindings succeeds.
	if err := svc.SetBindings(ctx, "user-1", "ws-mine", []string{created.ID}); err != nil {
		t.Fatalf("SetBindings on owned workspace: %v", err)
	}

	// Foreign workspace: must reject with ErrWorkspaceNotOwned.
	err = svc.SetBindings(ctx, "user-1", "ws-other", []string{created.ID})
	if !errors.Is(err, ErrWorkspaceNotOwned) {
		t.Errorf("SO-1: SetBindings on foreign workspace must return ErrWorkspaceNotOwned, got %v", err)
	}

	// AddBindings must also reject.
	err = svc.AddBindings(ctx, "user-1", "ws-other", []string{created.ID})
	if !errors.Is(err, ErrWorkspaceNotOwned) {
		t.Errorf("SO-1: AddBindings on foreign workspace must return ErrWorkspaceNotOwned, got %v", err)
	}
}

// TestSecretService_RequireOwnerVerification_FailsClosed is the
// regression test for NEW-1 + N-4: when RequireOwnerVerification has
// been called but no verifier is wired (e.g. a future wiring
// regression in app.New), every binding/read operation that touches
// a workspace must return ErrWorkspaceNotOwned. Pre-flag the same
// configuration silently bypassed the check, which would re-enable
// cross-tenant binding pollution and enumeration.
func TestSecretService_RequireOwnerVerification_FailsClosed(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	created, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name: "mine", Type: SecretTypeEnvSecret, Value: "v",
		Metadata: []byte(`{"var_name":"X"}`),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// Default service: no verifier wired, requireWsVerifier=false.
	// SetBindings should succeed (test ergonomics).
	if err := svc.SetBindings(ctx, "user-1", "ws-1", []string{created.ID}); err != nil {
		t.Fatalf("default SecretService: SetBindings should succeed without verifier: %v", err)
	}

	// Flip into fail-closed mode WITHOUT wiring a verifier.
	svc.RequireOwnerVerification()

	// Every workspace-touching method must now refuse.
	cases := []struct {
		name string
		fn   func() error
	}{
		{"SetBindings", func() error {
			return svc.SetBindings(ctx, "user-1", "ws-1", []string{created.ID})
		}},
		{"AddBindings", func() error {
			return svc.AddBindings(ctx, "user-1", "ws-1", []string{created.ID})
		}},
		{"GetBindings", func() error {
			_, err := svc.GetBindings(ctx, "user-1", "ws-1")
			return err
		}},
		{"PrepareSecretsForInjection", func() error {
			_, err := svc.PrepareSecretsForInjection(ctx, "user-1", sessionID, "ws-1")
			return err
		}},
	}
	for _, c := range cases {
		err := c.fn()
		if !errors.Is(err, ErrWorkspaceNotOwned) {
			t.Errorf("NEW-1/N-4: %s must return ErrWorkspaceNotOwned when verifier is required but missing, got %v",
				c.name, err)
		}
	}
}

func TestSecretService_CreateSecret_DuplicateName(t *testing.T) {
	svc, _, sessionID := setupSecretService(t)
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "my-key",
		Type:     SecretTypeAPIKey,
		Value:    "value1",
		Metadata: json.RawMessage(`{"provider": "openai"}`),
	})
	if err != nil {
		t.Fatalf("First create failed: %v", err)
	}

	_, err = svc.CreateSecret(ctx, "user-1", sessionID, CreateSecretRequest{
		Name:     "my-key",
		Type:     SecretTypeAPIKey,
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
		Type:  SecretTypeAPIKey,
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
		Type:     SecretTypeAPIKey,
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
		Name: "key-1", Type: SecretTypeAPIKey, Value: "v1", Metadata: json.RawMessage(`{"provider":"a"}`),
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
		Name: "updatable", Type: SecretTypeAPIKey, Value: "old-value", Metadata: json.RawMessage(`{"provider":"x"}`),
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
		Name: "deletable", Type: SecretTypeAPIKey, Value: "val", Metadata: json.RawMessage(`{"provider":"x"}`),
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
		Name: "decrypt-test", Type: SecretTypeAPIKey, Value: originalValue, Metadata: json.RawMessage(`{"provider":"x"}`),
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
		Name: "key-1", Type: SecretTypeAPIKey, Value: "v1", Metadata: json.RawMessage(`{"provider":"a"}`),
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
		Name: "audited", Type: SecretTypeAPIKey, Value: "v", Metadata: json.RawMessage(`{"provider":"x"}`),
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
		Name: "bound-secret", Type: SecretTypeAPIKey, Value: "v", Metadata: json.RawMessage(`{"provider":"x"}`),
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
