package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

const keyPrefix = "rl:"

type Service struct {
	logger       *logger.Logger
	cache        interfaces.CacheService
	localBuckets map[string]*bucket
	mu           sync.Mutex
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

var _ interfaces.RateLimiterService = (*Service)(nil)

func NewWithCache(log *logger.Logger, cache interfaces.CacheService) *Service {
	return &Service{
		logger:       log,
		cache:        cache,
		localBuckets: make(map[string]*bucket),
	}
}

func (s *Service) Start() error { return nil }
func (s *Service) Stop() error  { return nil }

func (s *Service) Increment(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error) {
	cacheKey := keyPrefix + key
	currentStr, err := s.cache.Get(ctx, cacheKey)
	current := int64(0)
	if err == nil && currentStr != "" {
		current, _ = strconv.ParseInt(currentStr, 10, 64)
	}
	current += value
	if err := s.cache.Set(ctx, cacheKey, strconv.FormatInt(current, 10), expiration); err != nil {
		return 0, fmt.Errorf("ratelimit: increment failed: %w", err)
	}
	return current, nil
}

func (s *Service) AddToWindow(ctx context.Context, key string, timestamp int64, member string, expiration time.Duration) error {
	cacheKey := keyPrefix + key + ":w:" + member
	return s.cache.Set(ctx, cacheKey, "1", expiration)
}

func (s *Service) RemoveFromWindow(ctx context.Context, key string, cutoff int64) error {
	return nil
}

func (s *Service) CountInWindow(ctx context.Context, key string, min, max int64) (int, error) {
	return 0, nil
}

func (s *Service) GetWindowEntries(ctx context.Context, key string, start, stop int) ([]string, error) {
	return nil, nil
}

func (s *Service) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	cacheKey := keyPrefix + key
	val, err := s.cache.Get(ctx, cacheKey)
	if err != nil || val == "" {
		return 0, nil
	}
	return 60 * time.Second, nil
}

func (s *Service) Allow(key string, rate float64, burst int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	b, exists := s.localBuckets[key]
	if !exists {
		b = &bucket{tokens: float64(burst), lastTime: now}
		s.localBuckets[key] = b
	}

	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rate
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
