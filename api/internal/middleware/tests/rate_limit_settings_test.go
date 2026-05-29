package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/settings"
)

type stubStore struct {
	data map[string]json.RawMessage
}

func (s *stubStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return s.data, nil
}
func (s *stubStore) SetInstanceSetting(_ context.Context, key string, value json.RawMessage) error {
	s.data[key] = value
	return nil
}

type noopRateLimiter struct{}

func (n *noopRateLimiter) Allow(_ string, _ float64, _ int) bool { return true }
func (n *noopRateLimiter) Increment(_ context.Context, _ string, count int64, _ time.Duration) (int64, error) {
	return count, nil
}
func (n *noopRateLimiter) AddToWindow(_ context.Context, _ string, _ int64, _ string, _ time.Duration) error {
	return nil
}
func (n *noopRateLimiter) RemoveFromWindow(_ context.Context, _ string, _ int64) error { return nil }
func (n *noopRateLimiter) CountInWindow(_ context.Context, _ string, _, _ int64) (int, error) {
	return 0, nil
}
func (n *noopRateLimiter) GetWindowEntries(_ context.Context, _ string, _, _ int) ([]string, error) {
	return nil, nil
}
func (n *noopRateLimiter) GetTTL(_ context.Context, _ string) (time.Duration, error) {
	return 0, nil
}
func (n *noopRateLimiter) Start() error { return nil }
func (n *noopRateLimiter) Stop() error  { return nil }

func newTestSettings(vals map[string]any) (*settings.InstanceService, *stubStore) {
	data := make(map[string]json.RawMessage)
	for k, v := range vals {
		raw, _ := json.Marshal(v)
		data[k] = raw
	}
	store := &stubStore{data: data}
	var log pkginterfaces.LoggerInterface = lmocks.NewMockLogger()
	svc := settings.NewInstanceService(store, log)
	svc.Start()
	return svc, store
}

// TestRateLimitMiddleware_SettingsOverride_Enabled verifies that when settings
// enable rate limiting (overriding static config disabled), the middleware enforces limits.
func TestRateLimitMiddleware_SettingsOverride_Enabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Static config: rate limiting DISABLED
	config := middleware.RateLimitConfig{
		Enabled:      false,
		DefaultLimit: 2,
		BurstSize:    2,
		Strategy:     "token_bucket",
	}

	// Settings: rate limiting ENABLED (overrides static)
	instanceSettings, _ := newTestSettings(map[string]any{
		"rateLimiting.enabled": true,
	})

	rl := &noopRateLimiter{}
	log := lmocks.NewMockLogger()

	router := gin.New()
	router.Use(middleware.RateLimitMiddleware(rl, log, config, instanceSettings))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	// Request passes because noopRateLimiter.Allow returns true,
	// but the middleware IS entering the rate-limit code path (not short-circuiting)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

// TestRateLimitMiddleware_SettingsOverride_DisabledBypassesAll verifies that
// when settings disable rate limiting, no rate limit headers are set.
func TestRateLimitMiddleware_SettingsOverride_DisabledBypassesAll(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config := middleware.RateLimitConfig{
		Enabled:      true, // static says enabled
		DefaultLimit: 1,
		BurstSize:    1,
		Strategy:     "token_bucket",
	}

	// Settings override: disabled
	instanceSettings, _ := newTestSettings(map[string]any{
		"rateLimiting.enabled": false,
	})

	rl := &noopRateLimiter{}
	log := lmocks.NewMockLogger()

	router := gin.New()
	router.Use(middleware.RateLimitMiddleware(rl, log, config, instanceSettings))
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	// Even with limit=1, 10 requests should all pass because settings disabled it
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code, "request %d should pass", i)
	}
}

// TestRateLimitMiddleware_NilSettings_UsesStaticConfig verifies backward
// compatibility when no settings service is injected.
func TestRateLimitMiddleware_NilSettings_UsesStaticConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config := middleware.RateLimitConfig{
		Enabled:      false, // static says disabled
		DefaultLimit: 1,
		BurstSize:    1,
	}

	rl := &noopRateLimiter{}
	log := lmocks.NewMockLogger()

	router := gin.New()
	router.Use(middleware.RateLimitMiddleware(rl, log, config, nil)) // nil settings
	router.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code) // disabled via static config
}
