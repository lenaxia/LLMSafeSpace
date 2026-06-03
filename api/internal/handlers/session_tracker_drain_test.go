// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSubscribeDrain_MultipleSubscribers_AllReceiveEvents(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)

	var mu sync.Mutex
	var received1, received2 []string

	cancel1 := tracker.SubscribeDrain("ws-1",
		func(_, sid string) { mu.Lock(); received1 = append(received1, "idle:"+sid); mu.Unlock() },
		func(_, sid string) { mu.Lock(); received1 = append(received1, "active:"+sid); mu.Unlock() },
	)
	cancel2 := tracker.SubscribeDrain("ws-1",
		func(_, sid string) { mu.Lock(); received2 = append(received2, "idle:"+sid); mu.Unlock() },
		func(_, sid string) { mu.Lock(); received2 = append(received2, "active:"+sid); mu.Unlock() },
	)
	defer cancel1()
	defer cancel2()

	tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
		`{"sessionID":"s1","status":{"type":"idle"}}`,
	))
	tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
		`{"sessionID":"s2","status":{"type":"busy"}}`,
	))

	time.Sleep(10 * time.Millisecond) // allow dispatch

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"idle:s1", "active:s2"}, received1)
	assert.Equal(t, []string{"idle:s1", "active:s2"}, received2)
}

func TestSubscribeDrain_Unsubscribe_StopsCallbacks(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)

	var received []string
	cancel := tracker.SubscribeDrain("ws-1",
		func(_, sid string) { received = append(received, "idle:"+sid) },
		func(_, sid string) { received = append(received, "active:"+sid) },
	)

	tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
		`{"sessionID":"s1","status":{"type":"idle"}}`,
	))
	assert.Len(t, received, 1)

	cancel()

	tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
		`{"sessionID":"s2","status":{"type":"idle"}}`,
	))
	// Should still be 1 — no new callback after cancel
	assert.Len(t, received, 1)
}

func TestSubscribeDrain_PerWorkspace_NoCrossTalk(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)

	var wsAEvents, wsBEvents []string
	cancelA := tracker.SubscribeDrain("ws-A",
		func(_, sid string) { wsAEvents = append(wsAEvents, sid) },
		func(_, sid string) { wsAEvents = append(wsAEvents, sid) },
	)
	cancelB := tracker.SubscribeDrain("ws-B",
		func(_, sid string) { wsBEvents = append(wsBEvents, sid) },
		func(_, sid string) { wsBEvents = append(wsBEvents, sid) },
	)
	defer cancelA()
	defer cancelB()

	tracker.dispatchProperties("ws-A", "session.status", json.RawMessage(
		`{"sessionID":"sA","status":{"type":"idle"}}`,
	))
	tracker.dispatchProperties("ws-B", "session.status", json.RawMessage(
		`{"sessionID":"sB","status":{"type":"busy"}}`,
	))

	assert.Equal(t, []string{"sA"}, wsAEvents)
	assert.Equal(t, []string{"sB"}, wsBEvents)
}

func TestSubscribeDrain_RetryTreatedAsActive(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)

	var events []string
	cancel := tracker.SubscribeDrain("ws-1",
		func(_, sid string) { events = append(events, "idle:"+sid) },
		func(_, sid string) { events = append(events, "active:"+sid) },
	)
	defer cancel()

	tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
		`{"sessionID":"s1","status":{"type":"retry"}}`,
	))

	assert.Equal(t, []string{"active:s1"}, events)
}

func TestSubscribeDrain_NonSessionStatusEvent_Ignored(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)

	var events []string
	cancel := tracker.SubscribeDrain("ws-1",
		func(_, sid string) { events = append(events, sid) },
		func(_, sid string) { events = append(events, sid) },
	)
	defer cancel()

	tracker.dispatchProperties("ws-1", "file.changed", json.RawMessage(`{"path":"/foo"}`))
	assert.Empty(t, events)
}
