package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SecretService provides encrypted secret CRUD operations.
type SecretService struct {
	keys  *KeyService
	store SecretStore
}

// NewSecretService creates a new SecretService.
//
// As a side-effect we register the SecretStore on the KeyService so
// RotateKeyWithPassword can re-encrypt secrets in-place (Bug 9 in
// worklog 0085). The two services share a store anyway; this just makes
// the linkage explicit at construction time.
func NewSecretService(keys *KeyService, store SecretStore) *SecretService {
	if keys != nil {
		keys.SetSecretStore(store)
	}
	return &SecretService{keys: keys, store: store}
}

// CreateSecret encrypts and stores a new secret.
func (s *SecretService) CreateSecret(ctx context.Context, userID, sessionID string, req CreateSecretRequest) (*SecretResponse, error) {
	if !ValidSecretTypes[req.Type] {
		return nil, fmt.Errorf("%w: %s (valid: %s)",
			ErrInvalidSecretType, req.Type, formatSecretTypes(ValidSecretTypesList()))
	}

	if err := validateMetadata(req.Type, req.Metadata); err != nil {
		return nil, err
	}

	dek, err := s.keys.GetDEK(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDEKUnavailable, err)
	}

	ciphertext, err := EncryptSecret(dek, []byte(req.Value))
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}

	// Get current key version
	record, err := s.keys.store.GetUserKey(ctx, userID)
	if err != nil || record == nil {
		return nil, errors.New("user key material not found")
	}

	metadata := req.Metadata
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	secret := &UserSecret{
		UserID:     userID,
		Name:       req.Name,
		Type:       req.Type,
		Ciphertext: ciphertext,
		KeyVersion: record.KeyVersion,
		Metadata:   metadata,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.store.CreateSecret(ctx, secret); err != nil {
		return nil, fmt.Errorf("store secret: %w", err)
	}

	s.audit(ctx, userID, "create", &secret.ID, nil, map[string]string{"name": req.Name, "type": string(req.Type)})

	return &SecretResponse{
		ID:        secret.ID,
		Name:      secret.Name,
		Type:      secret.Type,
		Metadata:  secret.Metadata,
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
	}, nil
}

// GetSecretByName returns secret metadata by name (never the value).
func (s *SecretService) GetSecretByName(ctx context.Context, userID, name string) (*SecretResponse, error) {
	secret, err := s.store.GetSecretByName(ctx, userID, name)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	return &SecretResponse{
		ID:        secret.ID,
		Name:      secret.Name,
		Type:      secret.Type,
		Metadata:  secret.Metadata,
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
	}, nil
}

// GetSecret returns secret metadata (never the value).
func (s *SecretService) GetSecret(ctx context.Context, userID, secretID string) (*SecretResponse, error) {
	secret, err := s.store.GetSecret(ctx, userID, secretID)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, ErrSecretNotFound
	}
	return &SecretResponse{
		ID:        secret.ID,
		Name:      secret.Name,
		Type:      secret.Type,
		Metadata:  secret.Metadata,
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
	}, nil
}

// ListSecrets returns all secret metadata for a user (never values).
func (s *SecretService) ListSecrets(ctx context.Context, userID string) ([]*SecretResponse, error) {
	secrets, err := s.store.ListSecrets(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]*SecretResponse, len(secrets))
	for i, sec := range secrets {
		result[i] = &SecretResponse{
			ID:        sec.ID,
			Name:      sec.Name,
			Type:      sec.Type,
			Metadata:  sec.Metadata,
			CreatedAt: sec.CreatedAt,
			UpdatedAt: sec.UpdatedAt,
		}
	}
	return result, nil
}

// UpdateSecret re-encrypts and updates a secret's value.
func (s *SecretService) UpdateSecret(ctx context.Context, userID, sessionID, secretID string, req UpdateSecretRequest) error {
	secret, err := s.store.GetSecret(ctx, userID, secretID)
	if err != nil {
		return err
	}
	if secret == nil {
		return ErrSecretNotFound
	}

	dek, err := s.keys.GetDEK(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDEKUnavailable, err)
	}

	ciphertext, err := EncryptSecret(dek, []byte(req.Value))
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	record, err := s.keys.store.GetUserKey(ctx, userID)
	if err != nil || record == nil {
		return errors.New("user key material not found")
	}

	secret.Ciphertext = ciphertext
	secret.KeyVersion = record.KeyVersion
	secret.UpdatedAt = time.Now()
	if req.Metadata != nil {
		if err := validateMetadata(secret.Type, req.Metadata); err != nil {
			return err
		}
		secret.Metadata = req.Metadata
	}

	if err := s.store.UpdateSecret(ctx, secret); err != nil {
		return fmt.Errorf("update secret: %w", err)
	}

	s.audit(ctx, userID, "update", &secretID, nil, map[string]string{"name": secret.Name})
	return nil
}

