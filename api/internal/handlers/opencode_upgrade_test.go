package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// --- stripVerboseQuery tests ---

func TestStripVerboseQuery_StripsVerbose(t *testing.T) {
	result := stripVerboseQuery("verbose=true&limit=10")
	assert.Equal(t, "limit=10", result)
}

func TestStripVerboseQuery_StripsWorkspace(t *testing.T) {
	result := stripVerboseQuery("workspace=ws_abc&limit=10")
	assert.Equal(t, "limit=10", result)
}

func TestStripVerboseQuery_StripsDirectory(t *testing.T) {
	result := stripVerboseQuery("directory=%2Fhome%2Fuser&limit=10")
	assert.Equal(t, "limit=10", result)
}

func TestStripVerboseQuery_StripsAllThree(t *testing.T) {
	result := stripVerboseQuery("verbose=true&workspace=ws_1&directory=/tmp&limit=5&offset=0")
	// Remaining params preserved (order may vary due to map iteration)
	assert.Contains(t, result, "limit=5")
	assert.Contains(t, result, "offset=0")
	assert.NotContains(t, result, "verbose")
	assert.NotContains(t, result, "workspace")
	assert.NotContains(t, result, "directory")
}

func TestStripVerboseQuery_PreservesOtherParams(t *testing.T) {
	result := stripVerboseQuery("limit=10&offset=0&search=hello")
	assert.Contains(t, result, "limit=10")
	assert.Contains(t, result, "offset=0")
	assert.Contains(t, result, "search=hello")
}

func TestStripVerboseQuery_EmptyString(t *testing.T) {
	assert.Equal(t, "", stripVerboseQuery(""))
}

func TestStripVerboseQuery_OnlyStrippedParams(t *testing.T) {
	result := stripVerboseQuery("verbose=true&workspace=ws_1&directory=/tmp")
	assert.Equal(t, "", result)
}

// --- persistTitleFromEvent tests ---

type mockSessionIndex struct {
	mu     sync.Mutex
	titles map[string]string // key: "workspaceID/sessionID"
}

func newMockSessionIndex() *mockSessionIndex {
	return &mockSessionIndex{titles: make(map[string]string)}
}

func (m *mockSessionIndex) UpsertTitle(_ context.Context, workspaceID, sessionID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.titles[workspaceID+"/"+sessionID] = title
	return nil
}

func (m *mockSessionIndex) RecordMessage(_, _, _ string, _ time.Time) {}
func (m *mockSessionIndex) ListByWorkspace(_ context.Context, _ string) ([]types.SessionListItem, error) {
	return nil, nil
}
func (m *mockSessionIndex) DeleteByWorkspace(_ context.Context, _ string) error { return nil }
func (m *mockSessionIndex) Start() error                                        { return nil }
func (m *mockSessionIndex) Stop() error                                         { return nil }

var _ interfaces.SessionIndexService = (*mockSessionIndex)(nil)

func TestPersistTitleFromEvent_V115FlatFormat(t *testing.T) {
	// v1.15 format: {"id":"evt_...","type":"session.updated","properties":{"sessionID":"ses_123","info":{"id":"ses_123","title":"My Title",...}}}
	event := `{"id":"evt_abc","type":"session.updated","properties":{"sessionID":"ses_123","info":{"id":"ses_123","title":"Hello World","slug":"hello-world"}}}`

	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-1", event)

	assert.Equal(t, "Hello World", mock.titles["ws-1/ses_123"])
}

func TestPersistTitleFromEvent_V127FlatFormat(t *testing.T) {
	// v1.2.27 format: {"type":"session.updated","properties":{"info":{"id":"ses_456","title":"Old Format"}}}
	event := `{"type":"session.updated","properties":{"info":{"id":"ses_456","title":"Old Format"}}}`

	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-2", event)

	assert.Equal(t, "Old Format", mock.titles["ws-2/ses_456"])
}

func TestPersistTitleFromEvent_MissingTitle(t *testing.T) {
	event := `{"id":"evt_abc","type":"session.updated","properties":{"sessionID":"ses_123","info":{"id":"ses_123"}}}`

	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-1", event)

	assert.Empty(t, mock.titles)
}

func TestPersistTitleFromEvent_MissingInfoID(t *testing.T) {
	event := `{"id":"evt_abc","type":"session.updated","properties":{"sessionID":"ses_123","info":{"title":"No ID"}}}`

	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-1", event)

	assert.Empty(t, mock.titles)
}

func TestPersistTitleFromEvent_MalformedJSON(t *testing.T) {
	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-1", "not json at all")

	assert.Empty(t, mock.titles)
}

func TestPersistTitleFromEvent_EmptyProperties(t *testing.T) {
	event := `{"id":"evt_abc","type":"session.updated","properties":{}}`

	mock := newMockSessionIndex()
	h := &ProxyHandler{sessionIndex: mock}
	h.persistTitleFromEvent("ws-1", event)

	assert.Empty(t, mock.titles)
}

// --- sseEvent ID field parsing tests ---

func TestSSETracker_ProcessEvent_V115Format_ParsesIDField(t *testing.T) {
	var mu sync.Mutex
	var capturedRawData string

	tracker := newTestSSETracker(func(workspaceID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		mu.Lock()
		capturedRawData = rawData
		mu.Unlock()
	})

	// v1.15 format with id field
	event := `{"id":"evt_01jw123","type":"session.status","properties":{"sessionID":"ses_abc","status":{"type":"idle"}}}`
	tracker.processEvent("ws-1", event)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, event, capturedRawData)

	// Verify the event was parsed correctly (id field didn't break parsing)
	var parsed sseEvent
	require.NoError(t, json.Unmarshal([]byte(event), &parsed))
	assert.Equal(t, "evt_01jw123", parsed.ID)
	assert.Equal(t, "session.status", parsed.Type)
}

func TestSSETracker_ProcessEvent_V115Format_SessionIdleStillWorks(t *testing.T) {
	var mu sync.Mutex
	var idleCalls []string

	tracker := newTestSSETracker(func(workspaceID, sessionID string) {
		mu.Lock()
		idleCalls = append(idleCalls, sessionID)
		mu.Unlock()
	})

	// v1.15 format with id field — should still trigger idle callback
	event := `{"id":"evt_01jw456","type":"session.status","properties":{"sessionID":"ses_xyz","status":{"type":"idle"}}}`
	tracker.processEvent("ws-1", event)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, idleCalls, 1)
	assert.Equal(t, "ses_xyz", idleCalls[0])
}

func TestSSETracker_ProcessEvent_V115Format_HeartbeatIgnored(t *testing.T) {
	var mu sync.Mutex
	var eventTypes []string

	tracker := newTestSSETracker(func(workspaceID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		mu.Lock()
		eventTypes = append(eventTypes, eventType)
		mu.Unlock()
	})

	// v1.15 heartbeat format
	event := `{"id":"evt_hb001","type":"server.heartbeat","properties":{}}`
	tracker.processEvent("ws-1", event)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, eventTypes, 1)
	assert.Equal(t, "server.heartbeat", eventTypes[0])
}
