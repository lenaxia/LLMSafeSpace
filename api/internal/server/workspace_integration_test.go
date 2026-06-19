// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/api/internal/services/workspace"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// integrationOrgStore is a controllable workspace.OrgMembershipChecker for the
// integration harness. It mirrors the stubOrgChecker pattern from the workspace
// package's d5/d6 tests but lives in the server test package (the original is
// unexported). Map keys are "orgID:userID".
type integrationOrgStore struct {
	members map[string]bool
	admins  map[string]bool
	err     error
}

func newIntegrationOrgStore() *integrationOrgStore {
	return &integrationOrgStore{members: map[string]bool{}, admins: map[string]bool{}}
}

func (s *integrationOrgStore) IsOrgMember(_ context.Context, orgID, userID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.members[orgID+":"+userID], nil
}

func (s *integrationOrgStore) IsOrgAdmin(_ context.Context, orgID, userID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.admins[orgID+":"+userID], nil
}

func (s *integrationOrgStore) GetUserOrgID(_ context.Context, _ string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "", nil
}

var _ workspace.OrgMembershipChecker = (*integrationOrgStore)(nil)

// newIntegrationService builds a REAL *workspace.Service backed by a mock DB
// (controllable GetWorkspace) and the supplied orgStore, satisfying
// workspaceAccessService via the concrete ResolveWorkspace + CheckOwnership
// methods. The k8s client is a bare mock — the middleware path never reaches
// it (ResolveWorkspace only touches dbService; CheckOwnership only touches
// orgStore), but the constructor requires non-nil.
func newIntegrationService(t *testing.T, meta *types.WorkspaceMetadata, dbErr error, org workspace.OrgMembershipChecker) (*workspace.Service, *imocks.MockDatabaseService) {
	t.Helper()
	log := lmocks.NewMockLogger()
	log.On("Info", mock.Anything, mock.Anything).Maybe()
	log.On("Debug", mock.Anything, mock.Anything).Maybe()
	log.On("Warn", mock.Anything, mock.Anything).Maybe()
	log.On("Error", mock.Anything, mock.Anything, mock.Anything).Maybe()
	log.On("With", mock.Anything).Return(log).Maybe()

	db := &imocks.MockDatabaseService{}
	switch {
	case dbErr != nil:
		db.On("GetWorkspace", mock.Anything, mock.Anything).Return((*types.WorkspaceMetadata)(nil), dbErr).Maybe()
	case meta == nil:
		db.On("GetWorkspace", mock.Anything, mock.Anything).Return((*types.WorkspaceMetadata)(nil), nil).Maybe()
	default:
		db.On("GetWorkspace", mock.Anything, mock.Anything).Return(meta, nil).Maybe()
	}

	svc, err := workspace.New(log, kmocks.NewMockKubernetesClient(), db, nil, nil, &workspace.Config{Namespace: "default"})
	require.NoError(t, err)
	if org != nil {
		svc.SetOrgStore(org)
	}
	return svc, db
}

// setupWorkspaceIntegrationRouter wires the REAL WorkspaceAccessMiddleware
// over the REAL *workspace.Service (with mock DB + mock orgStore) and registers
// a stub handler returning 200 {"ok":true} on every /:id route shape that
// exists in production. This is the Story 4 harness: it proves the ownership
// logic end-to-end through the gate (not via a mocked CheckOwnership).
//
// The stub auth middleware sets userID to the configured value so the test
// controls the acting identity. WorkspaceAccessMiddleware then resolves + owns
// the decision exactly as in production.
func setupWorkspaceIntegrationRouter(t *testing.T, userID string, meta *types.WorkspaceMetadata, dbErr error, org workspace.OrgMembershipChecker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc, _ := newIntegrationService(t, meta, dbErr, org)

	router := gin.New()
	idGroup := router.Group("/api/v1/workspaces/:id")
	idGroup.Use(func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	})
	idGroup.Use(middleware.WorkspaceAccessMiddleware(svc))

	registerIntegrationStubRoutes(idGroup)
	return router
}

