package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSETracker_ReconnectsOnTimeout(t *testing.T) {
	var connectCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Send one event so the connection is "successful" (triggers backoff reset)
		fmt.Fprintf(w, "data: {\"type\":\"session.updated\",\"properties\":{\"session\":{\"id\":\"s1\",\"status\":\"idle\"}}}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	agentAddrAtomic.Store(srv.URL)
	tracker := newSessionStatusTracker()
	client := &OpenCodeClient{password: "test", client: &http.Client{}}

	origTimeout := sseConnectionTimeout
	sseConnectionTimeout = 100 * time.Millisecond
	defer func() { sseConnectionTimeout = origTimeout }()

	// With 100ms timeout + 2s backoff (reset after timeout): need ~2.2s per cycle
	// Run for 5s to get at least 2 connections
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go tracker.subscribe(ctx, client)
	time.Sleep(4800 * time.Millisecond)
	cancel()

	count := connectCount.Load()
	assert.GreaterOrEqual(t, count, int32(2), "expected at least 2 connections due to timeout, got %d", count)
}

func TestSSETracker_PreservesMapAcrossReconnect(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if n == 1 {
			fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"ses_1\",\"status\":{\"type\":\"busy\"}}}\n\n")
			flusher.Flush()
		} else {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	agentAddrAtomic.Store(srv.URL)
	tracker := newSessionStatusTracker()
	client := &OpenCodeClient{password: "test", client: &http.Client{}}

	origTimeout := sseConnectionTimeout
	sseConnectionTimeout = 200 * time.Millisecond
	defer func() { sseConnectionTimeout = origTimeout }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go tracker.subscribe(ctx, client)

	// Wait for first event to be processed
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, "busy", tracker.get("ses_1"))

	// Wait for timeout + backoff + reconnect (200ms + 2s + 200ms)
	time.Sleep(3 * time.Second)

	// Session status preserved across reconnect
	assert.Equal(t, "busy", tracker.get("ses_1"))
	assert.GreaterOrEqual(t, callCount.Load(), int32(2))
}

func TestSSETracker_TimeoutDoesNotLogAsError(t *testing.T) {
	// Verify that a context deadline from our timeout is treated as
	// expected behavior (not logged as error, backoff resets)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	agentAddrAtomic.Store(srv.URL)
	tracker := newSessionStatusTracker()
	client := &OpenCodeClient{password: "test", client: &http.Client{}}

	// Call connectAndRead directly with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	origTimeout := sseConnectionTimeout
	sseConnectionTimeout = 50 * time.Millisecond
	defer func() { sseConnectionTimeout = origTimeout }()

	err := tracker.connectAndRead(ctx, client)
	// Should return context error (deadline exceeded from our timeout)
	assert.Error(t, err)
	assert.True(t, isTimeoutError(err), "expected timeout error, got: %v", err)
}
