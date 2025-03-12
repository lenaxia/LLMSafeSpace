package session

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512KB
)

// Manager implements the SessionManager interface
type Manager struct {
	logger   *logger.Logger
	sessions map[string]*sessionState
	mu       sync.RWMutex
}

// sessionState represents the internal state of a session
type sessionState struct {
	session       *types.Session
	cancellations map[string]context.CancelFunc
	mu            sync.Mutex
	done          chan struct{}
}

// NewManager creates a new session manager
func NewManager(logger *logger.Logger) interfaces.SessionManager {
	return &Manager{
		logger:   logger.With("component", "session-manager"),
		sessions: make(map[string]*sessionState),
	}
}

// CreateSession creates a new WebSocket session
func (m *Manager) CreateSession(userID, sandboxID string, conn interfaces.WSConnection) (*types.Session, error) {
	sessionID := uuid.New().String()
	
	// Create session
	session := &types.Session{
		ID:        sessionID,
		UserID:    userID,
		SandboxID: sandboxID,
		Conn:      conn,
		SendError: func(code, message string) error {
			return conn.WriteJSON(types.Message{
				Type:      "error",
				Code:      code,
				Message:   message,
				Timestamp: time.Now().UnixMilli(),
			})
		},
		Send: func(msg types.Message) error {
			return conn.WriteJSON(msg)
		},
	}
	
	// Create session state
	state := &sessionState{
		session:       session,
		cancellations: make(map[string]context.CancelFunc),
		done:          make(chan struct{}),
	}
	
	// Store session
	m.mu.Lock()
	m.sessions[sessionID] = state
	m.mu.Unlock()
	
	// Start session management goroutines
	go m.writePump(state)
	
	m.logger.Info("Session created", "sessionID", sessionID, "userID", userID, "sandboxID", sandboxID)
	
	return session, nil
}

// GetSession retrieves a session by ID
func (m *Manager) GetSession(sessionID string) (*types.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	state, exists := m.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	
	return state.session, nil
}

// CloseSession closes a session
func (m *Manager) CloseSession(sessionID string) {
	m.mu.Lock()
	state, exists := m.sessions[sessionID]
	if exists {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	
	if exists {
		// Cancel all pending executions
		state.mu.Lock()
		for _, cancel := range state.cancellations {
			cancel()
		}
		state.mu.Unlock()
		
		// Signal done
		close(state.done)
		
		m.logger.Info("Session closed", "sessionID", sessionID)
	}
}

// SetCancellationFunc sets a cancellation function for an execution
func (m *Manager) SetCancellationFunc(sessionID, executionID string, cancel context.CancelFunc) {
	m.mu.RLock()
	state, exists := m.sessions[sessionID]
	m.mu.RUnlock()
	
	if exists {
		state.mu.Lock()
		state.cancellations[executionID] = cancel
		state.mu.Unlock()
	}
}

// CancelExecution cancels an execution
func (m *Manager) CancelExecution(sessionID, executionID string) bool {
	m.mu.RLock()
	state, exists := m.sessions[sessionID]
	m.mu.RUnlock()
	
	if !exists {
		return false
	}
	
	state.mu.Lock()
	cancel, exists := state.cancellations[executionID]
	if exists {
		delete(state.cancellations, executionID)
	}
	state.mu.Unlock()
	
	if exists {
		cancel()
		return true
	}
	
	return false
}

// writePump pumps messages from the hub to the websocket connection.
func (m *Manager) writePump(state *sessionState) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()
	
	for {
		select {
		case <-ticker.C:
			state.session.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := state.session.Conn.WriteMessage(9, []byte{}); err != nil { // 9 is ping message
				m.logger.Error("Failed to write ping", err, "sessionID", state.session.ID)
				m.CloseSession(state.session.ID)
				return
			}
		case <-state.done:
			return
		}
	}
}

// Start initializes the session manager
func (m *Manager) Start() error {
	m.logger.Info("Session manager started")
	return nil
}

// Stop shuts down the session manager
func (m *Manager) Stop() error {
	m.mu.Lock()
	sessions := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		sessions = append(sessions, id)
	}
	m.mu.Unlock()
	
	for _, id := range sessions {
		m.CloseSession(id)
	}
	
	m.logger.Info("Session manager stopped")
	return nil
}
