package tests

import (
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestInformerFactory tests the informer factory creation and methods
func TestInformerFactory(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockLLMSafespaceV1Interface()
	
	// Create informer factory
	factory := kubernetes.NewInformerFactory(mockClient, 30*time.Second, "test-namespace")
	assert.NotNil(t, factory)
	
	// Test individual informers
	sandboxInformer := factory.SandboxInformer()
	assert.NotNil(t, sandboxInformer)
	
	warmPoolInformer := factory.WarmPoolInformer()
	assert.NotNil(t, warmPoolInformer)
	
	warmPodInformer := factory.WarmPodInformer()
	assert.NotNil(t, warmPodInformer)
	
	runtimeEnvInformer := factory.RuntimeEnvironmentInformer()
	assert.NotNil(t, runtimeEnvInformer)
	
	profileInformer := factory.SandboxProfileInformer()
	assert.NotNil(t, profileInformer)
}

// TestStartInformers tests starting all informers
func TestStartInformers(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockLLMSafespaceV1Interface()
	
	// Setup mock list and watch methods
	mockClient.SetupSandboxesMock("test-namespace")
	mockClient.SetupWarmPoolsMock("test-namespace")
	mockClient.SetupWarmPodsMock("test-namespace")
	mockClient.SetupRuntimeEnvironmentsMock("test-namespace")
	mockClient.SetupSandboxProfilesMock("test-namespace")
	
	// Create informer factory
	factory := kubernetes.NewInformerFactory(mockClient, 30*time.Second, "test-namespace")
	
	// Create a stop channel
	stopCh := make(chan struct{})
	
	// Start informers in a goroutine
	go func() {
		factory.StartInformers(stopCh)
		// Close the stop channel after a short delay to stop the informers
		time.Sleep(100 * time.Millisecond)
		close(stopCh)
	}()
	
	// Wait for informers to start and stop
	time.Sleep(200 * time.Millisecond)
	
	// No assertions needed, just ensuring it doesn't panic
}

// TestInformerListWatch tests the list and watch functionality of informers
func TestInformerListWatch(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockLLMSafespaceV1Interface()
	
	// Setup mock sandbox interface
	sandboxInterface := kmocks.NewMockSandboxInterface()
	mockClient.On("Sandboxes", "test-namespace").Return(sandboxInterface)
	
	// Setup list and watch methods
	sandboxInterface.SetupListMock()
	
	// Create a mock watch
	mockWatch := kmocks.NewMockWatch()
	mockWatch.On("ResultChan").Return(mockWatch.ResultChan())
	mockWatch.On("Stop").Return()
	
	sandboxInterface.On("Watch", metav1.ListOptions{}).Return(mockWatch, nil)
	
	// Create informer factory
	factory := kubernetes.NewInformerFactory(mockClient, 30*time.Second, "test-namespace")
	
	// Get sandbox informer
	informer := factory.SandboxInformer()
	
	// Create a stop channel
	stopCh := make(chan struct{})
	
	// Start the informer
	go informer.Run(stopCh)
	
	// Wait for informer to start
	time.Sleep(100 * time.Millisecond)
	
	// Stop the informer
	close(stopCh)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
	sandboxInterface.AssertExpectations(t)
}
