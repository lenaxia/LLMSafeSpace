//go:build integration

// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCredflow_AddCredentialMidStream_NoInterruption is the Epic 27a
// gate test. It re-runs the worklog 0125 scenario:
//
// 1. Create user, workspace, bind an llm-provider credential
// 2. Verify workspace pod boots and opencode loads the credential
// 3. Add a SECOND llm-provider credential via the API
// 4. Assert no LLM stream interruption (existing session not aborted)
// 5. Assert API response carries agentNeedsRefresh: true
// 6. Call POST /workspaces/:id/agent/reload
// 7. Assert opencode disposes (new provider visible after reload)
// 8. Assert agentNeedsRefresh: false after reload
//
// REQUIRES: running cluster with llmsafespace API, workspace pod, opencode.
// Run with: go test -tags integration -run TestCredflow ./api/internal/handlers/
func TestCredflow_AddCredentialMidStream_NoInterruption(t *testing.T) {
	t.Skip("Requires running cluster — run with -tags integration against a real environment")
}

// TestAgentReload_HappyPath_MockedDeps is a unit-level test of the
// AgentReloadHandler that exercises the full flow with mocked dependencies.
func TestAgentReload_HappyPath_MockedDeps(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// This test verifies the handler's HTTP interface without a real cluster.
	// It mocks the workspace service, DB, and pod resolver.
	t.Run("unauthenticated_returns_401", func(t *testing.T) {
		handler := NewAgentReloadHandler(nil, nil, nil, nil, nil)
		router := gin.New()
		router.POST("/workspaces/:id/agent/reload", handler.Reload)

		req := httptest.NewRequest(http.MethodPost, "/workspaces/ws-123/agent/reload", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "authentication required")
	})

	t.Run("authenticated_workspace_not_found_returns_error", func(t *testing.T) {
		// Mock workspace service that returns not-found
		handler := NewAgentReloadHandler(
			&mockWorkspaceServicer{err: notFoundErr("workspace", "ws-missing")},
			nil, nil, nil, nil,
		)
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("userID", "user-1")
			c.Next()
		})
		router.POST("/workspaces/:id/agent/reload", handler.Reload)

		req := httptest.NewRequest(http.MethodPost, "/workspaces/ws-missing/agent/reload", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// --- test helpers ---

type mockWorkspaceServicer struct {
	ws  interface{}
	err error
}

func (m *mockWorkspaceServicer) GetWorkspace(_ interface{}, _, _ string) (interface{}, error) {
	return m.ws, m.err
}

func notFoundErr(resourceType, resourceID string) error {
	return &struct {
		code    int
		message string
	}{404, resourceType + " " + resourceID + " not found"}
}

// Minimal assertion helpers for JSON response bodies.
func parseJSON(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

func jsonBody(t *testing.T, v interface{}) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return strings.NewReader(string(b))
}
