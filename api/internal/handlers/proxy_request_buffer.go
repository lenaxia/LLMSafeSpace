// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"
	"sync"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const (
	defaultBufferMaxSize      = 10
	defaultBufferTimeout      = 30 * time.Second
	defaultBufferPollInterval = 500 * time.Millisecond
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
}

type wsBufferQueue struct {
	workspaceID string
	mu          sync.Mutex
	pending     []*bufferedRequest
	draining    bool
}

func (q *wsBufferQueue) popHead() {
	q.mu.Lock()
	if len(q.pending) > 0 {
		q.pending = q.pending[1:]
	}
	n := len(q.pending)
	q.mu.Unlock()
	metrics.SetRequestBufferSize(q.workspaceID, n)
}

// deliverBuffered hands the drainer's terminal outcome to the parked handler.
// It is a blocking send and is provably non-blocking: result is cap-1, the
// drainer is the sole sender, and it delivers exactly once per request (always
// paired with popHead, so the buffer is empty at send time → 0→1 transition).
// The handler always receives exactly once (F2: it blocks on <-result in both
// select arms), so the send can never block and the value is never dropped.
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
			q.popHead()
			deliverBuffered(head, errClientGone)
			continue
		default:
		}

		if time.Now().After(head.deadline) {
			b.logger.Warn("Request buffer timed out waiting for upstream", "workspaceID", q.workspaceID)
			q.popHead()
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
				q.popHead()
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
				q.popHead()
				deliverBuffered(head, errClientGone)
				continue
			}
			continue
		}
		q.popHead()
		deliverBuffered(head, ferr)
	}
}

type requestBuffer struct {
	timeout      time.Duration
	maxSize      int
	pollInterval time.Duration
	logger       pkginterfaces.LoggerInterface

	mu     sync.Mutex
	queues map[string]*wsBufferQueue
}

func newRequestBuffer(maxSize int, timeout, pollInterval time.Duration, logger pkginterfaces.LoggerInterface) *requestBuffer {
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
		timeout:      timeout,
		maxSize:      maxSize,
		pollInterval: pollInterval,
		logger:       logger,
		queues:       make(map[string]*wsBufferQueue),
	}
}

func (b *requestBuffer) tryEnqueue(workspaceID string, req *bufferedRequest) bool {
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
