# LLMSafeSpace Mock Framework - Complete Documentation

This document provides comprehensive guidance for using the LLMSafeSpace mock framework for testing Kubernetes-related functionality. All mock implementations adhere to standard Go testing patterns using the testify/mock package.

## Table of Contents
1. [Core Concepts](#core-concepts)
2. [Mock Types Overview](#mock-types-overview)
3. [Initialization Patterns](#initialization-patterns)
4. [Common Use Cases](#common-use-cases)
5. [Method Reference](#method-reference)
6. [Debugging & Validation](#debugging--validation)
7. [Best Practices](#best-practices)

## Core Concepts <a name="core-concepts"></a>

The mock framework provides:
- Complete implementations of Kubernetes client interfaces
- Pre-configured responses for common operations
- Flexible expectation management
- Integrated validation system
- Lifecycle management utilities

Key components:
- `MockFactory`: Central factory for creating mock objects
- Interface-specific mocks (Sandbox, WarmPool, etc.)
- Watch system simulator
- Automatic argument matching

## Mock Types Overview <a name="mock-types-overview"></a>

### 1. Kubernetes Resource Mocks
```go
- MockSandboxInterface
- MockWarmPoolInterface  
- MockWarmPodInterface
- MockRuntimeEnvironmentInterface
- MockSandboxProfileInterface
```

### 2. Client Mocks
```go
- MockKubernetesClient
- MockLLMSafespaceV1Interface
```

### 3. Utility Mocks
```go
- MockWatch
- MockWSConnection
- MockLogger
```

## Initialization Patterns <a name="initialization-patterns"></a>

### Basic Initialization
```go
import (
    "testing"
    "github.com/lenaxia/llmsafespace/mocks/kubernetes"
)

func TestExample(t *testing.T) {
    // Create mock client
    mockClient := kubernetes.NewMockKubernetesClient()
    
    // Initialize interface mocks
    sandboxMock := kubernetes.NewMockSandboxInterface()
    warmPoolMock := kubernetes.NewMockWarmPoolInterface()
    
    // Set default expectations
    mockClient.On("LlmsafespaceV1").Return(
        kubernetes.NewMockLLMSafespaceV1Interface())
}
```

### Factory Pattern
```go
func TestFactoryExample(t *testing.T) {
    factory := mocks.NewMockFactory()
    
    // Create pre-configured resources
    sandbox := factory.NewSandbox("test-sb", "default", "python:3.10")
    warmPool := factory.NewWarmPool("test-pool", "default", "nodejs:18")
    
    // Create mock client with default expectations
    client := factory.NewKubernetesClient()
}
```

## Common Use Cases <a name="common-use-cases"></a>

### 1. Basic CRUD Operations
```go
func TestSandboxCreate(t *testing.T) {
    mockSandbox := kubernetes.NewMockSandboxInterface()
    
    // Set expectation
    mockSandbox.SetupCreateMock()
    
    // Execute test code
    _, err := sandboxController.Create(testSandbox)
    
    // Validate
    mockSandbox.AssertCalled(t, "Create", mock.Anything)
    assert.NoError(t, err)
}
```

### 2. Watch Simulations
```go
func TestWatchSandboxes(t *testing.T) {
    mockWatch := kubernetes.NewMockWatch()
    mockSandbox := kubernetes.NewMockSandboxInterface()
    
    // Setup watch expectations
    mockSandbox.SetupWatchMock().Return(mockWatch, nil)
    
    // Send test events
    mockWatch.SendEvent(watch.Added, factory.NewSandbox("test", "ns", "py"))
    
    // Test code would receive this event
    // Add your event handling assertions here
}
```

### 3. Error Simulation
```go
func TestCreateError(t *testing.T) {
    mockSandbox := kubernetes.NewMockSandboxInterface()
    
    // Force error
    mockSandbox.On("Create", mock.Anything).
        Return(nil, errors.New("storage error"))
        
    _, err := sandboxController.Create(testSandbox)
    assert.ErrorContains(t, err, "storage error")
}
```

## Method Reference <a name="method-reference"></a>

### MockSandboxInterface
```go
// Create - Create a sandbox
// Signature: 
// Create(sandbox *types.Sandbox) (*types.Sandbox, error)
//
// Setup: 
mock.SetupCreateMock().Return(sandbox, nil)

// Update - Update sandbox spec
// Signature:
// Update(sandbox *types.Sandbox) (*types.Sandbox, error)

// Delete - Delete a sandbox
// Signature:
// Delete(name string, options metav1.DeleteOptions) error

// Get - Retrieve sandbox
// Signature: 
// Get(name string, options metav1.GetOptions) (*types.Sandbox, error)

// List - List sandboxes
// Signature:
// List(opts metav1.ListOptions) (*types.SandboxList, error)

// Watch - Watch for changes
// Signature:
// Watch(opts metav1.ListOptions) (watch.Interface, error)
```

### MockWatch
```go
// SendEvent - Inject watch event
// Signature:
// SendEvent(eventType watch.EventType, object runtime.Object)

// ResultChan - Receive events
// Signature:
// ResultChan() <-chan watch.Event

// Stop - Terminate watch
// Signature:
// Stop()
```

## Debugging & Validation <a name="debugging--validation"></a>

### Common Issues & Solutions

1. **Unmet Expectations**
```go
// Always call AssertExpectations!
mockObj.AssertExpectations(t)

// Check call history
calls := mockObj.Calls
for _, call := range calls {
    t.Logf("Method: %s\nArgs: %v", call.Method, call.Arguments)
}
```

2. **Argument Matching Failures**
```go
// Use flexible matchers
mock.On("Get", mock.AnythingOfType("string"), mock.Anything).
    Return(testObj, nil)
    
// Match specific fields
mock.On("Update", mock.MatchedBy(func(s *types.Sandbox) bool {
    return s.Spec.Runtime == "python"
})).Return(nil, nil)
```

3. **Concurrency Issues**
```go
// Use lockable mocks
mock.Mock.Test(t) // Enables automatic locking

// Verify call order
mock.AssertCalled(t, "Create", firstArgs)
mock.AssertCalled(t, "Update", secondArgs)
```

### Validation Checklist
1. All expected methods were called
2. Arguments match expectations
3. Call count matches requirements
4. Return values are properly handled
5. Concurrency locks are properly managed

## Best Practices <a name="best-practices"></a>

1. **Lifecycle Management**
```go
func TestMain(m *testing.M) {
    // Global mock setup/teardown
    defer mockClient.Stop()
    m.Run()
}
```

2. **Reusable Setup**
```go
func sandboxTestSetup() (*Controller, *kubernetes.MockSandboxInterface) {
    mockSandbox := kubernetes.NewMockSandboxInterface()
    mockSandbox.SetupGetMock("test-sandbox")
    
    controller := NewController(mockSandbox)
    return controller, mockSandbox
}
```

3. **Argument Matching Hierarchy**
```go
mock.On("Method", 
    mock.Anything,             // Match any value
    mock.AnythingOfType("int"), // Match type
    mock.MatchedBy(func(x any) bool { // Custom logic
        return x.(string) == "expected"
    }))
```

4. **Parallel Testing**
```go
func TestParallel(t *testing.T) {
    mockObj := NewMockSandboxInterface()
    mockObj.Test(t) // Enable parallel safety
    
    t.Run("parallel1", func(t *testing.T) {
        t.Parallel()
        // Test code
    })
}
```

5. **Stateful Mocking**
```go
// Maintain internal state
type StatefulMock struct {
    kubernetes.MockSandboxInterface
    sandboxes map[string]*types.Sandbox
}

func (m *StatefulMock) Create(s *types.Sandbox) (*types.Sandbox, error) {
    m.sandboxes[s.Name] = s
    return s, nil
}
```

## Troubleshooting Guide

### Error: "missing call(s)"
1. Verify method signature matches exactly
2. Check argument matchers (use mock.Anything for wildcards)
3. Ensure method was actually called in code path
4. Verify test didn't exit early before call

### Error: "unexpected call(s)"
1. Add missing expectations
2. Use mock.AllowUnexpectedCalls() for optional methods
3. Add catch-all expectation:
```go 
mock.On(mock.Anything).Panic("unexpected call"))
```

### Performance Issues
1. Use mock.ExpectedCalls = nil to reset between tests
2. Avoid complex argument matchers in performance tests
3. Use mock.Call.WaitFor(time.Second) for concurrency tests

## Mock Lifecycle Diagram

[Test Start]
  │
  ├── Mock Initialization
  │     ├── NewMock...()
  │     └── Setup...Mock()
  │
  ├── Test Execution
  │     ├── Method Calls → Mock
  │     └── Return Values → Test Code
  │
  └── Validation Phase
        ├── AssertExpectations()
        └── AssertCalled/NotCalled()

This documentation provides complete coverage of mock usage patterns. For specific implementation details, refer to the interface definitions in the main package.
