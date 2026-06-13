// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

type Backend interface {
	IncrExpire(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error)
	WindowAdd(ctx context.Context, key string, score int64, member string, expiration time.Duration) error
	WindowRemove(ctx context.Context, key string, cutoff int64) error
	WindowCount(ctx context.Context, key string, min, max int64) (int, error)
	WindowMembers(ctx context.Context, key string, start, stop int64) ([]string, error)
	TTL(ctx context.Context, key string) (time.Duration, error)
	Close() error
}

var incrExpireScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return current
`)

var windowAddScript = redis.NewScript(`
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

type redisBackend struct {
	client *redis.Client
}

func NewRedisBackend(client *redis.Client) Backend {
	return &redisBackend{client: client}
}

func (b *redisBackend) IncrExpire(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error) {
	res, err := incrExpireScript.Run(ctx, b.client, []string{key}, value, expiration.Milliseconds()).Result()
	if err != nil {
		return 0, fmt.Errorf("ratelimit: atomic increment failed: %w", err)
	}
	n, ok := res.(int64)
	if !ok {
		return 0, fmt.Errorf("ratelimit: unexpected increment result type %T", res)
	}
	return n, nil
}

func (b *redisBackend) WindowAdd(ctx context.Context, key string, score int64, member string, expiration time.Duration) error {
	_, err := windowAddScript.Run(ctx, b.client, []string{key}, score, member, expiration.Milliseconds()).Result()
	if err != nil {
		return fmt.Errorf("ratelimit: window add failed: %w", err)
	}
	return nil
}

func (b *redisBackend) WindowRemove(ctx context.Context, key string, cutoff int64) error {
	if err := b.client.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff, 10)).Err(); err != nil {
		return fmt.Errorf("ratelimit: window remove failed: %w", err)
	}
	return nil
}

func (b *redisBackend) WindowCount(ctx context.Context, key string, min, max int64) (int, error) {
	n, err := b.client.ZCount(ctx, key, strconv.FormatInt(min, 10), strconv.FormatInt(max, 10)).Result()
	if err != nil {
		return 0, fmt.Errorf("ratelimit: window count failed: %w", err)
	}
	return int(n), nil
}

func (b *redisBackend) WindowMembers(ctx context.Context, key string, start, stop int64) ([]string, error) {
	members, err := b.client.ZRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("ratelimit: window members failed: %w", err)
	}
	return members, nil
}

func (b *redisBackend) TTL(ctx context.Context, key string) (time.Duration, error) {
	d, err := b.client.PTTL(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("ratelimit: ttl failed: %w", err)
	}
	if d < 0 {
		return 0, nil
	}
	return d, nil
}

// Close is a no-op: the redisBackend borrows a client it does not own (the
// cache service owns and closes the shared connection pool). Closing a
// borrowed client here would double-close when the cache service stops and
// could break other consumers sharing the same pool.
func (b *redisBackend) Close() error {
	return nil
}

type memCounter struct {
	value      int64
	expires    time.Time
	lastAccess time.Time
}

type memWindow struct {
	members    map[string]int64
	expires    time.Time
	lastAccess time.Time
}

type memoryBackend struct {
	mu              sync.Mutex
	counters        map[string]*memCounter
	windows         map[string]*memWindow
	bucketTTL       time.Duration
	cleanupInterval time.Duration
	lastCleanup     time.Time
}

func NewMemoryBackend() Backend {
	return &memoryBackend{
		counters:        make(map[string]*memCounter),
		windows:         make(map[string]*memWindow),
		bucketTTL:       defaultBucketTTL,
		cleanupInterval: defaultCleanupEvery,
		lastCleanup:     time.Now(),
	}
}

// NewMemoryBackendWithCleanup overrides the stale-entry eviction thresholds,
// primarily for tests. ttl is how long an idle entry is retained before being
// purged; interval is how often the background sweep runs (lazily, on access).
func NewMemoryBackendWithCleanup(ttl, interval time.Duration) Backend {
	b := NewMemoryBackend().(*memoryBackend)
	if ttl > 0 {
		b.bucketTTL = ttl
	}
	if interval > 0 {
		b.cleanupInterval = interval
	}
	return b
}

