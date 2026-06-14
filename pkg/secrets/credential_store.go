// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
)

// ErrAutoBindingProtected is returned when a caller attempts to Unbind a
// credential that is bound via an auto-apply rule (source_type='auto').
// Auto-bindings are managed by SeedWorkspaceCredentials and can only be
// removed by deleting the underlying credential or the workspace.
var ErrAutoBindingProtected = errors.New("auto-binding cannot be removed via unbind; delete the credential or workspace to remove it")

// CredentialBinding is a joined row from workspace_credential_bindings + provider_credentials.
type CredentialBinding struct {
	ID                 string
	OwnerType          string
	OwnerID            string
	Provider           string
	Ciphertext         []byte
	KeyVersion         int
	ModelAllowlist     []string
	ModelContextLimits map[string]int // model_id → context window size in tokens
	SourceType         string         // "explicit" or "auto"
	WithinPriority     int
}

// CredentialBindingInfo is a minimal binding row used for the ListBindings API.
// It carries the workspace ID and the source type so the UI can distinguish
// auto-seeded bindings (which cannot be manually unbound) from explicit ones.
type CredentialBindingInfo struct {
	WorkspaceID string `json:"workspaceId"`
	SourceType  string `json:"sourceType"` // "explicit" or "auto"
}

// CredentialStore abstracts database operations for provider credentials.
type CredentialStore interface {
	// GetWorkspaceCredentials returns all credential bindings for a workspace,
	// ordered by: (source_type='explicit') DESC, within_priority DESC, created_at ASC.
	GetWorkspaceCredentials(ctx context.Context, workspaceID string) ([]CredentialBinding, error)

	// UpsertFreeTierCredential atomically upserts the platform free-tier
	// credential row and its auto-apply rule in a single transaction.
	UpsertFreeTierCredential(ctx context.Context, ciphertext []byte) error

	// SeedWorkspaceCredentials inserts credential bindings for a new workspace:
	// admin auto-apply rules (all, user target types), user-owned credentials,
	// and org auto-apply rules + org credentials when orgID is non-nil.
	SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string, orgID *string) error

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
