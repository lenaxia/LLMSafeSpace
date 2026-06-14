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
	keyPrefix = "llmsafespace:msgqueue:"
	keyTTL    = 24 * time.Hour
)

type QueuedMessage struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	SessionID   string    `json:"session_id"`
	WorkspaceID string    `json:"workspace_id"`
	EnqueuedAt  time.Time `json:"enqueued_at"`
	RetryCount  int       `json:"retry_count"`
}

type Service struct {
	client *redis.Client
}

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
			continue
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
