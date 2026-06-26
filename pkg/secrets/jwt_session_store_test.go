// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mockJWTSessionStore is the in-memory JWTSessionStore used by unit
// tests in this package. Tracks call counts and supports an injected
// error per-operation so tests can exercise failure paths without a
// real Postgres. Concurrency-safe — KeyService callers may write to
// the store from a goroutine fan-out test.
type mockJWTSessionStore struct {
	mu          sync.Mutex
	rows        map[uuid.UUID]*JWTSession
	writeErr    error
	getErr      error
	deleteErr   error
	deleteForUE error
	expireErr   error

	// Call counters
	GetCount         int
	WriteCount       int
	DeleteCount      int
	DeleteForUserCnt int
	ExpireCount      int
}

func newMockJWTSessionStore() *mockJWTSessionStore {
	return &mockJWTSessionStore{rows: make(map[uuid.UUID]*JWTSession)}
}

func (m *mockJWTSessionStore) GetJWTSession(_ context.Context, jti uuid.UUID) (*JWTSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetCount++
	if m.getErr != nil {
		return nil, m.getErr
	}
	r, ok := m.rows[jti]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *mockJWTSessionStore) WriteJWTSession(_ context.Context, s *JWTSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WriteCount++
	if m.writeErr != nil {
		return m.writeErr
	}
	if s == nil {
		return errors.New("nil session")
	}
	cp := *s
	m.rows[s.JTI] = &cp
	return nil
}

func (m *mockJWTSessionStore) DeleteJWTSession(_ context.Context, jti uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteCount++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.rows, jti)
	return nil
}

func (m *mockJWTSessionStore) DeleteJWTSessionsForUser(_ context.Context, userID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteForUserCnt++
	if m.deleteForUE != nil {
		return 0, m.deleteForUE
	}
	var n int64
	for jti, row := range m.rows {
		if row.UserID == userID {
			delete(m.rows, jti)
			n++
		}
	}
	return n, nil
}

func (m *mockJWTSessionStore) DeleteExpiredJWTSessions(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExpireCount++
	if m.expireErr != nil {
		return 0, m.expireErr
	}
	var n int64
	for jti, row := range m.rows {
		if row.ExpiresAt.Before(before) {
			delete(m.rows, jti)
			n++
		}
	}
	return n, nil
}

// --- Tests ---

func TestMockJWTSessionStore_WriteAndGet(t *testing.T) {
	store := newMockJWTSessionStore()
	jti := uuid.New()
	row := &JWTSession{
		JTI:        jti,
		UserID:     "u-1",
		WrappedDEK: []byte("wrapped"),
		KEKSalt:    []byte("salt"),
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	if err := store.WriteJWTSession(context.Background(), row); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got, err := store.GetJWTSession(context.Background(), jti)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected row, got nil")
	}
	if got.UserID != "u-1" {
		t.Errorf("UserID = %q, want %q", got.UserID, "u-1")
	}
	if !bytes.Equal(got.WrappedDEK, []byte("wrapped")) {
		t.Errorf("WrappedDEK mismatch")
	}
	if !bytes.Equal(got.KEKSalt, []byte("salt")) {
		t.Errorf("KEKSalt mismatch")
	}
}

func TestMockJWTSessionStore_GetMissing_ReturnsNilNil(t *testing.T) {
	store := newMockJWTSessionStore()
	got, err := store.GetJWTSession(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil row for missing jti")
	}
}

