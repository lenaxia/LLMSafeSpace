// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// --- POST /api/v1/workspaces/:id/activate ---

func TestActivateWorkspace_Success(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ActivateWorkspace", mock.Anything, "test-user", "ws-1").Return(
		&types.ActivateWorkspaceResponse{Resumed: "ws-1", Suspended: "ws-old"}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/activate", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp types.ActivateWorkspaceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.Resumed)
	assert.Equal(t, "ws-old", resp.Suspended)
}

func TestActivateWorkspace_NotFound(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ActivateWorkspace", mock.Anything, "test-user", "ws-missing").Return(
		nil, apierrors.NewNotFoundError("workspace", "ws-missing", nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-missing/activate", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestActivateWorkspace_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/activate", nil)
	// No auth header
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestActivateWorkspace_ServiceError(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ActivateWorkspace", mock.Anything, "test-user", "ws-1").Return(
		nil, errors.New("internal error"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/activate", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- GET /api/v1/workspaces/:id/sessions ---

func TestListWorkspaceSessions_Success(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ListWorkspaceSessions", mock.Anything, "test-user", "ws-1").Return(
		[]types.SessionListItem{
			{ID: "sess-1", Title: "Chat about auth", MessageCount: 12, Status: "idle"},
			{ID: "sess-2", Title: "Debug proxy", MessageCount: 3, Status: "active"},
		}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var items []types.SessionListItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	assert.Len(t, items, 2)
	assert.Equal(t, "sess-1", items[0].ID)
	assert.Equal(t, "Chat about auth", items[0].Title)
	assert.Equal(t, 12, items[0].MessageCount)
}

func TestListWorkspaceSessions_EmptyList(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ListWorkspaceSessions", mock.Anything, "test-user", "ws-1").Return(
		[]types.SessionListItem{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var items []types.SessionListItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	assert.Empty(t, items)
}

func TestListWorkspaceSessions_NotFound(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("ListWorkspaceSessions", mock.Anything, "test-user", "ws-missing").Return(
		nil, apierrors.NewNotFoundError("workspace", "ws-missing", nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-missing/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListWorkspaceSessions_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sessions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// --- PUT /api/v1/workspaces/:id/sessions/:sessionId/title ---

func TestRenameSession_Success(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("RenameSession", mock.Anything, "test-user", "ws-1", "sess-1", "New Title").Return(nil)

	body, _ := json.Marshal(map[string]string{"title": "New Title"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/sessions/sess-1/title", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRenameSession_MissingTitle_Returns400(t *testing.T) {
	router, _ := newRouterFixture(t)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/sessions/sess-1/title", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRenameSession_EmptyBody_Returns400(t *testing.T) {
	router, _ := newRouterFixture(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/sessions/sess-1/title", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRenameSession_NotFound(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("RenameSession", mock.Anything, "test-user", "ws-missing", "sess-1", "Title").Return(
		apierrors.NewNotFoundError("workspace", "ws-missing", nil))

	body, _ := json.Marshal(map[string]string{"title": "Title"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-missing/sessions/sess-1/title", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRenameSession_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	body, _ := json.Marshal(map[string]string{"title": "Title"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/sessions/sess-1/title", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// --- PUT /api/v1/workspaces/:id ---

func TestRenameWorkspace_Success(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("RenameWorkspace", mock.Anything, "test-user", "ws-1", "New Name").Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "New Name"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRenameWorkspace_MissingName_Returns400(t *testing.T) {
	router, _ := newRouterFixture(t)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRenameWorkspace_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	body, _ := json.Marshal(map[string]string{"name": "New Name"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRenameWorkspace_NotFound(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("RenameWorkspace", mock.Anything, "test-user", "ws-missing", "Name").Return(
		apierrors.NewNotFoundError("workspace", "ws-missing", nil))

	body, _ := json.Marshal(map[string]string{"name": "Name"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-missing", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- POST /api/v1/workspaces/:id/sessions/new ---

func TestEnsureSession_Route_Success(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("EnsureSession", mock.Anything, "test-user", "ws-1").Return(
		&types.EnsureSessionResponse{
			WorkspaceID:    "sb-1",
			WorkspacePhase: "Active",
			SessionID:      "sess-abc",
			Resumed:        false,
		}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/sessions/new", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp types.EnsureSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "sb-1", resp.WorkspaceID)
	assert.Equal(t, "sess-abc", resp.SessionID)
	assert.False(t, resp.Resumed)
}

func TestEnsureSession_Route_Resumed(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("EnsureSession", mock.Anything, "test-user", "ws-2").Return(
		&types.EnsureSessionResponse{
			WorkspaceID:    "sb-new",
			WorkspacePhase: "Active",
			SessionID:      "sess-xyz",
			Resumed:        true,
		}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-2/sessions/new", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp types.EnsureSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Resumed)
}

func TestEnsureSession_Route_Unauthorized(t *testing.T) {
	router, _ := newRouterFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/sessions/new", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestEnsureSession_Route_ServiceError(t *testing.T) {
	router, svc := newRouterFixture(t)

	svc.workspace.On("EnsureSession", mock.Anything, "test-user", "ws-bad").Return(
		nil, errors.New("internal_error: sandbox_timeout"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-bad/sessions/new", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
