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
	
	// Setup mock interfaces with List method expectations
	sandboxClient := kmocks.NewMockSandboxInterface()
	sandboxClient.SetupListMock()
	mockClient.On("Sandboxes", "test-namespace").Return(sandboxClient)
	
	warmPoolClient := kmocks.NewMockWarmPoolInterface()
	warmPoolClient.SetupListMock()
	mockClient.On("WarmPools", "test-namespace").Return(warmPoolClient)
	
	warmPodClient := kmocks.NewMockWarmPodInterface()
	warmPodClient.SetupListMock()
	mockClient.On("WarmPods", "test-namespace").Return(warmPodClient)
	
	runtimeEnvClient := kmocks.NewMockRuntimeEnvironmentInterface()
	runtimeEnvClient.SetupListMock()
	mockClient.On("RuntimeEnvironments", "test-namespace").Return(runtimeEnvClient)
	
	profileClient := kmocks.NewMockSandboxProfileInterface()
	profileClient.SetupListMock()
	mockClient.On("SandboxProfiles", "test-namespace").Return(profileClient)
	
	// Create informer factory with mocked client
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

    // Setup mock interfaces with both List and Watch expectations
    sandboxMock := mockClient.SetupSandboxesMock("test-namespace")
    sandboxMock.SetupListMock()
    sandboxMock.SetupWatchMock()

    warmPoolMock := mockClient.SetupWarmPoolsMock("test-namespace")
    warmPoolMock.SetupListMock()
    warmPoolMock.SetupWatchMock()

    warmPodMock := mockClient.SetupWarmPodsMock("test-namespace")
    warmPodMock.SetupListMock()
    warmPodMock.SetupWatchMock()

    runtimeEnvMock := mockClient.SetupRuntimeEnvironmentsMock("test-namespace")
    runtimeEnvMock.SetupListMock()
    runtimeEnvMock.SetupWatchMock()

    profileMock := mockClient.SetupSandboxProfilesMock("test-namespace")
    profileMock.SetupListMock()
    profileMock.SetupWatchMock()

    // Create informer factory
    factory := kubernetes.NewInformerFactory(
        mockClient,
        30*time.Second,
        "test-namespace",
    )

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

    // Verify expectations
    mockClient.AssertExpectations(t)
    sandboxMock.AssertExpectations(t)
    warmPoolMock.AssertExpectations(t)
    warmPodMock.AssertExpectations(t)
    runtimeEnvMock.AssertExpectations(t)
    profileMock.AssertExpectations(t)
}

// TestInformerListWatch tests the list and watch functionality of informers
func TestInformerListWatch(t *testing.T) {
	// Create mock client
	mockClient := kmocks.NewMockLLMSafespaceV1Interface()
	
	// Setup mock sandbox interface with List method expectation
	sandboxInterface := kmocks.NewMockSandboxInterface()
	mockClient.On("Sandboxes", "test-namespace").Return(sandboxInterface)
	
	// Setup list method
	sandboxInterface.SetupListMock()
	
	// Create a mock watch
	mockFactory := mocks.NewMockFactory()
	mockWatch := mockFactory.NewMockWatch()
	mockWatch.On("ResultChan").Return(mockWatch.ResultChan())
	mockWatch.On("Stop").Return()
	
	// Setup watch method with proper matcher
	sandboxInterface.On("Watch", mock.MatchedBy(func(opts metav1.ListOptions) bool { return true })).Return(mockWatch, nil)
	
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
	
	// Send an event through the mock watch
	mockFactory := mocks.NewMockFactory()
	sandbox := mockFactory.NewSandbox("test-sandbox", "test-namespace", "python:3.10")
	go func() {
		mockWatch.SendEvent(watch.Added, sandbox)
	}()
	
	// Wait for event to be processed
	time.Sleep(100 * time.Millisecond)
	
	// Stop the informer
	close(stopCh)
	
	// Verify expectations
	mockClient.AssertExpectations(t)
	sandboxInterface.AssertExpectations(t)
	mockWatch.AssertExpectations(t)
}
