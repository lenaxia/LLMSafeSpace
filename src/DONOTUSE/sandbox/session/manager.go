package session

import (
	"context"
	"sync"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
)

// Manager handles WebSocket sessions
type Manager struct {
	sessions      map[string]*Session
	mu            sync.RWMutex
	cacheService  *cache.Service
}

// NewManager creates a new session manager
func NewManager(cacheService *cache.Service) *Manager {
	return &Manager{
		sessions:     make(map[string]*Session),
		cacheService: cacheService,
	}
}

// AddSession adds a session to the manager
func (m *Manager) AddSession(session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

// GetSession gets a session by ID
func (m *Manager) GetSession(sessionID string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

// CloseSession closes a session
func (m *Manager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	session, exists := m.sessions[sessionID]
	if !exists {
		return
	}
	
	// Close WebSocket connection
	if session.Conn != nil {
		_ = session.Conn.Close()
	}
	
	// Cancel any ongoing executions
	session.CancelAllExecutions()
	
	// Remove from sessions map
	delete(m.sessions, sessionID)
}

// CloseAllSessions closes all sessions
func (m *Manager) CloseAllSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	for _, session := range m.sessions {
		if session.Conn != nil {
			_ = session.Conn.Close()
		}
		session.CancelAllExecutions()
	}
	
	m.sessions = make(map[string]*Session)
}

// SaveSessionState saves session state to cache
func (m *Manager) SaveSessionState(sessionID string, state map[string]interface{}) error {
	return m.cacheService.SetSession(context.Background(), sessionID, state, 24*time.Hour)
}

// LoadSessionState loads session state from cache
func (m *Manager) LoadSessionState(sessionID string) (map[string]interface{}, error) {
	return m.cacheService.GetSession(context.Background(), sessionID)
}
