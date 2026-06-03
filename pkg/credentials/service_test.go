// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type mockCredStore struct {
	sets   map[string]*CredentialSetRow
	nextID int
	delErr error
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
	if updates.ProvidersEncrypted != nil {
		row.ProvidersEncrypted = *updates.ProvidersEncrypted
	}
	if updates.KeyVersion != nil {
		row.KeyVersion = *updates.KeyVersion
	}
	if updates.ModelAllowlist != nil {
		row.ModelAllowlist = *updates.ModelAllowlist
	}
	if updates.AssignedTo != nil {
		row.AssignedTo = *updates.AssignedTo
	}
	if updates.IsDefault != nil {
		row.IsDefault = *updates.IsDefault
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

func TestCredService_Delete_NonDefault_Succeeds(t *testing.T) {
	svc, _ := newTestCredService()

	cs, _ := svc.Create(context.Background(), CreateCredentialSetRequest{Name: "del", Providers: ProviderConfig{"x": {APIKey: "k"}}})

	err := svc.Delete(context.Background(), cs.ID)
	if err != nil {
		t.Errorf("unexpected error deleting non-default set: %v", err)
	}
}

func TestCredService_Delete_Default_Fails(t *testing.T) {
	svc, _ := newTestCredService()

	// Create as default — cannot delete while it's the default.
	cs, _ := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "default-set",
		Providers: ProviderConfig{"x": {APIKey: "k"}},
		IsDefault: true,
	})

	err := svc.Delete(context.Background(), cs.ID)
	if err == nil {
		t.Error("expected error when deleting the default credential set")
	}
	if err != nil && !strings.Contains(err.Error(), "default") {
		t.Errorf("expected 'default' in error, got: %v", err)
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

func TestCredService_Update_Name(t *testing.T) {
	svc, _ := newTestCredService()
	ctx := context.Background()

	cs, _ := svc.Create(ctx, CreateCredentialSetRequest{
		Name:      "original",
		Providers: ProviderConfig{"openai": {APIKey: "sk-1"}},
	})

	newName := "renamed"
	err := svc.Update(ctx, cs.ID, UpdateCredentialSetRequest{Name: &newName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := svc.Get(ctx, cs.ID)
	if got.Name != "renamed" {
		t.Errorf("expected name 'renamed', got %q", got.Name)
	}
}

func TestCredService_Update_Providers_ReEncrypts(t *testing.T) {
	svc, store := newTestCredService()
	ctx := context.Background()

	cs, _ := svc.Create(ctx, CreateCredentialSetRequest{
		Name:      "reencrypt",
		Providers: ProviderConfig{"openai": {APIKey: "sk-old"}},
	})

	oldEncrypted := make([]byte, len(store.sets[cs.ID].ProvidersEncrypted))
	copy(oldEncrypted, store.sets[cs.ID].ProvidersEncrypted)

	newProviders := ProviderConfig{"anthropic": {APIKey: "sk-new"}}
	err := svc.Update(ctx, cs.ID, UpdateCredentialSetRequest{Providers: &newProviders})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Encrypted bytes should differ
	if string(store.sets[cs.ID].ProvidersEncrypted) == string(oldEncrypted) {
		t.Error("expected providers to be re-encrypted")
	}

	// Verify provider names updated
	got, _ := svc.Get(ctx, cs.ID)
	if len(got.Providers) != 1 || got.Providers[0] != "anthropic" {
		t.Errorf("expected [anthropic], got %v", got.Providers)
	}
}

func TestCredService_Update_NotFound(t *testing.T) {
	svc, _ := newTestCredService()
	name := "x"
	err := svc.Update(context.Background(), "nonexistent", UpdateCredentialSetRequest{Name: &name})
	if err == nil {
		t.Error("expected error for nonexistent credential set")
	}
}

func TestCredService_GetDefault_Exists(t *testing.T) {
	svc, _ := newTestCredService()
	ctx := context.Background()

	cs, _ := svc.Create(ctx, CreateCredentialSetRequest{
		Name:      "default-one",
		Providers: ProviderConfig{"x": {APIKey: "k"}},
		IsDefault: true,
	})

	got, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != cs.ID {
		t.Errorf("expected ID %q, got %q", cs.ID, got.ID)
	}
}

func TestCredService_GetDefault_NoneSet(t *testing.T) {
	svc, _ := newTestCredService()

	got, err := svc.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil when no default, got %+v", got)
	}
}

func TestCredService_ListForUser_All(t *testing.T) {
	svc, _ := newTestCredService()
	ctx := context.Background()

	svc.Create(ctx, CreateCredentialSetRequest{
		Name:       "for-all",
		Providers:  ProviderConfig{"x": {APIKey: "k"}},
		AssignedTo: "all",
	})
	svc.Create(ctx, CreateCredentialSetRequest{
		Name:       "for-specific",
		Providers:  ProviderConfig{"y": {APIKey: "k"}},
		AssignedTo: []string{"user-1"},
	})

	list, err := svc.ListForUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 (all + specific), got %d", len(list))
	}
}

func TestCredService_ListForUser_ExcludesOtherUsers(t *testing.T) {
	svc, _ := newTestCredService()
	ctx := context.Background()

	svc.Create(ctx, CreateCredentialSetRequest{
		Name:       "for-user1",
		Providers:  ProviderConfig{"x": {APIKey: "k"}},
		AssignedTo: []string{"user-1"},
	})

	list, err := svc.ListForUser(ctx, "user-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 for user-2, got %d", len(list))
	}
}

