package msgqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	keyPrefix = "llmsafespaces:msgqueue:"
	keyTTL    = 24 * time.Hour
)

// QueuedMessage represents a message held in the Redis-backed queue.
type QueuedMessage struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	SessionID   string    `json:"session_id"`
	WorkspaceID string    `json:"workspace_id"`
	EnqueuedAt  time.Time `json:"enqueued_at"`
	RetryCount  int       `json:"retry_count"`
}

// Service provides a Redis-backed FIFO message queue per workspace+session.
type Service struct {
	client *redis.Client
}

// NewWithClient creates a queue Service backed by the given Redis client.
// The client is borrowed — its lifecycle is managed by the caller.
func NewWithClient(client *redis.Client) *Service {
	return &Service{client: client}
}

func queueKey(workspaceID, sessionID string) string {
	return fmt.Sprintf("%s%s:%s", keyPrefix, workspaceID, sessionID)
}

func (s *Service) Enqueue(ctx context.Context, workspaceID, sessionID, text string) (string, error) {
	msg := QueuedMessage{
		ID:          "msg_q_" + uuid.NewString(),
		Text:        text,
		SessionID:   sessionID,
		WorkspaceID: workspaceID,
		EnqueuedAt:  time.Now().UTC(),
		RetryCount:  0,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshaling queued message: %w", err)
	}
	key := queueKey(workspaceID, sessionID)
	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, key, data)
	pipe.Expire(ctx, key, keyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("enqueueing message: %w", err)
	}
	return msg.ID, nil
}

func (s *Service) Dequeue(ctx context.Context, workspaceID, sessionID string) (*QueuedMessage, error) {
	data, err := s.client.LPop(ctx, queueKey(workspaceID, sessionID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeuing message: %w", err)
	}
	var msg QueuedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling queued message: %w", err)
	}
	return &msg, nil
}

func (s *Service) Requeue(ctx context.Context, workspaceID, sessionID string, msg QueuedMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling requeued message: %w", err)
	}
	key := queueKey(workspaceID, sessionID)
	pipe := s.client.TxPipeline()
	pipe.LPush(ctx, key, data)
	pipe.Expire(ctx, key, keyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("requeueing message: %w", err)
	}
	return nil
}

func (s *Service) PeekAll(ctx context.Context, workspaceID, sessionID string) ([]QueuedMessage, error) {
	data, err := s.client.LRange(ctx, queueKey(workspaceID, sessionID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("peeking queue: %w", err)
	}
	msgs := make([]QueuedMessage, 0, len(data))
	for _, d := range data {
		var msg QueuedMessage
		if err := json.Unmarshal([]byte(d), &msg); err != nil {
			return nil, fmt.Errorf("unmarshaling queued message from list: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (s *Service) Len(ctx context.Context, workspaceID, sessionID string) (int64, error) {
	n, err := s.client.LLen(ctx, queueKey(workspaceID, sessionID)).Result()
	if err != nil {
		return 0, fmt.Errorf("getting queue length: %w", err)
	}
	return n, nil
}

func (s *Service) Remove(ctx context.Context, workspaceID, sessionID, messageID string) error {
	data, err := s.client.LRange(ctx, queueKey(workspaceID, sessionID), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("listing queue for remove: %w", err)
	}
	for _, d := range data {
		var msg QueuedMessage
		if err := json.Unmarshal([]byte(d), &msg); err != nil {
			continue
		}
		if msg.ID == messageID {
			if err := s.client.LRem(ctx, queueKey(workspaceID, sessionID), 1, d).Err(); err != nil {
				return fmt.Errorf("removing message from queue: %w", err)
			}
			return nil
		}
	}
	return nil
}

func (s *Service) Clear(ctx context.Context, workspaceID, sessionID string) error {
	if err := s.client.Del(ctx, queueKey(workspaceID, sessionID)).Err(); err != nil {
		return fmt.Errorf("clearing queue: %w", err)
	}
	return nil
}

func (s *Service) ClearWorkspace(ctx context.Context, workspaceID string) error {
	pattern := fmt.Sprintf("%s%s:*", keyPrefix, workspaceID)
	iter := s.client.Scan(ctx, 0, pattern, 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("scanning workspace queue keys: %w", err)
	}
	if len(keys) > 0 {
		if err := s.client.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("deleting workspace queue keys: %w", err)
		}
	}
	return nil
}

// PeekAllGlobal returns ALL queued messages across every workspace and session.
// It scans Redis for all queue keys matching the global prefix and peeks each
// one. The order of messages across workspaces/sessions is undefined.
// Use this for global operations like the periodic stranded-queue sweep.
func (s *Service) PeekAllGlobal(ctx context.Context) ([]QueuedMessage, error) {
	return s.peekByPattern(ctx, fmt.Sprintf("%s*", keyPrefix))
}

// PeekAllWorkspace returns all queued messages across every session for the
// given workspace. It scans Redis for all queue keys belonging to the workspace
// and peeks each one. The order of messages across sessions is undefined.
func (s *Service) PeekAllWorkspace(ctx context.Context, workspaceID string) ([]QueuedMessage, error) {
	return s.peekByPattern(ctx, fmt.Sprintf("%s%s:*", keyPrefix, workspaceID))
}

func (s *Service) peekByPattern(ctx context.Context, pattern string) ([]QueuedMessage, error) {
	iter := s.client.Scan(ctx, 0, pattern, 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scanning queue keys with pattern %q: %w", pattern, err)
	}
	var all []QueuedMessage
	for _, key := range keys {
		data, err := s.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("peeking key %s: %w", key, err)
		}
		for _, d := range data {
			var msg QueuedMessage
			if err := json.Unmarshal([]byte(d), &msg); err != nil {
				continue
			}
			all = append(all, msg)
		}
	}
	return all, nil
}
