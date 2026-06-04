// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUserEventBroker(t *testing.T) {
	b := NewUserEventBroker()
	require.NotNil(t, b)
}

func TestUserBroker_SubscribeAndPublish(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s)

	evt := WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active", WorkspaceID: "ws-1"}
	b.PublishToUser("user-1", evt)

	select {
	case got := <-s.ch:
		assert.Equal(t, "workspace.phase", got.Type)
		assert.Equal(t, "Active", got.Phase)
		assert.Equal(t, "ws-1", got.WorkspaceID)
		assert.Equal(t, uint64(1), got.EventID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestUserBroker_EventIDMonotonicallyIncreasing(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s)

	for i := 0; i < 5; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	var lastID uint64
	for i := 0; i < 5; i++ {
		select {
		case got := <-s.ch:
			assert.Greater(t, got.EventID, lastID)
			lastID = got.EventID
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}
}

func TestUserBroker_PerUserIsolation(t *testing.T) {
	b := NewUserEventBroker()

	s1, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s1)

	s2, err := b.SubscribeUser("user-2")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-2", s2)

	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case <-s1.ch:
		// expected
	case <-time.After(time.Second):
		t.Fatal("user-1 did not receive event")
	}

	select {
	case <-s2.ch:
		t.Fatal("user-2 should NOT receive user-1's event")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestUserBroker_MultipleSubscribersForSameUser(t *testing.T) {
	b := NewUserEventBroker()

	s1, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s1)

	s2, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s2)

	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	for _, s := range []*subscriber{s1, s2} {
		select {
		case got := <-s.ch:
			assert.Equal(t, "Active", got.Phase)
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestUserBroker_ErrTooManySubscribers(t *testing.T) {
	b := NewUserEventBroker()

	subs := make([]*subscriber, 0, maxSubscribersPerUser)
	for i := 0; i < maxSubscribersPerUser; i++ {
		s, err := b.SubscribeUser("user-1")
		require.NoError(t, err)
		subs = append(subs, s)
	}
	defer func() {
		for _, s := range subs {
			b.UnsubscribeUser("user-1", s)
		}
	}()

	_, err := b.SubscribeUser("user-1")
	assert.ErrorIs(t, err, ErrTooManySubscribers)
}

func TestUserBroker_UnsubscribeClosesChannel(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	b.UnsubscribeUser("user-1", s)

	// After unsubscribe, subscriber is marked closed — send is a no-op
	assert.True(t, s.closed.Load())

	// Publishing after unsubscribe must not deliver to this subscriber
	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case <-s.ch:
		t.Fatal("should not receive events after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestUserBroker_ReplayBasic(t *testing.T) {
	b := NewUserEventBroker()

	// Publish 5 events with no subscriber (they go to replay buffer only)
	for i := 0; i < 5; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{
			Type:  "workspace.phase",
			Phase: "Active",
		})
	}

	entries, gapDetected := b.Replay("user-1", 0)
	assert.False(t, gapDetected)
	assert.Len(t, entries, 5)
	assert.Equal(t, uint64(1), entries[0].ID)
	assert.Equal(t, uint64(5), entries[4].ID)
}

func TestUserBroker_ReplaySince(t *testing.T) {
	b := NewUserEventBroker()

	for i := 0; i < 5; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase"})
	}

	entries, gapDetected := b.Replay("user-1", 3)
	assert.False(t, gapDetected)
	assert.Len(t, entries, 2)
	assert.Equal(t, uint64(4), entries[0].ID)
	assert.Equal(t, uint64(5), entries[1].ID)
}

func TestUserBroker_ReplayGapDetection(t *testing.T) {
	b := NewUserEventBroker()

	// Fill beyond buffer size to cause wrap
	for i := 0; i < replayBufferSize+50; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase"})
	}

	// Ask for events since ID 1 — those are long gone
	entries, gapDetected := b.Replay("user-1", 1)
	assert.True(t, gapDetected)
	assert.Len(t, entries, replayBufferSize)
}

func TestUserBroker_ReplayNoGapWhenLastIDIsZero(t *testing.T) {
	b := NewUserEventBroker()

	// Fill beyond buffer
	for i := 0; i < replayBufferSize+10; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase"})
	}

	// lastID=0 means "give me all buffered" — not a gap
	entries, gapDetected := b.Replay("user-1", 0)
	assert.False(t, gapDetected)
	assert.Len(t, entries, replayBufferSize)
}

func TestUserBroker_ReplayEmptyUser(t *testing.T) {
	b := NewUserEventBroker()

	entries, gapDetected := b.Replay("nonexistent", 5)
	assert.False(t, gapDetected)
	assert.Empty(t, entries)
}

func TestUserBroker_OverflowSendsResync(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s)

	// Fill the subscriber channel completely
	for i := 0; i < userChannelBuffer; i++ {
		b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	// This publish should trigger overflow (channel full)
	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspended"})

	// Drain the channel — the first events are the buffered ones
	for i := 0; i < userChannelBuffer; i++ {
		<-s.ch
	}

	// Now send another event — it should be preceded by resync
	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case got := <-s.ch:
		assert.Equal(t, "resync", got.Type)
	case <-time.After(time.Second):
		t.Fatal("expected resync event")
	}
}

