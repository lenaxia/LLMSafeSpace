package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
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
	logger         *logger.Logger
	sessions       map[string]*sessionState
	executionSvc   interfaces.ExecutionService
	fileSvc        interfaces.FileService
	mu             sync.RWMutex
}

// sessionState represents the internal state of a session
type sessionState struct {
	session       *types.Session
	cancellations map[string]context.CancelFunc
	mu            sync.Mutex
	done          chan struct{}
}

// NewManager creates a new session manager
func NewManager(logger *logger.Logger, executionSvc interfaces.ExecutionService, fileSvc interfaces.FileService) interfaces.SessionManager {
	return &Manager{
		logger:       logger.With("component", "session-manager"),
		sessions:     make(map[string]*sessionState),
		executionSvc: executionSvc,
		fileSvc:      fileSvc,
	}
}

// CreateSession creates a new WebSocket session
func (m *Manager) CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error) {
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
	go m.readPump(state)
	
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

// readPump reads messages from the WebSocket connection
func (m *Manager) readPump(state *sessionState) {
	defer func() {
		m.CloseSession(state.session.ID)
	}()

	for {
		_, message, err := state.session.Conn.ReadMessage()
		if err != nil {
			m.logger.Error("Error reading from WebSocket", err, 
				"sessionID", state.session.ID, 
				"userID", state.session.UserID, 
				"sandboxID", state.session.SandboxID)
			break
		}

		// Process message
		if err := m.handleMessage(state, message); err != nil {
			m.logger.Error("Error handling WebSocket message", err, 
				"sessionID", state.session.ID, 
				"userID", state.session.UserID, 
				"sandboxID", state.session.SandboxID)
			
			// Send error to client
			state.session.SendError("message_processing_error", err.Error())
		}
	}
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

// handleMessage processes a WebSocket message
func (m *Manager) handleMessage(state *sessionState, rawMessage []byte) error {
	// Parse message
	var message map[string]interface{}
	if err := json.Unmarshal(rawMessage, &message); err != nil {
		return fmt.Errorf("invalid message format: %w", err)
	}

	// Get message type
	msgType, ok := message["type"].(string)
	if !ok {
		return fmt.Errorf("missing message type")
	}

	// Handle message based on type
	switch msgType {
	case "execute":
		return m.handleExecuteMessage(state, message)
	case "cancel":
		return m.handleCancelMessage(state, message)
	case "ping":
		return m.handlePingMessage(state, message)
	case "file_upload":
		return m.handleFileUploadMessage(state, message)
	case "file_download":
		return m.handleFileDownloadMessage(state, message)
	case "file_list":
		return m.handleFileListMessage(state, message)
	case "install_packages":
		return m.handleInstallPackagesMessage(state, message)
	default:
		return fmt.Errorf("unknown message type: %s", msgType)
	}
}

// handleExecuteMessage handles an execute message
func (m *Manager) handleExecuteMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	executionID, _ := message["executionId"].(string)
	if executionID == "" {
		executionID, _ = message["id"].(string)
	}
	if executionID == "" {
		executionID = uuid.New().String()
	}

	mode, _ := message["mode"].(string)
	content, _ := message["content"].(string)
	timeoutFloat, _ := message["timeout"].(float64)
	timeout := int(timeoutFloat)

	// Validate fields
	if mode != "code" && mode != "command" {
		return fmt.Errorf("invalid execution mode: %s", mode)
	}
	if content == "" {
		return fmt.Errorf("content is required")
	}
	if timeout <= 0 {
		timeout = 30 // Default timeout
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.session.SandboxID,
			Namespace: "default", // This should be retrieved from the actual sandbox
		},
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	
	// Store cancellation function
	m.SetCancellationFunc(state.session.ID, executionID, cancel)

	// Send execution start message
	state.session.Send(types.Message{
		Type:        "execution_start",
		ID:          executionID,
		Timestamp:   time.Now().UnixMilli(),
	})

	// Execute in goroutine
	go func() {
		defer cancel()
		defer func() {
			// Remove cancellation function when done
			state.mu.Lock()
			delete(state.cancellations, executionID)
			state.mu.Unlock()
		}()

		// Stream execution
		outputCallback := func(stream, content string) {
			state.session.Send(types.Message{
				Type:      "output",
				ID:        executionID,
				Stream:    stream,
				Content:   content,
				Timestamp: time.Now().UnixMilli(),
			})
		}

		// Execute code or command
		result, err := m.executionSvc.ExecuteStream(ctx, sandbox, mode, content, timeout, outputCallback)
		if err != nil {
			state.session.Send(types.Message{
				Type:      "error",
				ID:        executionID,
				Message:   fmt.Sprintf("Execution failed: %s", err.Error()),
				Timestamp: time.Now().UnixMilli(),
			})
			return
		}

		// Send execution complete message
		state.session.Send(types.Message{
			Type:        "execution_complete",
			ID:          executionID,
			ExitCode:    result.ExitCode,
			Timestamp:   time.Now().UnixMilli(),
		})
	}()

	return nil
}

// handleCancelMessage handles a cancel message
func (m *Manager) handleCancelMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	executionID, _ := message["executionId"].(string)
	if executionID == "" {
		executionID, _ = message["id"].(string)
	}
	if executionID == "" {
		return fmt.Errorf("executionId is required")
	}

	// Cancel execution
	if cancelled := m.CancelExecution(state.session.ID, executionID); cancelled {
		state.session.Send(types.Message{
			Type:        "execution_cancelled",
			ID:          executionID,
			Timestamp:   time.Now().UnixMilli(),
		})
	} else {
		return fmt.Errorf("execution not found or already completed: %s", executionID)
	}

	return nil
}

