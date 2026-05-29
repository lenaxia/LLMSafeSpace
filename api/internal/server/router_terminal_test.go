package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- Terminal test infrastructure ---

type terminalMockCache struct {
	store map[string]string
}

func newTerminalMockCache() *terminalMockCache {
	return &terminalMockCache{store: make(map[string]string)}
}

func (m *terminalMockCache) Get(_ context.Context, key string) (string, error) {
	v, ok := m.store[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

func (m *terminalMockCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.store[key] = value
	return nil
}

func (m *terminalMockCache) Delete(_ context.Context, key string) error {
	delete(m.store, key)
	return nil
}

type terminalMockWSGetter struct {
	workspaces map[string]*v1.Workspace
}

func (m *terminalMockWSGetter) GetWorkspace(id string) (*v1.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return ws, nil
}

func newTerminalTestRouter(t *testing.T) (*gin.Engine, *terminalMockCache) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err)

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}

	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	}))
	auth.On("GetUserID", mock.Anything).Return("user-1")
	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	svc := &mockServices{auth: auth, metrics: met}

	cache := newTerminalMockCache()
	wsGetter := &terminalMockWSGetter{
		workspaces: map[string]*v1.Workspace{
			"ws-active": {
				ObjectMeta: metav1.ObjectMeta{
					Name:   "ws-active",
					Labels: map[string]string{"user-id": "user-1"},
				},
				Status: v1.WorkspaceStatus{
					Phase:   v1.WorkspacePhaseActive,
					PodName: "ws-active-pod",
				},
			},
			"ws-suspended": {
				ObjectMeta: metav1.ObjectMeta{
					Name:   "ws-suspended",
					Labels: map[string]string{"user-id": "user-1"},
				},
				Status: v1.WorkspaceStatus{
					Phase: "Suspended",
				},
			},
			"ws-other": {
				ObjectMeta: metav1.ObjectMeta{
					Name:   "ws-other",
					Labels: map[string]string{"user-id": "other-user"},
				},
				Status: v1.WorkspaceStatus{
					Phase:   v1.WorkspacePhaseActive,
					PodName: "ws-other-pod",
				},
			},
		},
	}

	terminalHandler := handlers.NewTerminalHandler(cache, wsGetter, "llmsafespace", log)

	router := NewRouter(svc, log, nil, RouterConfig{
		TerminalHandler: terminalHandler,
	})

	return router, cache
}

// --- Integration tests ---

func TestTerminalTicket_E2E_Success(t *testing.T) {
	router, cache := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-active/terminal/ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expiresAt"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.Ticket, "tkt_")
	assert.NotEmpty(t, resp.ExpiresAt)

	// Verify ticket was stored in cache
	key := "terminal:ticket:" + resp.Ticket
	stored, err := cache.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Contains(t, stored, "ws-active")
	assert.Contains(t, stored, "user-1")
}

func TestTerminalTicket_E2E_WorkspaceNotActive(t *testing.T) {
	router, _ := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-suspended/terminal/ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestTerminalTicket_E2E_NotOwner(t *testing.T) {
	router, _ := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-other/terminal/ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Returns 404 (not 403) to avoid leaking existence
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTerminalTicket_E2E_WorkspaceNotFound(t *testing.T) {
	router, _ := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/nonexistent/terminal/ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTerminalWebSocket_E2E_InvalidTicket(t *testing.T) {
	router, _ := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-active/terminal?ticket=invalid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTerminalWebSocket_E2E_MissingTicket(t *testing.T) {
	router, _ := newTerminalTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-active/terminal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTerminalWebSocket_E2E_TicketConsumedOnce(t *testing.T) {
	router, cache := newTerminalTestRouter(t)

	// First: get a ticket
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-active/terminal/ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct{ Ticket string }
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Use the ticket (will fail WebSocket upgrade but ticket should be consumed)
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-active/terminal?ticket="+resp.Ticket, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	// The response won't be a proper WebSocket upgrade (no upgrade headers), but the ticket is consumed

	// Verify ticket is gone from cache
	key := "terminal:ticket:" + resp.Ticket
	_, err := cache.Get(context.Background(), key)
	assert.Error(t, err, "ticket should be consumed after first use")
}

func TestTerminalWebSocket_E2E_WorkspaceMismatch(t *testing.T) {
	router, cache := newTerminalTestRouter(t)

	// Store a ticket for ws-active
	ticketData := `{"userID":"user-1","workspaceID":"ws-active"}`
	cache.Set(context.Background(), "terminal:ticket:tkt_test123", ticketData, 30*time.Second)

	// Try to use it for a different workspace
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-suspended/terminal?ticket=tkt_test123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
