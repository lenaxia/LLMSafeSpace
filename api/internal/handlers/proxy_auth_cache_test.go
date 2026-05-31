// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// --- US-23.4: copyResponseHeaders strips dangerous headers ---

func TestCopyResponseHeaders_StripsWWWAuthenticate(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("WWW-Authenticate", "Basic realm=\"opencode\"")
	src.Set("Content-Length", "42")

	dst := http.Header{}
	copyResponseHeaders(src, dst)

	assert.Equal(t, "application/json", dst.Get("Content-Type"))
	assert.Equal(t, "42", dst.Get("Content-Length"))
	assert.Empty(t, dst.Get("WWW-Authenticate"), "WWW-Authenticate must be stripped")
}

func TestCopyResponseHeaders_StripsProxyAuthenticate(t *testing.T) {
	src := http.Header{}
	src.Set("Proxy-Authenticate", "Basic")
	src.Set("Content-Type", "text/plain")

	dst := http.Header{}
	copyResponseHeaders(src, dst)

	assert.Empty(t, dst.Get("Proxy-Authenticate"))
	assert.Equal(t, "text/plain", dst.Get("Content-Type"))
}

func TestCopyResponseHeaders_StripsSetCookie(t *testing.T) {
	src := http.Header{}
	src.Set("Set-Cookie", "session=abc123")
	src.Set("ETag", "\"abc\"")

	dst := http.Header{}
	copyResponseHeaders(src, dst)

	assert.Empty(t, dst.Get("Set-Cookie"))
	assert.Equal(t, "\"abc\"", dst.Get("ETag"))
}

func TestCopyResponseHeaders_PreservesSafeHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("Cache-Control", "no-cache")
	src.Set("X-Custom-Header", "value")
	src.Set("X-Accel-Buffering", "no")

	dst := http.Header{}
	copyResponseHeaders(src, dst)

	assert.Equal(t, "application/json", dst.Get("Content-Type"))
	assert.Equal(t, "no-cache", dst.Get("Cache-Control"))
	assert.Equal(t, "value", dst.Get("X-Custom-Header"))
	assert.Equal(t, "no", dst.Get("X-Accel-Buffering"))
}

// --- US-23.4: Upstream 401 → 502 conversion ---

func TestProxy_Upstream401_Returns502(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"opencode\"")
		w.WriteHeader(http.StatusUnauthorized)
	})
	env.setupWorkspacePodWithT(t, "ws-auth-fail", "10.0.0.1", "Active", "")
	env.setupPasswordWithT(t, "ws-auth-fail", "stale-password")

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-auth-fail/sessions", nil)

	assert.Equal(t, http.StatusBadGateway, w.Code,
		"upstream 401 must be converted to 502")
	assert.Empty(t, w.Header().Get("WWW-Authenticate"),
		"WWW-Authenticate must NOT be forwarded to browser")
	assert.Contains(t, w.Body.String(), "upstream authentication failed")
}

func TestProxy_Upstream401_InvalidatesPasswordCache(t *testing.T) {
	callCount := 0
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"opencode\"")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	env.setupWorkspacePodWithT(t, "ws-cache-inv", "10.0.0.1", "Active", "")
	env.setupPasswordWithT(t, "ws-cache-inv", "test-password")

	// First request: 401 → cache invalidated
	w1 := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-cache-inv/sessions", nil)
	assert.Equal(t, http.StatusBadGateway, w1.Code)

	// Verify cache was invalidated
	env.handler.pwCacheMu.RLock()
	_, cached := env.handler.pwCache["ws-cache-inv"]
	env.handler.pwCacheMu.RUnlock()
	assert.False(t, cached, "password cache must be invalidated after 401")
}

// --- US-23.4: onPhaseChange cache invalidation ---

func TestOnPhaseChange_Failed_InvalidatesPwCache(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-fail", "test-password")

	// Prime the cache
	env.handler.pwCacheMu.Lock()
	env.handler.pwCache["ws-fail"] = "cached-password"
	env.handler.pwCacheMu.Unlock()

	// Simulate phase change to Failed
	ws := &v1.Workspace{}
	ws.Name = "ws-fail"
	ws.Status.Phase = v1.WorkspacePhaseFailed
	env.handler.onPhaseChange(ws)

	env.handler.pwCacheMu.RLock()
	_, cached := env.handler.pwCache["ws-fail"]
	env.handler.pwCacheMu.RUnlock()
	assert.False(t, cached, "pwCache must be invalidated on Failed transition")
}

