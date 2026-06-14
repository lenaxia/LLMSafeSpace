// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- mock implementations for OrgKeyStore ---

type mockOrgKeyStore struct {
	mu           sync.Mutex
	members      map[string]*OrgKeyMemberRecord // key: orgID+":"+userID
	salts        map[string][]byte              // key: userID
	saltErr      error
	getErr       error
	upsertErr    error
	deleteErr    error
	listErr      error
	deleteAllErr error
}

func newMockOrgKeyStore() *mockOrgKeyStore {
	return &mockOrgKeyStore{
		members: make(map[string]*OrgKeyMemberRecord),
		salts:   make(map[string][]byte),
	}
}

func (m *mockOrgKeyStore) key(orgID, userID string) string { return orgID + ":" + userID }

func (m *mockOrgKeyStore) GetOrgKeyMember(_ context.Context, orgID, userID string) (*OrgKeyMemberRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	r, ok := m.members[m.key(orgID, userID)]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *mockOrgKeyStore) UpsertOrgKeyMember(_ context.Context, record *OrgKeyMemberRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return m.upsertErr
	}
	cp := *record
	cp.WrappedDEK = make([]byte, len(record.WrappedDEK))
	copy(cp.WrappedDEK, record.WrappedDEK)
	m.members[m.key(record.OrgID, record.UserID)] = &cp
	return nil
}

func (m *mockOrgKeyStore) DeleteOrgKeyMember(_ context.Context, orgID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.members, m.key(orgID, userID))
	return nil
}

func (m *mockOrgKeyStore) ListOrgKeyMembers(_ context.Context, orgID string) ([]*OrgKeyMemberRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []*OrgKeyMemberRecord
	for k, v := range m.members {
		if len(k) > len(orgID)+1 && k[:len(orgID)+1] == orgID+":" {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *mockOrgKeyStore) DeleteAllOrgKeyMembers(_ context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteAllErr != nil {
		return m.deleteAllErr
	}
	for k := range m.members {
		if len(k) > len(orgID)+1 && k[:len(orgID)+1] == orgID+":" {
			delete(m.members, k)
		}
	}
	return nil
}

func (m *mockOrgKeyStore) GetOrgKeyMembersForUser(_ context.Context, userID string) ([]*OrgKeyMemberRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	var out []*OrgKeyMemberRecord
	for _, v := range m.members {
		if v.UserID == userID {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *mockOrgKeyStore) GetUserSalt(_ context.Context, userID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saltErr != nil {
		return nil, m.saltErr
	}
	s, ok := m.salts[userID]
	if !ok {
		return nil, ErrUserKeysMissing
	}
	return s, nil
}

func (m *mockOrgKeyStore) BeginTx(_ context.Context) (pgx.Tx, error) {
	return nil, errors.New("BeginTx not implemented in mock")
}

func (m *mockOrgKeyStore) UpsertOrgKeyMemberTx(_ context.Context, _ pgx.Tx, _ *OrgKeyMemberRecord) error {
	return errors.New("UpsertOrgKeyMemberTx not implemented in mock")
}

func (m *mockOrgKeyStore) DeleteAllOrgKeyMembersTx(_ context.Context, _ pgx.Tx, _ string) error {
	return errors.New("DeleteAllOrgKeyMembersTx not implemented in mock")
}

func (m *mockOrgKeyStore) SetPendingKeyWrapForOtherAdminsTx(_ context.Context, _ pgx.Tx, _, _ string) error {
	return errors.New("SetPendingKeyWrapForOtherAdminsTx not implemented in mock")
}

// --- helpers ---

func newOrgKeyTestEnv(t *testing.T) (*mockOrgKeyStore, *mockDEKCache, *OrgKeyService, string, []byte) {
	t.Helper()
	store := newMockOrgKeyStore()
	cache := newMockDEKCache()
	svc := NewOrgKeyService(store, cache)

	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	userID := "admin-user-1"
	store.salts[userID] = salt

	return store, cache, svc, userID, salt
}

// --- Tests ---

func TestInitializeOrgKeys_Success(t *testing.T) {
	store, _, svc, userID, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-1"
	password := []byte("supersecret")

	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, userID, password)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}
	if len(orgDEK) != dekSize {
		t.Errorf("orgDEK expected %d bytes, got %d", dekSize, len(orgDEK))
	}

	// Verify one org_key_members row was stored
	rec, err := store.GetOrgKeyMember(ctx, orgID, userID)
	if err != nil {
		t.Fatalf("GetOrgKeyMember: %v", err)
	}
	if rec == nil {
		t.Fatal("expected org_key_members row to exist")
	}
	if rec.KeyVersion != 1 {
		t.Errorf("expected key_version=1, got %d", rec.KeyVersion)
	}
	if len(rec.WrappedDEK) == 0 {
		t.Error("wrapped_dek should not be empty")
	}
}

func TestInitializeOrgKeys_GetUserSaltError(t *testing.T) {
	store, _, svc, _, _ := newOrgKeyTestEnv(t)
	store.saltErr = errors.New("db down")
	ctx := context.Background()

	_, err := svc.InitializeOrgKeys(ctx, "org-1", "missing-user", []byte("pass"))
	if err == nil {
		t.Fatal("expected error when GetUserSalt fails")
	}
}

func TestUnlockOrgDEK_Success(t *testing.T) {
	store, cache, svc, userID, salt := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-unlock-1"
	password := []byte("mypassword")

	// Initialize first
	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, userID, password)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}

	// Clear the in-memory orgDEK and unlock from store
	rec, err := store.GetOrgKeyMember(ctx, orgID, userID)
	if err != nil || rec == nil {
		t.Fatalf("GetOrgKeyMember: %v %v", rec, err)
	}

	err = svc.UnlockOrgDEK(ctx, rec, salt, password, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK: %v", err)
	}

	cached, err := cache.GetDEK(ctx, OrgCacheKey(orgID))
	if err != nil || cached == nil {
		t.Fatal("expected org DEK in cache after unlock")
	}

	// Cached DEK should match original
	if string(cached) != string(orgDEK) {
		t.Error("cached DEK does not match original org DEK")
	}
}

