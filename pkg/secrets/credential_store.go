// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import "context"

// CredentialBinding is a joined row from workspace_credential_bindings + provider_credentials.
type CredentialBinding struct {
	ID             string
	OwnerType      string
	OwnerID        string
	Provider       string
	Ciphertext     []byte
	KeyVersion     int
	ModelAllowlist []string
	SourceType     string // "explicit" or "auto"
	WithinPriority int
}

// CredentialStore abstracts database operations for provider credentials.
type CredentialStore interface {
	// GetWorkspaceCredentials returns all credential bindings for a workspace,
	// ordered by: (source_type='explicit') DESC, within_priority DESC, created_at ASC.
	GetWorkspaceCredentials(ctx context.Context, workspaceID string) ([]CredentialBinding, error)

	// UpsertFreeTierCredential atomically upserts the platform free-tier
	// credential row and its auto-apply rule in a single transaction.
	UpsertFreeTierCredential(ctx context.Context, ciphertext []byte) error

	// SeedWorkspaceCredentials inserts auto-apply credential bindings for a
	// newly created workspace: admin auto-apply rules AND all user-owned credentials.
	SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string) error

	// BindCredentialToAllUserWorkspaces binds a credential to every workspace
	// owned by userID. Called on credential create to maintain the invariant
	// that all credentials are bound to all of a user's workspaces.
	BindCredentialToAllUserWorkspaces(ctx context.Context, credentialID, userID string) error

	// HasUserProviderCredential returns true if the user owns a credential for the given provider.
	HasUserProviderCredential(ctx context.Context, userID, provider string) (bool, error)
}

// AdminKeyDeriver derives a server-side encryption key for admin credentials.
// The label parameter scopes the derived key (e.g. "provider-credentials").
// Returns nil when LLMSAFESPACE_MASTER_SECRET is not set.
type AdminKeyDeriver func(label string) []byte