// maybeCleanup purges entries that have not been accessed for longer than
// bucketTTL. This bounds the size of the counters/windows maps so abandoned
// keys do not accumulate indefinitely — the same leak class fixed for the
// in-memory token buckets in Service.maybeCleanup. Must be called with b.mu held.
func (b *memoryBackend) maybeCleanup(now time.Time) {
	if now.Sub(b.lastCleanup) < b.cleanupInterval {
		return
	}
	b.lastCleanup = now
	for k, c := range b.counters {
		if now.Sub(c.lastAccess) > b.bucketTTL {
			delete(b.counters, k)
		}
	}
	for k, w := range b.windows {
		if now.Sub(w.lastAccess) > b.bucketTTL {
			delete(b.windows, k)
		}
	}
}

func (b *memoryBackend) IncrExpire(_ context.Context, key string, value int64, expiration time.Duration) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	c, ok := b.counters[key]
	if !ok || !c.expires.IsZero() && now.After(c.expires) {
		c = &memCounter{value: value, expires: now.Add(expiration), lastAccess: now}
		b.counters[key] = c
		return c.value, nil
	}
	c.value += value
	c.lastAccess = now
	return c.value, nil
}

func (b *memoryBackend) window(key string, now time.Time, expiration time.Duration) *memWindow {
	w, ok := b.windows[key]
	if !ok || !w.expires.IsZero() && now.After(w.expires) {
		w = &memWindow{members: make(map[string]int64), expires: now.Add(expiration), lastAccess: now}
		b.windows[key] = w
	}
	w.lastAccess = now
	return w
}

func (b *memoryBackend) WindowAdd(_ context.Context, key string, score int64, member string, expiration time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	w := b.window(key, now, expiration)
	w.members[member] = score
	return nil
}

func (b *memoryBackend) WindowRemove(_ context.Context, key string, cutoff int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	w, ok := b.windows[key]
	if !ok {
		return nil
	}
	w.lastAccess = now
	for m, score := range w.members {
		if score <= cutoff {
			delete(w.members, m)
		}
	}
	return nil
}

func (b *memoryBackend) WindowCount(_ context.Context, key string, min, max int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	w, ok := b.windows[key]
	if !ok {
		return 0, nil
	}
	w.lastAccess = now
	count := 0
	for _, score := range w.members {
		if score >= min && score <= max {
			count++
		}
	}
	return count, nil
}

func (b *memoryBackend) WindowMembers(_ context.Context, key string, start, stop int64) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	w, ok := b.windows[key]
	if !ok {
		return nil, nil
	}
	w.lastAccess = now
	type sm struct {
		member string
		score  int64
	}
	items := make([]sm, 0, len(w.members))
	for m, score := range w.members {
		items = append(items, sm{member: m, score: score})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].score < items[j].score })
	lo := int(start)
	if lo < 0 {
		lo = len(items) + lo
	}
	if lo < 0 {
		lo = 0
	}
	hi := int(stop)
	if hi < 0 {
		hi = len(items) + hi
	}
	if hi >= len(items) {
		hi = len(items) - 1
	}
	if lo > hi || lo >= len(items) {
		return []string{}, nil
	}
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, items[i].member)
	}
	return out, nil
}

func (b *memoryBackend) TTL(_ context.Context, key string) (time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeCleanup(now)
	if c, ok := b.counters[key]; ok && (c.expires.IsZero() || !now.After(c.expires)) {
		return c.expires.Sub(now), nil
	}
	if w, ok := b.windows[key]; ok && (w.expires.IsZero() || !now.After(w.expires)) {
		return w.expires.Sub(now), nil
	}
	return 0, nil
}

func (b *memoryBackend) Close() error { return nil }

// MemoryBackendCounterCount and MemoryBackendWindowCount expose the live map
// sizes for tests; they are not part of the Backend contract.
func (b *memoryBackend) counterCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.counters)
}

func (b *memoryBackend) windowCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.windows)
}