// DeleteSecret removes a secret and its bindings.
func (s *SecretService) DeleteSecret(ctx context.Context, userID, secretID string) error {
	secret, err := s.store.GetSecret(ctx, userID, secretID)
	if err != nil {
		return err
	}
	if secret == nil {
		return ErrSecretNotFound
	}

	if err := s.store.DeleteSecret(ctx, userID, secretID); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}

	s.audit(ctx, userID, "delete", &secretID, nil, map[string]string{"name": secret.Name, "type": string(secret.Type)})
	return nil
}

// DecryptSecretValue decrypts a secret's value (used for pod injection).
func (s *SecretService) DecryptSecretValue(ctx context.Context, userID, sessionID, secretID string) ([]byte, error) {
	secret, err := s.store.GetSecret(ctx, userID, secretID)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, ErrSecretNotFound
	}

	dek, err := s.keys.GetDEK(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDEKUnavailable, err)
	}

	plaintext, err := DecryptSecret(dek, secret.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}

	s.audit(ctx, userID, "read", &secretID, nil, map[string]string{"name": secret.Name})
	return plaintext, nil
}

// SetBindings sets which secrets are bound to a workspace.
func (s *SecretService) SetBindings(ctx context.Context, userID, workspaceID string, secretIDs []string) error {
	// Verify all secrets belong to the user
	for _, sid := range secretIDs {
		secret, err := s.store.GetSecret(ctx, userID, sid)
		if err != nil {
			return err
		}
		if secret == nil {
			return fmt.Errorf("%w: %s", ErrSecretNotFound, sid)
		}
	}

	// Get existing bindings to detect removals
	existing, _ := s.store.GetBindings(ctx, workspaceID)
	existingIDs := make(map[string]bool)
	for _, sec := range existing {
		existingIDs[sec.ID] = true
	}

	if err := s.store.SetBindings(ctx, workspaceID, secretIDs); err != nil {
		return fmt.Errorf("set bindings: %w", err)
	}

	// Log unbinds (IDs that were bound but are no longer)
	newIDs := make(map[string]bool)
	for _, sid := range secretIDs {
		newIDs[sid] = true
	}
	for id := range existingIDs {
		if !newIDs[id] {
			sid := id
			s.audit(ctx, userID, "unbind", &sid, &workspaceID, nil)
		}
	}

	for _, sid := range secretIDs {
		if !existingIDs[sid] {
			s.audit(ctx, userID, "bind", &sid, &workspaceID, nil)
		}
	}
	return nil
}

