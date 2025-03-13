package kubernetes

import (
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/watch"
)

// MockWatch implements watch.Interface for testing
type MockWatch struct {
	mock.Mock
	resultChan chan watch.Event
}

// NewMockWatch creates a new mock watch
func NewMockWatch() *MockWatch {
	return &MockWatch{
		resultChan: make(chan watch.Event, 10),
	}
}

// Stop implements watch.Interface
func (m *MockWatch) Stop() {
	m.Called()
	close(m.resultChan)
}

// ResultChan implements watch.Interface
func (m *MockWatch) ResultChan() <-chan watch.Event {
	m.Called()
	return m.resultChan
}

// SendEvent sends an event to the result channel
func (m *MockWatch) SendEvent(eventType watch.EventType, object runtime.Object) {
	m.resultChan <- watch.Event{
		Type:   eventType,
		Object: object,
	}
}
