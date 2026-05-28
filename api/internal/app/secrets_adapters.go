package app

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// dbKeyStoreAdapter adapts the DatabaseService interface to secrets.KeyStore.
// This is a temporary adapter until the database service exposes user_keys operations directly.
// For now, it stores key material in memory (suitable for single-instance deployments).
// TODO: Add GetUserKey/CreateUserKey/UpdateWrappedDEK to DatabaseService interface.
type dbKeyStoreAdapter struct {
	db      interfaces.DatabaseService
	memKeys map[string]*secrets.UserKeyRecord
}

func (a *dbKeyStoreAdapter) GetUserKey(_ context.Context, userID string) (*secrets.UserKeyRecord, error) {
	if a.memKeys == nil {
		return nil, nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (a *dbKeyStoreAdapter) CreateUserKey(_ context.Context, record *secrets.UserKeyRecord) error {
	if a.memKeys == nil {
		a.memKeys = make(map[string]*secrets.UserKeyRecord)
	}
	cp := *record
	a.memKeys[record.UserID] = &cp
	return nil
}

func (a *dbKeyStoreAdapter) UpdateWrappedDEK(_ context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error {
	if a.memKeys == nil {
		return nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil
	}
	r.WrappedDEK = wrappedDEK
	r.Salt = salt
	r.KeyVersion = keyVersion
	return nil
}

func (a *dbKeyStoreAdapter) UpdateWrappedDEKRecovery(_ context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error {
	if a.memKeys == nil {
		return nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil
	}
	r.WrappedDEKRecovery = wrappedDEKRecovery
	r.RecoverySalt = recoverySalt
	return nil
}

// dbSecretStoreAdapter adapts to secrets.SecretStore using in-memory storage.
// TODO: Replace with PgSecretStore once pgxpool is exposed from database service.
type dbSecretStoreAdapter struct {
	db       interfaces.DatabaseService
	secrets  map[string]*secrets.UserSecret
	bindings map[string][]string
	audit    []*secrets.AuditEntry
}

func (a *dbSecretStoreAdapter) init() {
	if a.secrets == nil {
		a.secrets = make(map[string]*secrets.UserSecret)
		a.bindings = make(map[string][]string)
	}
}

func (a *dbSecretStoreAdapter) CreateSecret(_ context.Context, secret *secrets.UserSecret) error {
	a.init()
	for _, s := range a.secrets {
		if s.UserID == secret.UserID && s.Name == secret.Name {
			return &duplicateErr{secret.Name}
		}
	}
	if secret.ID == "" {
		secret.ID = generateID()
	}
	cp := *secret
	a.secrets[secret.ID] = &cp
	return nil
}

func (a *dbSecretStoreAdapter) GetSecret(_ context.Context, userID, secretID string) (*secrets.UserSecret, error) {
	a.init()
	s, ok := a.secrets[secretID]
	if !ok || s.UserID != userID {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (a *dbSecretStoreAdapter) GetSecretByName(_ context.Context, userID, name string) (*secrets.UserSecret, error) {
	a.init()
	for _, s := range a.secrets {
		if s.UserID == userID && s.Name == name {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

func (a *dbSecretStoreAdapter) ListSecrets(_ context.Context, userID string) ([]*secrets.UserSecret, error) {
	a.init()
	var result []*secrets.UserSecret
	for _, s := range a.secrets {
		if s.UserID == userID {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (a *dbSecretStoreAdapter) UpdateSecret(_ context.Context, secret *secrets.UserSecret) error {
	a.init()
	if _, ok := a.secrets[secret.ID]; !ok {
		return &notFoundErr{secret.ID}
	}
	cp := *secret
	a.secrets[secret.ID] = &cp
	return nil
}

func (a *dbSecretStoreAdapter) DeleteSecret(_ context.Context, userID, secretID string) error {
	a.init()
	s, ok := a.secrets[secretID]
	if !ok || s.UserID != userID {
		return &notFoundErr{secretID}
	}
	delete(a.secrets, secretID)
	for wsID, sids := range a.bindings {
		var filtered []string
		for _, sid := range sids {
			if sid != secretID {
				filtered = append(filtered, sid)
			}
		}
		a.bindings[wsID] = filtered
	}
	return nil
}

func (a *dbSecretStoreAdapter) SetBindings(_ context.Context, workspaceID string, secretIDs []string) error {
	a.init()
	a.bindings[workspaceID] = secretIDs
	return nil
}

func (a *dbSecretStoreAdapter) GetBindings(_ context.Context, workspaceID string) ([]*secrets.UserSecret, error) {
	a.init()
	sids := a.bindings[workspaceID]
	var result []*secrets.UserSecret
	for _, sid := range sids {
		if s, ok := a.secrets[sid]; ok {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (a *dbSecretStoreAdapter) GetBindingsForSecret(_ context.Context, secretID string) ([]string, error) {
	a.init()
	var ws []string
	for wsID, sids := range a.bindings {
		for _, sid := range sids {
			if sid == secretID {
				ws = append(ws, wsID)
			}
		}
	}
	return ws, nil
}

func (a *dbSecretStoreAdapter) LogAudit(_ context.Context, entry *secrets.AuditEntry) error {
	a.audit = append(a.audit, entry)
	return nil
}

func (a *dbSecretStoreAdapter) QueryAudit(_ context.Context, userID string, _ secrets.AuditQuery) ([]*secrets.AuditEntry, error) {
	var result []*secrets.AuditEntry
	for _, e := range a.audit {
		if e.UserID == userID {
			result = append(result, e)
		}
	}
	return result, nil
}

type duplicateErr struct{ name string }

func (e *duplicateErr) Error() string { return "duplicate secret: " + e.name }

type notFoundErr struct{ id string }

func (e *notFoundErr) Error() string { return "not found: " + e.id }

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
