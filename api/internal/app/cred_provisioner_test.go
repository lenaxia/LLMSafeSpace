package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/credentials"
)

func TestCredProvisionerAdapter_GetDefault_ReturnsDecryptedJSON(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	ks := &credentials.EncryptionKeySet{Keys: []credentials.EncryptionKey{{Version: 1, Key: key}}}

	// Use a real in-memory store
	store := &inMemoryCredStore{sets: make(map[string]*credentials.CredentialSetRow)}
	svc := credentials.NewService(store, ks, nil)

	// Create a credential set and set it as default
	cs, err := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name:      "test-default",
		Providers: credentials.ProviderConfig{"openai": {APIKey: "sk-live-xyz", BaseURL: "https://api.openai.com/v1"}},
		IsDefault: true,
	})
	require.NoError(t, err)

	// Use the adapter
	adapter := &credProvisionerAdapter{credSvc: svc}
	id, config, err := adapter.GetDefault(context.Background())

	require.NoError(t, err)
	assert.Equal(t, cs.ID, id)
	assert.NotNil(t, config)

	// Verify the JSON contains the decrypted credentials
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(config, &parsed))
	assert.Contains(t, string(parsed["openai"]), "sk-live-xyz")
}

func TestCredProvisionerAdapter_GetDefault_NoDefault_ReturnsNil(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	ks := &credentials.EncryptionKeySet{Keys: []credentials.EncryptionKey{{Version: 1, Key: key}}}

	store := &inMemoryCredStore{sets: make(map[string]*credentials.CredentialSetRow)}
	svc := credentials.NewService(store, ks, nil)

	adapter := &credProvisionerAdapter{credSvc: svc}
	id, config, err := adapter.GetDefault(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, id)
	assert.Nil(t, config)
}

// inMemoryCredStore implements credentials.Store for testing.
type inMemoryCredStore struct {
	sets   map[string]*credentials.CredentialSetRow
	nextID int
}

func (s *inMemoryCredStore) CreateCredentialSet(_ context.Context, name string, encrypted []byte, keyVersion int, modelAllowlist []string, assignedTo json.RawMessage, isDefault bool) (string, error) {
	s.nextID++
	id := fmt.Sprintf("cred-%d", s.nextID)
	s.sets[id] = &credentials.CredentialSetRow{
		ID: id, Name: name, ProvidersEncrypted: encrypted,
		KeyVersion: keyVersion, ModelAllowlist: modelAllowlist,
		AssignedTo: assignedTo, IsDefault: isDefault,
	}
	return id, nil
}

func (s *inMemoryCredStore) GetCredentialSet(_ context.Context, id string) (*credentials.CredentialSetRow, error) {
	return s.sets[id], nil
}

func (s *inMemoryCredStore) ListCredentialSets(_ context.Context) ([]*credentials.CredentialSetRow, error) {
	var result []*credentials.CredentialSetRow
	for _, r := range s.sets {
		result = append(result, r)
	}
	return result, nil
}

func (s *inMemoryCredStore) UpdateCredentialSet(_ context.Context, _ string, _ credentials.CredentialSetUpdates) error {
	return nil
}
func (s *inMemoryCredStore) DeleteCredentialSet(_ context.Context, _ string) error { return nil }
func (s *inMemoryCredStore) SetDefault(_ context.Context, id string) error {
	for _, r := range s.sets {
		r.IsDefault = r.ID == id
	}
	return nil
}
func (s *inMemoryCredStore) GetDefault(_ context.Context) (*credentials.CredentialSetRow, error) {
	for _, r := range s.sets {
		if r.IsDefault {
			return r, nil
		}
	}
	return nil, nil
}
func (s *inMemoryCredStore) ListByKeyVersionBelow(_ context.Context, _ int) ([]*credentials.CredentialSetRow, error) {
	return nil, nil
}
func (s *inMemoryCredStore) UpdateEncrypted(_ context.Context, _ string, _ []byte, _ int) error {
	return nil
}
func (s *inMemoryCredStore) CountWorkspacesUsingCredentialSet(_ context.Context, _ string) (int, error) {
	return 0, nil
}
