// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import "context"

// SecretStore abstracts database operations for user secrets.
type SecretStore interface {
	CreateSecret(ctx context.Context, secret *UserSecret) error
	GetSecret(ctx context.Context, userID, secretID string) (*UserSecret, error)
	GetSecretByName(ctx context.Context, userID, name string) (*UserSecret, error)
	ListSecrets(ctx context.Context, userID string) ([]*UserSecret, error)
	// ListGlobalDefaultSecrets returns all secrets owned by userID that have
	// global_default=true. Used when seeding bindings on workspace creation.
	ListGlobalDefaultSecrets(ctx context.Context, userID string) ([]*UserSecret, error)
	UpdateSecret(ctx context.Context, secret *UserSecret) error
	DeleteSecret(ctx context.Context, userID, secretID string) error

	// ReEncryptUserSecrets re-encrypts every row owned by userID in a
	// single atomic operation. The transform closure receives the old
	// ciphertext for a row and must return the new ciphertext (decrypted
	// with the old DEK and re-encrypted with the new one). After all
	// rows are re-encrypted but before the transaction commits, the
	// commit closure is invoked with the same tx so the caller can
	// piggyback related updates (e.g. user_keys.wrapped_dek) into the
	// same atomic unit; if commit returns non-nil the entire transaction
	// rolls back. Implementations MUST run all of this in a single
	// SERIALIZABLE transaction with retry on serialization failure.
	//
	// A partial state would leave rows decryptable only by a key the
	// system has discarded — the failure mode of Bug 9 in worklog 0085.
	ReEncryptUserSecrets(
		ctx context.Context,
		userID string,
		newKeyVersion int,
		transform func(oldCiphertext []byte) (newCiphertext []byte, err error),
		commit func(ctx context.Context) error,
	) error

	// Bindings
	SetBindings(ctx context.Context, workspaceID string, secretIDs []string) error
	// AddBindings atomically adds secretIDs to a workspace's binding
	// set without removing any existing bindings. Implementations
	// MUST take the same workspace-scoped advisory lock as
	// SetBindings so concurrent Add+Set callers serialize. Existing
	// bindings to the same secret are silently ignored
	// (INSERT ... ON CONFLICT DO NOTHING semantics).
	//
	// Used by SetWorkspaceEnv to add new env-secrets without racing
	// on a Get-then-Set window — see worklog 0094 pass-2 finding O1.
	AddBindings(ctx context.Context, workspaceID string, secretIDs []string) error
	GetBindings(ctx context.Context, workspaceID string) ([]*UserSecret, error)
	GetBindingsForSecret(ctx context.Context, secretID string) ([]string, error)

	// Audit
	LogAudit(ctx context.Context, entry *AuditEntry) error
	QueryAudit(ctx context.Context, userID string, query AuditQuery) ([]*AuditEntry, error)
}
