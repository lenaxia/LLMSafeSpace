// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
)

const (
	relayWriteWait  = 10 * time.Second
	relayPongWait   = 60 * time.Second
	relayMaxMsgSize = 10 << 20 // 10MB
)

var relayUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// relayConn wraps a websocket.Conn with a write mutex to prevent concurrent writes.
type relayConn struct {
	conn *websocket.Conn
	wmu  sync.Mutex
}

func (rc *relayConn) writeMessage(msgType int, data []byte) error {
	rc.wmu.Lock()
	defer rc.wmu.Unlock()
	_ = rc.conn.SetWriteDeadline(time.Now().Add(relayWriteWait))
	return rc.conn.WriteMessage(msgType, data)
}

func (rc *relayConn) close() error {
	return rc.conn.Close()
}

// relayRoom is a per-workspace relay channel with exactly two participants.
type relayRoom struct {
	mu     sync.RWMutex
	agentd *relayConn
	client *relayConn
}

// RelayHandler manages the WebSocket relay endpoint for client-proxied inference.
type RelayHandler struct {
	wsGetter WorkspaceGetter // validates workspace ownership
	mu       sync.RWMutex
	rooms    map[string]*relayRoom
}

// NewRelayHandler creates a new relay handler. wsGetter may be nil (ownership
// check is skipped — only acceptable in tests).
func NewRelayHandler(wsGetter WorkspaceGetter) *RelayHandler {
	return &RelayHandler{
		wsGetter: wsGetter,
		rooms:    make(map[string]*relayRoom),
	}
}

// HandleRelay is the Gin handler for GET /api/v1/workspaces/:id/relay.
func (h *RelayHandler) HandleRelay(c *gin.Context) {
	workspaceID := c.Param("id")
	role := c.Query("role")
	if role != "agentd" && role != "client" {
		role = "client"
	}

	// Both roles require authentication. The auth middleware has already
	// validated the JWT/API key and set userID in the context.
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Ownership enforcement: verify the authenticated user owns this workspace.
	// This applies to BOTH roles — the agentd uses the workspace's API key
	// (which resolves to the workspace owner's userID), so the ownership check
	// passes naturally. A malicious user cannot impersonate the agentd role
	// for a workspace they don't own because their token resolves to a
	// different userID.
	if h.wsGetter != nil {
		ws, err := h.wsGetter.GetWorkspace(workspaceID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		if ws.Labels["user-id"] != uid {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
	}

	conn, err := relayUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WebSocket upgrade failed"})
		return
	}

	rc := &relayConn{conn: conn}
	room := h.getOrCreateRoom(workspaceID)

	room.mu.Lock()
	if role == "agentd" {
		if room.agentd != nil {
			_ = room.agentd.close()
		}
		room.agentd = rc
	} else {
		if room.client != nil {
			_ = room.client.close()
		}
		room.client = rc
	}
	room.mu.Unlock()

	h.readLoop(rc, room, role, workspaceID)
}

func (h *RelayHandler) getOrCreateRoom(workspaceID string) *relayRoom {
	h.mu.Lock()
	defer h.mu.Unlock()
	room, ok := h.rooms[workspaceID]
	if !ok {
		room = &relayRoom{}
		h.rooms[workspaceID] = room
	}
	return room
}

// readLoop reads from one participant and forwards to the other.
// All writes go through relayConn.writeMessage which serializes with a mutex.
func (h *RelayHandler) readLoop(rc *relayConn, room *relayRoom, role, workspaceID string) {
	defer func() {
		room.mu.Lock()
		if role == "agentd" && room.agentd == rc {
			room.agentd = nil
		} else if role == "client" && room.client == rc {
			room.client = nil
		}
		room.mu.Unlock()
		_ = rc.close()

		h.mu.Lock()
		room.mu.RLock()
		if room.agentd == nil && room.client == nil {
			delete(h.rooms, workspaceID)
		}
		room.mu.RUnlock()
		h.mu.Unlock()
	}()

	rc.conn.SetReadLimit(relayMaxMsgSize)
	_ = rc.conn.SetReadDeadline(time.Now().Add(relayPongWait))
	rc.conn.SetPongHandler(func(string) error {
		_ = rc.conn.SetReadDeadline(time.Now().Add(relayPongWait))
		return nil
	})

	for {
		_, msg, err := rc.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = rc.conn.SetReadDeadline(time.Now().Add(relayPongWait))

		// Application-level ping → respond with pong (via write mutex)
		var env relay.Envelope
		if json.Unmarshal(msg, &env) == nil && env.Type == relay.TypePing {
			pong, _ := json.Marshal(relay.Envelope{Type: relay.TypePong})
			_ = rc.writeMessage(websocket.TextMessage, pong)
			continue
		}

		// Forward to the other participant
		room.mu.RLock()
		var target *relayConn
		if role == "agentd" {
			target = room.client
		} else {
			target = room.agentd
		}
		room.mu.RUnlock()

		if target != nil {
			_ = target.writeMessage(websocket.TextMessage, msg)
		}
	}
}

// IsClientConnected returns whether a client relay is connected for the workspace.
func (h *RelayHandler) IsClientConnected(workspaceID string) bool {
	h.mu.RLock()
	room, ok := h.rooms[workspaceID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	room.mu.RLock()
	defer room.mu.RUnlock()
	return room.client != nil
}
