# LLMSafeSpace Mocks

This package provides mock implementations for testing the LLMSafeSpace project. The mocks are designed to be used in unit tests and can be easily set up with predefined behaviors or custom responses.

## Usage

### Using the Mock Factory

The `MockFactory` provides a convenient way to create mock objects for various components of the LLMSafeSpace project. Here's an example of how to use it:

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/lenaxia/llmsafespace/mocks"
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

If you need more control over the mock behavior, you can use the individual mock implementations directly. Here's an example of how to use the `MockKubernetesClient`:

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    
    kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
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

## Debugging Mock Issues

When working with mocks, it's important to ensure that the mock objects are set up correctly and that the expectations are properly defined. Here are some tips for debugging mock issues:

1. **Check the Mock Setup**: Ensure that the mock objects are created and set up correctly. Double-check the method calls and arguments used to set up the mock expectations.

2. **Verify Expectations**: After running the tests, use the `mock.AssertExpectations(t)` method to verify that all expectations were met. If any expectations were not met, the test will fail, and you can investigate the cause.

3. **Use Mock Argument Matchers**: If you're having trouble matching arguments in your mock expectations, consider using the argument matchers provided by the `testify/mock` package. These matchers can help you match arguments based on specific conditions or patterns.

4. **Check the Call Order**: If your test involves multiple method calls on the mock object, ensure that the expectations are set up in the correct order. The `testify/mock` package enforces the order of method calls by default.

5. **Enable Mock Logging**: The `testify/mock` package provides a logging feature that can help you debug mock issues. You can enable mock logging by setting the `mock.MockingMode` to `mock.LoggingMode` before running your tests.

6. **Inspect the Mock Object**: If you're still having trouble, you can inspect the mock object directly to see its state and the recorded method calls. The `testify/mock` package provides methods like `Mock.Calls` and `Mock.ExpectedCalls` that can help you inspect the mock object.

By following these tips and leveraging the features provided by the `testify/mock` package, you should be able to effectively debug and resolve any issues you encounter when working with mocks in the LLMSafeSpace project.
