package mocks

import (
	"context"

	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/mock"
)

// MockSessionManager implements the SessionManager interface for testing
type MockSessionManager struct {
	mock.Mock
}

func (m *MockSessionManager) CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error) {
	args := m.Called(userID, sandboxID, conn)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Session), args.Error(1)
}

func (m *MockSessionManager) GetSession(sessionID string) (*types.Session, error) {
	args := m.Called(sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Session), args.Error(1)
}

func (m *MockSessionManager) CloseSession(sessionID string) {
	m.Called(sessionID)
}

func (m *MockSessionManager) SetCancellationFunc(sessionID, executionID string, cancel context.CancelFunc) {
	m.Called(sessionID, executionID, cancel)
}

func (m *MockSessionManager) CancelExecution(sessionID, executionID string) bool {
	args := m.Called(sessionID, executionID)
	return args.Bool(0)
}

func (m *MockSessionManager) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSessionManager) Stop() error {
	args := m.Called()
	return args.Error(0)
}
