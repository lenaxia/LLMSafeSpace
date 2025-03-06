package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Service handles Redis cache operations
type Service struct {
	logger *logger.Logger
	config *config.Config
	client *redis.Client
}

// New creates a new cache service
func New(cfg *config.Config, log *logger.Logger) (*Service, error) {
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Service{
		logger: log,
		config: cfg,
		client: client,
	}, nil
}

// Start starts the cache service
func (s *Service) Start() error {
	s.logger.Info("Cache service started")
	return nil
}

// Stop stops the cache service
func (s *Service) Stop() error {
	s.logger.Info("Stopping cache service")
	return s.client.Close()
}

// Ping checks the Redis connection
func (s *Service) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// Get gets a value from the cache
func (s *Service) Get(ctx context.Context, key string) (string, error) {
	val, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed to get value from cache: %w", err)
	}
	return val, nil
}

// Set sets a value in the cache
func (s *Service) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	err := s.client.Set(ctx, key, value, expiration).Err()
	if err != nil {
		return fmt.Errorf("failed to set value in cache: %w", err)
	}
	return nil
}

// Delete deletes a value from the cache
func (s *Service) Delete(ctx context.Context, key string) error {
	err := s.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to delete value from cache: %w", err)
	}
	return nil
}

// GetObject gets an object from the cache and unmarshals it into the provided value
func (s *Service) GetObject(ctx context.Context, key string, value interface{}) error {
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get object from cache: %w", err)
	}

	err = redis.UnmarshalBinary(data, value)
	if err != nil {
		return fmt.Errorf("failed to unmarshal object from cache: %w", err)
	}

	return nil
}

// SetObject sets an object in the cache
func (s *Service) SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := redis.MarshalBinary(value)
	if err != nil {
		return fmt.Errorf("failed to marshal object for cache: %w", err)
	}

	err = s.client.Set(ctx, key, data, expiration).Err()
	if err != nil {
		return fmt.Errorf("failed to set object in cache: %w", err)
	}

	return nil
}

// GetSession gets a session from the cache
func (s *Service) GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	var session map[string]interface{}
	err := s.GetObject(ctx, fmt.Sprintf("session:%s", sessionID), &session)
	if err != nil {
		return nil, fmt.Errorf("failed to get session from cache: %w", err)
	}
	return session, nil
}

// SetSession sets a session in the cache
func (s *Service) SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error {
	err := s.SetObject(ctx, fmt.Sprintf("session:%s", sessionID), session, expiration)
	if err != nil {
		return fmt.Errorf("failed to set session in cache: %w", err)
	}
	return nil
}

// DeleteSession deletes a session from the cache
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	err := s.Delete(ctx, fmt.Sprintf("session:%s", sessionID))
	if err != nil {
		return fmt.Errorf("failed to delete session from cache: %w", err)
	}
	return nil
}