// handlePingMessage handles a ping message
func (m *Manager) handlePingMessage(state *sessionState, message map[string]interface{}) error {
	// Extract timestamp if present
	var timestamp int64
	if ts, ok := message["timestamp"].(float64); ok {
		timestamp = int64(ts)
	} else {
		timestamp = time.Now().UnixMilli()
	}

	// Send pong response
	state.session.Send(types.Message{
		Type:      "pong",
		Timestamp: timestamp,
	})

	return nil
}

// handleFileUploadMessage handles a file upload message
func (m *Manager) handleFileUploadMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	path, _ := message["path"].(string)
	if path == "" {
		return fmt.Errorf("path is required")
	}

	content, _ := message["content"].(string)
	if content == "" {
		return fmt.Errorf("content is required")
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.session.SandboxID,
			Namespace: "default", // This should be retrieved from the actual sandbox
		},
	}

	// Upload file
	fileInfo, err := m.fileSvc.UploadFile(context.Background(), sandbox, path, []byte(content))
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	// Send file upload success message
	state.session.Send(types.Message{
		Type:      "file_upload_success",
		Data: map[string]interface{}{
			"path":      fileInfo.Path,
			"size":      fileInfo.Size,
			"createdAt": fileInfo.CreatedAt.UnixMilli(),
		},
		Timestamp: time.Now().UnixMilli(),
	})

	return nil
}

// handleFileDownloadMessage handles a file download message
func (m *Manager) handleFileDownloadMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	path, _ := message["path"].(string)
	if path == "" {
		return fmt.Errorf("path is required")
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.session.SandboxID,
			Namespace: "default", // This should be retrieved from the actual sandbox
		},
	}

	// Download file
	content, err := m.fileSvc.DownloadFile(context.Background(), sandbox, path)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	// Send file download success message
	state.session.Send(types.Message{
		Type:      "file_download_success",
		Data: map[string]interface{}{
			"path":    path,
			"content": string(content),
			"size":    len(content),
		},
		Timestamp: time.Now().UnixMilli(),
	})

	return nil
}

// handleFileListMessage handles a file list message
func (m *Manager) handleFileListMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	path, _ := message["path"].(string)
	if path == "" {
		path = "/workspace" // Default path
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.session.SandboxID,
			Namespace: "default", // This should be retrieved from the actual sandbox
		},
	}

	// List files
	files, err := m.fileSvc.ListFiles(context.Background(), sandbox, path)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	// Convert files to map
	fileList := make([]map[string]interface{}, 0, len(files))
	for _, file := range files {
		fileList = append(fileList, map[string]interface{}{
			"path":      file.Path,
			"name":      file.Name,
			"size":      file.Size,
			"isDir":     file.IsDir,
			"createdAt": file.CreatedAt.UnixMilli(),
			"updatedAt": file.UpdatedAt.UnixMilli(),
		})
	}

	// Send file list success message
	state.session.Send(types.Message{
		Type:      "file_list_success",
		Data: map[string]interface{}{
			"path":  path,
			"files": fileList,
		},
		Timestamp: time.Now().UnixMilli(),
	})

	return nil
}

// handleInstallPackagesMessage handles a package installation message
func (m *Manager) handleInstallPackagesMessage(state *sessionState, message map[string]interface{}) error {
	// Extract fields
	packagesRaw, ok := message["packages"].([]interface{})
	if !ok || len(packagesRaw) == 0 {
		return fmt.Errorf("packages is required and must be a non-empty array")
	}

	// Convert packages to strings
	packages := make([]string, 0, len(packagesRaw))
	for _, pkg := range packagesRaw {
		if pkgStr, ok := pkg.(string); ok && pkgStr != "" {
			packages = append(packages, pkgStr)
		}
	}

	if len(packages) == 0 {
		return fmt.Errorf("no valid packages specified")
	}

	manager, _ := message["manager"].(string)
	executionID, _ := message["executionId"].(string)
	if executionID == "" {
		executionID, _ = message["id"].(string)
	}
	if executionID == "" {
		executionID = uuid.New().String()
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.session.SandboxID,
			Namespace: "default", // This should be retrieved from the actual sandbox
		},
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	
	// Store cancellation function
	m.SetCancellationFunc(state.session.ID, executionID, cancel)

	// Send installation start message
	state.session.Send(types.Message{
		Type:        "installation_start",
		ID:          executionID,
		Timestamp:   time.Now().UnixMilli(),
	})

	// Install packages in goroutine
	go func() {
		defer cancel()
		defer func() {
			// Remove cancellation function when done
			state.mu.Lock()
			delete(state.cancellations, executionID)
			state.mu.Unlock()
		}()

		// Install packages
		result, err := m.executionSvc.InstallPackages(ctx, sandbox, packages, manager)
		if err != nil {
			state.session.Send(types.Message{
				Type:      "error",
				ID:        executionID,
				Message:   fmt.Sprintf("Package installation failed: %s", err.Error()),
				Timestamp: time.Now().UnixMilli(),
			})
			return
		}

		// Send installation complete message
		state.session.Send(types.Message{
			Type:        "installation_complete",
			ID:          executionID,
			ExitCode:    result.ExitCode,
			Content:     result.Stdout,
			Message:     result.Stderr,
			Timestamp:   time.Now().UnixMilli(),
		})
	}()

	return nil
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