func TestUnlockOrgDEK_WrongPassword(t *testing.T) {
	store, cache, svc, userID, salt := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-wrong-pw"
	password := []byte("correctpassword")

	_, err := svc.InitializeOrgKeys(ctx, orgID, userID, password)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}

	rec, _ := store.GetOrgKeyMember(ctx, orgID, userID)

	err = svc.UnlockOrgDEK(ctx, rec, salt, []byte("wrongpassword"), time.Hour)
	if err == nil {
		t.Fatal("expected error with wrong password")
	}

	// Cache should be empty
	cached, _ := cache.GetDEK(ctx, OrgCacheKey(orgID))
	if cached != nil {
		t.Error("cache should be empty after failed unlock")
	}
}

func TestUnlockOrgDEK_NilRecord_NoOp(t *testing.T) {
	_, _, svc, _, salt := newOrgKeyTestEnv(t)
	ctx := context.Background()

	// nil record = user has no key for this org; should return nil
	err := svc.UnlockOrgDEK(ctx, nil, salt, []byte("pass"), time.Hour)
	if err != nil {
		t.Errorf("expected nil error for nil record, got: %v", err)
	}
}

func TestUnlockAllOrgDEKs_BatchQuery(t *testing.T) {
	store, cache, svc, userID, salt := newOrgKeyTestEnv(t)
	_ = store
	ctx := context.Background()
	password := []byte("batchpw")

	// Initialize 3 orgs
	for _, orgID := range []string{"org-a", "org-b", "org-c"} {
		_, err := svc.InitializeOrgKeys(ctx, orgID, userID, password)
		if err != nil {
			t.Fatalf("InitializeOrgKeys(%s): %v", orgID, err)
		}
	}

	// UnlockAllOrgDEKs should cache all 3 org DEKs
	// It uses GetOrgKeyMembersForUser internally
	_ = salt // salt stored in mock store, GetUserSalt will return it
	err := svc.UnlockAllOrgDEKs(ctx, userID, nil, password, time.Hour)
	if err != nil {
		t.Fatalf("UnlockAllOrgDEKs: %v", err)
	}

	for _, orgID := range []string{"org-a", "org-b", "org-c"} {
		dek, err := cache.GetDEK(ctx, OrgCacheKey(orgID))
		if err != nil || dek == nil {
			t.Errorf("expected org DEK cached for %s", orgID)
		}
	}
}