// GetBindings returns secrets bound to a workspace.
func (s *SecretService) GetBindings(ctx context.Context, userID, workspaceID string) (*BindingsResponse, error) {
	secrets, err := s.store.GetBindings(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	bindings := make([]BoundSecret, len(secrets))
	for i, sec := range secrets {
		bindings[i] = BoundSecret{
			SecretID: sec.ID,
			Name:     sec.Name,
			Type:     sec.Type,
		}
	}
	return &BindingsResponse{Bindings: bindings}, nil
}

// GetBindingsForSecret returns workspace IDs that a secret is bound to.
//
// Ownership-failure modes (secret not found, secret owned by someone
// else) are conflated to a uniform empty result so the response shape
// does not leak existence cross-tenant. Genuine system errors (DB
// outage on the lookup) propagate so the handler can return 5xx
// instead of a misleading empty 200.
func (s *SecretService) GetBindingsForSecret(ctx context.Context, userID, secretID string) ([]string, error) {
	secret, err := s.store.GetSecret(ctx, userID, secretID)
	if err != nil {
		return nil, fmt.Errorf("get secret for ownership check: %w", err)
	}
	if secret == nil {
		return nil, nil
	}
	return s.store.GetBindingsForSecret(ctx, secretID)
}

// QueryAudit returns audit log entries for the current user.
func (s *SecretService) QueryAudit(ctx context.Context, userID string, query AuditQuery) ([]*AuditEntry, error) {
	return s.store.QueryAudit(ctx, userID, query)
}

func (s *SecretService) audit(ctx context.Context, userID, action string, secretID, workspaceID *string, meta map[string]string) {
	metaJSON, _ := json.Marshal(meta)
	entry := &AuditEntry{
		UserID:    userID,
		Action:    action,
		Metadata:  metaJSON,
		Timestamp: time.Now(),
	}
	if secretID != nil {
		entry.SecretID = secretID
	}
	if workspaceID != nil {
		entry.WorkspaceID = workspaceID
	}
	// Fire-and-forget audit logging (async in production, sync in tests)
	_ = s.store.LogAudit(ctx, entry)
}

// validateMetadata validates type-specific metadata requirements.
// Errors wrap ErrInvalidMetadata so callers can use errors.Is to map
// any failure to a 400 response (handlers/secrets.go::handleSecretError).
func validateMetadata(secretType SecretType, metadata json.RawMessage) error {
	if metadata == nil || string(metadata) == "{}" || string(metadata) == "null" {
		// Metadata is optional for most types, but required for some
		switch secretType {
		case SecretTypeSSHKey:
			return fmt.Errorf("%w: ssh-key requires metadata with key_type field", ErrInvalidMetadata)
		case SecretTypeSecretFile:
			return fmt.Errorf("%w: secret-file requires metadata with mount_path field", ErrInvalidMetadata)
		case SecretTypeEnvSecret:
			return fmt.Errorf("%w: env-secret requires metadata with var_name field", ErrInvalidMetadata)
		}
		return nil
	}

	var m map[string]interface{}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return fmt.Errorf("%w: invalid metadata JSON: %v", ErrInvalidMetadata, err)
	}

	switch secretType {
	case SecretTypeSSHKey:
		if _, ok := m["key_type"]; !ok {
			return fmt.Errorf("%w: ssh-key metadata requires key_type field", ErrInvalidMetadata)
		}
	case SecretTypeSecretFile:
		mp, ok := m["mount_path"]
		if !ok {
			return fmt.Errorf("%w: secret-file metadata requires mount_path field", ErrInvalidMetadata)
		}
		mpStr, _ := mp.(string)
		if err := validateMountPath(mpStr); err != nil {
			return err
		}
	case SecretTypeEnvSecret:
		if _, ok := m["var_name"]; !ok {
			return fmt.Errorf("%w: env-secret metadata requires var_name field", ErrInvalidMetadata)
		}
	}
	return nil
}

// validateMountPath enforces the same path-traversal rules as the
// in-pod materializer's resolveMountPath, applied at the API layer as
// defence-in-depth (Bug 13 in worklog 0085). Rejects empty paths,
// absolute paths, the bare base directory ("."), and any relative path
// that resolves outside its (notional) base directory after Clean.
//
// All failures wrap ErrInvalidMetadata so callers can map them to a
// 400 response without substring matching.
func validateMountPath(mp string) error {
	if strings.TrimSpace(mp) == "" {
		return fmt.Errorf("%w: mount_path is empty", ErrInvalidMetadata)
	}
	if filepath.IsAbs(mp) {
		return fmt.Errorf("%w: mount_path %q must be relative to the secrets base directory", ErrInvalidMetadata, mp)
	}
	cleaned := filepath.Clean(mp)
	if cleaned == "." {
		return fmt.Errorf("%w: mount_path may not name the base directory itself", ErrInvalidMetadata)
	}
	// Notional base must be deep enough that filepath.Rel can produce a
	// "../" prefix when an input escapes; using "/" loses that signal
	// because filepath.Clean strips leading "..". The concrete base in
	// production is /home/sandbox/.secrets but only the depth matters
	// for this check.
	const base = "/llmsafespace/notional/secrets"
	candidate := filepath.Clean(filepath.Join(base, mp))
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return fmt.Errorf("%w: invalid mount_path %q: %v", ErrInvalidMetadata, mp, err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%w: mount_path %q escapes secrets base directory", ErrInvalidMetadata, mp)
	}
	return nil
}
