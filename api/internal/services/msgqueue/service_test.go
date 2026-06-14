package msgqueue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestService(t *testing.T) (*Service, *miniredis.Miniredis, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewWithClient(client)
	return svc, mr, func() {
		_ = client.Close()
		mr.Close()
	}
}

func TestEnqueue_Dequeue_FIFO(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id1, err := svc.Enqueue(ctx, "ws-1", "ses-1", "first")
	require.NoError(t, err)
	assert.NotEmpty(t, id1)

	id2, err := svc.Enqueue(ctx, "ws-1", "ses-1", "second")
	require.NoError(t, err)
	assert.NotEmpty(t, id2)

	msg1, err := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	require.NotNil(t, msg1)
	assert.Equal(t, "first", msg1.Text)
	assert.Equal(t, id1, msg1.ID)

	msg2, err := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	require.NotNil(t, msg2)
	assert.Equal(t, "second", msg2.Text)
	assert.Equal(t, id2, msg2.ID)

	msg3, err := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	assert.Nil(t, msg3)
}

func TestDequeue_EmptyQueue(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	msg, err := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	assert.Nil(t, msg)
}

func TestLen(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	n, err := svc.Len(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "a")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "b")

	n, err = svc.Len(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestPeekAll(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "a")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "b")

	msgs, err := svc.PeekAll(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, "a", msgs[0].Text)
	assert.Equal(t, "b", msgs[1].Text)
}

func TestClear(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "a")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "b")

	err := svc.Clear(ctx, "ws-1", "ses-1")
	require.NoError(t, err)

	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(0), n)
}

func TestRequeue(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id1, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "first")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "second")

	msg, _ := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.Equal(t, "first", msg.Text)

	err := svc.Requeue(ctx, "ws-1", "ses-1", QueuedMessage{
		ID:        id1,
		Text:      msg.Text,
		SessionID: "ses-1",
	})
	require.NoError(t, err)

	msgs, _ := svc.PeekAll(ctx, "ws-1", "ses-1")
	require.Len(t, msgs, 2)
	assert.Equal(t, "first", msgs[0].Text, "requeued message should be at front")
	assert.Equal(t, "second", msgs[1].Text)
}

func TestSessionIsolation(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-A", "for A")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-B", "for B")

	msg, _ := svc.Dequeue(ctx, "ws-1", "ses-A")
	require.NotNil(t, msg)
	assert.Equal(t, "for A", msg.Text)

	msg, _ = svc.Dequeue(ctx, "ws-1", "ses-A")
	assert.Nil(t, msg, "ses-A should be empty")

	msg, _ = svc.Dequeue(ctx, "ws-1", "ses-B")
	require.NotNil(t, msg)
	assert.Equal(t, "for B", msg.Text)
}

func TestWorkspaceIsolation(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "for ws-1")
	_, _ = svc.Enqueue(ctx, "ws-2", "ses-1", "for ws-2")

	msg, _ := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NotNil(t, msg)
	assert.Equal(t, "for ws-1", msg.Text)

	msg, _ = svc.Dequeue(ctx, "ws-2", "ses-1")
	require.NotNil(t, msg)
	assert.Equal(t, "for ws-2", msg.Text)
}

func TestEnqueuedMessageFields(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id, err := svc.Enqueue(ctx, "ws-1", "ses-1", "hello world")
	require.NoError(t, err)

	msg, err := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NoError(t, err)
	require.NotNil(t, msg)

	assert.Equal(t, id, msg.ID)
	assert.Equal(t, "hello world", msg.Text)
	assert.Equal(t, "ses-1", msg.SessionID)
	assert.Equal(t, "ws-1", msg.WorkspaceID)
	assert.False(t, msg.EnqueuedAt.IsZero())
	assert.Equal(t, 0, msg.RetryCount)
}

func TestRetryCountIncrement(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "retry me")
	msg, _ := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NotNil(t, msg)
	assert.Equal(t, 0, msg.RetryCount)

	msg.RetryCount = 3
	err := svc.Requeue(ctx, "ws-1", "ses-1", *msg)
	require.NoError(t, err)

	msg2, _ := svc.Dequeue(ctx, "ws-1", "ses-1")
	require.NotNil(t, msg2)
	assert.Equal(t, id, msg2.ID)
	assert.Equal(t, 3, msg2.RetryCount)
}

func TestTTLExpiry(t *testing.T) {
	svc, mr, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "ephemeral")

	mr.FastForward(keyTTL + time.Second)

	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(0), n, "queue should expire after TTL")
}
