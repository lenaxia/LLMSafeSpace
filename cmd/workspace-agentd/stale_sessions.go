// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// stale_sessions.go — clears sessions stuck in "busy" after an opencode restart.
//
// # Problem
//
// opencode persists session state in SQLite. When the opencode process is
// killed mid-run (e.g. by the relay injector's SIGTERM, an agent reload,
// or a pod OOM), the in-flight session is left with a busy flag in SQLite.
// The next opencode instance loads that state and reports the session as
// busy to callers — including the safespace API's "session is busy; retry
// after idle" guard — even though no LLM call is actually in progress.
// Users cannot send new messages until the session is manually aborted.
//
// # Why not filter to only busy sessions?
//
// Session status is a runtime concept in opencode. It is not stored in
// SQLite and is not returned by the REST API (/session or /session/:id)
// — it lives only in the in-memory SSE event stream. After a restart,
// the SSE tracker is empty, so we cannot distinguish busy from idle via
// any API call.
//
// # Fix
//
// Abort every session unconditionally after each opencode restart. After
// a restart opencode has no in-flight LLM calls, so any busy flag is
// definitionally stale. Aborting an already-idle session is a no-op in
// opencode (returns true, does nothing). This is safe and idempotent.
//
// abortStaleSessions is called by abortStaleSessionsAfterStart, which is
// wired as managedProcess.onStart and fires after every opencode start
// (both initial pod boot and supervisor-managed restarts), once opencode
// has passed its health check.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

// doPost issues an authenticated POST with an empty body to the opencode
// server at path. The caller is responsible for closing the response body.
func (c *OpenCodeClient) doPost(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, getAgentAddr()+path, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(agentd.AuthUsername, c.password)
	req.Header.Set("Content-Type", "application/json")
	return c.client.Do(req)
}

// AbortSession calls POST /session/:id/abort on the opencode server.
// opencode returns "true" on success; we treat any 2xx as success.
func (c *OpenCodeClient) AbortSession(ctx context.Context, sessionID string) error {
	resp, err := c.doPost(ctx, "/session/"+sessionID+"/abort")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("abort session %s: unexpected status %d", sessionID, resp.StatusCode)
	}
	return nil
}

// abortStaleSessions aborts every session after an opencode restart.
//
// Since session status is not available via the REST API after a restart,
// we abort all sessions unconditionally. Aborting an idle session is a
// no-op in opencode. Individual failures are logged and skipped.
func abortStaleSessions(ctx context.Context, client *OpenCodeClient, lg *zap.Logger) {
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		lg.Warn("abortStaleSessions: failed to list sessions", zap.Error(err))
		return
	}
	if len(sessions) == 0 {
		return
	}

	var aborted, failed int
	for _, s := range sessions {
		abortCtx, abortCancel := context.WithTimeout(ctx, 5*time.Second)
		err := client.AbortSession(abortCtx, s.ID)
		abortCancel()
		if err != nil {
			lg.Warn("abortStaleSessions: failed to abort session",
				zap.String("sessionID", s.ID),
				zap.Error(err))
			failed++
			continue
		}
		aborted++
	}

	lg.Info("abortStaleSessions: complete",
		zap.Int("aborted", aborted),
		zap.Int("failed", failed),
		zap.Int("total", len(sessions)))
}

// abortStaleSessionsAfterStart waits for opencode to be healthy (up to
// 30s), then calls abortStaleSessions. This is the production onStart
// callback wired into managedProcess.
//
// We poll the health endpoint directly rather than relying on the
// healthzCache because the cache takes a few seconds to populate after
// the process starts, and we want to run as soon as opencode is ready.
func abortStaleSessionsAfterStart(ctx context.Context, client *OpenCodeClient, lg *zap.Logger) {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		select {
		case <-deadline.Done():
			lg.Warn("abortStaleSessionsAfterStart: opencode did not become healthy in time, skipping")
			return
		case <-time.After(time.Second):
		}
		probeCtx, probeCancel := context.WithTimeout(deadline, 2*time.Second)
		healthy, _, err := client.IsHealthy(probeCtx)
		probeCancel()
		if err == nil && healthy {
			break
		}
	}

	abortStaleSessions(deadline, client, lg)
}