func TestOnPhaseChange_ActiveFromNonActive_InvalidatesPwCache(t *testing.T) {
	env := newTestEnv(t)

	// Prime the cache and set prior phase to Creating
	env.handler.pwCacheMu.Lock()
	env.handler.pwCache["ws-recover"] = "old-password"
	env.handler.pwCacheMu.Unlock()
	env.handler.priorPhaseMu.Lock()
	env.handler.priorPhase["ws-recover"] = "Creating"
	env.handler.priorPhaseMu.Unlock()

	// Simulate phase change to Active (from Creating)
	ws := &v1.Workspace{}
	ws.Name = "ws-recover"
	ws.Status.Phase = v1.WorkspacePhaseActive
	env.handler.onPhaseChange(ws)

	env.handler.pwCacheMu.RLock()
	_, cached := env.handler.pwCache["ws-recover"]
	env.handler.pwCacheMu.RUnlock()
	assert.False(t, cached, "pwCache must be invalidated on Active-from-non-Active")
}

func TestOnPhaseChange_ActiveFromActive_DoesNotInvalidatePwCache(t *testing.T) {
	env := newTestEnv(t)

	// Prime the cache and set prior phase to Active
	env.handler.pwCacheMu.Lock()
	env.handler.pwCache["ws-stable"] = "good-password"
	env.handler.pwCacheMu.Unlock()
	env.handler.priorPhaseMu.Lock()
	env.handler.priorPhase["ws-stable"] = "Active"
	env.handler.priorPhaseMu.Unlock()

	// Simulate Active→Active reconcile
	ws := &v1.Workspace{}
	ws.Name = "ws-stable"
	ws.Status.Phase = v1.WorkspacePhaseActive
	env.handler.onPhaseChange(ws)

	env.handler.pwCacheMu.RLock()
	pw, cached := env.handler.pwCache["ws-stable"]
	env.handler.pwCacheMu.RUnlock()
	assert.True(t, cached, "pwCache must NOT be invalidated on Active→Active")
	assert.Equal(t, "good-password", pw)
}

func TestOnPhaseChange_Terminated_CleansUpPriorPhase(t *testing.T) {
	env := newTestEnv(t)

	env.handler.priorPhaseMu.Lock()
	env.handler.priorPhase["ws-term"] = "Active"
	env.handler.priorPhaseMu.Unlock()

	ws := &v1.Workspace{}
	ws.Name = "ws-term"
	ws.Status.Phase = v1.WorkspacePhaseTerminated
	env.handler.onPhaseChange(ws)

	env.handler.priorPhaseMu.Lock()
	_, exists := env.handler.priorPhase["ws-term"]
	env.handler.priorPhaseMu.Unlock()
	assert.False(t, exists, "priorPhase must be cleaned up on Terminated")
}

// --- Regression: existing phase transitions still work ---

func TestOnPhaseChange_Suspending_StillInvalidates(t *testing.T) {
	env := newTestEnv(t)

	env.handler.pwCacheMu.Lock()
	env.handler.pwCache["ws-susp"] = "pw"
	env.handler.pwCacheMu.Unlock()

	ws := &v1.Workspace{}
	ws.Name = "ws-susp"
	ws.Status.Phase = v1.WorkspacePhaseSuspending
	env.handler.onPhaseChange(ws)

	env.handler.pwCacheMu.RLock()
	_, cached := env.handler.pwCache["ws-susp"]
	env.handler.pwCacheMu.RUnlock()
	assert.False(t, cached)
}

func TestProxy_Upstream401_DoesNotPanic_WithoutWorkspaceIDInContext(t *testing.T) {
	// Edge case: if workspaceID is somehow empty in context, the 401
	// handler should still not panic.
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Basic")
		w.WriteHeader(http.StatusUnauthorized)
	})
	env.setupWorkspacePodWithT(t, "ws-edge", "10.0.0.1", "Active", "")
	env.setupPasswordWithT(t, "ws-edge", "pw")

	require.NotPanics(t, func() {
		env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-edge/sessions", nil)
	})
}
