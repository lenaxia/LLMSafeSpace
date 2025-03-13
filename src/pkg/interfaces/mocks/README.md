# LLMSafeSpace Interfaces Mocks

This package provides mock implementations of the interfaces defined in the `github.com/lenaxia/llmsafespace/pkg/interfaces` package.

## Usage

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    
    "github.com/lenaxia/llmsafespace/pkg/interfaces/mocks"
    "github.com/lenaxia/llmsafespace/pkg/types"
)

func TestSomething(t *testing.T) {
    // Create a mock Kubernetes client
    mockClient := &mocks.MockKubernetesClient{}
    
    // Set up expectations
    mockClient.On("Start").Return(nil)
    mockClient.On("LlmsafespaceV1").Return(&mocks.MockLLMSafespaceV1{})
    
    // Call the method under test
    err := mockClient.Start()
    
    // Assert expectations
    assert.NoError(t, err)
    mockClient.AssertExpectations(t)
}
```

## Available Mocks

- `MockKubernetesClient`: Mock implementation of `KubernetesClient`
- `MockLLMSafespaceV1`: Mock implementation of `LLMSafespaceV1Interface`
- `MockSandboxInterface`: Mock implementation of `SandboxInterface`
- `MockWarmPoolInterface`: Mock implementation of `WarmPoolInterface`
- `MockWarmPodInterface`: Mock implementation of `WarmPodInterface`
- `MockRuntimeEnvironmentInterface`: Mock implementation of `RuntimeEnvironmentInterface`
- `MockSandboxProfileInterface`: Mock implementation of `SandboxProfileInterface`