func TestUnlockAllOrgDEKs_PartialFailure_ContinuesOtherOrgs(t *testing.T) {
	store, cache, svc, userID, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	password := []byte("partialpass")

	// Initialize org-good with correct password
	_, err := svc.InitializeOrgKeys(ctx, "org-good", userID, password)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}

	// Manually insert a stale-wrapped record for org-bad (wrong KEK)
	badDEK := make([]byte, dekSize)
	badSalt, _ := GenerateSalt()
	badKEK, _ := DeriveKEK([]byte("differentpassword"), badSalt, OrgKEKInfo)
	badWrapped, _ := WrapDEK(badKEK, badDEK)
	_ = store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{
		OrgID: "org-bad", UserID: userID, WrappedDEK: badWrapped, KeyVersion: 1,
	})

	err = svc.UnlockAllOrgDEKs(ctx, userID, nil, password, time.Hour)
	if err != nil {
		t.Errorf("UnlockAllOrgDEKs should return nil even on partial failure, got: %v", err)
	}

	// org-good should still be cached
	dek, _ := cache.GetDEK(ctx, OrgCacheKey("org-good"))
	if dek == nil {
		t.Error("org-good should be cached despite org-bad failure")
	}
	// org-bad should not be cached
	badCached, _ := cache.GetDEK(ctx, OrgCacheKey("org-bad"))
	if badCached != nil {
		t.Error("org-bad should not be cached after unlock failure")
	}
}

func TestUnlockAllOrgDEKs_DBError_NonFatal(t *testing.T) {
	store, _, svc, userID, _ := newOrgKeyTestEnv(t)
	store.getErr = errors.New("db connection refused")
	ctx := context.Background()

	err := svc.UnlockAllOrgDEKs(ctx, userID, nil, []byte("pass"), time.Hour)
	if err != nil {
		t.Errorf("DB error during UnlockAllOrgDEKs should be non-fatal, got: %v", err)
	}
}

func TestWrapOrgDEKForNewAdmin_DEKNotInCache(t *testing.T) {
	store, _, svc, userID, _ := newOrgKeyTestEnv(t)
	_ = store
	ctx := context.Background()
	orgID := "org-wrap-test"

	// No org DEK in cache
	err := svc.WrapOrgDEKForNewAdmin(ctx, orgID, userID, []byte("newadminpass"))
	if !errors.Is(err, ErrOrgDEKUnavailable) {
		t.Errorf("expected ErrOrgDEKUnavailable, got: %v", err)
	}
}

func TestWrapOrgDEKForNewAdmin_Success(t *testing.T) {
	store, cache, svc, adminUserID, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-handshake"
	adminPass := []byte("adminpass")

	// Admin initializes org
	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, adminUserID, adminPass)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}
	// Cache the org DEK (simulates post-login state)
	if err := cache.CacheDEK(ctx, OrgCacheKey(orgID), orgDEK, time.Hour); err != nil {
		t.Fatalf("CacheDEK: %v", err)
	}

	// Set up new admin
	newAdminID := "new-admin-2"
	newAdminSalt, _ := GenerateSalt()
	store.salts[newAdminID] = newAdminSalt
	newAdminPass := []byte("newadminpass")

	err = svc.WrapOrgDEKForNewAdmin(ctx, orgID, newAdminID, newAdminPass)
	if err != nil {
		t.Fatalf("WrapOrgDEKForNewAdmin: %v", err)
	}

	// New admin should have an org_key_members row
	rec, err := store.GetOrgKeyMember(ctx, orgID, newAdminID)
	if err != nil || rec == nil {
		t.Fatal("expected org_key_members row for new admin")
	}

	// New admin should be able to unlock org DEK with their password
	err = svc.UnlockOrgDEK(ctx, rec, newAdminSalt, newAdminPass, time.Hour)
	if err != nil {
		t.Fatalf("new admin UnlockOrgDEK: %v", err)
	}
}

