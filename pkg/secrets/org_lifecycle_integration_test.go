// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration
// +build integration

package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cleanupOrg(t *testing.T, pool *pgxpool.Pool, orgID string) {
	t.Helper()
	ctx := context.Background()
	pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE credential_id IN (SELECT id FROM provider_credentials WHERE owner_type='org' AND owner_id=$1)", orgID)
	pool.Exec(ctx, "DELETE FROM credential_auto_apply WHERE target_type='org' AND target_id=$1", orgID)
	pool.Exec(ctx, "DELETE FROM provider_credentials WHERE owner_type='org' AND owner_id=$1", orgID)
	pool.Exec(ctx, "DELETE FROM org_key_members WHERE org_id=$1", orgID)
	pool.Exec(ctx, "DELETE FROM org_memberships WHERE org_id=$1", orgID)
	pool.Exec(ctx, "DELETE FROM workspaces WHERE org_id=$1", orgID)
	pool.Exec(ctx, "DELETE FROM organizations WHERE id=$1", orgID)
}

func cleanupOrgUser(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	ctx := context.Background()
	cleanupUserKeys(t, pool, userID)
	pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE credential_id IN (SELECT id FROM provider_credentials WHERE owner_type='user' AND owner_id=$1)", userID)
	pool.Exec(ctx, "DELETE FROM provider_credentials WHERE owner_type='user' AND owner_id=$1", userID)
	pool.Exec(ctx, "DELETE FROM user_secrets WHERE user_id=$1", userID)
	pool.Exec(ctx, "DELETE FROM users WHERE id=$1", userID)
}

func createTestOrg(t *testing.T, pool *pgxpool.Pool, orgID, adminUserID string) {
	t.Helper()
	ctx := context.Background()
	pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, created_by) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		orgID, "Test Org "+orgID[:8], "test-"+orgID[:8], adminUserID)
	pool.Exec(ctx, `INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap) VALUES ($1, $2, 'admin', false) ON CONFLICT DO NOTHING`,
		orgID, adminUserID)
}

func ensureTestOrgWorkspace(t *testing.T, pool *pgxpool.Pool, wsID, userID, orgID string) {
	t.Helper()
	ctx := context.Background()
	pool.Exec(ctx, `INSERT INTO workspaces (id, name, user_id, runtime, storage_size, org_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'base', '5Gi', $4, NOW(), NOW()) ON CONFLICT DO NOTHING`,
		wsID, "test-ws-"+wsID[:8], userID, orgID)
}

