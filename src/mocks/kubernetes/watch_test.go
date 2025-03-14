package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/watch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

func TestWatchFunctionality(t *testing.T) {
	// Create the mock interfaces
	v1Client := NewMockLLMSafespaceV1Interface()
	sandboxClient := NewMockSandboxInterface()

	// Set up the mock chain
	v1Client.On("Sandboxes", "test-namespace").Return(sandboxClient)
	
	// Create and set up the watch mock
	mockWatch := NewMockWatch()
	sandboxClient.On("Watch", mock.Anything).Return(mockWatch, nil)

	// Get the sandbox client and create a watcher
	client := v1Client.Sandboxes("test-namespace")
	watcher, err := client.Watch(metav1.ListOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, watcher)

	// Create a sandbox object to send in events
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "test-namespace",
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
	}

	// Create a channel to coordinate the test
	done := make(chan bool)

	// Start watching for events in a goroutine
	go func() {
		// Get the first event
		event := <-watcher.ResultChan()
		
		// Verify the event
		assert.Equal(t, watch.Added, event.Type)
		assert.Equal(t, sandbox.Name, event.Object.(*types.Sandbox).Name)
		
		// Signal completion
		done <- true
	}()

	// Send an event through the mock watch
	mockWatch.SendEvent(watch.Added, sandbox)

	// Wait for the test to complete or timeout
	select {
	case <-done:
		// Test completed successfully
	case <-time.After(1 * time.Second):
		t.Fatal("Test timed out waiting for watch event")
	}

	// Clean up
	watcher.Stop()

	// Verify all expectations were met
	v1Client.AssertExpectations(t)
	sandboxClient.AssertExpectations(t)
	mockWatch.AssertExpectations(t)
}
