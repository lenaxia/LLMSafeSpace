// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

type fakeTokenReviewer struct {
	username string
	err      error
	called   bool
	token    string
}

func (f *fakeTokenReviewer) Review(_ context.Context, token string) (string, error) {
	f.called = true
	f.token = token
	return f.username, f.err
}

type fakeBootstrapInjector struct {
	secrets []byte
	err     error
}

func (f *fakeBootstrapInjector) PrepareSecretsForInjection(_ context.Context, _, _, _ string) ([]byte, error) {
	return f.secrets, f.err
}

type fakeBootstrapLookup struct {
	ws  *types.WorkspaceMetadata
	err error
}

func (f *fakeBootstrapLookup) GetWorkspace(_ context.Context, _ string) (*types.WorkspaceMetadata, error) {
	return f.ws, f.err
}

func newTestBootstrapRouter(t *testing.T, reviewer *fakeTokenReviewer, injector *fakeBootstrapInjector, lookup *fakeBootstrapLookup) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewPodBootstrapHandler(reviewer, injector, lookup)
	r.POST("/internal/v1/pod-bootstrap", h.Bootstrap)
	return r
}

func doBootstrap(t *testing.T, router *gin.Engine, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/pod-bootstrap", bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestPodBootstrap_ValidToken_ReturnsSecrets(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	injector := &fakeBootstrapInjector{secrets: []byte(`[{"type":"llm-provider","name":"test"}]`)}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1", DefaultModel: "glm-5.2"}}

	router := newTestBootstrapRouter(t, reviewer, injector, lookup)
	w := doBootstrap(t, router, "valid-token", `{"workspaceID":"ws-abc"}`)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "valid-token", reviewer.token, "token reviewer must receive the raw bearer token")

	var resp bootstrapAPIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, string(resp.Secrets), "llm-provider")

	var wsCfg map[string]any
	require.NoError(t, json.Unmarshal(resp.WorkspaceConfig, &wsCfg))
	assert.Equal(t, "glm-5.2", wsCfg["defaultModel"])
}

func TestPodBootstrap_MissingAuthHeader_Returns401(t *testing.T) {
	router := newTestBootstrapRouter(t, &fakeTokenReviewer{}, &fakeBootstrapInjector{}, &fakeBootstrapLookup{})
	w := doBootstrap(t, router, "", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPodBootstrap_TokenReviewError_Returns500(t *testing.T) {
	reviewer := &fakeTokenReviewer{err: context.DeadlineExceeded}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, &fakeBootstrapLookup{})
	w := doBootstrap(t, router, "some-token", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPodBootstrap_SANameMismatch_Returns403(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-xyz"}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1"}}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPodBootstrap_SANotWorkspacePattern_Returns403(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:default"}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1"}}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPodBootstrap_WorkspaceNotFound_Returns404(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-ghost"}
	lookup := &fakeBootstrapLookup{ws: nil}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-ghost"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPodBootstrap_InjectorError_Returns500(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	injector := &fakeBootstrapInjector{err: context.DeadlineExceeded}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1"}}
	router := newTestBootstrapRouter(t, reviewer, injector, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPodBootstrap_EmptySecrets_Returns200Empty(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	injector := &fakeBootstrapInjector{secrets: []byte(`[]`)}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1"}}
	router := newTestBootstrapRouter(t, reviewer, injector, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp bootstrapAPIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "[]", string(resp.Secrets))
}

func TestPodBootstrap_NoDefaultModel_OmitsWorkspaceConfig(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	injector := &fakeBootstrapInjector{secrets: []byte(`[]`)}
	lookup := &fakeBootstrapLookup{ws: &types.WorkspaceMetadata{ID: "ws-abc", UserID: "user-1", DefaultModel: ""}}
	router := newTestBootstrapRouter(t, reviewer, injector, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp bootstrapAPIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.WorkspaceConfig, "workspaceConfig must be omitted when no default model")
}

func TestPodBootstrap_LookupError_Returns500(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	lookup := &fakeBootstrapLookup{err: context.DeadlineExceeded}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, lookup)
	w := doBootstrap(t, router, "token", `{"workspaceID":"ws-abc"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPodBootstrap_MissingWorkspaceID_Returns400(t *testing.T) {
	reviewer := &fakeTokenReviewer{username: "system:serviceaccount:llmsafespace:workspace-ws-abc"}
	router := newTestBootstrapRouter(t, reviewer, &fakeBootstrapInjector{}, &fakeBootstrapLookup{})
	w := doBootstrap(t, router, "token", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestParseWorkspaceIDFromSAName(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantID   string
		wantOK   bool
	}{
		{"uuid", "system:serviceaccount:llmsafespace:workspace-550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000", true},
		{"short", "system:serviceaccount:default:workspace-abc", "abc", true},
		{"not workspace prefix", "system:serviceaccount:default:default", "", false},
		{"garbage", "not-a-valid-username", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := parseWorkspaceIDFromSAName(tt.username)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantID, id)
		})
	}
}
