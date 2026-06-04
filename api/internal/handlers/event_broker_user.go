// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
)

const (
	numShards             = 16
	userChannelBuffer     = 128
	replayBufferSize      = 128
	maxSubscribersPerUser = 20
	heartbeatSentinelType = "_heartbeat"
)

// ErrTooManySubscribers is returned when a user has reached the max connection limit.
var ErrTooManySubscribers = errors.New("too many subscribers for user")

// subscriber is a single SSE connection to a user or workspace stream.
type subscriber struct {
	ch          chan WorkspaceSSEEvent
	missedEvent atomic.Bool
	closed      atomic.Bool
}

// send delivers an event to the subscriber's channel. If the channel is full,
// the event is dropped and missedEvent is flagged. On next successful send,
// a resync event is prepended. Safe to call after the subscriber has been closed.
func (s *subscriber) send(evt WorkspaceSSEEvent) {
	if s.closed.Load() {
		return
	}
	defer func() { _ = recover() }()
	if s.missedEvent.Load() {
		resync := WorkspaceSSEEvent{Type: "resync"}
		select {
		case s.ch <- resync:
			s.missedEvent.Store(false)
		default:
			return
		}
	}
	select {
	case s.ch <- evt:
	default:
		s.missedEvent.Store(true)
	}
}

// markClosed sets the closed flag so send() becomes a no-op.
func (s *subscriber) markClosed() {
	s.closed.Store(true)
}

// replayEntry stores a buffered event with its assigned ID.
type replayEntry struct {
	ID    uint64
	Event WorkspaceSSEEvent
}

// replayBuffer is a fixed-size ring buffer of events per user.
// All methods assume the caller holds the containing shard's mutex.
type replayBuffer struct {
	entries [replayBufferSize]replayEntry
	nextID  uint64
	count   int
	head    int // index of next write position
}

func newReplayBuffer() *replayBuffer {
	return &replayBuffer{nextID: 1}
}

// appendLocked adds an event to the ring. Caller must hold shard.mu.
func (rb *replayBuffer) appendLocked(evt WorkspaceSSEEvent) uint64 {
	id := rb.nextID
	rb.nextID++
	rb.entries[rb.head] = replayEntry{ID: id, Event: evt}
	rb.head = (rb.head + 1) % replayBufferSize
	if rb.count < replayBufferSize {
		rb.count++
	}
	return id
}

// sinceLocked returns all entries with ID > lastID. Caller must hold shard.mu.
// Returns (entries, gapDetected). gapDetected is true when lastID < oldest buffered ID.
func (rb *replayBuffer) sinceLocked(lastID uint64) ([]replayEntry, bool) {
	if rb.count == 0 {
		return nil, false
	}

	oldestID := rb.nextID - uint64(rb.count) //nolint:gosec // count is always [0, replayBufferSize]
	gapDetected := lastID > 0 && lastID < oldestID

	var result []replayEntry
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

// shard holds subscribers and replay buffers for a subset of users/workspaces.
type shard struct {
	mu       sync.Mutex
	userSubs map[string][]*subscriber
	wsSubs   map[string][]*subscriber
	replay   map[string]*replayBuffer
	wsOwner  map[string]string // workspaceID → userID
}

// UserEventBroker is the new sharded, user-scoped event broker.
// It provides both user-scoped (PublishToUser) and workspace-scoped (PublishToWorkspace)
// pub/sub, plus replay and ownership tracking.
type UserEventBroker struct {
	shards [numShards]shard
}

// NewUserEventBroker creates an initialized broker.
func NewUserEventBroker() *UserEventBroker {
	b := &UserEventBroker{}
	for i := range b.shards {
		b.shards[i] = shard{
			userSubs: make(map[string][]*subscriber),
			wsSubs:   make(map[string][]*subscriber),
			replay:   make(map[string]*replayBuffer),
			wsOwner:  make(map[string]string),
		}
	}
	return b
}

func (b *UserEventBroker) userShard(userID string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	return &b.shards[h.Sum32()&(numShards-1)]
}

func (b *UserEventBroker) wsShard(workspaceID string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspaceID))
	return &b.shards[h.Sum32()&(numShards-1)]
}

