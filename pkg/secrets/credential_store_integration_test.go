// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration
// +build integration

package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func cleanupProviderCredentials(t *testing.T, store *PgSecretStore, ownerType, ownerID string) {
	t.Helper()
	ctx := context.Background()
	store.pool.Exec(ctx, "DELETE FROM credential_backfill_jobs WHERE credential_id IN (SELECT id FROM provider_credentials WHERE owner_type = $1 AND owner_id = $2)", ownerType, ownerID)
	store.pool.Exec(ctx, "DELETE FROM credential_auto_apply WHERE credential_id IN (SELECT id FROM provider_credentials WHERE owner_type = $1 AND owner_id = $2)", ownerType, ownerID)
	store.pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE credential_id IN (SELECT id FROM provider_credentials WHERE owner_type = $1 AND owner_id = $2)", ownerType, ownerID)
	store.pool.Exec(ctx, "DELETE FROM provider_credentials WHERE owner_type = $1 AND owner_id = $2", ownerType, ownerID)
}

func TestPgCredentialStore_UpsertFreeTierCredential(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	defer cleanupProviderCredentials(t, store, "admin", "_platform")

	ciphertext := []byte("encrypted-free-tier-key")

	// First call: creates the row.
	err := store.UpsertFreeTierCredential(ctx, ciphertext)
	if err != nil {
		t.Fatalf("UpsertFreeTierCredential (first call): %v", err)
	}

	// Verify provider_credentials row exists.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM provider_credentials WHERE owner_type='admin' AND owner_id='_platform' AND provider='opencode'`).Scan(&count)
	if err != nil {
		t.Fatalf("query provider_credentials: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 provider_credentials row, got %d", count)
	}

	// Verify credential_auto_apply row exists.
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM credential_auto_apply WHERE target_type='all' AND target_id IS NULL`).Scan(&count)
	if err != nil {
		t.Fatalf("query credential_auto_apply: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 credential_auto_apply row, got %d", count)
	}

	// Second call (idempotent): updates ciphertext.
	newCiphertext := []byte("updated-encrypted-free-tier-key")
	err = store.UpsertFreeTierCredential(ctx, newCiphertext)
	if err != nil {
		t.Fatalf("UpsertFreeTierCredential (second call): %v", err)
	}

	// Still only 1 row.
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM provider_credentials WHERE owner_type='admin' AND owner_id='_platform' AND provider='opencode'`).Scan(&count)
	if err != nil {
		t.Fatalf("query after upsert: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", count)
	}

	// Verify ciphertext was updated.
	var stored []byte
	err = pool.QueryRow(ctx,
		`SELECT ciphertext FROM provider_credentials WHERE owner_type='admin' AND owner_id='_platform' AND provider='opencode'`).Scan(&stored)
	if err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if string(stored) != string(newCiphertext) {
		t.Fatalf("ciphertext not updated: got %q, want %q", stored, newCiphertext)
	}
}