func TestUserBroker_SendToClosedChannelDoesNotPanic(t *testing.T) {
	// FP1: verify s.send() safely handles a closed subscriber
	s := &subscriber{ch: make(chan WorkspaceSSEEvent, 1)}
	s.markClosed()

	// Must not panic and must not send
	assert.NotPanics(t, func() {
		s.send(WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	})

	// Channel should be empty since send was a no-op
	select {
	case <-s.ch:
		t.Fatal("should not have sent to closed subscriber")
	default:
		// expected
	}
}

func TestUserBroker_ConcurrentPublishMonotonicOrder(t *testing.T) {
	b := NewUserEventBroker()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase"})
		}()
	}
	wg.Wait()

	entries, _ := b.Replay("user-1", 0)
	assert.Len(t, entries, n)

	// Verify monotonic ordering
	for i := 1; i < len(entries); i++ {
		assert.Greater(t, entries[i].ID, entries[i-1].ID,
			"entries should be monotonically ordered")
	}
}

func TestUserBroker_WorkspaceSubscribeAndPublish(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeWorkspace("ws-1")
	require.NoError(t, err)
	defer b.UnsubscribeWorkspace("ws-1", s)

	evt := WorkspaceSSEEvent{Type: "session.status", SessionID: "s1", Status: "busy", WorkspaceID: "ws-1"}
	b.PublishToWorkspace("ws-1", evt)

	select {
	case got := <-s.ch:
		assert.Equal(t, "session.status", got.Type)
		assert.Equal(t, "s1", got.SessionID)
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestUserBroker_WorkspaceIsolation(t *testing.T) {
	b := NewUserEventBroker()

	s1, err := b.SubscribeWorkspace("ws-1")
	require.NoError(t, err)
	defer b.UnsubscribeWorkspace("ws-1", s1)

	s2, err := b.SubscribeWorkspace("ws-2")
	require.NoError(t, err)
	defer b.UnsubscribeWorkspace("ws-2", s2)

	b.PublishToWorkspace("ws-1", WorkspaceSSEEvent{Type: "session.status"})

	select {
	case <-s1.ch:
	case <-time.After(time.Second):
		t.Fatal("ws-1 subscriber timed out")
	}

	select {
	case <-s2.ch:
		t.Fatal("ws-2 should not receive ws-1 events")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserBroker_RecordAndLookupWorkspaceOwner(t *testing.T) {
	b := NewUserEventBroker()

	b.RecordWorkspaceOwner("ws-1", "user-1")
	b.RecordWorkspaceOwner("ws-2", "user-2")

	assert.Equal(t, "user-1", b.WorkspaceOwner("ws-1"))
	assert.Equal(t, "user-2", b.WorkspaceOwner("ws-2"))
	assert.Equal(t, "", b.WorkspaceOwner("ws-unknown"))
}

func TestUserBroker_CleanupWorkspace(t *testing.T) {
	b := NewUserEventBroker()

	b.RecordWorkspaceOwner("ws-1", "user-1")
	b.CleanupWorkspace("ws-1")

	assert.Equal(t, "", b.WorkspaceOwner("ws-1"))
}

func TestUserBroker_CleanupUser(t *testing.T) {
	b := NewUserEventBroker()

	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase"})

	entries, _ := b.Replay("user-1", 0)
	assert.Len(t, entries, 1)

	b.CleanupUser("user-1")

	entries, _ = b.Replay("user-1", 0)
	assert.Empty(t, entries)
}

func TestUserBroker_ShardedIsolation(t *testing.T) {
	b := NewUserEventBroker()

	// Use users that hash to different shards
	s1, err := b.SubscribeUser("alice")
	require.NoError(t, err)
	defer b.UnsubscribeUser("alice", s1)

	s2, err := b.SubscribeUser("bob")
	require.NoError(t, err)
	defer b.UnsubscribeUser("bob", s2)

	b.PublishToUser("alice", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case <-s1.ch:
	case <-time.After(time.Second):
		t.Fatal("alice should receive")
	}

	select {
	case <-s2.ch:
		t.Fatal("bob should not receive alice's event")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserBroker_ConcurrentSubscribePublishRace(t *testing.T) {
	// Run under -race detector: validates no data races in the
	// subscribe → publish → unsubscribe lifecycle.
	b := NewUserEventBroker()
	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Publish goroutines
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.PublishToUser("user-race", WorkspaceSSEEvent{Type: "workspace.phase"})
		}()
	}

	// Subscribe/drain/unsubscribe goroutines — each has its own lifecycle
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s, err := b.SubscribeUser("user-race")
			if err != nil {
				return // at limit, ok
			}
			// Drain a few events
			for j := 0; j < 3; j++ {
				select {
				case <-s.ch:
				case <-time.After(5 * time.Millisecond):
				}
			}
			b.UnsubscribeUser("user-race", s)
		}()
	}

	wg.Wait()
}

func TestUserBroker_PublishToUserSetsEventIDOnDeliveredEvent(t *testing.T) {
	b := NewUserEventBroker()

	s, err := b.SubscribeUser("user-1")
	require.NoError(t, err)
	defer b.UnsubscribeUser("user-1", s)

	b.PublishToUser("user-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case got := <-s.ch:
		assert.NotZero(t, got.EventID, "event delivered to subscriber must have event_id set")
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestUserBroker_UnsubscribeReleasesSlot(t *testing.T) {
	b := NewUserEventBroker()

	// Fill to max
	subs := make([]*subscriber, maxSubscribersPerUser)
	for i := 0; i < maxSubscribersPerUser; i++ {
		s, err := b.SubscribeUser("user-1")
		require.NoError(t, err)
		subs[i] = s
	}

	// At limit
	_, err := b.SubscribeUser("user-1")
	assert.ErrorIs(t, err, ErrTooManySubscribers)

	// Unsubscribe one
	b.UnsubscribeUser("user-1", subs[0])

	// Should succeed now
	s, err := b.SubscribeUser("user-1")
	assert.NoError(t, err)
	b.UnsubscribeUser("user-1", s)

	// Clean up rest
	for i := 1; i < maxSubscribersPerUser; i++ {
		b.UnsubscribeUser("user-1", subs[i])
	}
}
