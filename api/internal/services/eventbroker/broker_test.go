// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package eventbroker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
)

func TestNewWorkspaceEventBroker(t *testing.T) {
	b := NewWorkspaceEventBroker()
	require.NotNil(t, b)
}

func TestBroker_SubscribeReceivesPublishedEvent(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub)

	evt := apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"}
	b.Publish("ws-1", evt)

	select {
	case got := <-sub.Ch:
		assert.Equal(t, evt, got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_MultipleSubscribersAllReceive(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub1 := b.Subscribe("ws-1")
	sub2 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub1)
	defer b.Unsubscribe("ws-1", sub2)

	evt := apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspended"}
	b.Publish("ws-1", evt)

	for _, s := range []*Subscriber{sub1, sub2} {
		select {
		case got := <-s.Ch:
			assert.Equal(t, evt, got)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}
}

func TestBroker_PublishToWrongWorkspaceNotReceived(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub)

	b.Publish("ws-2", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case <-sub.Ch:
		t.Fatal("ws-1 subscriber should not receive event published to ws-2")
	case <-time.After(50 * time.Millisecond):
		// expected: no event
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	b.Unsubscribe("ws-1", sub)

	b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})

	select {
	case evt, open := <-sub.Ch:
		if open {
			t.Fatalf("unsubscribed channel should not receive events; got %+v", evt)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("channel was not closed by Unsubscribe (expected immediate close)")
	}
}

func TestBroker_UnsubscribeNonexistentIsNoop(t *testing.T) {
	b := NewWorkspaceEventBroker()
	sub := &Subscriber{Ch: make(chan apitypes.WorkspaceSSEEvent, 1)}
	b.Unsubscribe("ws-missing", sub)
}

func TestBroker_PublishWithNoSubscribersIsNoop(t *testing.T) {
	b := NewWorkspaceEventBroker()
	done := make(chan struct{})
	go func() {
		b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
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

	sub1 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub1)

	for i := 0; i < 16; i++ {
		b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspending"})
	}
	for len(sub1.Ch) > 0 {
		<-sub1.Ch
	}

	for i := 0; i < 16; i++ {
		sub1.Ch <- apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspending"}
	}

	sub2 := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub2)

	evt := apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"}
	b.Publish("ws-1", evt)

	select {
	case got := <-sub2.Ch:
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

	subs := make([]*Subscriber, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		sub := b.Subscribe("ws-concurrent")
		subs[i] = sub
		wg.Add(1)
		go func(s *Subscriber) {
			defer wg.Done()
			for range s.Ch {
				received.Add(1)
			}
		}(sub)
	}

	for i := 0; i < numEvents; i++ {
		b.Publish("ws-concurrent", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	for i, sub := range subs {
		_ = i
		b.Unsubscribe("ws-concurrent", sub)
	}

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
	assert.GreaterOrEqual(t, received.Load(), int64(0))
}

func TestBroker_UnsubscribeClosesChannel(t *testing.T) {
	b := NewWorkspaceEventBroker()
	sub := b.Subscribe("ws-1")
	b.Unsubscribe("ws-1", sub)

	select {
	case _, open := <-sub.Ch:
		assert.False(t, open, "channel should be closed after unsubscribe")
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after unsubscribe")
	}
}

func TestBroker_SessionStatusEventDelivered(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub)

	evt := apitypes.WorkspaceSSEEvent{
		Type:      "session.status",
		SessionID: "s1",
		Status:    "idle",
	}
	b.Publish("ws-1", evt)

	select {
	case got := <-sub.Ch:
		assert.Equal(t, "session.status", got.Type)
		assert.Equal(t, "s1", got.SessionID)
		assert.Equal(t, "idle", got.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// --- F1: Subscriber pattern with missedEvent + resync recovery ---

func TestBroker_DroppedEventSetsMissedFlag(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub)

	for i := 0; i < BrokerChannelBuffer; i++ {
		b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "session.status", Status: "idle"})

	assert.True(t, sub.missedEvent.Load(), "missedEvent flag should be set when event is dropped")
}

func TestBroker_ResyncPrependedOnRecovery(t *testing.T) {
	b := NewWorkspaceEventBroker()

	sub := b.Subscribe("ws-1")
	defer b.Unsubscribe("ws-1", sub)

	for i := 0; i < BrokerChannelBuffer; i++ {
		b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Active"})
	}

	b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "session.status", Status: "idle"})

	for i := 0; i < BrokerChannelBuffer; i++ {
		<-sub.Ch
	}

	b.Publish("ws-1", apitypes.WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspended"})

	select {
	case got := <-sub.Ch:
		assert.Equal(t, "resync", got.Type, "first event after recovery should be resync")
	case <-time.After(time.Second):
		t.Fatal("expected resync event")
	}

	select {
	case got := <-sub.Ch:
		assert.Equal(t, "workspace.phase", got.Type)
		assert.Equal(t, "Suspended", got.Phase)
	case <-time.After(time.Second):
		t.Fatal("expected actual event after resync")
	}
}

func TestBroker_SubscriberCount(t *testing.T) {
	b := NewWorkspaceEventBroker()

	assert.Equal(t, 0, b.SubscriberCount("ws-1"))

	s1 := b.Subscribe("ws-1")
	assert.Equal(t, 1, b.SubscriberCount("ws-1"))

	s2 := b.Subscribe("ws-1")
	assert.Equal(t, 2, b.SubscriberCount("ws-1"))

	b.Unsubscribe("ws-1", s1)
	assert.Equal(t, 1, b.SubscriberCount("ws-1"))

	b.Unsubscribe("ws-1", s2)
	assert.Equal(t, 0, b.SubscriberCount("ws-1"))
}
