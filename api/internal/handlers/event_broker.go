// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

const brokerChannelBuffer = 16

var brokerDroppedEvents = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "llmsafespace_sse_broker_dropped_events_total",
		Help: "Events dropped because subscriber channel was full",
	},
	[]string{"broker", "event_type"},
)

func init() {
	prometheus.MustRegister(brokerDroppedEvents)
}

// WorkspaceSSEEvent is the canonical event type sent to browser SSE clients.
// All fields beyond Type are zero-valued when not applicable to the event type.
//
// Event types:
//   - "workspace.phase":           workspace phase changed; Phase is set.
//   - "session.status":            session idle/busy notification; SessionID and Status are set.
//   - "opencode.event":            raw event forwarded from the opencode agent; EventType and Data are set.
//   - "agent.question":            agent is asking the user a question; Data is *agent.QuestionRequest.
//   - "agent.question.resolved":   question was answered or dismissed; Data is map[string]string{request_id, session_id}.
//   - "agent.permission":          agent needs permission approval; Data is *agent.PermissionRequest.
//   - "agent.permission.resolved": permission was approved or denied; Data is map[string]string{request_id, session_id, reply}.
//   - "resync":                    subscriber missed events; client should re-fetch state.
type WorkspaceSSEEvent struct {
	EventID     uint64      `json:"event_id,omitempty"`
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Type        string      `json:"type"`
	Phase       string      `json:"phase,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	Status      string      `json:"status,omitempty"`
	EventType   string      `json:"event_type,omitempty"`
	Data        interface{} `json:"data,omitempty"`
}

// WorkspaceEventBroker is a fan-out pub/sub for per-workspace SSE events.
// Subscribers receive a *subscriber with a buffered channel; events dropped
// when the channel is full are tracked via missedEvent and a resync event is
// prepended on recovery — matching the UserEventBroker pattern.
//
// All methods are safe for concurrent use.
type WorkspaceEventBroker struct {
	mu        sync.Mutex
	subs      map[string]map[uint64]*subscriber
	nextSubID atomic.Uint64
}

// NewWorkspaceEventBroker returns an initialized, empty broker.
func NewWorkspaceEventBroker() *WorkspaceEventBroker {
	return &WorkspaceEventBroker{
		subs: make(map[string]map[uint64]*subscriber),
	}
}

// Subscribe registers a new subscriber for workspaceID and returns a
// *subscriber with a buffered channel. The caller must call Unsubscribe
// when done to release resources.
func (b *WorkspaceEventBroker) Subscribe(workspaceID string) *subscriber {
	s := &subscriber{
		ch: make(chan WorkspaceSSEEvent, brokerChannelBuffer),
		onDrop: func(eventType string) {
			brokerDroppedEvents.WithLabelValues("workspace", eventType).Inc()
		},
	}
	b.mu.Lock()
	if b.subs[workspaceID] == nil {
		b.subs[workspaceID] = make(map[uint64]*subscriber)
	}
	id := b.nextSubID.Add(1)
	b.subs[workspaceID][id] = s
	b.mu.Unlock()
	return s
}

// Unsubscribe marks the subscriber closed and removes it from the broker.
// After this call, send() becomes a no-op and the channel is closed so
// range loops terminate. Calling Unsubscribe with a subscriber that is
// not registered (or for a workspaceID with no subscribers) is a no-op.
func (b *WorkspaceEventBroker) Unsubscribe(workspaceID string, s *subscriber) {
	s.markClosed()
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[workspaceID]
	for id, sub := range subs {
		if sub == s {
			delete(subs, id)
			break
		}
	}
	if len(subs) == 0 {
		delete(b.subs, workspaceID)
	}
	close(s.ch)
}

// Publish delivers evt to all current subscribers of workspaceID. Events are
// sent via subscriber.send() which tracks missedEvent and prepends resync on
// recovery.
func (b *WorkspaceEventBroker) Publish(workspaceID string, evt WorkspaceSSEEvent) {
	b.mu.Lock()
	targets := make([]*subscriber, 0, len(b.subs[workspaceID]))
	for _, s := range b.subs[workspaceID] {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	for _, s := range targets {
		s.send(evt)
	}
}

// SubscriberCount returns the number of active subscribers for a workspace.
func (b *WorkspaceEventBroker) SubscriberCount(workspaceID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[workspaceID])
}
