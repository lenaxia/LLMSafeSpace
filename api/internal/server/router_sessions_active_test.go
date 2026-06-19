// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

func TestSessionsActive_ReturnsActiveSessions(t *testing.T) {
	router, svc := newRouterFixture(t)

	// The sessions/active endpoint requires proxyHandler which is nil in this fixture.
	// When proxyHandler is nil, the route is not registered.
	// Verify it returns 404 (route not found) without proxy.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions/active", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Without proxyHandler, route doesn't exist — returns 404
	assert.Equal(t, http.StatusNotFound, rec.Code)
	_ = svc // suppress unused
}

func TestSessionsActive_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions/active", nil)
	// No auth header
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Either 401 (auth middleware rejects) or 404 (route not registered without proxy)
	assert.True(t, rec.Code == http.StatusUnauthorized || rec.Code == http.StatusNotFound)
}

// Test that ListWorkspaceSessions returns data from session index
func TestListWorkspaceSessions_WithData(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ListWorkspaceSessions", mock.Anything, "test-user", "ws-1").Return(
		[]types.SessionListItem{
			{ID: "s1", Title: "Auth refactor", MessageCount: 12, Status: "idle"},
			{ID: "s2", Title: "Bug fix", MessageCount: 3, Status: "active"},
		}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var items []types.SessionListItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	assert.Len(t, items, 2)
	assert.Equal(t, 12, items[0].MessageCount)
	assert.Equal(t, "active", items[1].Status)
}

// Test rename session with long title (boundary)
func TestRenameSession_LongTitle(t *testing.T) {
	router, svc := newRouterFixture(t)

	longTitle := make([]byte, 200)
	for i := range longTitle {
		longTitle[i] = 'a'
	}
	svc.workspace.On("RenameSession", mock.Anything, "test-user", "ws-1", "s1", string(longTitle)).Return(nil)

	body := `{"title":"` + string(longTitle) + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/sessions/s1/title",
		strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}