func ensureTestUserWithKeys(t *testing.T, pool *pgxpool.Pool, userID string) []byte {
	t.Helper()
	ensureTestUser(t, pool, userID)
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	err = NewPgKeyStore(pool).CreateUserKey(context.Background(), &UserKeyRecord{
		UserID:     userID,
		KeyVersion: 1,
		WrappedDEK: []byte("dummy-wrapped-dek"),
		Salt:       salt,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateUserKey(%s): %v", userID, err)
	}
	return salt
}

func setupOrgTestEnv(t *testing.T) (*pgxpool.Pool, *PgOrgKeyStore, *PgSecretStore, *OrgKeyService, string, []byte) {
	t.Helper()
	pool := getTestPool(t)

	orgStore := NewPgOrgKeyStore(pool)
	secretStore := NewPgSecretStore(pool)

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	cache := NewRedisDEKCache(rdb)
	svc := NewOrgKeyService(orgStore, cache)
	svc.SetCredentialStore(secretStore)

	adminID := "org-test-admin-1"
	salt := ensureTestUserWithKeys(t, pool, adminID)

	return pool, orgStore, secretStore, svc, adminID, salt
}

func getUserSalt(t *testing.T, pool *pgxpool.Pool, userID string) []byte {
	t.Helper()
	var salt []byte
	err := pool.QueryRow(context.Background(), `SELECT salt FROM user_keys WHERE user_id = $1`, userID).Scan(&salt)
	if err != nil {
		t.Fatalf("getUserSalt(%s): %v", userID, err)
	}
	return salt
}

func TestPgOrgKeyStore_CRUD(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgOrgKeyStore(pool)
	ctx := context.Background()

	adminID := "org-crud-admin"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000001"
	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	record := &OrgKeyMemberRecord{
		OrgID:      orgID,
		UserID:     adminID,
		WrappedDEK: []byte("test-wrapped-dek-32-bytes-padding!!"),
		KeyVersion: 1,
	}

	err := store.UpsertOrgKeyMember(ctx, record)
	if err != nil {
		t.Fatalf("UpsertOrgKeyMember: %v", err)
	}

	got, err := store.GetOrgKeyMember(ctx, orgID, adminID)
	if err != nil {
		t.Fatalf("GetOrgKeyMember: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil record")
	}
	if got.KeyVersion != 1 {
		t.Errorf("KeyVersion: got %d, want 1", got.KeyVersion)
	}
	if string(got.WrappedDEK) != "test-wrapped-dek-32-bytes-padding!!" {
		t.Error("WrappedDEK mismatch")
	}

	got.KeyVersion = 2
	got.WrappedDEK = []byte("updated-wrapped-dek-32-bytes!!!!!")
	err = store.UpsertOrgKeyMember(ctx, got)
	if err != nil {
		t.Fatalf("UpsertOrgKeyMember (update): %v", err)
	}

	got2, _ := store.GetOrgKeyMember(ctx, orgID, adminID)
	if got2.KeyVersion != 2 {
		t.Errorf("KeyVersion after upsert: got %d, want 2", got2.KeyVersion)
	}

	members, err := store.ListOrgKeyMembers(ctx, orgID)
	if err != nil {
		t.Fatalf("ListOrgKeyMembers: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}

	err = store.DeleteOrgKeyMember(ctx, orgID, adminID)
	if err != nil {
		t.Fatalf("DeleteOrgKeyMember: %v", err)
	}

	got3, _ := store.GetOrgKeyMember(ctx, orgID, adminID)
	if got3 != nil {
		t.Error("expected nil after delete")
	}
}

func TestPgOrgKeyStore_GetOrgKeyMembersForUser(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgOrgKeyStore(pool)
	ctx := context.Background()

	adminID := "org-multi-admin"
	org1 := "aaaaaaaa-bbbb-cccc-dddd-000000000010"
	org2 := "aaaaaaaa-bbbb-cccc-dddd-000000000011"
	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, org1, adminID)
	createTestOrg(t, pool, org2, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, org1); cleanupOrg(t, pool, org2) })

	store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{OrgID: org1, UserID: adminID, WrappedDEK: []byte("dek1"), KeyVersion: 1})
	store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{OrgID: org2, UserID: adminID, WrappedDEK: []byte("dek2"), KeyVersion: 1})

	records, err := store.GetOrgKeyMembersForUser(ctx, adminID)
	if err != nil {
		t.Fatalf("GetOrgKeyMembersForUser: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	orgs := map[string]bool{}
	for _, r := range records {
		orgs[r.OrgID] = true
	}
	if !orgs[org1] || !orgs[org2] {
		t.Errorf("expected both orgs, got %v", orgs)
	}
}

func TestOrgCredentialStore_CRUD(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	adminID := "org-cred-admin"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000020"
	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	credID, err := store.CreateOrgCredential(ctx, orgID, "shared-anthropic", "anthropic", []byte("encrypted-api-key"), nil)
	if err != nil {
		t.Fatalf("CreateOrgCredential: %v", err)
	}
	if credID == "" {
		t.Fatal("expected non-empty credential ID")
	}

	creds, err := store.ListOrgCredentials(ctx, orgID)
	if err != nil {
		t.Fatalf("ListOrgCredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	if creds[0].Name != "shared-anthropic" {
		t.Errorf("Name: got %q", creds[0].Name)
	}
	if creds[0].Provider != "anthropic" {
		t.Errorf("Provider: got %q", creds[0].Provider)
	}

	row, err := store.GetOrgCredential(ctx, orgID, credID)
	if err != nil {
		t.Fatalf("GetOrgCredential: %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row")
	}
	if string(row.Ciphertext) != "encrypted-api-key" {
		t.Error("Ciphertext mismatch")
	}

	err = store.UpdateOrgCredential(ctx, orgID, credID, nil, []byte("updated-key"), nil, 2)
	if err != nil {
		t.Fatalf("UpdateOrgCredential: %v", err)
	}

	row2, _ := store.GetOrgCredential(ctx, orgID, credID)
	if string(row2.Ciphertext) != "updated-key" {
		t.Error("Ciphertext not updated")
	}
	if row2.KeyVersion != 2 {
		t.Errorf("KeyVersion: got %d, want 2", row2.KeyVersion)
	}

	err = store.DeleteOrgCredential(ctx, orgID, credID)
	if err != nil {
		t.Fatalf("DeleteOrgCredential: %v", err)
	}

	row3, _ := store.GetOrgCredential(ctx, orgID, credID)
	if row3 != nil {
		t.Error("expected nil after delete")
	}
}

func TestOrgCredentialStore_AutoApply(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	adminID := "org-aa-admin"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000030"
	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	credID, _ := store.CreateOrgCredential(ctx, orgID, "shared-openai", "openai", []byte("cipher"), nil)

	err := store.CreateOrgAutoApply(ctx, orgID, credID, 15)
	if err != nil {
		t.Fatalf("CreateOrgAutoApply: %v", err)
	}

	rules, err := store.ListOrgAutoApply(ctx, orgID)
	if err != nil {
		t.Fatalf("ListOrgAutoApply: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].CredentialID != credID {
		t.Error("CredentialID mismatch")
	}
	if rules[0].TargetType != "org" {
		t.Errorf("TargetType: got %q", rules[0].TargetType)
	}
	if rules[0].Priority != 15 {
		t.Errorf("Priority: got %d, want 15", rules[0].Priority)
	}

	err = store.DeleteOrgAutoApply(ctx, credID, orgID)
	if err != nil {
		t.Fatalf("DeleteOrgAutoApply: %v", err)
	}

	rules2, _ := store.ListOrgAutoApply(ctx, orgID)
	if len(rules2) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules2))
	}
}

