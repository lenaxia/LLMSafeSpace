// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package eventbroker

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
)

const (
	BrokerChannelBuffer   = 16
	numShards             = 16
	userChannelBuffer     = 128
	replayBufferSize      = 128
	MaxSubscribersPerUser = 20
	HeartbeatSentinelType = "_heartbeat"
)

var ErrTooManySubscribers = errors.New("too many subscribers for user")

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

type Subscriber struct {
	Ch          chan apitypes.WorkspaceSSEEvent
	missedEvent atomic.Bool
	closed      atomic.Bool
	onDrop      func(eventType string)
}

func (s *Subscriber) Send(evt apitypes.WorkspaceSSEEvent) {
	if s.closed.Load() {
		return
	}
	defer func() { _ = recover() }()
	if s.missedEvent.Load() {
		resync := apitypes.WorkspaceSSEEvent{Type: "resync"}
		select {
		case s.Ch <- resync:
			s.missedEvent.Store(false)
		default:
			if s.onDrop != nil {
				s.onDrop("resync")
			}
			return
		}
	}
	select {
	case s.Ch <- evt:
	default:
		s.missedEvent.Store(true)
		if s.onDrop != nil {
			s.onDrop(evt.Type)
		}
	}
}

func (s *Subscriber) MarkClosed() {
	s.closed.Store(true)
}

type ReplayEntry struct {
	ID    uint64
	Event apitypes.WorkspaceSSEEvent
}

type replayBuffer struct {
	entries [replayBufferSize]ReplayEntry
	nextID  uint64
	count   int
	head    int
}

func newReplayBuffer() *replayBuffer {
	return &replayBuffer{nextID: 1}
}

func (rb *replayBuffer) appendLocked(evt apitypes.WorkspaceSSEEvent) uint64 {
	id := rb.nextID
	rb.nextID++
	rb.entries[rb.head] = ReplayEntry{ID: id, Event: evt}
	rb.head = (rb.head + 1) % replayBufferSize
	if rb.count < replayBufferSize {
		rb.count++
	}
	return id
}

func (rb *replayBuffer) sinceLocked(lastID uint64) ([]ReplayEntry, bool) {
	if rb.count == 0 {
		return nil, false
	}

	oldestID := rb.nextID - uint64(rb.count) //nolint:gosec // count is always [0, replayBufferSize]
	gapDetected := lastID > 0 && lastID < oldestID

	var result []ReplayEntry
	start := (rb.head - rb.count + replayBufferSize) % replayBufferSize
	for i := 0; i < rb.count; i++ {
		idx := (start + i) % replayBufferSize
		entry := rb.entries[idx]
		if entry.ID > lastID {
			result = append(result, entry)
		}
	}
	return result, gapDetected
}

type shard struct {
	mu       sync.Mutex
	userSubs map[string][]*Subscriber
	wsSubs   map[string][]*Subscriber
	replay   map[string]*replayBuffer
	wsOwner  map[string]string
}

type WorkspaceEventBroker struct {
	mu        sync.Mutex
	subs      map[string]map[uint64]*Subscriber
	nextSubID atomic.Uint64
}

func NewWorkspaceEventBroker() *WorkspaceEventBroker {
	return &WorkspaceEventBroker{
		subs: make(map[string]map[uint64]*Subscriber),
	}
}

func (b *WorkspaceEventBroker) Subscribe(workspaceID string) *Subscriber {
	s := &Subscriber{
		Ch: make(chan apitypes.WorkspaceSSEEvent, BrokerChannelBuffer),
		onDrop: func(eventType string) {
			brokerDroppedEvents.WithLabelValues("workspace", eventType).Inc()
		},
	}
	b.mu.Lock()
	if b.subs[workspaceID] == nil {
		b.subs[workspaceID] = make(map[uint64]*Subscriber)
	}
	id := b.nextSubID.Add(1)
	b.subs[workspaceID][id] = s
	b.mu.Unlock()
	return s
}

func (b *WorkspaceEventBroker) Unsubscribe(workspaceID string, s *Subscriber) {
	s.MarkClosed()
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
	close(s.Ch)
}

func (b *WorkspaceEventBroker) Publish(workspaceID string, evt apitypes.WorkspaceSSEEvent) {
	b.mu.Lock()
	targets := make([]*Subscriber, 0, len(b.subs[workspaceID]))
	for _, s := range b.subs[workspaceID] {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	for _, s := range targets {
		s.Send(evt)
	}
}

func (b *WorkspaceEventBroker) SubscriberCount(workspaceID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[workspaceID])
}
