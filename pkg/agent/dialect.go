package agent

import "encoding/json"

// Dialect encodes the HTTP API contract of the agent running inside a workspace pod.
// Implement this interface for each supported agent runtime.
type Dialect interface {
	// --- Session route paths (pod-internal, relative) ---
	SessionCreatePath() string
	SessionListPath() string
	SessionMessagePath(sessionID string) string
	SessionPromptAsyncPath(sessionID string) string
	SessionAbortPath(sessionID string) string
	SessionGetPath(sessionID string) string
	EventStreamPath() string

	// --- Input request route paths ---
	QuestionListPath() string
	QuestionReplyPath(requestID string) string
	QuestionRejectPath(requestID string) string
	PermissionListPath() string
	PermissionReplyPath(requestID string) string

	// --- SSE event classification ---
	IsQuestionAsked(eventType string) bool
	IsQuestionResolved(eventType string) bool
	IsPermissionAsked(eventType string) bool
	IsPermissionResolved(eventType string) bool
	IsSessionIdle(eventType string, properties json.RawMessage) bool
	IsSessionBusy(eventType string, properties json.RawMessage) bool

	// --- Event parsing (returns nil+error if event doesn't match) ---
	ParseQuestionRequest(eventType string, properties json.RawMessage) (*QuestionRequest, error)
	ParsePermissionRequest(eventType string, properties json.RawMessage) (*PermissionRequest, error)
	ParseSessionStatus(properties json.RawMessage) (sessionID string, status string, err error)
}