// registerIntegrationStubRoutes mirrors every /:id route registered in
// production router.go (registerWorkspaceRoutes, registerProxyRoutes, the
// inline sessions/active + terminal/ticket, and the SecretsHandler block).
// Each handler is a no-op 200 so allowed-case assertions confirm the request
// reached downstream without needing real K8s/DB.
func registerIntegrationStubRoutes(idGroup *gin.RouterGroup) {
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }

	idGroup.GET("", ok)
	idGroup.PUT("", ok)
	idGroup.DELETE("", ok)

	idGroup.POST("/suspend", ok)
	idGroup.POST("/restart", ok)
	idGroup.POST("/activate", ok)
	idGroup.GET("/status", ok)
	idGroup.POST("/agent/reload", ok)

	idGroup.GET("/sessions", ok)
	idGroup.POST("/sessions/new", ok)
	idGroup.GET("/sessions/active", ok)
	idGroup.PUT("/sessions/:sessionId/title", ok)
	idGroup.PUT("/sessions/:sessionId/seen", ok)

	idGroup.POST("/sessions/:sessionId/message", ok)
	idGroup.GET("/sessions/:sessionId/message", ok)
	idGroup.POST("/sessions/:sessionId/prompt", ok)
	idGroup.GET("/sessions/:sessionId", ok)
	idGroup.POST("/sessions/:sessionId/abort", ok)
	idGroup.DELETE("/sessions/:sessionId", ok)
	idGroup.POST("/sessions/:sessionId/queue", ok)
	idGroup.GET("/sessions/:sessionId/queue", ok)
	idGroup.DELETE("/sessions/:sessionId/queue/:messageId", ok)

	idGroup.GET("/session-events", ok)

	idGroup.GET("/question", ok)
	idGroup.POST("/question/:requestID/reply", ok)
	idGroup.POST("/question/:requestID/reject", ok)
	idGroup.GET("/permission", ok)
	idGroup.POST("/permission/:requestID/reply", ok)

	idGroup.POST("/terminal/ticket", ok)

	idGroup.PUT("/bindings", ok)
	idGroup.GET("/bindings", ok)
	idGroup.POST("/reload-secrets", ok)
	idGroup.PUT("/env", ok)
	idGroup.GET("/env", ok)
	idGroup.DELETE("/env/:name", ok)
	idGroup.GET("/models", ok)
	idGroup.PUT("/model", ok)
}

