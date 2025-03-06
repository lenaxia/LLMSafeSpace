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
)

func setupMockRedis(t *testing.T) (*Service, *miniredis.Miniredis, func()) {
	// Create a mock Redis server
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to create mock Redis: %v", err)
	}

	// Create a mock logger
	mockLogger, err := logger.New(true, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create mock logger: %v", err)
	}

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

func TestPingCache(t *testing.T) {
	service, _, cleanup := setupMockRedis(t)
	defer cleanup()

	// Call the Ping method
	ctx := context.Background()
	err := service.Ping(ctx)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestGetSetDelete(t *testing.T) {
	service, mr, cleanup := setupMockRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := "test_key"
	value := "test_value"

	// Test Set
	err := service.Set(ctx, key, value, time.Minute)
	if err != nil {
		t.Errorf("Expected no error from Set, got %v", err)
	}

	// Verify value was set in mock Redis
	if got, err := mr.Get(key); err != nil || got != value {
		t.Errorf("Expected value %q in Redis, got %q, err: %v", value, got, err)
	}

	// Test Get
	gotValue, err := service.Get(ctx, key)
	if err != nil {
		t.Errorf("Expected no error from Get, got %v", err)
	}
	if gotValue != value {
		t.Errorf("Expected value %q, got %q", value, gotValue)
	}

	// Test Delete
	err = service.Delete(ctx, key)
	if err != nil {
		t.Errorf("Expected no error from Delete, got %v", err)
	}

	// Verify key was deleted
	if mr.Exists(key) {
		t.Errorf("Expected key %q to be deleted", key)
	}

	// Test Get non-existent key
	gotValue, err = service.Get(ctx, "non_existent_key")
	if err != nil {
		t.Errorf("Expected no error for non-existent key, got %v", err)
	}
	if gotValue != "" {
		t.Errorf("Expected empty string for non-existent key, got %q", gotValue)
	}
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
	if err != nil {
		t.Errorf("Expected no error from SetObject, got %v", err)
	}

	// Test GetObject
	var retrievedValue map[string]interface{}
	err = service.GetObject(ctx, key, &retrievedValue)
	if err != nil {
		t.Errorf("Expected no error from GetObject, got %v", err)
	}

	// Check if retrieved value matches original
	if retrievedValue["name"] != value["name"] {
		t.Errorf("Expected name %v, got %v", value["name"], retrievedValue["name"])
	}
	if retrievedValue["value"] != value["value"] {
		t.Errorf("Expected value %v, got %v", value["value"], retrievedValue["value"])
	}

	// Test GetObject for non-existent key
	var emptyValue map[string]interface{}
	err = service.GetObject(ctx, "non_existent_key", &emptyValue)
	if err != nil {
		t.Errorf("Expected no error for non-existent key, got %v", err)
	}
	if emptyValue != nil {
		t.Errorf("Expected nil for non-existent key, got %v", emptyValue)
	}
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
	if err != nil {
		t.Errorf("Expected no error from SetSession, got %v", err)
	}

	// Test GetSession
	retrievedSession, err := service.GetSession(ctx, sessionID)
	if err != nil {
		t.Errorf("Expected no error from GetSession, got %v", err)
	}
	if retrievedSession["user_id"] != sessionData["user_id"] {
		t.Errorf("Expected user_id %v, got %v", sessionData["user_id"], retrievedSession["user_id"])
	}

	// Test DeleteSession
	err = service.DeleteSession(ctx, sessionID)
	if err != nil {
		t.Errorf("Expected no error from DeleteSession, got %v", err)
	}

	// Verify session was deleted
	retrievedSession, err = service.GetSession(ctx, sessionID)
	if err != nil {
		t.Errorf("Expected no error after DeleteSession, got %v", err)
	}
	if retrievedSession != nil {
		t.Errorf("Expected nil after DeleteSession, got %v", retrievedSession)
	}
}
