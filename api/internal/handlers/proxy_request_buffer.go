// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
	pkginterfaces "github.com/lenaxia/llmsafespaces/pkg/interfaces"
)

const (
	defaultBufferMaxSize      = 10
	defaultBufferTimeout      = 30 * time.Second
	defaultBufferPollInterval = 500 * time.Millisecond

	// defaultGlobalBufferBytesCap (worklog 371 C5) bounds the total memory
	// the request buffer can consume across ALL workspaces. Without it, a
	// coordinated platform-wide restart (e.g. opencode version rollout)
	// could buffer up to maxWorkspaces × maxSize × maxBodySize bytes
	// (10000 × 10 × 10MB ≈ 1TB) and OOM the API server. 500MB is generous
	// for the intended purpose (smoothing the ~5s restart window) while
	// bounded enough that the API server survives a platform-wide flap.
	// Excess requests are rejected with 429 (same as per-workspace full).
	defaultGlobalBufferBytesCap int64 = 500 * 1024 * 1024
)

var (
	errBufferTimeout    = errors.New("request buffer timeout")
	errBufferCommitted  = errors.New("buffered response already committed")
	errBufferRetryLater = errors.New("buffered forward waiting for connection slot")
	errClientGone       = errors.New("buffered client disconnected")
)

func isRetryableBufferErr(err error) bool {
	return err != nil && (isConnectionError(err) || errors.Is(err, errBufferRetryLater))
}

type bufferedRequest struct {
	forward  func() error
	result   chan error
	deadline time.Time
	cancelCh chan struct{}
	// bodySize is the byte length of the request body captured by forward.
	// Used to account against the global buffer memory cap (C5). 0 means
	// "unknown" — treated as 0 bytes by the cap, which is safe (the cap
	// is a DoS guard, not a precise budget).
	bodySize int
}

type wsBufferQueue struct {
	workspaceID string
	mu          sync.Mutex
	pending     []*bufferedRequest
	draining    bool
}

func (q *wsBufferQueue) popHead(b *requestBuffer) {
	q.mu.Lock()
	var removed *bufferedRequest
	if len(q.pending) > 0 {
		removed = q.pending[0]
		q.pending = q.pending[1:]
	}
	n := len(q.pending)
	q.mu.Unlock()
	metrics.SetRequestBufferSize(q.workspaceID, n)
	// C5: release the global byte budget the removed request held.
	if removed != nil && removed.bodySize > 0 {
		b.releaseGlobalBytes(int64(removed.bodySize))
	}
}

// deliverBuffered hands the drainer's terminal outcome to the parked handler.
// The send uses blocking channel syntax but is guaranteed never to actually
// block (deadlock-free): result is cap-1, the drainer is the sole sender, and
// it delivers exactly once per request (always paired with popHead, so the
// slot is free at send time → a 0→1 transition). The handler always reaches a
// <-result receive exactly once (F2: in both select arms, including after
// ctx.Done it blocks on <-result), so a receiver is always waiting and the
// value is never dropped.
func deliverBuffered(req *bufferedRequest, err error) {
	req.result <- err
}

func (q *wsBufferQueue) drain(b *requestBuffer) {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.draining = false
			b.mu.Lock()
			if cur, ok := b.queues[q.workspaceID]; !ok || cur == q {
				delete(b.queues, q.workspaceID)
			}
			b.mu.Unlock()
			q.mu.Unlock()
			metrics.DeleteRequestBufferMetrics(q.workspaceID)
			return
		}
		head := q.pending[0]
		q.mu.Unlock()

		select {
		case <-head.cancelCh:
			q.popHead(b)
			deliverBuffered(head, errClientGone)
			continue
		default:
		}

		if time.Now().After(head.deadline) {
			b.logger.Warn("Request buffer timed out waiting for upstream", "workspaceID", q.workspaceID)
			q.popHead(b)
			deliverBuffered(head, errBufferTimeout)
			continue
		}

		// Forwarding is serial FIFO: the next request does not begin until
		// this one completes. A long first turn can cause later requests to
		// exhaust their deadline and 503 even once upstream is healthy.
		// Bounded by the per-request deadline (no infinite hang) and
		// spec-literal; concurrent forwarding once healthy is a possible
		// future improvement if buffer-timeout metrics spike in production.
		ferr := head.forward()
		if isRetryableBufferErr(ferr) {
			wait := b.pollInterval
			if d := time.Until(head.deadline); d < wait {
				wait = d
			}
			if wait <= 0 {
				b.logger.Warn("Request buffer timed out waiting for upstream", "workspaceID", q.workspaceID)
				q.popHead(b)
				deliverBuffered(head, errBufferTimeout)
				continue
			}
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-head.cancelCh:
				timer.Stop()
				select {
				case <-timer.C:
				default:
				}
				q.popHead(b)
				deliverBuffered(head, errClientGone)
				continue
			}
			continue
		}
		q.popHead(b)
		deliverBuffered(head, ferr)
	}
}