// integrationRoutes is the exhaustive list of /:id route shapes exercised by
// the matrix. Path values are appended to /api/v1/workspaces/ws-1. Param
// segments use concrete placeholders so the request actually matches a
// registered route. Every previously-UNGUARDED route from design 0041
// (sessions/:sessionId/message, session-events, sessions/active, reload-secrets,
// bindings, env/:name) is a row here.
var integrationRoutes = []struct {
	label  string
	method string
	path   string
}{
	{"get_root", http.MethodGet, ""},
	{"put_root", http.MethodPut, ""},
	{"delete_root", http.MethodDelete, ""},

	{"suspend", http.MethodPost, "/suspend"},
	{"restart", http.MethodPost, "/restart"},
	{"activate", http.MethodPost, "/activate"},
	{"status", http.MethodGet, "/status"},
	{"agent_reload", http.MethodPost, "/agent/reload"},

	{"list_sessions", http.MethodGet, "/sessions"},
	{"new_session", http.MethodPost, "/sessions/new"},
	{"active_sessions", http.MethodGet, "/sessions/active"},
	{"session_title", http.MethodPut, "/sessions/sess-1/title"},
	{"session_seen", http.MethodPut, "/sessions/sess-1/seen"},

	{"send_message", http.MethodPost, "/sessions/sess-1/message"},
	{"get_message", http.MethodGet, "/sessions/sess-1/message"},
	{"send_prompt", http.MethodPost, "/sessions/sess-1/prompt"},
	{"get_session", http.MethodGet, "/sessions/sess-1"},
	{"abort_session", http.MethodPost, "/sessions/sess-1/abort"},
	{"delete_session", http.MethodDelete, "/sessions/sess-1"},
	{"enqueue", http.MethodPost, "/sessions/sess-1/queue"},
	{"list_queue", http.MethodGet, "/sessions/sess-1/queue"},
	{"delete_queue_msg", http.MethodDelete, "/sessions/sess-1/queue/msg-1"},

	{"session_events", http.MethodGet, "/session-events"},

	{"list_questions", http.MethodGet, "/question"},
	{"question_reply", http.MethodPost, "/question/req-1/reply"},
	{"question_reject", http.MethodPost, "/question/req-1/reject"},
	{"list_permissions", http.MethodGet, "/permission"},
	{"permission_reply", http.MethodPost, "/permission/req-1/reply"},

	{"terminal_ticket", http.MethodPost, "/terminal/ticket"},

	{"put_bindings", http.MethodPut, "/bindings"},
	{"get_bindings", http.MethodGet, "/bindings"},
	{"reload_secrets", http.MethodPost, "/reload-secrets"},
	{"put_env", http.MethodPut, "/env"},
	{"get_env", http.MethodGet, "/env"},
	{"delete_env_name", http.MethodDelete, "/env/MY_VAR"},
	{"get_models", http.MethodGet, "/models"},
	{"put_model", http.MethodPut, "/model"},
}