func TestMockJWTSessionStore_WriteUpsert_OverwritesWrappedDEK(t *testing.T) {
	store := newMockJWTSessionStore()
	jti := uuid.New()
	first := &JWTSession{JTI: jti, UserID: "u-1", WrappedDEK: []byte("v1"), KEKSalt: []byte("s1"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.WriteJWTSession(context.Background(), first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Soft-unlock backfill scenario: same jti, fresh kek_salt + wrapped_dek.
	second := &JWTSession{JTI: jti, UserID: "u-1", WrappedDEK: []byte("v2"), KEKSalt: []byte("s2"), ExpiresAt: time.Now().Add(2 * time.Hour)}
	if err := store.WriteJWTSession(context.Background(), second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := store.GetJWTSession(context.Background(), jti)
	if !bytes.Equal(got.WrappedDEK, []byte("v2")) {
		t.Errorf("WrappedDEK should overwrite on conflict, got %q", got.WrappedDEK)
	}
	if !bytes.Equal(got.KEKSalt, []byte("s2")) {
		t.Errorf("KEKSalt should overwrite on conflict, got %q", got.KEKSalt)
	}
}

func TestMockJWTSessionStore_DeleteJWTSession(t *testing.T) {
	store := newMockJWTSessionStore()
	jti := uuid.New()
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: jti, UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: time.Now().Add(time.Hour)})

	if err := store.DeleteJWTSession(context.Background(), jti); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := store.GetJWTSession(context.Background(), jti)
	if got != nil {
		t.Errorf("expected row deleted, still present")
	}
}

func TestMockJWTSessionStore_DeleteJWTSession_NonexistentIsIdempotent(t *testing.T) {
	store := newMockJWTSessionStore()
	if err := store.DeleteJWTSession(context.Background(), uuid.New()); err != nil {
		t.Errorf("delete of missing row should not error: %v", err)
	}
}

func TestMockJWTSessionStore_DeleteJWTSessionsForUser(t *testing.T) {
	store := newMockJWTSessionStore()
	now := time.Now()
	keep := uuid.New()
	drop1 := uuid.New()
	drop2 := uuid.New()
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: keep, UserID: "u-other", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: now.Add(time.Hour)})
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: drop1, UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: now.Add(time.Hour)})
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: drop2, UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: now.Add(time.Hour)})

	n, err := store.DeleteJWTSessionsForUser(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("delete-for-user: %v", err)
	}
	if n != 2 {
		t.Errorf("rows affected = %d, want 2", n)
	}
	// Other user's row preserved
	if got, _ := store.GetJWTSession(context.Background(), keep); got == nil {
		t.Errorf("expected other user's row preserved")
	}
}

func TestMockJWTSessionStore_DeleteExpiredJWTSessions(t *testing.T) {
	store := newMockJWTSessionStore()
	now := time.Now()
	expired := uuid.New()
	active := uuid.New()
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: expired, UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: now.Add(-time.Hour)})
	_ = store.WriteJWTSession(context.Background(), &JWTSession{JTI: active, UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: now.Add(time.Hour)})

	n, err := store.DeleteExpiredJWTSessions(context.Background(), now)
	if err != nil {
		t.Fatalf("delete-expired: %v", err)
	}
	if n != 1 {
		t.Errorf("rows affected = %d, want 1", n)
	}
	if got, _ := store.GetJWTSession(context.Background(), expired); got != nil {
		t.Errorf("expected expired row removed")
	}
	if got, _ := store.GetJWTSession(context.Background(), active); got == nil {
		t.Errorf("expected active row preserved")
	}
}

func TestMockJWTSessionStore_PropagatesInjectedErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*mockJWTSessionStore)
		run   func(context.Context, *mockJWTSessionStore) error
	}{
		{
			name:  "Get error",
			setup: func(m *mockJWTSessionStore) { m.getErr = errors.New("get fail") },
			run: func(ctx context.Context, m *mockJWTSessionStore) error {
				_, err := m.GetJWTSession(ctx, uuid.New())
				return err
			},
		},
		{
			name:  "Write error",
			setup: func(m *mockJWTSessionStore) { m.writeErr = errors.New("write fail") },
			run: func(ctx context.Context, m *mockJWTSessionStore) error {
				return m.WriteJWTSession(ctx, &JWTSession{JTI: uuid.New(), UserID: "u-1", WrappedDEK: []byte("w"), KEKSalt: []byte("s"), ExpiresAt: time.Now().Add(time.Hour)})
			},
		},
		{
			name:  "Delete error",
			setup: func(m *mockJWTSessionStore) { m.deleteErr = errors.New("delete fail") },
			run: func(ctx context.Context, m *mockJWTSessionStore) error {
				return m.DeleteJWTSession(ctx, uuid.New())
			},
		},
		{
			name:  "DeleteForUser error",
			setup: func(m *mockJWTSessionStore) { m.deleteForUE = errors.New("delete-for-user fail") },
			run: func(ctx context.Context, m *mockJWTSessionStore) error {
				_, err := m.DeleteJWTSessionsForUser(ctx, "u-1")
				return err
			},
		},
		{
			name:  "DeleteExpired error",
			setup: func(m *mockJWTSessionStore) { m.expireErr = errors.New("expire fail") },
			run: func(ctx context.Context, m *mockJWTSessionStore) error {
				_, err := m.DeleteExpiredJWTSessions(ctx, time.Now())
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockJWTSessionStore()
			tc.setup(store)
			if err := tc.run(context.Background(), store); err == nil {
				t.Errorf("expected injected error to surface")
			}
		})
	}
}
