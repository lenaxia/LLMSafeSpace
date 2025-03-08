package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockRedis(t *testing.T) (*Service, *miniredis.Miniredis, func()) {
	// Create a mock Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err, "Failed to create mock Redis")

	// Create a mock logger
	mockLogger, err := logger.New(true, "debug", "console")
	require.NoError(t, err, "Failed to create mock logger")

	// Create a mock config
	mockConfig := &config.Config{}
	mockConfig.Redis.Host = mr.Host()
	mockConfig.Redis.Port, _ = strconv.Atoi(mr.Port())
	mockConfig.Redis.Password = ""
	mockConfig.Redis.DB = 0
	mockConfig.Redis.PoolSize = 10

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     mr.Addr(),
		Password: "",
		DB:       0,
		PoolSize: 10,
	})

	// Create the cache service with the mock Redis
	service := &Service{
		logger: mockLogger,
		config: mockConfig,
		client: client,
	}

	// Return the service, mock Redis, and a cleanup function
	return service, mr, func() {
		client.Close()
		mr.Close()
	}
}

// TestNew tests the New function
func TestNew(t *testing.T) {
	// Start a mock Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create test dependencies
	log, err := logger.New(true, "debug", "console")
	require.NoError(t, err)
	
	// Create a valid config
	cfg := &config.Config{}
	cfg.Redis.Host = mr.Host()
	cfg.Redis.Port, _ = strconv.Atoi(mr.Port())
	cfg.Redis.Password = ""
	cfg.Redis.DB = 0
	cfg.Redis.PoolSize = 10

	// Test successful creation
	service, err := New(cfg, log)
	assert.NoError(t, err)
	assert.NotNil(t, service)
	
	// Clean up
	err = service.Stop()
	assert.NoError(t, err)

	// Test connection failure
	badCfg := &config.Config{}
	badCfg.Redis.Host = "nonexistent"
	badCfg.Redis.Port = 6379
	
	service, err = New(badCfg, log)
	assert.Error(t, err)
	assert.Nil(t, service)
	assert.Contains(t, err.Error(), "failed to connect to Redis")
}

func TestPingCache(t *testing.T) {
	service, _, cleanup := setupMockRedis(t)
	defer cleanup()

	// Call the Ping method
	ctx := context.Background()
	err := service.Ping(ctx)
	assert.NoError(t, err, "Expected no error from Ping")
}

func TestGetSetDelete(t *testing.T) {
	service, mr, cleanup := setupMockRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := "test_key"
	value := "test_value"

	// Test Set
	err := service.Set(ctx, key, value, time.Minute)
	assert.NoError(t, err, "Expected no error from Set")

	// Verify value was set in mock Redis
	got, err := mr.Get(key)
	assert.NoError(t, err, "Expected no error getting value from miniredis")
	assert.Equal(t, value, got, "Expected value %q in Redis, got %q", value, got)

	// Test Get
	gotValue, err := service.Get(ctx, key)
	assert.NoError(t, err, "Expected no error from Get")
	assert.Equal(t, value, gotValue, "Expected value %q, got %q", value, gotValue)

	// Test Delete
	err = service.Delete(ctx, key)
	assert.NoError(t, err, "Expected no error from Delete")

	// Verify key was deleted
	assert.False(t, mr.Exists(key), "Expected key %q to be deleted", key)

	// Test Get non-existent key
	gotValue, err = service.Get(ctx, "non_existent_key")
	assert.NoError(t, err, "Expected no error for non-existent key")
	assert.Equal(t, "", gotValue, "Expected empty string for non-existent key")
}

func TestGetSetObject(t *testing.T) {
	service, _, cleanup := setupMockRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := "test_object_key"
	value := map[string]interface{}{
		"name":  "test",
		"value": 123,
		"tags":  []string{"tag1", "tag2"},
	}

	// Test SetObject
	err := service.SetObject(ctx, key, value, time.Minute)
	assert.NoError(t, err, "Expected no error from SetObject")

	// Test GetObject
	var retrievedValue map[string]interface{}
	err = service.GetObject(ctx, key, &retrievedValue)
	assert.NoError(t, err, "Expected no error from GetObject")

	// Check if retrieved value matches original
	assert.Equal(t, value["name"], retrievedValue["name"], "Expected name %v, got %v", value["name"], retrievedValue["name"])
	assert.Equal(t, float64(value["value"].(int)), retrievedValue["value"].(float64), "Expected value %v, got %v", value["value"], retrievedValue["value"])

	// Test GetObject for non-existent key
	var emptyValue map[string]interface{}
	err = service.GetObject(ctx, "non_existent_key", &emptyValue)
	assert.NoError(t, err, "Expected no error for non-existent key")
	assert.Nil(t, emptyValue, "Expected nil for non-existent key")
}

func TestSessionOperations(t *testing.T) {
	service, _, cleanup := setupMockRedis(t)
	defer cleanup()

	ctx := context.Background()
	sessionID := "test_session_id"
	sessionData := map[string]interface{}{
		"user_id":    "user123",
		"created_at": time.Now().Unix(),
		"data":       "session data",
	}

	// Test SetSession
	err := service.SetSession(ctx, sessionID, sessionData, time.Minute)
	assert.NoError(t, err, "Expected no error from SetSession")

	// Test GetSession
	retrievedSession, err := service.GetSession(ctx, sessionID)
	assert.NoError(t, err, "Expected no error from GetSession")
	assert.Equal(t, sessionData["user_id"], retrievedSession["user_id"], 
		"Expected user_id %v, got %v", sessionData["user_id"], retrievedSession["user_id"])

	// Test DeleteSession
	err = service.DeleteSession(ctx, sessionID)
	assert.NoError(t, err, "Expected no error from DeleteSession")

	// Verify session was deleted
	retrievedSession, err = service.GetSession(ctx, sessionID)
	assert.NoError(t, err, "Expected no error after DeleteSession")
	assert.Nil(t, retrievedSession, "Expected nil after DeleteSession")
}

func TestStartStop(t *testing.T) {
	service, _, cleanup := setupMockRedis(t)
	defer cleanup()

	// Test Start
	err := service.Start()
	assert.NoError(t, err, "Expected no error from Start")

	// Test Stop - this is already called in cleanup, but we test it explicitly
	err = service.Stop()
	assert.NoError(t, err, "Expected no error from Stop")
}
