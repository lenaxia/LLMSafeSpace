package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
)

func newFixture(t *testing.T) (*Service, *mocks.MockCacheService) {
	t.Helper()
	log, _ := apilogger.New(true, "debug", "console")
	cfg := &config.Config{}
	cfg.Redis.Host = "localhost"
	cfg.Redis.Port = 6379
	mockCache := new(mocks.MockCacheService)
	svc := NewWithCache(log, mockCache)
	return svc, mockCache
}

func TestIncrement(t *testing.T) {
	svc, cache := newFixture(t)
	ctx := context.Background()

	cache.On("Get", ctx, "rl:testkey").Return("", assert.AnError)
	cache.On("Set", ctx, "rl:testkey", "1", 60*time.Second).Return(nil)

	count, err := svc.Increment(ctx, "testkey", 1, 60*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestIncrement_Existing(t *testing.T) {
	svc, cache := newFixture(t)
	ctx := context.Background()

	cache.On("Get", ctx, "rl:testkey").Return("5", nil)
	cache.On("Set", ctx, "rl:testkey", "6", 60*time.Second).Return(nil)

	count, err := svc.Increment(ctx, "testkey", 1, 60*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, int64(6), count)
}

func TestAllow_UnderLimit(t *testing.T) {
	svc, _ := newFixture(t)
	assert.True(t, svc.Allow("key1", 100, 20))
}

func TestGetTTL(t *testing.T) {
	svc, cache := newFixture(t)
	ctx := context.Background()

	cache.On("Get", ctx, "rl:testkey").Return("5", nil)

	ttl, err := svc.GetTTL(ctx, "testkey")
	assert.NoError(t, err)
	assert.Equal(t, 60*time.Second, ttl)
}

func TestGetTTL_NotFound(t *testing.T) {
	svc, cache := newFixture(t)
	ctx := context.Background()

	cache.On("Get", ctx, "rl:testkey").Return("", assert.AnError)

	ttl, err := svc.GetTTL(ctx, "testkey")
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), ttl)
}

func TestStartStop(t *testing.T) {
	svc, _ := newFixture(t)
	assert.NoError(t, svc.Start())
	assert.NoError(t, svc.Stop())
}

func TestNewWithCache_NilPanics(t *testing.T) {
	log, _ := apilogger.New(true, "debug", "console")
	require.NotNil(t, NewWithCache(log, &mocks.MockCacheService{}))
	_ = log
}
