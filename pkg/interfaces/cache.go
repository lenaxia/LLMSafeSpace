package interfaces

import (
	"context"
	"time"
)

// CacheService defines the interface for caching operations
type CacheService interface {
	// Key-Value operations
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	// Session management  
	CreateSession(ctx context.Context, userID string, data map[string]string, ttl time.Duration) (string, error)
	GetSession(ctx context.Context, sessionID string) (map[string]string, error)
	RefreshSession(ctx context.Context, sessionID string, ttl time.Duration) error
	InvalidateSession(ctx context.Context, sessionID string) error

	// Utility methods
	Ping(ctx context.Context) error
	Close() error
}
