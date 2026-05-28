package credentials

import (
	"context"
	"encoding/json"
	"fmt"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// Store is the database interface for credential sets.
type Store interface {
	CreateCredentialSet(ctx context.Context, name string, encrypted []byte, keyVersion int, modelAllowlist []string, assignedTo json.RawMessage, isDefault bool) (string, error)
	GetCredentialSet(ctx context.Context, id string) (*CredentialSetRow, error)
	ListCredentialSets(ctx context.Context) ([]*CredentialSetRow, error)
	UpdateCredentialSet(ctx context.Context, id string, updates CredentialSetUpdates) error
	DeleteCredentialSet(ctx context.Context, id string) error
	SetDefault(ctx context.Context, id string) error
	GetDefault(ctx context.Context) (*CredentialSetRow, error)
	ListByKeyVersionBelow(ctx context.Context, version int) ([]*CredentialSetRow, error)
	UpdateEncrypted(ctx context.Context, id string, encrypted []byte, keyVersion int) error
	CountWorkspacesUsingCredentialSet(ctx context.Context, credSetID string) (int, error)
}

// CredentialSetRow is the raw DB row representation.
type CredentialSetRow struct {
	ID                 string
	Name               string
	IsDefault          bool
	ProvidersEncrypted []byte
	KeyVersion         int
	ModelAllowlist     []string
	AssignedTo         json.RawMessage
	CreatedAt          string
	UpdatedAt          string
}

// CredentialSetUpdates holds optional fields for partial updates.
type CredentialSetUpdates struct {
	Name               *string
	ProvidersEncrypted *[]byte
	KeyVersion         *int
	ModelAllowlist     *[]string
	AssignedTo         *json.RawMessage
	IsDefault          *bool
}

// Service manages credential sets with encryption.
type Service struct {
	store  Store
	keySet *EncryptionKeySet
	logger pkginterfaces.LoggerInterface
}

// NewService creates a new credential sets service.
func NewService(store Store, keySet *EncryptionKeySet, logger pkginterfaces.LoggerInterface) *Service {
	return &Service{store: store, keySet: keySet, logger: logger}
}

func (s *Service) Start() error { return nil }
func (s *Service) Stop() error  { return nil }

// Create creates a new credential set with encrypted providers.
func (s *Service) Create(ctx context.Context, req CreateCredentialSetRequest) (*CredentialSet, error) {
	plaintext, err := MarshalProviders(req.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal providers: %w", err)
	}

	// Use the credential set name as AAD (will be set after ID is known for future versions)
	encrypted, keyVersion, err := Encrypt(s.keySet, plaintext, []byte(req.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt providers: %w", err)
	}

	assignedTo, _ := json.Marshal(req.AssignedTo)
	if req.AssignedTo == nil {
		assignedTo = []byte(`"all"`)
	}

	id, err := s.store.CreateCredentialSet(ctx, req.Name, encrypted, keyVersion, req.ModelAllowlist, assignedTo, req.IsDefault)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential set: %w", err)
	}

	return &CredentialSet{
		ID:             id,
		Name:           req.Name,
		IsDefault:      req.IsDefault,
		Providers:      providerNames(req.Providers),
		ModelAllowlist: req.ModelAllowlist,
		AssignedTo:     req.AssignedTo,
		KeyVersion:     keyVersion,
	}, nil
}

// Get retrieves a credential set by ID (providers are NOT decrypted — names only).
func (s *Service) Get(ctx context.Context, id string) (*CredentialSet, error) {
	row, err := s.store.GetCredentialSet(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("credential set %q not found", id)
	}
	return rowToCredentialSet(row, s.keySet), nil
}

// List returns all credential sets (providers are NOT decrypted).
func (s *Service) List(ctx context.Context) ([]*CredentialSet, error) {
	rows, err := s.store.ListCredentialSets(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*CredentialSet, len(rows))
	for i, row := range rows {
		result[i] = rowToCredentialSet(row, s.keySet)
	}
	return result, nil
}

// Delete deletes a credential set. Returns error if referenced by workspaces.
func (s *Service) Delete(ctx context.Context, id string) error {
	count, err := s.store.CountWorkspacesUsingCredentialSet(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to check references: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("credential set is referenced by %d workspace(s)", count)
	}
	return s.store.DeleteCredentialSet(ctx, id)
}

// SetDefault sets a credential set as the default.
func (s *Service) SetDefault(ctx context.Context, id string) error {
	return s.store.SetDefault(ctx, id)
}

// RotateEncryptionKey re-encrypts all credential sets with the active key.
func (s *Service) RotateEncryptionKey(ctx context.Context) (*RotateKeyResult, error) {
	active, err := s.keySet.ActiveKey()
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListByKeyVersionBelow(ctx, active.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to list rows for rotation: %w", err)
	}

	result := &RotateKeyResult{}
	for _, row := range rows {
		// Decrypt with old key
		plaintext, err := Decrypt(s.keySet, row.ProvidersEncrypted, []byte(row.Name))
		if err != nil {
			result.Errors++
			if s.logger != nil {
				s.logger.Error("rotation decrypt failed", err, "id", row.ID)
			}
			continue
		}

		// Re-encrypt with active key
		encrypted, _, err := Encrypt(s.keySet, plaintext, []byte(row.Name))
		if err != nil {
			result.Errors++
			continue
		}

		if err := s.store.UpdateEncrypted(ctx, row.ID, encrypted, active.Version); err != nil {
			result.Errors++
			continue
		}
		result.Rotated++
	}

	// Count already-current rows
	allRows, _ := s.store.ListCredentialSets(ctx)
	for _, row := range allRows {
		if row.KeyVersion == active.Version {
			result.AlreadyCurrent++
		}
	}

	return result, nil
}

func rowToCredentialSet(row *CredentialSetRow, keySet *EncryptionKeySet) *CredentialSet {
	// Extract provider names by decrypting
	var providers []string
	if plaintext, err := Decrypt(keySet, row.ProvidersEncrypted, []byte(row.Name)); err == nil {
		if config, err := UnmarshalProviders(plaintext); err == nil {
			for name := range config {
				providers = append(providers, name)
			}
		}
	}

	var assignedTo any
	json.Unmarshal(row.AssignedTo, &assignedTo)

	return &CredentialSet{
		ID:             row.ID,
		Name:           row.Name,
		IsDefault:      row.IsDefault,
		Providers:      providers,
		ModelAllowlist: row.ModelAllowlist,
		AssignedTo:     assignedTo,
		KeyVersion:     row.KeyVersion,
	}
}

func providerNames(config ProviderConfig) []string {
	names := make([]string, 0, len(config))
	for name := range config {
		names = append(names, name)
	}
	return names
}