func TestBindCredentialToAllOrgWorkspaces(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	adminID := "org-bind-admin"
	memberID := "org-bind-member"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000040"
	wsID1 := "00000000-0000-0000-0000-000000000041"
	wsID2 := "00000000-0000-0000-0000-000000000042"

	ensureTestUser(t, pool, adminID)
	ensureTestUser(t, pool, memberID)
	createTestOrg(t, pool, orgID, adminID)
	ensureTestWorkspace(t, pool, wsID1, adminID)
	ensureTestWorkspace(t, pool, wsID2, memberID)
	t.Cleanup(func() {
		cleanupOrg(t, pool, orgID)
		pool.Exec(ctx, "DELETE FROM workspaces WHERE id IN ($1, $2)", wsID1, wsID2)
		pool.Exec(ctx, "DELETE FROM users WHERE id IN ($1, $2)", adminID, memberID)
	})

	pool.Exec(ctx, "UPDATE workspaces SET org_id = $1 WHERE id = $2", orgID, wsID1)
	pool.Exec(ctx, "UPDATE workspaces SET org_id = $1 WHERE id = $2", orgID, wsID2)

	credID, _ := store.CreateOrgCredential(ctx, orgID, "shared-anthropic", "anthropic", []byte("cipher"), nil)

	err := store.BindCredentialToAllOrgWorkspaces(ctx, credID, orgID)
	if err != nil {
		t.Fatalf("BindCredentialToAllOrgWorkspaces: %v", err)
	}

	var count int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_credential_bindings WHERE credential_id = $1`, credID).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 bindings, got %d", count)
	}
}

func TestOrgLifecycle_FullFlow(t *testing.T) {
	pool, orgStore, secretStore, svc, adminID, _ := setupOrgTestEnv(t)
	defer pool.Close()
	ctx := context.Background()
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000050"

	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	adminPass := []byte("admin-secret-password")
	adminSalt := getUserSalt(t, pool, adminID)

	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, adminID, adminPass)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}
	if len(orgDEK) != dekSize {
		t.Fatalf("orgDEK expected %d bytes, got %d", dekSize, len(orgDEK))
	}

	rec, err := orgStore.GetOrgKeyMember(ctx, orgID, adminID)
	if err != nil || rec == nil {
		t.Fatalf("GetOrgKeyMember: record=%v err=%v", rec, err)
	}

	err = svc.UnlockOrgDEK(ctx, rec, adminSalt, adminPass, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK: %v", err)
	}

	gotDEK, err := svc.GetOrgDEK(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrgDEK: %v", err)
	}
	if string(gotDEK) != string(orgDEK) {
		t.Error("GetOrgDEK returned wrong DEK")
	}

	newAdminID := "org-test-admin-2"
	newAdminSalt := ensureTestUserWithKeys(t, pool, newAdminID)
	t.Cleanup(func() { cleanupOrgUser(t, pool, newAdminID) })

	newAdminPass := []byte("new-admin-passphrase")
	err = svc.WrapOrgDEKForNewAdmin(ctx, orgID, newAdminID, newAdminPass)
	if err != nil {
		t.Fatalf("WrapOrgDEKForNewAdmin: %v", err)
	}

	newRec, err := orgStore.GetOrgKeyMember(ctx, orgID, newAdminID)
	if err != nil || newRec == nil {
		t.Fatalf("GetOrgKeyMember (new admin): record=%v err=%v", newRec, err)
	}

	err = svc.UnlockOrgDEK(ctx, newRec, newAdminSalt, newAdminPass, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK (new admin): %v", err)
	}

	gotDEK2, err := svc.GetOrgDEK(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrgDEK after new admin unlock: %v", err)
	}
	if string(gotDEK2) != string(orgDEK) {
		t.Error("new admin got different DEK")
	}

	plainAPIKey := []byte("sk-ant-api03-secret-key-value")
	ciphertext, err := EncryptSecret(orgDEK, plainAPIKey)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	credID, err := secretStore.CreateOrgCredential(ctx, orgID, "shared-anthropic", "anthropic", ciphertext, nil)
	if err != nil {
		t.Fatalf("CreateOrgCredential: %v", err)
	}

	credRow, err := secretStore.GetOrgCredential(ctx, orgID, credID)
	if err != nil {
		t.Fatalf("GetOrgCredential: %v", err)
	}
	decrypted, err := DecryptSecret(orgDEK, credRow.Ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if string(decrypted) != string(plainAPIKey) {
		t.Errorf("decrypted credential mismatch: got %q", decrypted)
	}

	newPass := []byte("updated-password-123")
	err = svc.RewrapOrgDEKForAdmin(ctx, orgID, adminID, newPass)
	if err != nil {
		t.Fatalf("RewrapOrgDEKForAdmin: %v", err)
	}

	rewrappedRec, _ := orgStore.GetOrgKeyMember(ctx, orgID, adminID)
	err = svc.UnlockOrgDEK(ctx, rewrappedRec, adminSalt, newPass, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK after rewrap: %v", err)
	}

	reencrypted, err := svc.RotateOrgDEK(ctx, orgID, adminID, newPass)
	if err != nil {
		t.Fatalf("RotateOrgDEK: %v", err)
	}
	if reencrypted != 1 {
		t.Errorf("expected 1 credential re-encrypted, got %d", reencrypted)
	}

	rotatedCred, _ := secretStore.GetOrgCredential(ctx, orgID, credID)
	newOrgDEK, _ := svc.GetOrgDEK(ctx, orgID)
	decrypted2, err := DecryptSecret(newOrgDEK, rotatedCred.Ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret after rotation: %v", err)
	}
	if string(decrypted2) != string(plainAPIKey) {
		t.Error("credential plaintext changed after rotation")
	}

	_, err = DecryptSecret(orgDEK, rotatedCred.Ciphertext)
	if err == nil {
		t.Error("old DEK should NOT decrypt new ciphertext")
	}

	newAdminRec, _ := orgStore.GetOrgKeyMember(ctx, orgID, newAdminID)
	if newAdminRec != nil {
		t.Error("new admin key member should have been deleted during rotation")
	}

	adminRec, _ := orgStore.GetOrgKeyMember(ctx, orgID, adminID)
	if adminRec == nil {
		t.Fatal("rotating admin key member should still exist")
	}
	if adminRec.KeyVersion != 1 {
		t.Errorf("KeyVersion after rotation: got %d, want 1", adminRec.KeyVersion)
	}
}

func TestSeedWorkspaceCredentials_OrgVsPersonal(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	adminID := "seed-org-admin"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000060"
	personalWS := "00000000-0000-0000-0000-000000000061"
	orgWS := "00000000-0000-0000-0000-000000000062"
	otherOrgID := "aaaaaaaa-bbbb-cccc-dddd-000000000063"
	otherOrgWS := "00000000-0000-0000-0000-000000000064"

	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, orgID, adminID)
	createTestOrg(t, pool, otherOrgID, adminID)
	ensureTestWorkspace(t, pool, personalWS, adminID)
	ensureTestWorkspace(t, pool, orgWS, adminID)
	ensureTestWorkspace(t, pool, otherOrgWS, adminID)
	pool.Exec(ctx, "UPDATE workspaces SET org_id = $1 WHERE id = $2", orgID, orgWS)
	pool.Exec(ctx, "UPDATE workspaces SET org_id = $1 WHERE id = $2", otherOrgID, otherOrgWS)

	t.Cleanup(func() {
		cleanupOrg(t, pool, orgID)
		cleanupOrg(t, pool, otherOrgID)
		pool.Exec(ctx, "DELETE FROM workspace_credential_bindings WHERE workspace_id IN ($1, $2, $3)", personalWS, orgWS, otherOrgWS)
		pool.Exec(ctx, "DELETE FROM workspaces WHERE id IN ($1, $2, $3)", personalWS, orgWS, otherOrgWS)
		pool.Exec(ctx, "DELETE FROM users WHERE id = $1", adminID)
	})

	orgCredID, _ := store.CreateOrgCredential(ctx, orgID, "org-anthropic", "anthropic", []byte("org-cipher"), nil)
	store.CreateOrgAutoApply(ctx, orgID, orgCredID, 5)

	otherOrgCredID, _ := store.CreateOrgCredential(ctx, otherOrgID, "other-org-anthropic", "anthropic", []byte("other-cipher"), nil)
	store.CreateOrgAutoApply(ctx, otherOrgID, otherOrgCredID, 5)

	err := store.SeedWorkspaceCredentials(ctx, personalWS, adminID, nil)
	if err != nil {
		t.Fatalf("SeedWorkspaceCredentials (personal): %v", err)
	}

	var personalCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_credential_bindings wcb
		JOIN provider_credentials pc ON pc.id = wcb.credential_id
		WHERE wcb.workspace_id = $1 AND pc.owner_type = 'org'`, personalWS).Scan(&personalCount)
	if personalCount != 0 {
		t.Errorf("personal workspace should have 0 org bindings, got %d", personalCount)
	}

	err = store.SeedWorkspaceCredentials(ctx, orgWS, adminID, &orgID)
	if err != nil {
		t.Fatalf("SeedWorkspaceCredentials (org): %v", err)
	}

	var orgBindCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_credential_bindings WHERE workspace_id = $1 AND credential_id = $2`, orgWS, orgCredID).Scan(&orgBindCount)
	if orgBindCount != 1 {
		t.Errorf("org workspace should have org credential binding, got %d", orgBindCount)
	}

	err = store.SeedWorkspaceCredentials(ctx, otherOrgWS, adminID, &otherOrgID)
	if err != nil {
		t.Fatalf("SeedWorkspaceCredentials (other org): %v", err)
	}

	var crossCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_credential_bindings WHERE workspace_id = $1 AND credential_id = $2`, otherOrgWS, orgCredID).Scan(&crossCount)
	if crossCount != 0 {
		t.Errorf("other org workspace should NOT have first org's credential, got %d", crossCount)
	}
}

