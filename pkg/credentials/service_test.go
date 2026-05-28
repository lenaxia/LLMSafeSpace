package credentials

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"
)

type mockCredStore struct {
	sets    map[string]*CredentialSetRow
	nextID  int
	delErr  error
	refCount int
}

func newMockCredStore() *mockCredStore {
	return &mockCredStore{sets: make(map[string]*CredentialSetRow)}
}

func (m *mockCredStore) CreateCredentialSet(_ context.Context, name string, encrypted []byte, keyVersion int, modelAllowlist []string, assignedTo json.RawMessage, isDefault bool) (string, error) {
	m.nextID++
	id := fmt.Sprintf("cred-%d", m.nextID)
	m.sets[id] = &CredentialSetRow{
		ID: id, Name: name, ProvidersEncrypted: encrypted,
		KeyVersion: keyVersion, ModelAllowlist: modelAllowlist,
		AssignedTo: assignedTo, IsDefault: isDefault,
	}
	return id, nil
}

func (m *mockCredStore) GetCredentialSet(_ context.Context, id string) (*CredentialSetRow, error) {
	row, ok := m.sets[id]
	if !ok {
		return nil, nil
	}
	return row, nil
}

func (m *mockCredStore) ListCredentialSets(_ context.Context) ([]*CredentialSetRow, error) {
	var rows []*CredentialSetRow
	for _, row := range m.sets {
		rows = append(rows, row)
	}
	return rows, nil
}

func (m *mockCredStore) UpdateCredentialSet(_ context.Context, id string, updates CredentialSetUpdates) error {
	row, ok := m.sets[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	if updates.Name != nil {
		row.Name = *updates.Name
	}
	return nil
}

func (m *mockCredStore) DeleteCredentialSet(_ context.Context, id string) error {
	if m.delErr != nil {
		return m.delErr
	}
	delete(m.sets, id)
	return nil
}

func (m *mockCredStore) SetDefault(_ context.Context, id string) error {
	for _, row := range m.sets {
		row.IsDefault = false
	}
	if row, ok := m.sets[id]; ok {
		row.IsDefault = true
	}
	return nil
}

func (m *mockCredStore) GetDefault(_ context.Context) (*CredentialSetRow, error) {
	for _, row := range m.sets {
		if row.IsDefault {
			return row, nil
		}
	}
	return nil, nil
}

func (m *mockCredStore) ListByKeyVersionBelow(_ context.Context, version int) ([]*CredentialSetRow, error) {
	var rows []*CredentialSetRow
	for _, row := range m.sets {
		if row.KeyVersion < version {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (m *mockCredStore) UpdateEncrypted(_ context.Context, id string, encrypted []byte, keyVersion int) error {
	if row, ok := m.sets[id]; ok {
		row.ProvidersEncrypted = encrypted
		row.KeyVersion = keyVersion
	}
	return nil
}

func (m *mockCredStore) CountWorkspacesUsingCredentialSet(_ context.Context, _ string) (int, error) {
	return m.refCount, nil
}

func newTestCredService() (*Service, *mockCredStore) {
	key := make([]byte, 32)
	rand.Read(key)
	ks := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: key}}}
	store := newMockCredStore()
	return NewService(store, ks, nil), store
}

func TestCredService_Create_Success(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:           "production",
		Providers:      ProviderConfig{"openai": {APIKey: "sk-test"}},
		ModelAllowlist: []string{"gpt-4"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.Name != "production" {
		t.Errorf("expected name production, got %q", cs.Name)
	}
	if len(cs.Providers) != 1 || cs.Providers[0] != "openai" {
		t.Errorf("expected [openai], got %v", cs.Providers)
	}
}

func TestCredService_Get_ReturnsProviderNames(t *testing.T) {
	svc, _ := newTestCredService()

	created, _ := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "test",
		Providers: ProviderConfig{"openai": {APIKey: "sk-1"}, "anthropic": {APIKey: "sk-2"}},
	})

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(got.Providers))
	}
}