// SubscribeUser registers a new user-scoped subscriber. Returns ErrTooManySubscribers
// if the user already has maxSubscribersPerUser connections.
func (b *UserEventBroker) SubscribeUser(userID string) (*subscriber, error) {
	sh := b.userShard(userID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	if len(sh.userSubs[userID]) >= maxSubscribersPerUser {
		return nil, ErrTooManySubscribers
	}

	s := &subscriber{ch: make(chan WorkspaceSSEEvent, userChannelBuffer)}
	sh.userSubs[userID] = append(sh.userSubs[userID], s)
	return s, nil
}

// UnsubscribeUser removes a subscriber and marks it closed.
// After this call, send() becomes a no-op and the live loop should exit via context.
func (b *UserEventBroker) UnsubscribeUser(userID string, s *subscriber) {
	s.markClosed()
	sh := b.userShard(userID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	subs := sh.userSubs[userID]
	for i, sub := range subs {
		if sub == s {
			sh.userSubs[userID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(sh.userSubs[userID]) == 0 {
		delete(sh.userSubs, userID)
	}
}

// SubscribeWorkspace registers a workspace-scoped subscriber.
// Returns ErrTooManySubscribers if the workspace exceeds the connection limit.
func (b *UserEventBroker) SubscribeWorkspace(workspaceID string) (*subscriber, error) {
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	if len(sh.wsSubs[workspaceID]) >= maxSubscribersPerUser {
		return nil, ErrTooManySubscribers
	}

	s := &subscriber{ch: make(chan WorkspaceSSEEvent, userChannelBuffer)}
	sh.wsSubs[workspaceID] = append(sh.wsSubs[workspaceID], s)
	return s, nil
}

// UnsubscribeWorkspace removes a workspace subscriber and marks it closed.
func (b *UserEventBroker) UnsubscribeWorkspace(workspaceID string, s *subscriber) {
	s.markClosed()
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	subs := sh.wsSubs[workspaceID]
	for i, sub := range subs {
		if sub == s {
			sh.wsSubs[workspaceID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(sh.wsSubs[workspaceID]) == 0 {
		delete(sh.wsSubs, workspaceID)
	}
}

// PublishToUser assigns an event_id, appends to the replay buffer, and fans out
// to all user subscribers. The event_id is set on the event before delivery.
func (b *UserEventBroker) PublishToUser(userID string, evt WorkspaceSSEEvent) {
	sh := b.userShard(userID)
	sh.mu.Lock()

	if sh.replay[userID] == nil {
		sh.replay[userID] = newReplayBuffer()
	}
	id := sh.replay[userID].appendLocked(evt)
	evt.EventID = id

	targets := make([]*subscriber, len(sh.userSubs[userID]))
	copy(targets, sh.userSubs[userID])
	sh.mu.Unlock()

	for _, s := range targets {
		s.send(evt)
	}
}

// PublishToWorkspace fans out an event to all workspace-scoped subscribers.
// No replay buffer for workspace streams (per design: replay deferred).
func (b *UserEventBroker) PublishToWorkspace(workspaceID string, evt WorkspaceSSEEvent) {
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	targets := make([]*subscriber, len(sh.wsSubs[workspaceID]))
	copy(targets, sh.wsSubs[workspaceID])
	sh.mu.Unlock()

	for _, s := range targets {
		s.send(evt)
	}
}

// Replay returns buffered events for a user since lastID.
// Returns (entries, gapDetected). gapDetected is true when events have been lost.
func (b *UserEventBroker) Replay(userID string, lastID uint64) ([]replayEntry, bool) {
	sh := b.userShard(userID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	rb := sh.replay[userID]
	if rb == nil {
		return nil, false
	}
	return rb.sinceLocked(lastID)
}

// RecordWorkspaceOwner maps a workspace to its owning user.
func (b *UserEventBroker) RecordWorkspaceOwner(workspaceID, userID string) {
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	sh.wsOwner[workspaceID] = userID
	sh.mu.Unlock()
}

// WorkspaceOwner returns the userID that owns a workspace (or "" if unknown).
func (b *UserEventBroker) WorkspaceOwner(workspaceID string) string {
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.wsOwner[workspaceID]
}

// CleanupWorkspace removes a workspace from the ownership map.
func (b *UserEventBroker) CleanupWorkspace(workspaceID string) {
	sh := b.wsShard(workspaceID)
	sh.mu.Lock()
	delete(sh.wsOwner, workspaceID)
	sh.mu.Unlock()
}

// CleanupUser removes all replay data for a user.
func (b *UserEventBroker) CleanupUser(userID string) {
	sh := b.userShard(userID)
	sh.mu.Lock()
	delete(sh.replay, userID)
	sh.mu.Unlock()
}

// GetAllWorkspaceOwners returns a copy of the full workspace→user ownership map.
// Used by the snapshot goroutine.
func (b *UserEventBroker) GetAllWorkspaceOwners() map[string]string {
	result := make(map[string]string)
	for i := range b.shards {
		b.shards[i].mu.Lock()
		for wsID, userID := range b.shards[i].wsOwner {
			result[wsID] = userID
		}
		b.shards[i].mu.Unlock()
	}
	return result
}
