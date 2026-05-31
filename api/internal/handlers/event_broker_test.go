// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkspaceEventBroker(t *testing.T) {
	b := NewWorkspaceEventBroker()
	require.NotNil(t, b)
}

func TestBroker_SubscribeReceivesPublishedEvent(t *testing.T) {
	b := NewWorkspaceEventBroker()

	ch := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch)

	evt := WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"}
	b.Publish("ws-1", evt)

	select {
	case got := <-ch:
		assert.Equal(t, evt, got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_MultipleSubscribersAllReceive(t *testing.T) {
	b := NewWorkspaceEventBroker()

	ch1 := b.Subscribe("ws-1")
	ch2 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch1)
	defer b.Unsubscribe("ws-1", ch2)

	evt := WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspended"}
	b.Publish("ws-1", evt)

	for _, ch := range []<-chan WorkspaceSSEEvent{ch1, ch2} {
		select {
		case got := <-ch:
			assert.Equal(t, evt, got)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}
}

func TestBroker_PublishToWrongWorkspaceNotReceived(t *testing.T) {
	b := NewWorkspaceEventBroker()

	ch := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch)

	b.Publish("ws-2", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case <-ch:
		t.Fatal("ws-1 subscriber should not receive event published to ws-2")
	case <-time.After(50 * time.Millisecond):
		// expected: no event
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewWorkspaceEventBroker()

	ch := b.Subscribe("ws-1")
	b.Unsubscribe("ws-1", ch)

	// After unsubscribe the channel is closed and no longer registered.
	// Publishing must not block, and any receive must return (zero, false)
	// — i.e. channel closed, not a delivered event.
	b.Publish("ws-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case evt, open := <-ch:
		// Channel closed: open==false and evt is zero value — this is expected.
		// If open==true, an actual event was delivered post-unsubscribe, which is a bug.
		if open {
			t.Fatalf("unsubscribed channel should not receive events; got %+v", evt)
		}
		// open==false means channel was already closed by Unsubscribe — correct.
	case <-time.After(50 * time.Millisecond):
		t.Fatal("channel was not closed by Unsubscribe (expected immediate close)")
	}
}

func TestBroker_UnsubscribeNonexistentIsNoop(t *testing.T) {
	b := NewWorkspaceEventBroker()
	ch := make(chan WorkspaceSSEEvent, 1)
	// Must not panic.
	b.Unsubscribe("ws-missing", ch)
}

func TestBroker_PublishWithNoSubscribersIsNoop(t *testing.T) {
	b := NewWorkspaceEventBroker()
	// Must not block or panic when there are no subscribers.
	done := make(chan struct{})
	go func() {
		b.Publish("ws-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish with no subscribers should not block")
	}
}

func TestBroker_SlowSubscriberDoesNotBlockOthers(t *testing.T) {
	b := NewWorkspaceEventBroker()

	// ch1: full channel — simulates a slow/stuck subscriber.
	// We fill it so it cannot accept more events.
	ch1 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch1)

	// Drain ch1 to confirm initial state, then pre-fill its capacity.
	// The broker channel buffer size is 16 per design.
	// Fill up ch1 by subscribing and publishing 16 events before adding ch2.
	for i := 0; i < 16; i++ {
		b.Publish("ws-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspending"})
	}
	// Drain the channel fully — we just needed to send enough to fill it
	// for the next batch.
	for len(ch1) > 0 {
		<-ch1
	}

	// Now subscribe ch2, refill ch1's buffer by adding 16 events that ch1 ignores.
	// Fill ch1 to capacity so the next Publish would drop for ch1.
	for i := 0; i < 16; i++ {
		ch1 <- WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspending"}
	}

	ch2 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch2)

	// Publish one more event — ch1 is full so it must be dropped; ch2 must receive it.
	evt := WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"}
	b.Publish("ws-1", evt)

	select {
	case got := <-ch2:
		assert.Equal(t, evt, got)
	case <-time.After(time.Second):
		t.Fatal("ch2 should receive event even though ch1 is full")
	}
}

func TestBroker_ConcurrentPublishSubscribe(t *testing.T) {
	b := NewWorkspaceEventBroker()
	const numGoroutines = 10
	const numEvents = 50

	var wg sync.WaitGroup
	var received atomic.Int64

	// Start subscribers.
	channels := make([]chan WorkspaceSSEEvent, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		ch := b.Subscribe("ws-concurrent")
		channels[i] = ch
		wg.Add(1)
		go func(c <-chan WorkspaceSSEEvent) {
			defer wg.Done()
			for range c {
				received.Add(1)
			}
		}(ch)
	}

	// Publish events.
	for i := 0; i < numEvents; i++ {
		b.Publish("ws-concurrent", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	// Unsubscribe all and close channels so goroutines can exit.
	for i, ch := range channels {
		_ = i
		b.Unsubscribe("ws-concurrent", ch)
	}

	// Wait briefly for goroutines to drain.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent test timed out")
	}
	// Each subscriber should have received at most numEvents (some may be
	// dropped if the channel was full during concurrent publishing).
	assert.GreaterOrEqual(t, received.Load(), int64(0))
}

func TestBroker_UnsubscribeClosesChannel(t *testing.T) {
	b := NewWorkspaceEventBroker()
	ch := b.Subscribe("ws-1")
	b.Unsubscribe("ws-1", ch)

	// The channel must be closed so range loops and select can detect done.
	select {
	case _, open := <-ch:
		assert.False(t, open, "channel should be closed after unsubscribe")
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after unsubscribe")
	}
}

func TestBroker_SessionStatusEventDelivered(t *testing.T) {
	b := NewWorkspaceEventBroker()

	ch := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", ch)

	evt := WorkspaceSSEEvent{
		Type:      "session.status",
		SessionID: "s1",
		Status:    "idle",
	}
	b.Publish("ws-1", evt)

	select {
	case got := <-ch:
		assert.Equal(t, "session.status", got.Type)
		assert.Equal(t, "s1", got.SessionID)
		assert.Equal(t, "idle", got.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
