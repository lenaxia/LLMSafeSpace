// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// managed_process_concurrency_test.go — Focused concurrency tests for the
// data-race fix in restart()/stop() vs supervise() Wait().
//
// Why these tests exist
// ---------------------
// PR #422 fixed a data race: restart() and stop() accessed cmd.Process.Signal/
// Kill (which dereference the *os.Process struct) concurrently with the
// supervisor goroutine's cmd.Wait(). The fix captures pid under p.mu and uses
// syscall.Kill(pid, sig) after unlock, eliminating the shared-struct access.
//
// The race detector is the primary guard for this class of bug. These tests
// exercise the exact interleaving under -race with many goroutines, and also
// cover the SIGKILL fallback timer path (child that ignores SIGTERM >5s) that
// was previously untested.

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestManagedProcess_ConcurrentRestarts fires N goroutines all calling
// restart() simultaneously while the supervisor is running. Before the fix,
// each restart() accessed cmd.Process.Signal concurrently with supervise()'s
// cmd.Wait() — a textbook data race. Under -race this test would fail
// pre-fix. Post-fix it must complete without races, deadlocks, or leaked
// subprocesses, and the port must remain reachable.
func TestManagedProcess_ConcurrentRestarts(t *testing.T) {
	withTestLogger(t)
	port := freeTCPPort(t)
	// Short SIGTERM grace so concurrent restarts don't stack up.
	p := newTestManagedProcess(t, port, 50)
	p.start()
	defer p.stop()
	requireFakeReachable(t, port, 2*time.Second)

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	startGate := make(chan struct{})

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			p.restart()
		}()
	}

	close(startGate)

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent restarts deadlocked — restart() blocked on upCh")
	}

	// After all restarts settle, the port must be reachable by a fresh child.
	requireFakeReachable(t, port, 3*time.Second)
}

// TestManagedProcess_ConcurrentRestartAndPIDReads exercises the exact field
// access pattern that the #422 fix addressed: one set of goroutines calls
// restart() (which writes p.cmd under p.mu), while another set reads p.cmd
// and p.cmd.Process.Pid under p.mu. Pre-fix, restart() accessed
// cmd.Process.Signal OUTSIDE the mutex; the supervisor's Wait() and these
// reads would all race. Post-fix, all access is serialized through the mutex
// or the captured-by-value pid.
func TestManagedProcess_ConcurrentRestartAndPIDReads(t *testing.T) {
	withTestLogger(t)
	port := freeTCPPort(t)
	p := newTestManagedProcess(t, port, 30)
	p.start()
	defer p.stop()
	requireFakeReachable(t, port, 2*time.Second)

	const restarters = 4
	const readers = 8

	var restarterWg sync.WaitGroup
	restarterWg.Add(restarters)
	startGate := make(chan struct{})

	for i := 0; i < restarters; i++ {
		go func() {
			defer restarterWg.Done()
			<-startGate
			for j := 0; j < 3; j++ {
				p.restart()
			}
		}()
	}

	// Readers spin on p.mu reading p.cmd.Process.Pid concurrently with the
	// restarters and the supervisor. They use a stop signal independent of
	// the restarter WaitGroup to avoid a circular wait.
	readerStop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer readerWg.Done()
			<-startGate
			for {
				select {
				case <-readerStop:
					return
				default:
				}
				p.mu.Lock()
				if p.cmd != nil && p.cmd.Process != nil {
					_ = p.cmd.Process.Pid
				}
				p.mu.Unlock()
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}

	close(startGate)

	doneCh := make(chan struct{})
	go func() {
		restarterWg.Wait()
		close(readerStop)
		readerWg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(30 * time.Second):
		t.Fatal("restart + PID-read interleaving deadlocked")
	}

	requireFakeReachable(t, port, 3*time.Second)
}

