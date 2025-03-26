package interfaces

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestMockCacheService(t *testing.T) {
	ctx := context.Background()
	mockCache := &MockCacheService{}

	// Setup expectations
	mockCache.On("Get", ctx, "test-key").Return("test-value", nil)
	mockCache.On("Set", ctx, "test-key", "test-value", 10*time.Minute).Return(nil)
	mockCache.On("Exists", ctx, "test-key").Return(true, nil)

	// Test the mock
	val, err := mockCache.Get(ctx, "test-key")
	assert.NoError(t, err)
	assert.Equal(t, "test-value", val)

	err = mockCache.Set(ctx, "test-key", "test-value", 10*time.Minute)
	assert.NoError(t, err)

	exists, err := mockCache.Exists(ctx, "test-key")
	assert.NoError(t, err)
	assert.True(t, exists)

	// Verify all expectations were met
	mockCache.AssertExpectations(t)
}
