package handlers

import (
	"sync"
)

const brokerChannelBuffer = 16

// WorkspaceSSEEvent is the canonical event type sent to browser SSE clients.
// All fields beyond Type are zero-valued when not applicable to the event type.
//
// Event types:
//   - "workspace.phase": workspace phase changed; Phase is set.
//   - "session.status":  session idle/busy notification; SessionID and Status are set.
//   - "opencode.event":  raw event forwarded from the opencode agent; EventType and Data are set.
type WorkspaceSSEEvent struct {
	Type      string      `json:"type"`
	Phase     string      `json:"phase,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	Status    string      `json:"status,omitempty"`
	EventType string      `json:"event_type,omitempty"` // opencode event type (e.g. "session.diff", "message.updated")
	Data      interface{} `json:"data,omitempty"`       // raw opencode event payload for "opencode.event"
}

// WorkspaceEventBroker is a fan-out pub/sub for per-workspace SSE events.
// Subscribers receive a buffered channel; events dropped when the channel is full
// (slow consumer) rather than blocking the publisher.
//
// All methods are safe for concurrent use.
type WorkspaceEventBroker struct {
	mu   sync.Mutex
	subs map[string]map[chan WorkspaceSSEEvent]struct{}
}

// NewWorkspaceEventBroker returns an initialised, empty broker.
func NewWorkspaceEventBroker() *WorkspaceEventBroker {
	return &WorkspaceEventBroker{
		subs: make(map[string]map[chan WorkspaceSSEEvent]struct{}),
	}
}

// Subscribe registers a new subscriber for workspaceID and returns a buffered
// channel on which events will be delivered. The caller must call Unsubscribe
// when done to release resources and close the channel.
func (b *WorkspaceEventBroker) Subscribe(workspaceID string) chan WorkspaceSSEEvent {
	ch := make(chan WorkspaceSSEEvent, brokerChannelBuffer)
	b.mu.Lock()
	if b.subs[workspaceID] == nil {
		b.subs[workspaceID] = make(map[chan WorkspaceSSEEvent]struct{})
	}
	b.subs[workspaceID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes the subscriber and closes its channel. After this call
// the channel will be drained and then closed; range loops over the channel
// will terminate. Calling Unsubscribe with a channel that is not subscribed
// (or for a workspaceID with no subscribers) is a no-op.
func (b *WorkspaceEventBroker) Unsubscribe(workspaceID string, ch chan WorkspaceSSEEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[workspaceID][ch]; ok {
		delete(b.subs[workspaceID], ch)
		if len(b.subs[workspaceID]) == 0 {
			delete(b.subs, workspaceID)
		}
		close(ch)
	}
}

// Publish delivers evt to all current subscribers of workspaceID. Events are
// sent non-blocking: if a subscriber's buffer is full the event is dropped for
// that subscriber only, without affecting others.
func (b *WorkspaceEventBroker) Publish(workspaceID string, evt WorkspaceSSEEvent) {
	b.mu.Lock()
	// Copy the subscriber set under the lock so we release it before sending.
	targets := make([]chan WorkspaceSSEEvent, 0, len(b.subs[workspaceID]))
	for ch := range b.subs[workspaceID] {
		targets = append(targets, ch)
	}
	b.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- evt:
		default:
			// Subscriber is slow; drop event rather than block.
		}
	}
}
