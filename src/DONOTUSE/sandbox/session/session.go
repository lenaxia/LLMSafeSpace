package session

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Session represents a WebSocket session
type Session struct {
	ID            string
	UserID        string
	SandboxID     string
	Conn          *websocket.Conn
	cancellations map[string]context.CancelFunc
	mu            sync.Mutex
}

// NewSession creates a new session
func NewSession(userID, sandboxID string, conn *websocket.Conn) *Session {
	return &Session{
		ID:            uuid.New().String(),
		UserID:        userID,
		SandboxID:     sandboxID,
		Conn:          conn,
		cancellations: make(map[string]context.CancelFunc),
	}
}

// SendError sends an error message
func (s *Session) SendError(code, message string) error {
	return s.Conn.WriteJSON(Message{
		Type:      "error",
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UnixMilli(),
	})
}

// Send sends a message
func (s *Session) Send(msg Message) error {
	// Set timestamp if not set
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}
	
	// Write message as JSON
	return s.Conn.WriteJSON(msg)
}

// SetCancellationFunc sets a cancellation function for an execution
func (s *Session) SetCancellationFunc(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancellations[id] = cancel
}

// SetCancellationFuncByID sets a cancellation function for an execution using the new ID field
// This is the preferred method over SetCancellationFunc
func (s *Session) SetCancellationFuncByID(id string, cancel context.CancelFunc) {
	s.SetCancellationFunc(id, cancel)
}

// RemoveCancellationFunc removes a cancellation function for an execution
func (s *Session) RemoveCancellationFunc(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancellations, id)
}

// CancelExecution cancels an execution
func (s *Session) CancelExecution(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	cancel, exists := s.cancellations[id]
	if !exists {
		return false
	}
	
	cancel()
	delete(s.cancellations, id)
	return true
}

// CancelExecutionByID cancels an execution using the new ID field
// This is the preferred method over CancelExecution
func (s *Session) CancelExecutionByID(id string) bool {
	return s.CancelExecution(id)
}

// CancelAllExecutions cancels all executions
func (s *Session) CancelAllExecutions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	for _, cancel := range s.cancellations {
		cancel()
	}
	
	s.cancellations = make(map[string]context.CancelFunc)
}