type requestBuffer struct {
	timeout      time.Duration
	maxSize      int
	pollInterval time.Duration
	logger       pkginterfaces.LoggerInterface

	// globalBytesCap (C5) bounds total buffered body bytes across all
	// workspaces. 0 disables the global cap (test/local-dev escape hatch).
	globalBytesCap int64

	// globalBytes tracks the sum of bodySize across all currently-buffered
	// requests. Accessed atomically — tryEnqueue does check-and-add, popHead
	// does add(-n). No mutex: the atomic is the synchronizer.
	globalBytes atomic.Int64

	mu     sync.Mutex
	queues map[string]*wsBufferQueue
}

func newRequestBuffer(maxSize int, timeout, pollInterval time.Duration, logger pkginterfaces.LoggerInterface) *requestBuffer {
	return newRequestBufferWithGlobalCap(maxSize, timeout, pollInterval, defaultGlobalBufferBytesCap, logger)
}

// newRequestBufferWithGlobalCap is the constructor used by tests that need
// to set a specific global byte cap (e.g. to verify the cap rejects
// requests). Production uses newRequestBuffer which applies the default.
func newRequestBufferWithGlobalCap(maxSize int, timeout, pollInterval time.Duration, globalBytesCap int64, logger pkginterfaces.LoggerInterface) *requestBuffer {
	if maxSize < 0 {
		maxSize = 0
	}
	if timeout <= 0 {
		timeout = defaultBufferTimeout
	}
	if pollInterval <= 0 {
		pollInterval = defaultBufferPollInterval
	}
	return &requestBuffer{
		timeout:        timeout,
		maxSize:        maxSize,
		pollInterval:   pollInterval,
		logger:         logger,
		globalBytesCap: globalBytesCap,
		queues:         make(map[string]*wsBufferQueue),
	}
}

// reserveGlobalBytes atomically reserves n bytes against the global cap.
// Returns false if the reservation would exceed the cap (the caller must
// NOT then enqueue). 0-size requests always succeed (they don't consume
// budget). A cap of 0 disables the check entirely.
func (b *requestBuffer) reserveGlobalBytes(n int64) bool {
	if n <= 0 || b.globalBytesCap <= 0 {
		return true
	}
	for {
		current := b.globalBytes.Load()
		if current+n > b.globalBytesCap {
			return false
		}
		if b.globalBytes.CompareAndSwap(current, current+n) {
			metrics.SetRequestBufferGlobalBytes(current + n)
			return true
		}
		// CAS failed — another goroutine changed the counter; retry.
	}
}

// releaseGlobalBytes returns n bytes to the global budget. Called when a
// buffered request is removed from a queue (popHead). Clamped at 0 so a
// bookkeeping bug can't drive the counter negative.
func (b *requestBuffer) releaseGlobalBytes(n int64) {
	if n <= 0 || b.globalBytesCap <= 0 {
		return
	}
	newTotal := b.globalBytes.Add(-n)
	if newTotal < 0 {
		// Defensive: a negative counter indicates a bookkeeping bug; reset
		// to 0 so subsequent enqueues are not falsely admitted.
		b.globalBytes.Store(0)
		newTotal = 0
	}
	metrics.SetRequestBufferGlobalBytes(newTotal)
}

func (b *requestBuffer) tryEnqueue(workspaceID string, req *bufferedRequest) bool {
	// C5: reserve against the global byte cap BEFORE taking any locks.
	// Reservation is atomic (CAS loop) so concurrent enqueues cannot
	// oversubscribe. If the reservation fails, the request is rejected
	// with the same 429 the per-workspace cap produces.
	if !b.reserveGlobalBytes(int64(req.bodySize)) {
		metrics.RecordRequestBufferGlobalFull(workspaceID)
		return false
	}
	reservedBytes := req.bodySize

	b.mu.Lock()
	q, ok := b.queues[workspaceID]
	if !ok {
		q = &wsBufferQueue{workspaceID: workspaceID}
		b.queues[workspaceID] = q
	}
	b.mu.Unlock()

	q.mu.Lock()
	if len(q.pending) >= b.maxSize {
		q.mu.Unlock()
		// Per-workspace cap hit — release the global reservation we just made.
		b.releaseGlobalBytes(int64(reservedBytes))
		return false
	}
	q.pending = append(q.pending, req)
	n := len(q.pending)
	startDrainer := !q.draining
	if startDrainer {
		q.draining = true
	}
	q.mu.Unlock()

	metrics.SetRequestBufferSize(workspaceID, n)
	if startDrainer {
		go q.drain(b)
	}
	return true
}

func (b *requestBuffer) queueCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queues)
}
