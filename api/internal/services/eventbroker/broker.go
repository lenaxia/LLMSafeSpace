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