func TestReEncryptOrgCredentials(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	store := NewPgSecretStore(pool)
	ctx := context.Background()

	adminID := "reenc-org-admin"
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000070"
	ensureTestUser(t, pool, adminID)
	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	oldDEK, _ := GenerateDEK()
	newDEK, _ := GenerateDEK()

	plain1 := []byte("sk-first-api-key")
	plain2 := []byte("sk-second-api-key")
	cipher1, _ := EncryptSecret(oldDEK, plain1)
	cipher2, _ := EncryptSecret(oldDEK, plain2)

	_, err := store.CreateOrgCredential(ctx, orgID, "cred-1", "anthropic", cipher1, nil)
	if err != nil {
		t.Fatalf("CreateOrgCredential 1: %v", err)
	}
	_, err = store.CreateOrgCredential(ctx, orgID, "cred-2", "openai", cipher2, nil)
	if err != nil {
		t.Fatalf("CreateOrgCredential 2: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	count, err := store.ReEncryptOrgCredentials(ctx, tx, orgID, oldDEK, newDEK)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("ReEncryptOrgCredentials: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 re-encrypted, got %d", count)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	creds, _ := store.ListOrgCredentials(ctx, orgID)
	for _, c := range creds {
		row, _ := store.GetOrgCredential(ctx, orgID, c.ID)
		dec, err := DecryptSecret(newDEK, row.Ciphertext)
		if err != nil {
			t.Errorf("DecryptSecret with new DEK failed for %s: %v", c.Name, err)
			continue
		}
		if c.Name == "cred-1" && string(dec) != string(plain1) {
			t.Errorf("cred-1 plaintext mismatch")
		}
		if c.Name == "cred-2" && string(dec) != string(plain2) {
			t.Errorf("cred-2 plaintext mismatch")
		}

		_, err = DecryptSecret(oldDEK, row.Ciphertext)
		if err == nil {
			t.Errorf("old DEK should not decrypt %s after re-encryption", c.Name)
		}
	}
}

func TestRotateOrgDEK_DeletesOtherAdminKeys(t *testing.T) {
	pool, orgStore, secretStore, svc, adminID, _ := setupOrgTestEnv(t)
	defer pool.Close()
	ctx := context.Background()
	orgID := "aaaaaaaa-bbbb-cccc-dddd-000000000080"

	createTestOrg(t, pool, orgID, adminID)
	t.Cleanup(func() { cleanupOrg(t, pool, orgID) })

	adminPass := []byte("rotation-admin-pass")
	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, adminID, adminPass)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}

	adminSalt := getUserSalt(t, pool, adminID)
	adminRec, _ := orgStore.GetOrgKeyMember(ctx, orgID, adminID)
	err = svc.UnlockOrgDEK(ctx, adminRec, adminSalt, adminPass, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK: %v", err)
	}

	otherAdminID := "org-test-admin-other"
	ensureTestUserWithKeys(t, pool, otherAdminID)
	t.Cleanup(func() { cleanupOrgUser(t, pool, otherAdminID) })

	otherAdminPass := []byte("other-admin-pass")
	err = svc.WrapOrgDEKForNewAdmin(ctx, orgID, otherAdminID, otherAdminPass)
	if err != nil {
		t.Fatalf("WrapOrgDEKForNewAdmin: %v", err)
	}

	plainKey := []byte("sk-rotation-test-key")
	cipher, _ := EncryptSecret(orgDEK, plainKey)
	secretStore.CreateOrgCredential(ctx, orgID, "rot-cred", "anthropic", cipher, nil)

	reencrypted, err := svc.RotateOrgDEK(ctx, orgID, adminID, adminPass)
	if err != nil {
		t.Fatalf("RotateOrgDEK: %v", err)
	}
	if reencrypted != 1 {
		t.Errorf("expected 1 re-encrypted, got %d", reencrypted)
	}

	otherRec, _ := orgStore.GetOrgKeyMember(ctx, orgID, otherAdminID)
	if otherRec != nil {
		t.Error("other admin's key member should be deleted after rotation")
	}

	adminRec2, _ := orgStore.GetOrgKeyMember(ctx, orgID, adminID)
	if adminRec2 == nil {
		t.Fatal("rotating admin's key member should still exist")
	}
	if adminRec2.KeyVersion != 1 {
		t.Errorf("KeyVersion after rotation: got %d, want 1", adminRec2.KeyVersion)
	}
}
