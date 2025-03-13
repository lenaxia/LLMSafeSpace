package mocks

import (
	"github.com/stretchr/testify/mock"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// MockSession is a mock implementation of Session
type MockSession struct {
	mock.Mock
	ID        string
	UserID    string
	SandboxID string
	Conn      types.WSConnection
}

// SendError mocks the SendError method
func (m *MockSession) SendError(code, message string) error {
	args := m.Called(code, message)
	return args.Error(0)
}

// Send mocks the Send method
func (m *MockSession) Send(msg types.Message) error {
	args := m.Called(msg)
	return args.Error(0)
}
