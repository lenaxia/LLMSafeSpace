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

func (m *MockAPIClient) CreateSandbox(ctx context.Context, req CreateSandboxReq) (*SandboxResp, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SandboxResp), args.Error(1)
}

func (m *MockAPIClient) TerminateSandbox(ctx context.Context, sandboxID string) error {
	return m.Called(ctx, sandboxID).Error(0)
}

func (m *MockAPIClient) CreateSession(ctx context.Context, sandboxID string) (*SessionResp, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SessionResp), args.Error(1)
}

func (m *MockAPIClient) GetHistory(ctx context.Context, sandboxID, sessionID string) ([]Message, error) {
	args := m.Called(ctx, sandboxID, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Message), args.Error(1)
}

func (m *MockAPIClient) SendMessage(ctx context.Context, sandboxID, sessionID, message string, timeout time.Duration) (string, error) {
	args := m.Called(ctx, sandboxID, sessionID, message, timeout)
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

// ===== sandbox_create =====

func TestSandboxCreate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateSandbox", ctx, CreateSandboxReq{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
	}).Return(&SandboxResp{ID: "sb-123", Status: "creating", Runtime: "python:3.10"}, nil)

	result, err := h.sandboxCreate(ctx, makeReq("sandbox_create", map[string]any{
		"runtime":        "python:3.10",
		"security_level": "standard",
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "sb-123")
	mockClient.AssertExpectations(t)
}

func TestSandboxCreate_MissingRuntime(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sandboxCreate(context.Background(), makeReq("sandbox_create", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "runtime")
}

func TestSandboxCreate_APIError(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateSandbox", ctx, CreateSandboxReq{Runtime: "python:3.10"}).
		Return((*SandboxResp)(nil), assert.AnError)

	result, err := h.sandboxCreate(ctx, makeReq("sandbox_create", map[string]any{"runtime": "python:3.10"}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "failed to create sandbox")
	mockClient.AssertExpectations(t)
}

// ===== sandbox_terminate =====

func TestSandboxTerminate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("TerminateSandbox", ctx, "sb-123").Return(nil)

	result, err := h.sandboxTerminate(ctx, makeReq("sandbox_terminate", map[string]any{"sandbox_id": "sb-123"}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "sb-123")
	mockClient.AssertExpectations(t)
}

func TestSandboxTerminate_MissingSandboxID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sandboxTerminate(context.Background(), makeReq("sandbox_terminate", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "sandbox_id")
}

// ===== session_create =====

func TestSessionCreate_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("CreateSession", ctx, "sb-123").Return(&SessionResp{ID: "sess-456"}, nil)

	result, err := h.sessionCreate(ctx, makeReq("session_create", map[string]any{"sandbox_id": "sb-123"}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "sess-456")
	mockClient.AssertExpectations(t)
}

func TestSessionCreate_MissingSandboxID(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sessionCreate(context.Background(), makeReq("session_create", map[string]any{}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// ===== session_message =====

func TestSessionMessage_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("SendMessage", ctx, "sb-123", "sess-456", "hello", 300*time.Second).
		Return("Hello! How can I help?", nil)

	result, err := h.sessionMessage(ctx, makeReq("session_message", map[string]any{
		"sandbox_id": "sb-123",
		"session_id": "sess-456",
		"message":    "hello",
	}))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "Hello! How can I help?", result.Content[0].(mcp.TextContent).Text)
	mockClient.AssertExpectations(t)
}

func TestSessionMessage_MissingMessage(t *testing.T) {
	h, _ := newTestHandlers()

	result, err := h.sessionMessage(context.Background(), makeReq("session_message", map[string]any{
		"sandbox_id": "sb-123",
		"session_id": "sess-456",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "message")
}

func TestSessionMessage_APIError(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("SendMessage", ctx, "sb-123", "sess-456", "hello", 300*time.Second).
		Return("", assert.AnError)

	result, err := h.sessionMessage(ctx, makeReq("session_message", map[string]any{
		"sandbox_id": "sb-123",
		"session_id": "sess-456",
		"message":    "hello",
	}))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	mockClient.AssertExpectations(t)
}

// ===== session_history =====

func TestSessionHistory_HappyPath(t *testing.T) {
	h, mockClient := newTestHandlers()
	ctx := context.Background()

	mockClient.On("GetHistory", ctx, "sb-123", "sess-456").Return([]Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi there!"},
	}, nil)

	result, err := h.sessionHistory(ctx, makeReq("session_history", map[string]any{
		"sandbox_id": "sb-123",
		"session_id": "sess-456",
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
		"sandbox_id": "sb-123",
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
