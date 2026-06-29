package msgqueue

import (
	"context"
	"strings"
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

func TestPeekAllGlobal_MultiWorkspace(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	id1, err := svc.Enqueue(ctx, "ws-1", "ses-A", "msg1")
	require.NoError(t, err)
	id2, err := svc.Enqueue(ctx, "ws-2", "ses-B", "msg2")
	require.NoError(t, err)
	id3, err := svc.Enqueue(ctx, "ws-1", "ses-A", "msg3")
	require.NoError(t, err)

	msgs, err := svc.PeekAllGlobal(ctx)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)

	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ID] = true
	}
	assert.True(t, ids[id1])
	assert.True(t, ids[id2])
	assert.True(t, ids[id3])
}

func TestPeekAllGlobal_Empty(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	msgs, err := svc.PeekAllGlobal(context.Background())
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

// TestEnqueue_IDFormatMatchesOpencode pins the queued message ID layout to
// opencode's Identifier.ascending("message") format:
//
//	"msg_" + 12 lowercase-hex chars + 14 base62 chars  (total 30 chars)
//
// We use this shape so queue IDs sort chronologically alongside opencode-
// generated history IDs in the frontend queue UI. The queue ID is NOT
// shipped to opencode as `messageID` on prompt_async (see
// proxy_events.go::promptRequestBody) — that field is omitted so opencode
// generates its own user-message ID via MessageID.ascending().
//
// See packages/opencode/src/id/id.ts for the upstream definition.
func TestEnqueue_IDFormatMatchesOpencode(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	id, err := svc.Enqueue(context.Background(), "ws-1", "ses-1", "hello")
	require.NoError(t, err)

	require.Len(t, id, 30, "ID must be msg_ + 26 body chars (opencode Identifier.ascending layout)")
	require.True(t, strings.HasPrefix(id, "msg_"), "ID must start with msg_")

	body := id[len("msg_"):]
	hexPart := body[:12]
	randPart := body[12:]

	for i, c := range hexPart {
		assert.True(t,
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"hex part char %d (%q) must be lowercase hex", i, c)
	}

	const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i, c := range randPart {
		assert.True(t, strings.ContainsRune(base62Alphabet, c),
			"random suffix char %d (%q) must be base62", i, c)
	}
}

// TestEnqueue_IDsAreTemporallyOrdered verifies that successive Enqueue calls
// produce IDs that lex-sort in creation order. This is the property the
// frontend queue UI relies on to render queue entries alongside opencode
// history entries without manual sorting. Same-millisecond ties resolve by
// random suffix, which is fine for display (the order within a millisecond
// is undefined in any case).
func TestEnqueue_IDsAreTemporallyOrdered(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	var prev string
	monotonicMisses := 0
	for i := 0; i < 100; i++ {
		id, err := svc.Enqueue(ctx, "ws-1", "ses-1", "x")
		require.NoError(t, err)
		_, err = svc.Dequeue(ctx, "ws-1", "ses-1")
		require.NoError(t, err)

		if prev != "" && id <= prev {
			// Same-ms case: hex prefix matches, random suffix decides.
			// Acceptable; just note it.
			if id[:16] == prev[:16] {
				monotonicMisses++
			} else {
				t.Fatalf("non-monotonic across millisecond boundary: prev=%q current=%q", prev, id)
			}
		}
		prev = id
		time.Sleep(50 * time.Microsecond)
	}
	t.Logf("same-ms unordered pairs: %d/99 (expected: common, since 50µs sleep typically does NOT cross a 1ms boundary)", monotonicMisses)
}

// TestEnqueue_LegacyUUIDFormatWouldRegress documents and pins the failure mode
// of the legacy "msg_q_" + UUID scheme that triggered the 2026-06-29 role-flip
// incident. The current implementation does NOT ship queue IDs to opencode,
// so this test is mainly archeological — it pins the historical failure
// mode in case anyone reintroduces both (a) supplying messageID on
// prompt_async AND (b) a UUID-style queue ID.
func TestEnqueue_LegacyUUIDFormatWouldRegress(t *testing.T) {
	// Sample opencode message IDs from a real session
	// (ses_0ed760478ffeQVPJGD5iEvRRmu, 2026-06-29).
	opencodeIDs := []string{
		"msg_f128a7599001h9w1qFEzg8Nv87",
		"msg_f128cb848001qefJU6L6hgzhao",
		"msg_f129165800011eRMYaAAr7w3mx",
	}
	legacyID := "msg_q_884a9b62-8a2f-47b2-80cc-3ae8eebdcecf"

	for _, ocID := range opencodeIDs {
		require.Greater(t, legacyID, ocID,
			"legacy scheme: %q should sort ABOVE %q (this is the bug)",
			legacyID, ocID)
	}
}
