package interfaces

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCacheServiceInterface(t *testing.T) {
	var cs CacheService = &MockCacheService{}
	assert.NotNil(t, cs)
}

func TestCacheServiceMethods(t *testing.T) {
	ctx := context.Background()
	mock := &MockCacheService{}

	// Test Get
	mock.On("Get", ctx, "key").Return("value", nil)
	val, err := mock.Get(ctx, "key")
	assert.NoError(t, err)
	assert.Equal(t, "value", val)

	// Test Set
	mock.On("Set", ctx, "key", "value", 5*time.Minute).Return(nil)
	err = mock.Set(ctx, "key", "value", 5*time.Minute)
	assert.NoError(t, err)

	// Test CreateSession
	sessionData := map[string]string{"user": "test"}
	mock.On("CreateSession", ctx, "user1", sessionData, time.Hour).Return("session123", nil)
	sessionID, err := mock.CreateSession(ctx, "user1", sessionData, time.Hour)
	assert.NoError(t, err)
	assert.Equal(t, "session123", sessionID)

	mock.AssertExpectations(t)
}