func TestCredService_GetDecryptedProviders(t *testing.T) {
	svc, _ := newTestCredService()
	ctx := context.Background()

	cs, _ := svc.Create(ctx, CreateCredentialSetRequest{
		Name:      "decrypt-test",
		Providers: ProviderConfig{"openai": {APIKey: "sk-secret-123", BaseURL: "https://api.openai.com/v1"}},
	})

	config, err := svc.GetDecryptedProviders(ctx, cs.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config["openai"].APIKey != "sk-secret-123" {
		t.Errorf("expected sk-secret-123, got %q", config["openai"].APIKey)
	}
	if config["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected base URL, got %q", config["openai"].BaseURL)
	}
}

func TestCredService_GetDecryptedProviders_NotFound(t *testing.T) {
	svc, _ := newTestCredService()
	_, err := svc.GetDecryptedProviders(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent credential set")
	}
}

// --- Null-slice regression guards ---
//
// Why these tests exist
// ---------------------
// A user reported a frontend crash:
//
//   Cannot read properties of null (reading 'length')
//   at Array.map -> sets.map(cs => ... cs.modelAllowlist.length ...)
//
// Root cause: the Go API returned a CredentialSet whose ModelAllowlist
// was a nil []string. encoding/json renders nil slices as `null`, NOT
// `[]`. The frontend's TypeScript types declare `modelAllowlist:
// string[]`, so calling `.length` on `null` crashes when re-rendering
// after a successful create.
//
// Two paths produced nil slices:
//
//   1. Create response: `ModelAllowlist: req.ModelAllowlist` literally
//      copied the request's nil through to the response, even though
//      the DB write was correctly defaulted to []string{} by an
//      earlier fix (commit b7548de).
//   2. rowToCredentialSet (used by Get/List): if the encrypted
//      Providers field failed to decrypt OR the row's ModelAllowlist
//      came back as a nil from the DB driver, both fields stayed nil.
//
// These tests pin the contract: every API-returned CredentialSet MUST
// have non-nil Providers and ModelAllowlist slices, AND when JSON-
// marshaled they MUST appear as `[]`, never `null`.