// TestWorkspaceIntegration_AccessMatrix is the Story 4 Definition of Done: for
// every authorization scenario × every /:id route shape, it drives the REAL
// WorkspaceAccessMiddleware over the REAL workspace.Service.ResolveWorkspace +
// CheckOwnership (via mock DB + mock orgStore) and asserts the middleware's
// decision. A 200 means the request reached the stub handler (the gate let it
// through); 403/404/500 means the middleware short-circuited before any
// handler ran.
func TestWorkspaceIntegration_AccessMatrix(t *testing.T) {
	orgID := "org-1"

	scenarios := []struct {
		name       string
		userID     string
		meta       *types.WorkspaceMetadata
		dbErr      error
		orgSetup   func(org *integrationOrgStore)
		wantStatus int
	}{
		{
			name:   "offboarded_creator_D5_denied",
			userID: "creator",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			orgSetup: func(o *integrationOrgStore) {
				o.members[orgID+":creator"] = false
				o.admins[orgID+":creator"] = false
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "non_owner_non_admin_org_denied",
			userID: "stranger",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			orgSetup: func(o *integrationOrgStore) {
				o.admins[orgID+":stranger"] = false
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "org_admin_authorized",
			userID: "admin",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			orgSetup: func(o *integrationOrgStore) {
				o.admins[orgID+":admin"] = true
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "creator_current_member_authorized",
			userID: "creator",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			orgSetup: func(o *integrationOrgStore) {
				o.members[orgID+":creator"] = true
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "personal_workspace_owner_authorized",
			userID:     "owner",
			meta:       &types.WorkspaceMetadata{ID: "ws-1", UserID: "owner"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "personal_workspace_non_owner_denied",
			userID:     "stranger",
			meta:       &types.WorkspaceMetadata{ID: "ws-1", UserID: "owner"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "unknown_workspace_not_found",
			userID:     "anyone",
			meta:       nil,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "db_error_internal_fail_safe",
			userID:     "anyone",
			dbErr:      errors.New("db connection refused"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			org := newIntegrationOrgStore()
			if sc.orgSetup != nil {
				sc.orgSetup(org)
			}
			router := setupWorkspaceIntegrationRouter(t, sc.userID, sc.meta, sc.dbErr, org)

			for _, r := range integrationRoutes {
				r := r
				t.Run(r.label, func(t *testing.T) {
					url := "/api/v1/workspaces/ws-1" + r.path
					req := httptest.NewRequest(r.method, url, nil)
					req.Header.Set("Authorization", "Bearer test")
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)

					assert.Equalf(t, sc.wantStatus, rec.Code,
						"scenario=%s route=%s %s: expected %d, body=%s",
						sc.name, r.method, url, sc.wantStatus, rec.Body.String())
				})
			}
		})
	}
}

// TestWorkspaceIntegration_AllowedReachesHandler confirms that the 200 cases
// in the matrix are not coincidental non-404s: the stub handler actually ran
// and produced the documented {"ok":true} body. This proves the gate lets
// authorized traffic reach downstream.
func TestWorkspaceIntegration_AllowedReachesHandler(t *testing.T) {
	orgID := "org-1"
	org := newIntegrationOrgStore()
	org.members[orgID+":creator"] = true
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID}
	router := setupWorkspaceIntegrationRouter(t, "creator", meta, nil, org)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/status", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"ok":true`, "stub handler must have run for an authorized request")
}

// TestWorkspaceIntegration_MetaPropagatedToContext proves design 0041 D1
// step 4: on an ALLOWED request the middleware stores the resolved
// *WorkspaceMetadata under types.ContextKeyWorkspaceMeta so downstream
// handlers (and service-layer methods reading a plain context.Context) can
// reuse it without a second DB hit. A handler reads it via
// types.WorkspaceMetaFromCtx and echoes the fields.
func TestWorkspaceIntegration_MetaPropagatedToContext(t *testing.T) {
	orgID := "org-1"
	org := newIntegrationOrgStore()
	org.members[orgID+":creator"] = true
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID}

	svc, _ := newIntegrationService(t, meta, nil, org)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	idGroup := router.Group("/api/v1/workspaces/:id")
	idGroup.Use(func(c *gin.Context) { c.Set("userID", "creator"); c.Next() })
	idGroup.Use(middleware.WorkspaceAccessMiddleware(svc))

	var gotMeta *types.WorkspaceMetadata
	var gotOK bool
	idGroup.GET("/status", func(c *gin.Context) {
		gotMeta, gotOK = types.WorkspaceMetaFromCtx(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/status", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.True(t, gotOK, "WorkspaceMetaFromCtx must report present for an allowed request")
	require.NotNil(t, gotMeta, "meta must be non-nil in context")
	assert.Equal(t, "ws-1", gotMeta.ID)
	assert.Equal(t, "creator", gotMeta.UserID)
	require.NotNil(t, gotMeta.OrgID)
	assert.Equal(t, orgID, *gotMeta.OrgID)
}

// TestWorkspaceIntegration_TerminalWebSocketNotGated documents design 0041
// edge case 3: GET /api/v1/workspaces/:id/terminal is the WebSocket endpoint
// that authenticates via a one-time ticket, NOT via WorkspaceAccessMiddleware.
// It is registered on the ROOT router in production (router.go:226), not on
// idGroup. The harness therefore does NOT register it behind the middleware,
// and this test asserts that fact so a future harness change cannot silently
// claim middleware coverage for the WebSocket route. Only POST /:id/terminal/
// ticket (ticket issuance) is gated; the ticket was issued after middleware
// verification, so the WebSocket inherits the check transitively.
func TestWorkspaceIntegration_TerminalWebSocketNotGated(t *testing.T) {
	orgID := "org-1"
	org := newIntegrationOrgStore()
	org.members[orgID+":creator"] = true
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID}
	router := setupWorkspaceIntegrationRouter(t, "creator", meta, nil, org)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/terminal", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"GET /:id/terminal (WebSocket) must NOT be registered behind the middleware in this harness; "+
			"it lives on the root router with ticket-auth in production (router.go:226)")
}
