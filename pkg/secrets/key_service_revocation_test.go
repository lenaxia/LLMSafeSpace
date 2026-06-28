// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Epic 56 Step 8: every path that invalidates the in-memory DEK must
// also invalidate the durable jwt_sessions row, otherwise an attacker
// who has the old JWT can rehydrate from PG after the user "changed
// password to be safe". These tests pin the contract.

func unlockedSvc(t *testing.T) (*KeyService, *mockJWTSessionStore, string, []byte) {
	t.Helper()
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	svc.SetSecretStore(newMockSecretStore())
	ctx := context.Background()

	password := []byte("pw")
	_, _ = svc.InitializeUserKeys(ctx, "u-1", password)
	jti := uuid.New().String()
	if err := svc.UnlockDEKWithSigningKey(ctx, "u-1", password, jti, time.Hour, []byte("active-signing-key")); err != nil {
		t.Fatalf("setup unlock: %v", err)
	}
	// Confirm durable row exists.
	parsed, _ := uuid.Parse(jti)
	got, _ := jwtStore.GetJWTSession(ctx, parsed)
	if got == nil {
		t.Fatalf("setup: expected durable row")
	}
	return svc, jwtStore, jti, password
}

func TestKeyService_EvictDEK_AlsoDeletesDurableRow(t *testing.T) {
	svc, jwtStore, jti, _ := unlockedSvc(t)
	ctx := context.Background()

	if err := svc.EvictDEK(ctx, jti); err != nil {
		t.Fatalf("EvictDEK: %v", err)
	}
	parsed, _ := uuid.Parse(jti)
	got, _ := jwtStore.GetJWTSession(ctx, parsed)
	if got != nil {
		t.Errorf("EvictDEK must also delete durable row")
	}
}

func TestKeyService_EvictDEK_NonJTISessionID_SkipsDurableDelete(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	// API-key sessionID — durable row doesn't exist; the delete call
	// must not even reach the store.
	if err := svc.EvictDEK(ctx, "apikey:abcdef"); err != nil {
		t.Errorf("EvictDEK non-uuid should not error: %v", err)
	}
	if jwtStore.DeleteCount != 0 {
		t.Errorf("non-uuid sessionID must not hit jwt_sessions, DeleteCount=%d", jwtStore.DeleteCount)
	}
}

func TestKeyService_ChangePassword_AlsoDeletesDurableRow(t *testing.T) {
	svc, jwtStore, jti, password := unlockedSvc(t)
	ctx := context.Background()

	if err := svc.ChangePassword(ctx, "u-1", jti, password, []byte("new-pw")); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	parsed, _ := uuid.Parse(jti)
	got, _ := jwtStore.GetJWTSession(ctx, parsed)
	if got != nil {
		t.Errorf("ChangePassword must also delete durable row (old DEK no longer recoverable)")
	}
}

func TestKeyService_RotateKeyWithPassword_AlsoDeletesDurableRow(t *testing.T) {
	svc, jwtStore, jti, password := unlockedSvc(t)
	ctx := context.Background()

	_, err := svc.RotateKeyWithPassword(ctx, "u-1", password, jti, time.Hour)
	if err != nil {
		t.Fatalf("RotateKeyWithPassword: %v", err)
	}
	parsed, _ := uuid.Parse(jti)
	got, _ := jwtStore.GetJWTSession(ctx, parsed)
	if got != nil {
		t.Errorf("RotateKeyWithPassword must also delete durable row (new DEK; old wrap is stale)")
	}
}