// TestCredService_Create_ResponseHasNonNilSlices is the regression
// guard for the Create path. The user's repro: POST with no
// ModelAllowlist → response had ModelAllowlist null → frontend crashed.
func TestCredService_Create_ResponseHasNonNilSlices(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "no-allowlist",
		Providers: ProviderConfig{"openai": {APIKey: "sk-test"}},
		// NO ModelAllowlist — simulates the frontend's CreateCredentialForm
		// which currently does not surface a model-allowlist input.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cs.ModelAllowlist == nil {
		t.Error("ModelAllowlist must NOT be nil on Create response (frontend assumes string[])")
	}
	if cs.Providers == nil {
		t.Error("Providers must NOT be nil on Create response")
	}

	// Cross-check: JSON marshaling must produce `[]`, never `null`.
	body, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["modelAllowlist"]) == "null" {
		t.Errorf("modelAllowlist serialized as `null`; frontend.cs.modelAllowlist.length will throw\nbody: %s", body)
	}
	if string(raw["providers"]) == "null" {
		t.Errorf("providers serialized as `null`; frontend.cs.providers.join will throw\nbody: %s", body)
	}
}

// TestCredService_Get_ResponseHasNonNilSlices is the regression guard
// for the Get path (rowToCredentialSet). Even on rows where decrypt
// fails, providers must be `[]` not `nil`.
func TestCredService_Get_ResponseHasNonNilSlices(t *testing.T) {
	svc, _ := newTestCredService()
	created, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "g",
		Providers: ProviderConfig{"openai": {APIKey: "sk"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ModelAllowlist == nil {
		t.Error("Get returned ModelAllowlist=nil; frontend will crash on .length")
	}
	if got.Providers == nil {
		t.Error("Get returned Providers=nil; frontend will crash on .join")
	}

	body, _ := json.Marshal(got)
	if bytes.Contains(body, []byte(`"modelAllowlist":null`)) {
		t.Errorf("Get response serializes modelAllowlist as null: %s", body)
	}
	if bytes.Contains(body, []byte(`"providers":null`)) {
		t.Errorf("Get response serializes providers as null: %s", body)
	}
}

// TestCredService_List_ResponseHasNonNilSlices guards the List path.
// This is the EXACT path that crashed the user's UI — sets.map() on
// the List response, then .modelAllowlist.length on each.
func TestCredService_List_ResponseHasNonNilSlices(t *testing.T) {
	svc, _ := newTestCredService()
	_, _ = svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "a",
		Providers: ProviderConfig{"openai": {APIKey: "sk"}},
	})
	_, _ = svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "b",
		Providers: ProviderConfig{"anthropic": {APIKey: "sk"}},
	})

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i, cs := range list {
		if cs.ModelAllowlist == nil {
			t.Errorf("list[%d].ModelAllowlist is nil; frontend will crash", i)
		}
		if cs.Providers == nil {
			t.Errorf("list[%d].Providers is nil; frontend will crash", i)
		}
	}

	body, _ := json.Marshal(list)
	if bytes.Contains(body, []byte(`"modelAllowlist":null`)) {
		t.Errorf("List response contains modelAllowlist:null — frontend will crash on .length\nbody: %s", body)
	}
	if bytes.Contains(body, []byte(`"providers":null`)) {
		t.Errorf("List response contains providers:null — frontend will crash on .join\nbody: %s", body)
	}
}

// TestCredService_RowToCredentialSet_DecryptFailure_ProvidersNotNil
// is the deep regression guard. If decrypt fails for any reason
// (corrupt row, key rotation mid-flight), providers becomes nil at
// line 311 of service.go ("var providers []string"). This test pins
// the invariant that even in the failure path, the field is `[]`.
func TestCredService_RowToCredentialSet_DecryptFailure_ProvidersNotNil(t *testing.T) {
	svc, store := newTestCredService()

	// Corrupt the encrypted blob so decrypt fails.
	created, _ := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "corrupt-me",
		Providers: ProviderConfig{"openai": {APIKey: "sk"}},
	})
	store.sets[created.ID].ProvidersEncrypted = []byte("not-a-valid-ciphertext")

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get must succeed even when decrypt fails (got error: %v)", err)
	}
	if got.Providers == nil {
		t.Error("Providers must be []string{} (not nil) when decrypt fails")
	}
	body, _ := json.Marshal(got)
	if bytes.Contains(body, []byte(`"providers":null`)) {
		t.Errorf("decrypt-failure path still emits providers:null: %s", body)
	}
}
