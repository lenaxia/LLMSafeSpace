// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Epic 56 Step 5: KeyService.GetDEK now takes an explicit matchedSigningKey
// and rehydrates from the durable jwt_sessions table on Redis miss.

func wrapDEKForJWT(t *testing.T, matchedKey []byte, jti uuid.UUID, dek []byte) (wrappedDEK, salt []byte) {
	t.Helper()
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	kek, err := DeriveKEKFromKey(append([]byte{}, append(matchedKey, []byte(jti.String())...)...), salt, JWTSessionKEKInfo)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	wrapped, err := EncryptSecret(kek, dek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	return wrapped, salt
}

func TestKeyService_GetDEK_CacheHit_DoesNotTouchDurableStore(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	dek := []byte("test-dek-32-bytes-padding-here-x")
	jti := uuid.New()
	_ = cache.CacheDEK(ctx, jti.String(), dek, time.Hour)

	got, err := svc.GetDEK(ctx, jti.String(), []byte("any-matched-key"))
	if err != nil {
		t.Fatalf("GetDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("DEK mismatch")
	}
	if jwtStore.GetCount != 0 {
		t.Errorf("durable store touched on cache hit: GetCount=%d", jwtStore.GetCount)
	}
}

func TestKeyService_GetDEK_CacheMiss_RehydratesFromDurableStore(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	matchedKey := []byte("matched-signing-key-32-bytes-pad")
	jti := uuid.New()
	dek := []byte("durable-dek-32-bytes-padding-12x")
	wrapped, salt := wrapDEKForJWT(t, matchedKey, jti, dek)
	_ = jwtStore.WriteJWTSession(ctx, &JWTSession{
		JTI: jti, UserID: "u-1", WrappedDEK: wrapped, KEKSalt: salt,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})

	got, err := svc.GetDEK(ctx, jti.String(), matchedKey)
	if err != nil {
		t.Fatalf("GetDEK rehydrate: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("rehydrated DEK mismatch")
	}

	// Rehydrate should re-cache for fast subsequent reads.
	cached, _ := cache.GetDEK(ctx, jti.String())
	if !bytes.Equal(cached, dek) {
		t.Errorf("rehydrate should re-cache to Redis")
	}
}

func TestKeyService_GetDEK_CacheMiss_NoDurableRow_ReturnsErrDEKUnavailable(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	jti := uuid.New()
	matchedKey := []byte("any-key")

	_, err := svc.GetDEK(ctx, jti.String(), matchedKey)
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("expected ErrDEKUnavailable, got %v", err)
	}
}

func TestKeyService_GetDEK_CacheMiss_ExpiredRow_ReturnsErrDEKUnavailable(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	matchedKey := []byte("k")
	jti := uuid.New()
	dek := make([]byte, 32)
	wrapped, salt := wrapDEKForJWT(t, matchedKey, jti, dek)
	_ = jwtStore.WriteJWTSession(ctx, &JWTSession{
		JTI: jti, UserID: "u-1", WrappedDEK: wrapped, KEKSalt: salt,
		CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour), // already expired
	})

	_, err := svc.GetDEK(ctx, jti.String(), matchedKey)
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("expected ErrDEKUnavailable for expired row, got %v", err)
	}
}

func TestKeyService_GetDEK_CacheMiss_WrongMatchedKey_ReturnsErrDEKUnavailable(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	jti := uuid.New()
	dek := make([]byte, 32)
	wrapped, salt := wrapDEKForJWT(t, []byte("correct-key"), jti, dek)
	_ = jwtStore.WriteJWTSession(ctx, &JWTSession{
		JTI: jti, UserID: "u-1", WrappedDEK: wrapped, KEKSalt: salt,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})

	_, err := svc.GetDEK(ctx, jti.String(), []byte("WRONG-key"))
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("wrong key → ErrDEKUnavailable, got %v", err)
	}
}

func TestKeyService_GetDEK_NilMatchedKey_SkipsRehydrate(t *testing.T) {
	// API-key callers, controller-internal callers, and tests that pass
	// nil all share this path: the durable row may exist (login wrote one)
	// but we cannot rehydrate without the matched key.
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	jti := uuid.New()
	wrapped, salt := wrapDEKForJWT(t, []byte("k"), jti, make([]byte, 32))
	_ = jwtStore.WriteJWTSession(ctx, &JWTSession{
		JTI: jti, UserID: "u-1", WrappedDEK: wrapped, KEKSalt: salt,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})

	_, err := svc.GetDEK(ctx, jti.String(), nil)
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("nil matched key → ErrDEKUnavailable, got %v", err)
	}
}

func TestKeyService_GetDEK_APIKeySessionID_SkipsRehydrate(t *testing.T) {
	// API-key sessions use "apikey:<hash>" as sessionID. These rehydrate
	// from the api_keys table, not jwt_sessions, so the durable rehydrate
	// path must skip them. Returns ErrDEKUnavailable so the API-key auth
	// path's own rehydrate logic (auth.validateAPIKey) is the only writer.
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	_, err := svc.GetDEK(ctx, "apikey:somehex", []byte("any-key"))
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("apikey sessionID → ErrDEKUnavailable, got %v", err)
	}
	if jwtStore.GetCount != 0 {
		t.Errorf("apikey sessionID must not touch jwt_sessions, GetCount=%d", jwtStore.GetCount)
	}
}

func TestKeyService_GetDEK_NoJWTSessionStore_OldBehaviorPreserved(t *testing.T) {
	// Tests without SetJWTSessionStore (e.g. unit tests for non-DEK-cache
	// behavior) should still get the pre-Epic-56 result: cache miss ⇒
	// ErrDEKUnavailable, no panic, no rehydrate attempt.
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, err := svc.GetDEK(ctx, uuid.New().String(), []byte("k"))
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("no jwt store → ErrDEKUnavailable, got %v", err)
	}
}

func TestKeyService_GetDEK_NonUUIDSessionID_SkipsRehydrate(t *testing.T) {
	// Legacy sessionIDs that aren't canonical UUIDs (e.g. raw strings from
	// older tests) must not trigger a UUID parse error — they fall through
	// to ErrDEKUnavailable.
	store := newMockKeyStore()
	cache := newMockDEKCache()
	jwtStore := newMockJWTSessionStore()
	svc := NewKeyService(store, cache)
	svc.SetJWTSessionStore(jwtStore)
	ctx := context.Background()

	_, err := svc.GetDEK(ctx, "not-a-uuid", []byte("k"))
	if !errors.Is(err, ErrDEKUnavailable) {
		t.Errorf("non-uuid sessionID → ErrDEKUnavailable, got %v", err)
	}
	if jwtStore.GetCount != 0 {
		t.Errorf("non-uuid sessionID must not touch jwt_sessions, GetCount=%d", jwtStore.GetCount)
	}
}
