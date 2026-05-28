package secrets

import "context"

// SecretStore abstracts database operations for user secrets.
type SecretStore interface {
	CreateSecret(ctx context.Context, secret *UserSecret) error
	GetSecret(ctx context.Context, userID, secretID string) (*UserSecret, error)
	GetSecretByName(ctx context.Context, userID, name string) (*UserSecret, error)
	ListSecrets(ctx context.Context, userID string) ([]*UserSecret, error)
	UpdateSecret(ctx context.Context, secret *UserSecret) error
	DeleteSecret(ctx context.Context, userID, secretID string) error

	// Bindings
	SetBindings(ctx context.Context, workspaceID string, secretIDs []string) error
	GetBindings(ctx context.Context, workspaceID string) ([]*UserSecret, error)
	GetBindingsForSecret(ctx context.Context, secretID string) ([]string, error)

	// Audit
	LogAudit(ctx context.Context, entry *AuditEntry) error
	QueryAudit(ctx context.Context, userID string, query AuditQuery) ([]*AuditEntry, error)
}
