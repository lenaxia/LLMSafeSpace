package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Message represents a WebSocket message
type Message struct {
	Type        string      `json:"type"`
	ExecutionID string      `json:"executionId,omitempty"`
	Stream      string      `json:"stream,omitempty"`
	Content     string      `json:"content,omitempty"`
	Code        string      `json:"code,omitempty"`
	Message     string      `json:"message,omitempty"`
	ExitCode    int         `json:"exitCode,omitempty"`
	Timestamp   int64       `json:"timestamp,omitempty"`
	Data        interface{} `json:"data,omitempty"`
}

// GetString gets a string value from the message data
func (m *Message) GetString(key string) (string, bool) {
	if m.Data == nil {
		return "", false
	}

	data, ok := m.Data.(map[string]interface{})
	if !ok {
		return "", false
	}

	value, ok := data[key].(string)
	return value, ok
}

// GetInt gets an int value from the message data
func (m *Message) GetInt(key string) (int, bool) {
	if m.Data == nil {
		return 0, false
	}

	data, ok := m.Data.(map[string]interface{})
	if !ok {
		return 0, false
	}

	value, ok := data[key].(float64)
	if !ok {
		return 0, false
	}

	return int(value), true
}

// ParseMessage parses a JSON message
func ParseMessage(data []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return msg, fmt.Errorf("failed to parse message: %w", err)
	}
	return msg, nil
}

// Session represents a WebSocket session
type Session struct {
	ID            string
	UserID        string
	SandboxID     string
	Conn          *websocket.Conn
	cancellations map[string]context.CancelFunc
	mu            sync.Mutex
}

// SessionManager manages WebSocket sessions
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession creates a new session
func (m *SessionManager) CreateSession(userID, sandboxID string, conn *websocket.Conn) *Session {
	session := &Session{
		ID:            uuid.New().String(),
		UserID:        userID,
		SandboxID:     sandboxID,
		Conn:          conn,
		cancellations: make(map[string]context.CancelFunc),
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()

	return session
}

// GetSession gets a session by ID
func (m *SessionManager) GetSession(sessionID string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

// CloseSession closes a session
func (m *SessionManager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[sessionID]; ok {
		// Cancel all pending executions
		session.mu.Lock()
		for _, cancel := range session.cancellations {
			cancel()
		}
		session.mu.Unlock()

		// Close connection
		session.Conn.Close()

		// Remove from sessions map
		delete(m.sessions, sessionID)
	}
}

// Send sends a message to the session
func (s *Session) Send(msg Message) error {
	// Set timestamp if not set
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}

	// Write message as JSON
	return s.Conn.WriteJSON(msg)
}

// SendError sends an error message to the session
func (s *Session) SendError(code, message string) error {
	return s.Send(Message{
		Type:      "error",
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UnixMilli(),
	})
}

// SetCancellationFunc sets a cancellation function for an execution
func (s *Session) SetCancellationFunc(executionID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancellations[executionID] = cancel
}

// RemoveCancellationFunc removes a cancellation function for an execution
func (s *Session) RemoveCancellationFunc(executionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancellations, executionID)
}

// CancelExecution cancels an execution
func (s *Session) CancelExecution(executionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cancel, ok := s.cancellations[executionID]; ok {
		cancel()
		delete(s.cancellations, executionID)
		return true
	}

	return false
}
