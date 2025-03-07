Here's a comprehensive debugging plan with file mappings and execution order:

Debugging Plan - Phase Ordering

1. Fix Auth Service Database Signature Mismatch

 • Core Error: auth.go:47:52: too many arguments
 • Relevant Files:
    • Source:
       • src/api/internal/services/database/database.go
       • src/api/internal/services/services.go
       • src/api/internal/services/auth/auth.go
    • Tests:
       • src/api/internal/services/auth/auth_test.go
 • Steps:
    1 Update database/database.go method signature to include context
    2 Update services.go interface definition
    3 Verify auth.go calls match new signature
    4 Update mocks in auth_test.go
    5 Test: go test -v ./internal/services/auth/...

2. Fix Kubernetes Client Mock Implementation

 • Core Error: cannot use mockK8sClient as *kubernetes.Client value
 • Relevant Files:
    • Source:
       • src/api/internal/kubernetes/client.go
    • Tests:
       • src/api/internal/services/execution/execution_test.go
       • src/api/internal/services/file/file_test.go
       • src/api/internal/services/sandbox/sandbox_test.go
       • src/api/internal/services/warmpool/warmpool_test.go
 • Steps:
    1 Add missing interface methods to all MockK8sClient implementations
    2 Ensure mocks implement full kubernetes.Client interface
    3 Update test initializations to use interface types
    4 Test: go test -v ./internal/services/execution/... ./internal/services/file/...

3. Fix Warmpool Test Setup Issues

 • Core Errors:
    • undefined: mockLLMClient
    • Function assignment errors
 • Relevant Files:
    • Source:
       • src/api/internal/services/warmpool/warmpool.go
    • Tests:
       • src/api/internal/services/warmpool/warmpool_test.go
 • Steps:
    1 Add proper mockLLMClient initialization in test setup
    2 Convert function fields to interface dependencies
    3 Fix function assignment syntax
    4 Test: go test -v ./internal/services/warmpool/...

4. Fix Sandbox Service Test Mismatches

 • Core Errors:
    • Type mismatches in service initialization
    • Unused variables
 • **Relevant Files:
    • Tests:
       • src/api/internal/services/sandbox/sandbox_test.go
 • Steps:
    1 Update mock assignments to use interface types
    2 Remove unused cacheService variable
    3 Verify all mock services implement correct interfaces
    4 Test: go test -v ./internal/services/sandbox/...

5. Fix Remaining Type Mismatches

 • Core Errors:
    • cannot use mock... as *...Service value
 • Relevant Files:
    • All test files using mocks
 • Steps:
    1 Ensure all mock structs implement full service interfaces
    2 Convert concrete type references to interface types
    3 Add missing interface method implementations
    4 Global test: go test -v ./internal/...

Critical File Matrix


  Component           Source Files                        Test Files
 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Auth Service        database.go, services.go, auth.go   auth_test.go
  Kubernetes Client   client.go                           execution_test.go, file_test.go, etc.
  Warmpool            warmpool.go                         warmpool_test.go
  Sandbox             (Already covered)                   sandbox_test.go
  Common Interfaces   services.go                         All test files with mocks


Recommended Execution Order:

 1 Auth Service database fix
 2 Kubernetes client interface fixes
 3 Warmpool test setup
 4 Sandbox test cleanup
 5 Global type validation

Verification Commands:


 # After each phase:
 go test -v ./internal/services/[component]/...

 # Final validation:
 go test -v ./internal/...


Key Principles:

 1 Fix interface mismatches before type errors
 2 Address build errors before runtime errors
 3 Verify mock implementations match real interfaces
 4 Use interface types in tests rather than concrete types

Would you like me to provide specific code patches for any of these phases?

