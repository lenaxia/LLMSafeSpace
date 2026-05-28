package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

func setupRedisCache(t *testing.T) (*RedisDEKCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisDEKCache(client), mr
}

func setupRedisCacheWithMasterKey(t *testing.T) (*RedisDEKCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	masterKey := []byte("01234567890123456789012345678901") // 32 bytes
	return NewRedisDEKCache(client, masterKey), mr
}

func TestRedisDEKCache_CacheAndGet(t *testing.T) {
	cache, mr := setupRedisCache(t)
	defer mr.Close()
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	err := cache.CacheDEK(ctx, "session-1", dek, time.Hour)
	if err != nil {
		t.Fatalf("CacheDEK failed: %v", err)
	}

	got, err := cache.GetDEK(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetDEK failed: %v", err)
	}
	if len(got) != 32 {
		t.Errorf("Expected 32 bytes, got %d", len(got))
	}
	for i := range dek {
		if dek[i] != got[i] {
			t.Error("Retrieved DEK doesn't match stored DEK")
			break
		}
	}
}

func TestRedisDEKCache_GetNonexistent(t *testing.T) {
	cache, mr := setupRedisCache(t)
	defer mr.Close()
	ctx := context.Background()

	got, err := cache.GetDEK(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetDEK should not error for missing key: %v", err)
	}
	if got != nil {
		t.Error("Expected nil for nonexistent session")
	}
}

func TestRedisDEKCache_Evict(t *testing.T) {
	cache, mr := setupRedisCache(t)
	defer mr.Close()
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef")
	cache.CacheDEK(ctx, "session-1", dek, time.Hour)

	err := cache.EvictDEK(ctx, "session-1")
	if err != nil {
		t.Fatalf("EvictDEK failed: %v", err)
	}

	got, _ := cache.GetDEK(ctx, "session-1")
	if got != nil {
		t.Error("DEK should be nil after eviction")
	}
}

func TestRedisDEKCache_TTLExpiry(t *testing.T) {
	cache, mr := setupRedisCache(t)
	defer mr.Close()
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef")
	cache.CacheDEK(ctx, "session-1", dek, time.Second)

	mr.FastForward(2 * time.Second)

	got, _ := cache.GetDEK(ctx, "session-1")
	if got != nil {
		t.Error("DEK should be nil after TTL expiry")
	}
}

// --- Master key tests ---

func TestRedisDEKCache_MasterKey_CacheAndGet(t *testing.T) {
	cache, mr := setupRedisCacheWithMasterKey(t)
	defer mr.Close()
	ctx := context.Background()

	dek := []byte("abcdefghijklmnopqrstuvwxyz012345") // 32 bytes
	err := cache.CacheDEK(ctx, "session-mk", dek, time.Hour)
	if err != nil {
		t.Fatalf("CacheDEK with master key failed: %v", err)
	}

	got, err := cache.GetDEK(ctx, "session-mk")
	if err != nil {
		t.Fatalf("GetDEK with master key failed: %v", err)
	}
	for i := range dek {
		if dek[i] != got[i] {
			t.Error("DEK round-trip with master key failed")
			break
		}
	}
}

func TestRedisDEKCache_MasterKey_ValueEncryptedInRedis(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	masterKey := []byte("01234567890123456789012345678901")
	cache := NewRedisDEKCache(client, masterKey)
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef")
	cache.CacheDEK(ctx, "session-enc", dek, time.Hour)

	// Read raw value from Redis — should NOT be the plain hex of the DEK
	rawVal, _ := mr.Get("dek:session-enc")
	plainHex := "30313233343536373839616263646566303132333435363738396162636465660" // hex of dek without encryption would be shorter
	if rawVal == plainHex[:64] {
		t.Error("Value in Redis should be encrypted, not plain hex of DEK")
	}
	// Encrypted value should be longer than 64 chars (32 bytes hex) due to nonce + tag
	if len(rawVal) <= 64 {
		t.Errorf("Encrypted value should be longer than plain DEK hex (64), got %d", len(rawVal))
	}
}

func TestRedisDEKCache_MasterKey_WrongKeyCannotDecrypt(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	masterKey1 := []byte("01234567890123456789012345678901")
	masterKey2 := []byte("abcdefghijklmnopqrstuvwxyz012345")

	cache1 := NewRedisDEKCache(client, masterKey1)
	cache2 := NewRedisDEKCache(client, masterKey2)
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef")
	cache1.CacheDEK(ctx, "session-wrong", dek, time.Hour)

	// cache2 with different master key should fail to decrypt
	_, err := cache2.GetDEK(ctx, "session-wrong")
	if err == nil {
		t.Error("Should fail to decrypt with wrong master key")
	}
}

func TestRedisDEKCache_NoMasterKey_BackwardsCompatible(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := NewRedisDEKCache(client) // no master key
	ctx := context.Background()

	dek := []byte("0123456789abcdef0123456789abcdef")
	cache.CacheDEK(ctx, "session-plain", dek, time.Hour)

	// Raw value should be plain hex (64 chars for 32 bytes)
	rawVal, _ := mr.Get("dek:session-plain")
	if len(rawVal) != 64 {
		t.Errorf("Without master key, value should be 64 hex chars, got %d", len(rawVal))
	}

	got, _ := cache.GetDEK(ctx, "session-plain")
	for i := range dek {
		if dek[i] != got[i] {
			t.Error("Plain mode round-trip failed")
			break
		}
	}
}
