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

	// Fast-forward time in miniredis
	mr.FastForward(2 * time.Second)

	got, _ := cache.GetDEK(ctx, "session-1")
	if got != nil {
		t.Error("DEK should be nil after TTL expiry")
	}
}
