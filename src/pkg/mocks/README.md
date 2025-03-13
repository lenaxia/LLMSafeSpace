# LLMSafeSpace Mocks

This package provides mock implementations for testing the LLMSafeSpace project.

## Structure

```
src/pkg/mocks/
  ├── factory.go           # Factory for creating mock objects
  ├── kubernetes/          # Kubernetes client mocks
  ├── logger/              # Logger mocks
  └── types/               # Type mocks (WSConnection, Session, etc.)
```

## Usage

### Using the Mock Factory

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/lenaxia/llmsafespace/pkg/mocks"
)

func TestSomething(t *testing.T) {
    // Create a mock factory
    factory := mocks.NewMockFactory()
    
    // Create mock objects
    sandbox := factory.NewSandbox("test-sandbox", "default", "python:3.10")
    warmPool := factory.NewWarmPool("test-pool", "default", "python:3.10")
    
    // Use the mock objects in your tests
    assert.Equal(t, "test-sandbox", sandbox.Name)
    assert.Equal(t, "python:3.10", sandbox.Spec.Runtime)
}
```

### Using Individual Mocks

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    
    kmocks "github.com/lenaxia/llmsafespace/pkg/mocks/kubernetes"
)

func TestKubernetesClient(t *testing.T) {
    // Create a mock Kubernetes client
    client := kmocks.NewMockKubernetesClient()
    
    // Set up expectations
    client.On("Start").Return(nil)
    client.On("Stop").Return()
    
    // Call the methods under test
    err := client.Start()
    assert.NoError(t, err)
    client.Stop()
    
    // Verify expectations
    client.AssertExpectations(t)
}
```

## Available Mocks

### Kubernetes Mocks

- `MockKubernetesClient`: Mock implementation of `KubernetesClient`
- `MockLLMSafespaceV1Interface`: Mock implementation of `LLMSafespaceV1Interface`
- `MockSandboxInterface`: Mock implementation of `SandboxInterface`
- `MockWarmPoolInterface`: Mock implementation of `WarmPoolInterface`
- `MockWarmPodInterface`: Mock implementation of `WarmPodInterface`
- `MockRuntimeEnvironmentInterface`: Mock implementation of `RuntimeEnvironmentInterface`
- `MockSandboxProfileInterface`: Mock implementation of `SandboxProfileInterface`
- `MockWatch`: Mock implementation of `watch.Interface`

### Logger Mocks

- `MockLogger`: Mock implementation of the logger interface

### Types Mocks

- `MockWSConnection`: Mock implementation of `WSConnection`
- `MockSession`: Mock implementation of `Session`
