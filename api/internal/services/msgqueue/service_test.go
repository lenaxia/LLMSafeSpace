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

func TestClearWorkspace(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-A", "for A")
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-B", "for B")
	_, _ = svc.Enqueue(ctx, "ws-2", "ses-C", "for ws-2")

	err := svc.ClearWorkspace(ctx, "ws-1")
	require.NoError(t, err)

	n, _ := svc.Len(ctx, "ws-1", "ses-A")
	assert.Equal(t, int64(0), n, "ws-1 ses-A should be cleared")

	n, _ = svc.Len(ctx, "ws-1", "ses-B")
	assert.Equal(t, int64(0), n, "ws-1 ses-B should be cleared")

	n, _ = svc.Len(ctx, "ws-2", "ses-C")
	assert.Equal(t, int64(1), n, "ws-2 should be unaffected")
}

func TestClearWorkspace_EmptyWorkspace(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.ClearWorkspace(ctx, "nonexistent")
	require.NoError(t, err)
}

func TestRemove(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id1, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "first")
	id2, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "second")
	id3, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "third")

	err := svc.Remove(ctx, "ws-1", "ses-1", id2)
	require.NoError(t, err)

	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(2), n)

	msgs, _ := svc.PeekAll(ctx, "ws-1", "ses-1")
	require.Len(t, msgs, 2)
	assert.Equal(t, id1, msgs[0].ID)
	assert.Equal(t, id3, msgs[1].ID)
}

func TestRemove_NotFound(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "only")

	err := svc.Remove(ctx, "ws-1", "ses-1", "nonexistent_id")
	require.NoError(t, err, "Remove should be idempotent — no error on not found")

	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(1), n)
}

func TestPeekAllWorkspace_MultiSession(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id1, err := svc.Enqueue(ctx, "ws-1", "ses-A", "msg1")
	require.NoError(t, err)
	id2, err := svc.Enqueue(ctx, "ws-1", "ses-B", "msg2")
	require.NoError(t, err)
	id3, err := svc.Enqueue(ctx, "ws-1", "ses-A", "msg3") // second message in ses-A
	require.NoError(t, err)

	msgs, err := svc.PeekAllWorkspace(ctx, "ws-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 3)

	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ID] = true
	}
	assert.True(t, ids[id1])
	assert.True(t, ids[id2])
	assert.True(t, ids[id3])

	// Verify: a different workspace's messages are not included.
	_, _ = svc.Enqueue(ctx, "ws-2", "ses-X", "other ws")
	msgs, err = svc.PeekAllWorkspace(ctx, "ws-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 3, "ws-2 messages should not appear in ws-1 results")
}

func TestPeekAllWorkspace_Empty(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	msgs, err := svc.PeekAllWorkspace(context.Background(), "nonexistent-ws")
	require.NoError(t, err)
	assert.Empty(t, msgs)
}