func TestRewrapOrgDEKForAdmin_Success(t *testing.T) {
	store, cache, svc, userID, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-rewrap"
	oldPass := []byte("oldpassword")
	newPass := []byte("newpassword")

	// Initialize and cache
	orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, userID, oldPass)
	if err != nil {
		t.Fatalf("InitializeOrgKeys: %v", err)
	}
	if err := cache.CacheDEK(ctx, OrgCacheKey(orgID), orgDEK, time.Hour); err != nil {
		t.Fatalf("CacheDEK: %v", err)
	}

	err = svc.RewrapOrgDEKForAdmin(ctx, orgID, userID, newPass)
	if err != nil {
		t.Fatalf("RewrapOrgDEKForAdmin: %v", err)
	}

	// Old salt still in store — verify new password can now unlock
	rec, _ := store.GetOrgKeyMember(ctx, orgID, userID)
	salt := store.salts[userID]
	err = svc.UnlockOrgDEK(ctx, rec, salt, newPass, time.Hour)
	if err != nil {
		t.Fatalf("UnlockOrgDEK with new password should succeed after rewrap: %v", err)
	}
}

func TestGetOrgDEK_CacheMiss(t *testing.T) {
	_, _, svc, _, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()

	_, err := svc.GetOrgDEK(ctx, "nonexistent-org")
	if !errors.Is(err, ErrOrgDEKUnavailable) {
		t.Errorf("expected ErrOrgDEKUnavailable, got: %v", err)
	}
}

func TestGetOrgDEK_CacheHit(t *testing.T) {
	_, cache, svc, _, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	orgID := "org-getdek"

	dek, _ := GenerateDEK()
	_ = cache.CacheDEK(ctx, OrgCacheKey(orgID), dek, time.Hour)

	got, err := svc.GetOrgDEK(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrgDEK: %v", err)
	}
	if string(got) != string(dek) {
		t.Error("GetOrgDEK returned wrong DEK")
	}
}

func TestOrgCacheKey_NoCollision(t *testing.T) {
	// OrgCacheKey always starts with "org:" — UUIDs never start with "org:"
	uuids := []string{
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"550e8400-e29b-41d4-a716-446655440000",
		"00000000-0000-0000-0000-000000000000",
	}
	for _, u := range uuids {
		key := OrgCacheKey(u)
		if key == u {
			t.Errorf("OrgCacheKey(%q) == UUID — collision possible", u)
		}
		if len(key) < 5 || key[:4] != "org:" {
			t.Errorf("OrgCacheKey(%q) does not start with 'org:': %q", u, key)
		}
	}
}

func TestPgOrgKeyStore_GetUserSalt_NotFound(t *testing.T) {
	store := newMockOrgKeyStore()
	store.saltErr = ErrUserKeysMissing

	ctx := context.Background()
	_, err := store.GetUserSalt(ctx, "nonexistent")
	if !errors.Is(err, ErrUserKeysMissing) {
		t.Errorf("expected ErrUserKeysMissing, got: %v", err)
	}
}

func TestRewrapAllOrgDEKsForAdmin_Success(t *testing.T) {
	store, cache, svc, userID, _ := newOrgKeyTestEnv(t)
	ctx := context.Background()
	newPass := []byte("newpassword2")

	// Set up two orgs
	for _, orgID := range []string{"org-rewrap-a", "org-rewrap-b"} {
		orgDEK, err := svc.InitializeOrgKeys(ctx, orgID, userID, []byte("oldpass"))
		if err != nil {
			t.Fatalf("InitializeOrgKeys(%s): %v", orgID, err)
		}
		if err := cache.CacheDEK(ctx, OrgCacheKey(orgID), orgDEK, time.Hour); err != nil {
			t.Fatalf("CacheDEK(%s): %v", orgID, err)
		}
	}

	err := svc.RewrapAllOrgDEKsForAdmin(ctx, userID, newPass)
	if err != nil {
		t.Errorf("RewrapAllOrgDEKsForAdmin should return nil always, got: %v", err)
	}

	// Both orgs should now have updated wrapped DEKs decryptable with new password
	salt := store.salts[userID]
	for _, orgID := range []string{"org-rewrap-a", "org-rewrap-b"} {
		rec, _ := store.GetOrgKeyMember(ctx, orgID, userID)
		if rec == nil {
			t.Errorf("org_key_members row should exist for %s", orgID)
			continue
		}
		err = svc.UnlockOrgDEK(ctx, rec, salt, newPass, time.Hour)
		if err != nil {
			t.Errorf("UnlockOrgDEK with new password should succeed for %s: %v", orgID, err)
		}
	}
}
