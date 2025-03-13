package types

import (
	"time"
	
	"github.com/stretchr/testify/mock"
)

// MockWSConnection is a mock implementation of WSConnection
type MockWSConnection struct {
	mock.Mock
}

// NewMockWSConnection creates a new mock WebSocket connection
func NewMockWSConnection() *MockWSConnection {
	return &MockWSConnection{}
}

// ReadMessage mocks the ReadMessage method
func (m *MockWSConnection) ReadMessage() (messageType int, p []byte, err error) {
	args := m.Called()
	return args.Int(0), args.Get(1).([]byte), args.Error(2)
}

// WriteMessage mocks the WriteMessage method
func (m *MockWSConnection) WriteMessage(messageType int, data []byte) error {
	args := m.Called(messageType, data)
	return args.Error(0)
}

// WriteJSON mocks the WriteJSON method
func (m *MockWSConnection) WriteJSON(v interface{}) error {
	args := m.Called(v)
	return args.Error(0)
}

// Close mocks the Close method
func (m *MockWSConnection) Close() error {
	args := m.Called()
	return args.Error(0)
}

// SetWriteDeadline mocks the SetWriteDeadline method
func (m *MockWSConnection) SetWriteDeadline(t time.Time) error {
	args := m.Called(t)
	return args.Error(0)
}