// TestManagedProcess_RestartWithSlowChild_SIGKILLFallback exercises the
// SIGKILL fallback timer inside restart(): the child IGNORES SIGTERM
// entirely (only SIGKILL can terminate it). The killTimer closure (which
// captures pid by value) must fire syscall.Kill(pid, SIGKILL) so the
// supervisor can reap the child and spawn a replacement.
//
// Discrimination: if the killTimer is removed from restart(), the child
// never exits (it ignores SIGTERM and only SIGKILL can kill it), so
// restart() would hang on <-upCh forever and the test would time out.
func TestManagedProcess_RestartWithSlowChild_SIGKILLFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("SIGKILL fallback requires >5s (the hardcoded grace)")
	}
	withTestLogger(t)
	port := freeTCPPort(t)
	// IGNORE_SIGTERM=1: child catches and discards SIGTERM in a loop.
	// Only the killTimer's SIGKILL at 5s can terminate it.
	p := newIgnoreSIGTERMManagedProcess(t, port)
	p.start()
	defer p.stop()
	requireFakeReachable(t, port, 2*time.Second)

	p.mu.Lock()
	origPID := 0
	if p.cmd != nil && p.cmd.Process != nil {
		origPID = p.cmd.Process.Pid
	}
	p.mu.Unlock()
	require.NotZero(t, origPID, "child must be running before restart")

	// restart() must return within the killTimer bound (5s) + startup
	// overhead. If the SIGKILL path is broken, restart() hangs on <-upCh
	// forever (the child ignores SIGTERM and never exits).
	doneCh := make(chan struct{})
	go func() {
		p.restart()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("restart() did not complete within 10s — SIGKILL fallback may not have fired")
	}

	requireFakeReachable(t, port, 3*time.Second)

	p.mu.Lock()
	newPID := 0
	if p.cmd != nil && p.cmd.Process != nil {
		newPID = p.cmd.Process.Pid
	}
	p.mu.Unlock()
	require.NotEqual(t, origPID, newPID, "restart must produce a new PID after SIGKILL fallback")
}

// TestManagedProcess_StopWithSlowChild_SIGKILLFallback exercises the same
// SIGKILL fallback path in stop(): a child that ignores SIGTERM must be
// force-killed so the supervisor can exit and stop() can return.
//
// Discrimination: if the killTimer is removed from stop(), the child never
// exits and stop() hangs on <-doneCh forever.
func TestManagedProcess_StopWithSlowChild_SIGKILLFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("SIGKILL fallback requires >5s (the hardcoded grace)")
	}
	withTestLogger(t)
	port := freeTCPPort(t)
	p := newIgnoreSIGTERMManagedProcess(t, port)
	p.start()
	requireFakeReachable(t, port, 2*time.Second)

	// stop() must return within the killTimer bound (5s) + probeWg drain.
	doneCh := make(chan struct{})
	go func() {
		p.stop()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("stop() did not complete within 10s — SIGKILL fallback in stop() may be broken")
	}

	// Port must be free after stop.
	time.Sleep(500 * time.Millisecond)
	require.False(t, isPortBound("127.0.0.1:"+strconv.Itoa(port)),
		"port must be free after stop() with SIGKILL fallback")
}

// TestManagedProcess_DoubleStopIsIdempotent verifies stop() can be called
// concurrently from multiple goroutines without panic or deadlock. After the
// first stop() returns, doneCh is closed; subsequent callers unblock
// immediately on the closed channel. Pre-start calls are no-op'd by the
// doneCh-nil guard.
func TestManagedProcess_DoubleStopIsIdempotent(t *testing.T) {
	withTestLogger(t)
	port := freeTCPPort(t)
	p := newTestManagedProcess(t, port, 0)
	p.start()
	requireFakeReachable(t, port, 2*time.Second)

	var wg sync.WaitGroup
	wg.Add(3)
	startGate := make(chan struct{})
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			p.stop()
		}()
	}
	close(startGate)

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent stop() calls deadlocked")
	}
}

// newIgnoreSIGTERMManagedProcess builds a managedProcess whose child ignores
// SIGTERM entirely (catches and discards in a loop). Only SIGKILL can
// terminate the child, so the SIGKILL-fallback timer in restart()/stop() is
// the sole mechanism that lets those methods complete. If the killTimer is
// removed, tests using this factory hang.
func newIgnoreSIGTERMManagedProcess(t *testing.T, port int) *managedProcess {
	t.Helper()
	p := &managedProcess{}
	p.cmdFactory = func() *exec.Cmd {
		//nolint:gosec // os.Args[0] is the trusted test binary path
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = []string{
			"GO_TEST_FAKE_OPENCODE=1",
			"FAKE_PORT=" + strconv.Itoa(port),
			"IGNORE_SIGTERM=1",
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd
	}
	p.healthCheckURL = "http://127.0.0.1:" + strconv.Itoa(port) + "/v1/readyz"
	return p
}
