package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// opencodeMessageResponse mirrors the shape opencode returns for
// POST /session/:id/message and GET /session/:id/message.
//
// The API proxy streams responses by default (including patch parts).
// Clients are responsible for filtering out unwanted part types.
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

// TestProxy_DefaultKeepsPatchParts verifies that POST .../message
// returns all parts including type=="patch" by default.
func TestProxy_DefaultKeepsPatchParts(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/ses_1/message",
		strings.NewReader(`{"parts":[{"type":"text","text":"hi"}]}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Info  map[string]interface{}   `json:"info"`
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// All 4 original parts should be preserved (no default stripping).
	assert.Len(t, resp.Parts, 4)
	hasPatch := false
	for _, p := range resp.Parts {
		if p["type"] == "patch" {
			hasPatch = true
		}
	}
	assert.True(t, hasPatch, "patch parts should be present by default")
}

// TestProxy_VerboseFlag_NotForwardedToOpencode verifies that ?verbose=true
// is consumed by the API proxy and not forwarded to opencode.
func TestProxy_VerboseFlag_NotForwardedToOpencode(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify the verbose flag is NOT forwarded to opencode (it's our flag).
		assert.NotContains(t, r.URL.RawQuery, "verbose",
			"verbose query param should not be forwarded to opencode")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST",
		"/api/v1/workspaces/ws-1/sessions/ses_1/message?verbose=true",
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

// TestProxy_DefaultKeepsPatchParts_FromHistoryResponse verifies that the history
// endpoint preserves all parts including patches by default.
func TestProxy_DefaultKeepsPatchParts_FromHistoryResponse(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeHistoryBody))
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions/ses_1/message", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var msgs []struct {
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 2)

	for _, m := range msgs {
		hasPatch := false
		for _, p := range m.Parts {
			if p["type"] == "patch" {
				hasPatch = true
			}
		}
		assert.True(t, hasPatch, "patch parts should be preserved in history by default")
	}
}

// TestProxy_VerboseFlag_FalseKeepsPatchParts ensures that ?verbose=false
// still preserves patch parts (verbose is now the default).
func TestProxy_VerboseFlag_FalseKeepsPatchParts(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(opencodeMessageBody))
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST",
		"/api/v1/workspaces/ws-1/sessions/ses_1/message?verbose=false",
		strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Parts []map[string]interface{} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Parts, 4, "patch parts should be preserved even with verbose=false")
	hasPatch := false
	for _, p := range resp.Parts {
		if p["type"] == "patch" {
			hasPatch = true
		}
	}
	assert.True(t, hasPatch, "patch parts should be present")
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
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
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
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions/ses_1/message", nil)
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
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/ses_1/message",
		strings.NewReader(`{}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, `{"error":"bad request"}`, w.Body.String())
}

// dummyVar makes go vet happy when this file is the only one in the package
// adding new variables; remove if a real one is added.
var _ = httptest.NewRecorder
