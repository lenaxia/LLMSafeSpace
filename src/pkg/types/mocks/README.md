# LLMSafeSpace Types Mocks

This package provides mock implementations of the types defined in the `github.com/lenaxia/llmsafespace/pkg/types` package.

## Usage

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    
    "github.com/lenaxia/llmsafespace/pkg/types/mocks"
)

func TestSomething(t *testing.T) {
    // Create a mock WebSocket connection
    mockConn := &mocks.MockWSConnection{}
    
    // Set up expectations
    mockConn.On("WriteMessage", 1, []byte("test")).Return(nil)
    
    // Call the method under test
    err := mockConn.WriteMessage(1, []byte("test"))
    
    // Assert expectations
    assert.NoError(t, err)
    mockConn.AssertExpectations(t)
}
```

## Available Mocks

- `MockWSConnection`: Mock implementation of `WSConnection`
- `MockSession`: Mock implementation of `Session`

## Factory

The package also provides a `Factory` to create mock objects for testing:

```go
import (
    "testing"
    
    "github.com/stretchr/testify/assert"
    
    "github.com/lenaxia/llmsafespace/pkg/types/mocks"
)

func TestSomething(t *testing.T) {
    factory := mocks.NewFactory()
    
    // Create mock objects
    sandbox := factory.CreateMockSandbox("test-sandbox", "default", "python:3.10")
    warmPool := factory.CreateMockWarmPool("test-pool", "default", "python:3.10")
    
    // Use the mock objects in your tests
    assert.Equal(t, "test-sandbox", sandbox.Name)
    assert.Equal(t, "python:3.10", sandbox.Spec.Runtime)
}
```
