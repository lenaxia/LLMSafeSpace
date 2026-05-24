package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAPIClient implements APIClient for testing.
type MockAPIClient struct {
	mock.Mock
}

func (m *MockAPIClient) CreateWorkspace(ctx context.Context, req CreateWorkspaceReq) (*WorkspaceResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*WorkspaceResp), args.Error(1)
}

func (m *MockAPIClient) ActivateWorkspace(ctx context.Context, workspaceID string) (*ActivateResp, error) {
	args := m.Called(ctx, workspaceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ActivateResp), args.Error(1)
}

func (m *MockAPIClient) SuspendWorkspace(ctx context.Context, workspaceID string) error {
	return m.Called(ctx, workspaceID).Error(0)
}

func (m *MockAPIClient) CreateSession(ctx context.Context, workspaceID string) (*SessionResp, error) {
	args := m.Called(ctx, workspaceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SessionResp), args.Error(1)
}

func (m *MockAPIClient) GetHistory(ctx context.Context, workspaceID, sessionID string) ([]Message, error) {
	args := m.Called(ctx, workspaceID, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Message), args.Error(1)
}

func (m *MockAPIClient) SendMessage(ctx context.Context, workspaceID, sessionID, message string, timeout time.Duration) (string, error) {
	args := m.Called(ctx, workspaceID, sessionID, message, timeout)
	return args.String(0), args.Error(1)
}

func newTestHandlers() (*handlers, *MockAPIClient) {
	mockClient := &MockAPIClient{}
	h := &handlers{client: mockClient, timeout: 300 * time.Second}
	return h, mockClient
}

func makeReq(name string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

// ===== workspace_create =====

func TestWorkspaceCreate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateWorkspace", ctx, CreateWorkspaceReq{
		Runtime: "python:3.10",
		Name:    "my-project",
	}).Return(&WorkspaceResp{ID: "ws-123", Name: "my-project", Runtime: "python:3.10", Phase: "Active"}, nil)

	result, err := h.workspaceCreate(ctx, makeReq("workspace_create", map[string]any{
		"runtime": "python:3.10",
		"name":    "my-project",
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "ws-123")
	mockClient.AssertExpectations(t)
}

func TestWorkspaceCreate_MissingRuntime(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.workspaceCreate(context.Background(), makeReq("workspace_create", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "runtime")
}

func TestWorkspaceCreate_APIError(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateWorkspace", ctx, CreateWorkspaceReq{Runtime: "python:3.10"}).
		Return((*WorkspaceResp)(nil), assert.AnError)

	result, err := h.workspaceCreate(ctx, makeReq("workspace_create", map[string]any{"runtime": "python:3.10"}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	mockClient.AssertExpectations(t)
}

// ===== workspace_activate =====

func TestWorkspaceActivate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("ActivateWorkspace", ctx, "ws-123").
		Return(&ActivateResp{Resumed: "ws-123"}, nil)

	result, err := h.workspaceActivate(ctx, makeReq("workspace_activate", map[string]any{"workspace_id": "ws-123"}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "ws-123")
	mockClient.AssertExpectations(t)
}

func TestWorkspaceActivate_MissingID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.workspaceActivate(context.Background(), makeReq("workspace_activate", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "workspace_id")
}

// ===== workspace_stop =====

func TestWorkspaceStop_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("SuspendWorkspace", ctx, "ws-123").Return(nil)

	result, err := h.workspaceStop(ctx, makeReq("workspace_stop", map[string]any{"workspace_id": "ws-123"}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "ws-123")
	mockClient.AssertExpectations(t)
}

func TestWorkspaceStop_MissingID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.workspaceStop(context.Background(), makeReq("workspace_stop", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// ===== session_create =====

func TestSessionCreate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateSession", ctx, "ws-123").Return(&SessionResp{ID: "sess-456"}, nil)

	result, err := h.sessionCreate(ctx, makeReq("session_create", map[string]any{"workspace_id": "ws-123"}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "sess-456")
	mockClient.AssertExpectations(t)
}

func TestSessionCreate_MissingWorkspaceID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sessionCreate(context.Background(), makeReq("session_create", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// ===== session_message =====

func TestSessionMessage_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("SendMessage", ctx, "ws-123", "sess-456", "hello", 300*time.Second).
		Return("Hello! How can I help?", nil)

	result, err := h.sessionMessage(ctx, makeReq("session_message", map[string]any{
		"workspace_id": "ws-123",
		"session_id":   "sess-456",
		"message":      "hello",
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "Hello! How can I help?", result.Content[0].(mcp.TextContent).Text)
	mockClient.AssertExpectations(t)
}

func TestSessionMessage_MissingMessage(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sessionMessage(context.Background(), makeReq("session_message", map[string]any{
		"workspace_id": "ws-123",
		"session_id":   "sess-456",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "message")
}

func TestSessionMessage_APIError(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("SendMessage", ctx, "ws-123", "sess-456", "hello", 300*time.Second).
		Return("", assert.AnError)

	result, err := h.sessionMessage(ctx, makeReq("session_message", map[string]any{
		"workspace_id": "ws-123",
		"session_id":   "sess-456",
		"message":      "hello",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	mockClient.AssertExpectations(t)
}

// ===== session_history =====

func TestSessionHistory_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("GetHistory", ctx, "ws-123", "sess-456").Return([]Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi there!"},
	}, nil)

	result, err := h.sessionHistory(ctx, makeReq("session_history", map[string]any{
		"workspace_id": "ws-123",
		"session_id":   "sess-456",
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "hello")
	assert.Contains(t, text, "Hi there!")
	mockClient.AssertExpectations(t)
}

func TestSessionHistory_MissingSessionID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sessionHistory(context.Background(), makeReq("session_history", map[string]any{
		"workspace_id": "ws-123",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// ===== NewServer integration =====

func TestNewServer_RegistersAllTools(t *testing.T) {
	mockClient := &MockAPIClient{}
	srv := NewServer(mockClient, 300*time.Second)
	require.NotNil(t, srv)
}
