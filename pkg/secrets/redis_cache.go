package secrets

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

const dekCachePrefix = "dek:"

// RedisDEKCache implements DEKCache using Redis.
type RedisDEKCache struct {
	client *redis.Client
}

// NewRedisDEKCache creates a new Redis-backed DEK cache.
func NewRedisDEKCache(client *redis.Client) *RedisDEKCache {
	return &RedisDEKCache{client: client}
}

func (c *RedisDEKCache) CacheDEK(ctx context.Context, sessionID string, dek []byte, ttl time.Duration) error {
	key := dekCachePrefix + sessionID
	// Store as hex to avoid binary issues in Redis
	return c.client.Set(ctx, key, hex.EncodeToString(dek), ttl).Err()
}

func (c *RedisDEKCache) GetDEK(ctx context.Context, sessionID string) ([]byte, error) {
	key := dekCachePrefix + sessionID
	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get DEK: %w", err)
	}
	return hex.DecodeString(val)
}

func (c *RedisDEKCache) EvictDEK(ctx context.Context, sessionID string) error {
	key := dekCachePrefix + sessionID
	return c.client.Del(ctx, key).Err()
}
