package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opencodeMessageResponse mirrors the shape opencode returns for
// POST /session/:id/message and GET /session/:id/message.
//
// The API proxy strips parts where type=="patch" by default to keep responses
// concise; clients can pass ?verbose=true to receive the unfiltered response.
const opencodeMessageBody = `{
  "info": {"role":"assistant","id":"msg_1","sessionID":"ses_1"},
  "parts": [
    {"type":"step-start","id":"p1","sessionID":"ses_1","messageID":"msg_1"},
    {"type":"text","text":"Hello!","id":"p2","sessionID":"ses_1","messageID":"msg_1"},
    {"type":"step-finish","id":"p3","sessionID":"ses_1","messageID":"msg_1"},
    {"type":"patch","hash":"abc","files":["/workspace/foo","/workspace/bar"],"id":"p4","sessionID":"ses_1","messageID":"msg_1"}
  ]
}`

// opencodeHistoryBody mirrors the shape returned by GET /session/:id/message,
// which is an array of {info,parts} objects.
const opencodeHistoryBody = `[
  {
    "info": {"role":"user","id":"msg_0"},
    "parts": [
      {"type":"text","text":"hi"},
      {"type":"patch","files":["/workspace/x"]}
    ]
  },
  {
    "info": {"role":"assistant","id":"msg_1"},
    "parts": [
      {"type":"text","text":"hello"},
      {"type":"patch","files":["/workspace/y"]}
    ]
  }
]`

// TestProxy_StripsPatchParts_FromMessageResponse verifies that POST .../message
// returns parts without any type=="patch" entries by default.
func TestProxy_StripsPatchParts_FromMessageResponse(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST", "/api/v1/sandboxes/sb-1/sessions/ses_1/message",
		strings.NewReader(`{"parts":[{"type":"text","text":"hi"}]}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Info  map[string]interface{}   `json:"info"`
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	for _, p := range resp.Parts {
		assert.NotEqual(t, "patch", p["type"], "patch parts should be stripped by default")
	}
	// Original had 4 parts; 1 patch removed → 3 should remain.
	assert.Len(t, resp.Parts, 3, "expected 3 non-patch parts to remain")
}

// TestProxy_VerboseFlag_KeepsPatchParts verifies that ?verbose=true preserves
// patch parts in the response.
func TestProxy_VerboseFlag_KeepsPatchParts(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify the verbose flag is NOT forwarded to opencode (it's our flag).
		assert.NotContains(t, r.URL.RawQuery, "verbose",
			"verbose query param should not be forwarded to opencode")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST",
		"/api/v1/sandboxes/sb-1/sessions/ses_1/message?verbose=true",
		strings.NewReader(`{"parts":[{"type":"text","text":"hi"}]}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// All 4 original parts should be preserved.
	assert.Len(t, resp.Parts, 4)
	hasPatch := false
	for _, p := range resp.Parts {
		if p["type"] == "patch" {
			hasPatch = true
		}
	}
	assert.True(t, hasPatch, "patch parts should be present with verbose=true")
}

// TestProxy_StripsPatchParts_FromHistoryResponse verifies that the history
// endpoint also strips patch parts from each message in the array.
func TestProxy_StripsPatchParts_FromHistoryResponse(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeHistoryBody))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/sandboxes/sb-1/sessions/ses_1/message", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var msgs []struct {
		Info  map[string]interface{}   `json:"info"`
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 2)

	for _, m := range msgs {
		for _, p := range m.Parts {
			assert.NotEqual(t, "patch", p["type"],
				"history endpoint should strip patch parts from every message")
		}
	}
}

// TestProxy_VerboseFlag_FalseStillStripsParts ensures that ?verbose=false (or
// any value other than "true") still strips patch parts.
func TestProxy_VerboseFlag_FalseStillStripsParts(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST",
		"/api/v1/sandboxes/sb-1/sessions/ses_1/message?verbose=false",
		strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	for _, p := range resp.Parts {
		assert.NotEqual(t, "patch", p["type"])
	}
}

// TestProxy_StripDoesNotApplyToSessionList verifies that creating/listing
// sessions never has patch parts stripped — those endpoints don't return
// parts arrays anyway, but we want to confirm pass-through behaviour.
func TestProxy_StripDoesNotApplyToSessionList(t *testing.T) {
	body := `[{"id":"ses_1","slug":"x"}]`
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, w.Body.String(),
		"session list response should pass through unchanged")
}

// TestProxy_StripPreservesNonJSONResponses ensures non-JSON responses are
// passed through unchanged even on the message endpoint (defense in depth).
func TestProxy_StripPreservesNonJSONResponses(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json {{{"))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/sandboxes/sb-1/sessions/ses_1/message", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "not json {{{", w.Body.String())
}

// TestProxy_StripPreservesNon200Responses ensures non-2xx responses (errors)
// are passed through unchanged even on the message endpoint.
func TestProxy_StripPreservesNon200Responses(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	})
	env.setupSandboxWithT(t, "sb-1", "10.0.0.1", "Running", "ws-1")
	env.setupPasswordWithT(t, "sb-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST", "/api/v1/sandboxes/sb-1/sessions/ses_1/message",
		strings.NewReader(`{}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, `{"error":"bad request"}`, w.Body.String())
}

// dummyVar makes go vet happy when this file is the only one in the package
// adding new variables; remove if a real one is added.
var _ = httptest.NewRecorder