func TestPgCredentialStore_SeedWorkspaceCredentials(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	userID := "cred-seed-user-1"
	wsID := "00000000-0000-0000-0000-000000000001"
	ensureTestUser(t, pool, userID)
	ensureTestWorkspace(t, pool, wsID, userID)
	defer cleanupProviderCredentials(t, store, "admin", "_platform")
	defer pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE workspace_id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	// Seed the free-tier credential first.
	err := store.UpsertFreeTierCredential(ctx, []byte("cipher"))
	if err != nil {
		t.Fatalf("UpsertFreeTierCredential: %v", err)
	}

	// Now seed workspace credentials.
	err = store.SeedWorkspaceCredentials(ctx, wsID, userID, nil)
	if err != nil {
		t.Fatalf("SeedWorkspaceCredentials: %v", err)
	}

	// Verify binding created with source_type='auto'.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM workspace_credential_bindings WHERE workspace_id = $1 AND source_type = 'auto'`, wsID).Scan(&count)
	if err != nil {
		t.Fatalf("query bindings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 auto binding, got %d", count)
	}

	// Idempotent: calling again should not fail or duplicate.
	err = store.SeedWorkspaceCredentials(ctx, wsID, userID, nil)
	if err != nil {
		t.Fatalf("SeedWorkspaceCredentials (idempotent): %v", err)
	}
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM workspace_credential_bindings WHERE workspace_id = $1`, wsID).Scan(&count)
	if err != nil {
		t.Fatalf("query after re-seed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 binding after re-seed, got %d", count)
	}
}

func TestPgCredentialStore_GetWorkspaceCredentials(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	userID := "cred-get-user-1"
	wsID := "00000000-0000-0000-0000-000000000002"
	ensureTestUser(t, pool, userID)
	ensureTestWorkspace(t, pool, wsID, userID)
	defer cleanupProviderCredentials(t, store, "admin", "_platform")
	defer cleanupProviderCredentials(t, store, "user", userID)
	defer pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE workspace_id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	// Create admin credential.
	var adminCredID string
	err := pool.QueryRow(ctx,
		`INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		 VALUES ('admin', '_platform', 'admin-anthropic', 'anthropic', $1)
		 RETURNING id`, []byte("admin-cipher")).Scan(&adminCredID)
	if err != nil {
		t.Fatalf("insert admin cred: %v", err)
	}

	// Create user credential for same provider.
	var userCredID string
	err = pool.QueryRow(ctx,
		`INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		 VALUES ('user', $1, 'my-anthropic', 'anthropic', $2)
		 RETURNING id`, userID, []byte("user-cipher")).Scan(&userCredID)
	if err != nil {
		t.Fatalf("insert user cred: %v", err)
	}

	// Bind admin credential as auto (lower priority).
	_, err = pool.Exec(ctx,
		`INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		 VALUES ($1, $2, 'auto', 0)`, adminCredID, wsID)
	if err != nil {
		t.Fatalf("bind admin cred: %v", err)
	}

	// Bind user credential as explicit (higher priority).
	_, err = pool.Exec(ctx,
		`INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		 VALUES ($1, $2, 'explicit', 0)`, userCredID, wsID)
	if err != nil {
		t.Fatalf("bind user cred: %v", err)
	}

	// Get workspace credentials — should return explicit first.
	bindings, err := store.GetWorkspaceCredentials(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceCredentials: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	// First binding should be explicit (user).
	if bindings[0].SourceType != "explicit" {
		t.Errorf("first binding source_type = %q, want 'explicit'", bindings[0].SourceType)
	}
	if bindings[0].OwnerType != "user" {
		t.Errorf("first binding owner_type = %q, want 'user'", bindings[0].OwnerType)
	}
	if bindings[0].Provider != "anthropic" {
		t.Errorf("first binding provider = %q, want 'anthropic'", bindings[0].Provider)
	}

	// Second binding should be auto (admin).
	if bindings[1].SourceType != "auto" {
		t.Errorf("second binding source_type = %q, want 'auto'", bindings[1].SourceType)
	}
	if bindings[1].OwnerType != "admin" {
		t.Errorf("second binding owner_type = %q, want 'admin'", bindings[1].OwnerType)
	}
}

func TestPgCredentialStore_GetWorkspaceCredentials_Empty(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	userID := "cred-empty-user"
	wsID := "00000000-0000-0000-0000-000000000003"
	ensureTestUser(t, pool, userID)
	ensureTestWorkspace(t, pool, wsID, userID)
	defer pool.Exec(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	bindings, err := store.GetWorkspaceCredentials(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceCredentials (empty): %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings, got %d", len(bindings))
	}
}

func TestPgCredentialStore_HasUserProviderCredential(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	userID := "cred-has-user-1"
	ensureTestUser(t, pool, userID)
	defer cleanupProviderCredentials(t, store, "user", userID)
	defer pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	// No credential exists.
	has, err := store.HasUserProviderCredential(ctx, userID, "anthropic")
	if err != nil {
		t.Fatalf("HasUserProviderCredential: %v", err)
	}
	if has {
		t.Fatal("expected false, got true (no credential)")
	}

	// Create one.
	_, err = pool.Exec(ctx,
		`INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		 VALUES ('user', $1, 'my-anthropic', 'anthropic', $2)`, userID, []byte("cipher"))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	has, err = store.HasUserProviderCredential(ctx, userID, "anthropic")
	if err != nil {
		t.Fatalf("HasUserProviderCredential after insert: %v", err)
	}
	if !has {
		t.Fatal("expected true, got false (credential exists)")
	}

	// Different provider: should be false.
	has, err = store.HasUserProviderCredential(ctx, userID, "openai")
	if err != nil {
		t.Fatalf("HasUserProviderCredential (different provider): %v", err)
	}
	if has {
		t.Fatal("expected false for different provider")
	}
}

func TestPgCredentialStore_GetWorkspaceCredentials_PriorityOrder(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	userID := "cred-priority-user"
	wsID := "00000000-0000-0000-0000-000000000004"
	ensureTestUser(t, pool, userID)
	ensureTestWorkspace(t, pool, wsID, userID)
	defer cleanupProviderCredentials(t, store, "admin", "_platform")
	defer pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE workspace_id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM workspaces WHERE id = $1", wsID)
	defer pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	// Create two admin credentials for different providers.
	var cred1ID, cred2ID string
	err := pool.QueryRow(ctx,
		`INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		 VALUES ('admin', '_platform', 'admin-openai', 'openai', $1)
		 RETURNING id`, []byte("cipher1")).Scan(&cred1ID)
	if err != nil {
		t.Fatalf("insert cred1: %v", err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		 VALUES ('admin', '_platform', 'admin-anthropic', 'anthropic', $1)
		 RETURNING id`, []byte("cipher2")).Scan(&cred2ID)
	if err != nil {
		t.Fatalf("insert cred2: %v", err)
	}

	// Bind both as auto with different within_priority.
	_, err = pool.Exec(ctx,
		`INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		 VALUES ($1, $2, 'auto', 10)`, cred1ID, wsID)
	if err != nil {
		t.Fatalf("bind cred1: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		 VALUES ($1, $2, 'auto', 20)`, cred2ID, wsID)
	if err != nil {
		t.Fatalf("bind cred2: %v", err)
	}

	bindings, err := store.GetWorkspaceCredentials(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceCredentials: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	// Higher within_priority should come first.
	if bindings[0].WithinPriority != 20 {
		t.Errorf("first binding within_priority = %d, want 20", bindings[0].WithinPriority)
	}
	if bindings[1].WithinPriority != 10 {
		t.Errorf("second binding within_priority = %d, want 10", bindings[1].WithinPriority)
	}
}

// TestPgCredentialStore_UpdateCredential_NilPreservesLimits verifies that
// UpdateCredential's COALESCE semantics preserve model_context_limits and
// model_allowlist when the update row passes nil for those fields. This is the
// org handler's partial-update contract: nil = "don't change", empty = "clear".
// Regression test for the critical nil→{} normalization bug.
func TestPgCredentialStore_UpdateCredential_NilPreservesLimits(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	defer cleanupProviderCredentials(t, store, "org", "org-test-update-nil")

	// Create a credential with non-empty limits and allowlist.
	credID := uuid.New().String()
	now := time.Now()
	row := &CredentialRow{
		ID:                 credID,
		Name:               "original",
		Provider:           "test-provider-nil",
		Ciphertext:         []byte("encrypted"),
		KeyVersion:         1,
		ModelAllowlist:     []string{"glm-5.1", "gpt-4o"},
		ModelContextLimits: map[string]int{"glm-5.1": 200000, "gpt-4o": 128000},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := store.CreateCredential(ctx, "org", "org-test-update-nil", row); err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}

	// Update with nil limits/allowlist — must NOT overwrite existing values.
	upd := &CredentialRow{
		ID:                 credID,
		Name:               "renamed",
		Provider:           "test-provider-nil",
		Ciphertext:         []byte("encrypted"),
		KeyVersion:         1,
		ModelAllowlist:     nil, // nil = don't change
		ModelContextLimits: nil, // nil = don't change
	}
	if err := store.UpdateCredential(ctx, "org", "org-test-update-nil", credID, upd); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}

	// Read back and verify limits/allowlist are preserved.
	got, err := store.GetCredential(ctx, "org", "org-test-update-nil", credID)
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "renamed")
	}
	if len(got.ModelAllowlist) != 2 || got.ModelAllowlist[0] != "glm-5.1" {
		t.Errorf("ModelAllowlist = %v, want [glm-5.1, gpt-4o] (nil must preserve)", got.ModelAllowlist)
	}
	if got.ModelContextLimits["glm-5.1"] != 200000 || got.ModelContextLimits["gpt-4o"] != 128000 {
		t.Errorf("ModelContextLimits = %v, want {glm-5.1:200000, gpt-4o:128000} (nil must preserve)", got.ModelContextLimits)
	}
}