func TestCredService_List(t *testing.T) {
	svc, _ := newTestCredService()

	svc.Create(context.Background(), CreateCredentialSetRequest{Name: "a", Providers: ProviderConfig{"x": {APIKey: "k"}}})
	svc.Create(context.Background(), CreateCredentialSetRequest{Name: "b", Providers: ProviderConfig{"y": {APIKey: "k"}}})

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestCredService_Delete_NoReferences_Succeeds(t *testing.T) {
	svc, store := newTestCredService()
	store.refCount = 0

	cs, _ := svc.Create(context.Background(), CreateCredentialSetRequest{Name: "del", Providers: ProviderConfig{"x": {APIKey: "k"}}})

	err := svc.Delete(context.Background(), cs.ID)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCredService_Delete_WithReferences_Fails(t *testing.T) {
	svc, store := newTestCredService()
	store.refCount = 3

	cs, _ := svc.Create(context.Background(), CreateCredentialSetRequest{Name: "ref", Providers: ProviderConfig{"x": {APIKey: "k"}}})

	err := svc.Delete(context.Background(), cs.ID)
	if err == nil {
		t.Error("expected error when credential set is referenced")
	}
}

func TestCredService_SetDefault(t *testing.T) {
	svc, store := newTestCredService()

	cs1, _ := svc.Create(context.Background(), CreateCredentialSetRequest{Name: "a", Providers: ProviderConfig{"x": {APIKey: "k"}}})
	cs2, _ := svc.Create(context.Background(), CreateCredentialSetRequest{Name: "b", Providers: ProviderConfig{"y": {APIKey: "k"}}})

	svc.SetDefault(context.Background(), cs1.ID)
	if !store.sets[cs1.ID].IsDefault {
		t.Error("expected cs1 to be default")
	}

	svc.SetDefault(context.Background(), cs2.ID)
	if store.sets[cs1.ID].IsDefault {
		t.Error("expected cs1 to no longer be default")
	}
	if !store.sets[cs2.ID].IsDefault {
		t.Error("expected cs2 to be default")
	}
}

func TestCredService_RotateKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ks := &EncryptionKeySet{Keys: []EncryptionKey{
		{Version: 1, Key: key1},
		{Version: 2, Key: key2},
	}}

	store := newMockCredStore()
	svc := NewService(store, ks, nil)

	// Create with key v1 by temporarily using only key1
	ks1 := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: key1}}}
	svc1 := NewService(store, ks1, nil)
	svc1.Create(context.Background(), CreateCredentialSetRequest{Name: "old", Providers: ProviderConfig{"x": {APIKey: "secret"}}})

	// Now rotate with the full key set (active = v2)
	result, err := svc.RotateEncryptionKey(context.Background())
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}
	if result.Rotated != 1 {
		t.Errorf("expected 1 rotated, got %d", result.Rotated)
	}

	// Verify the row is now at version 2
	for _, row := range store.sets {
		if row.KeyVersion != 2 {
			t.Errorf("expected key_version 2 after rotation, got %d", row.KeyVersion)
		}
	}
}

func TestCredService_RotateKey_Idempotent(t *testing.T) {
	key1 := make([]byte, 32)
	rand.Read(key1)
	ks := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: key1}}}

	store := newMockCredStore()
	svc := NewService(store, ks, nil)
	svc.Create(context.Background(), CreateCredentialSetRequest{Name: "x", Providers: ProviderConfig{"a": {APIKey: "k"}}})

	// All rows already at active version — nothing to rotate
	result, err := svc.RotateEncryptionKey(context.Background())
	if err != nil {
		t.Fatalf("rotation failed: %v", err)
	}
	if result.Rotated != 0 {
		t.Errorf("expected 0 rotated (already current), got %d", result.Rotated)
	}
	if result.AlreadyCurrent != 1 {
		t.Errorf("expected 1 already current, got %d", result.AlreadyCurrent)
	}
}
