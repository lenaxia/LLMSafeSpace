package websocket

import (
	"github.com/gorilla/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Connection implements the WSConnection interface using gorilla/websocket
type Connection struct {
	conn *websocket.Conn
}

// New creates a new WebSocket connection wrapper
func New(conn *websocket.Conn) *Connection {
	return &Connection{conn: conn}
}

// ReadMessage reads a message from the WebSocket connection
func (c *Connection) ReadMessage() (messageType int, p []byte, err error) {
	return c.conn.ReadMessage()
}

// WriteMessage writes a message to the WebSocket connection
func (c *Connection) WriteMessage(messageType int, data []byte) error {
	return c.conn.WriteMessage(messageType, data)
}

// WriteJSON writes a JSON message to the WebSocket connection
func (c *Connection) WriteJSON(v interface{}) error {
	return c.conn.WriteJSON(v)
}

// Close closes the WebSocket connection
func (c *Connection) Close() error {
	return c.conn.Close()
}

// SetWriteDeadline sets the write deadline for the WebSocket connection
func (c *Connection) SetWriteDeadline(t metav1.Time) error {
	return c.conn.SetWriteDeadline(t.Time)
}

// DeepCopyWSConnection creates a copy of the WebSocket connection
// Note: This doesn't actually clone the underlying connection as WebSocket connections
// cannot be cloned. It returns a new Connection with the same underlying connection.
func (c *Connection) DeepCopyWSConnection() types.WSConnection {
	if c == nil {
		return nil
	}
	return &Connection{
		conn: c.conn,
	}
}
