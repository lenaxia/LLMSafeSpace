# Refined Sandbox Service Design Doc

## Architecture Overview

The Sandbox Service is a critical component of the LLMSafeSpace platform that manages the lifecycle of secure execution environments. It interfaces with Kubernetes to create, monitor, and manage sandbox environments where user code can be executed safely.

Key Components:
1. **Core Service**: Handles CRUD operations for sandboxes via Kubernetes CRDs
2. **Metrics Integration**: Provides observability through Prometheus metrics
3. **Session Management**: Manages WebSocket connections for real-time interaction
4. **Reconciliation Engine**: Ensures sandbox state consistency and handles failures
5. **File Operations**: Manages secure file transfers to/from sandboxes
6. **Warm Pool Integration**: Optimizes sandbox creation through pre-warmed environments
7. **Security Subsystem**: Enforces isolation and validates inputs

---

## Phase 1: Core Service Structure

**Objective:** Implement the foundational sandbox service with basic CRUD operations that interact with Kubernetes CRDs. This phase establishes the core service structure following the `interfaces.SandboxService` contract and implements proper error handling, validation, and database integration.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/sandbox_service.go`
- `src/api/internal/services/sandbox/client/k8s_client.go`
- `src/api/internal/services/sandbox/sandbox_service_test.go`
- `src/api/internal/services/sandbox/validation/validators.go`

**Implementation Tasks:**

1. **Complete `CreateSandbox` method**:
   - Implement the method according to `interfaces.SandboxService` contract
   - Add validation using `validation.ValidateCreateRequest`
   - Integrate with warm pool service to check for available warm pods
   - Convert API request to Kubernetes CRD using `client.ConvertToCRD`
   - Create sandbox in Kubernetes using `k8sClient.LlmsafespaceV1().Sandboxes().Create()`
   - Store metadata in database using `dbService.CreateSandboxMetadata`
   - Add proper error handling with cleanup on partial failures
   - Record metrics using `metrics.RecordSandboxCreation`

2. **Implement `GetSandbox` with namespace fallback**:
   - First attempt to get sandbox from specified namespace
   - If not found, try to find it in any namespace
   - Return `types.SandboxNotFoundError` if not found
   - Convert Kubernetes CRD to API type using `client.ConvertFromCRD`

3. **Implement `ListSandboxes` method**:
   - Query database for sandbox metadata using `dbService.ListSandboxes`
   - Enrich data with Kubernetes status information
   - Apply pagination with limit and offset
   - Add sorting by creation time (newest first)

4. **Implement `TerminateSandbox` method**:
   - Verify sandbox exists and user has permission
   - Delete sandbox using `k8sClient.LlmsafespaceV1().Sandboxes().Delete()`
   - Record metrics using `metrics.RecordSandboxTermination`
   - Add proper error handling and status checking

5. **Implement `GetSandboxStatus` method**:
   - Get sandbox from Kubernetes
   - Return detailed status information including phase, conditions, and resources

**Test Cases to Add:**
```go
// sandbox_service_test.go

func TestCreateSandbox(t *testing.T) {
    // Test successful creation
    // Test creation with warm pod
    // Test creation with invalid parameters
    // Test database failure handling
    // Test Kubernetes API failure handling
}

func TestGetSandbox(t *testing.T) {
    // Test getting existing sandbox
    // Test namespace fallback logic
    // Test handling of non-existent sandbox
}

func TestListSandboxes(t *testing.T) {
    // Test listing with pagination
    // Test enrichment with Kubernetes data
    // Test empty result handling
}

func TestTerminateSandbox(t *testing.T) {
    // Test successful termination
    // Test termination of non-existent sandbox
    // Test permission checking
}

func TestSandboxLifecycle(t *testing.T) {
    // Test full lifecycle: create -> get -> execute -> terminate
    // Verify state transitions
    // Verify resource cleanup
}
```

**Verification:**
- All methods of `interfaces.SandboxService` are implemented
- Service correctly interacts with Kubernetes API
- Database contains consistent metadata
- Error handling covers all failure scenarios
- 85% test coverage on service.go

---

## Phase 2: Metrics Integration

**Objective:** Implement comprehensive observability for the sandbox service through metrics collection. This phase focuses on instrumenting all service methods to record operation durations, success/failure rates, and resource utilization metrics using the `metrics.MetricsRecorder` interface.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/metrics/metrics.go` (complete implementation)
- `src/api/internal/services/sandbox/sandbox_service.go` (add metrics instrumentation)
- `src/api/internal/services/sandbox/sandbox_service_test.go` (add metrics tests)

**Implementation Tasks:**

1. **Complete `metrics.MetricsRecorder` implementation**:
   - Implement `prometheusRecorder` struct that satisfies the interface
   - Define and register all required Prometheus metrics:
     - `sandbox_creations_total` (counter with runtime and warm_pod_used labels)
     - `sandbox_terminations_total` (counter with runtime label)
     - `sandbox_operation_duration_seconds` (histogram with operation label)
     - `warm_pool_hits_total` and `warm_pool_misses_total` (counters)
   - Implement all interface methods with proper label handling
   - Add `NewPrometheusRecorder()` factory function
   - Implement `NewNoopRecorder()` for testing

2. **Instrument `CreateSandbox` method**:
   - Add timer at start of method
   - Record operation duration at end using `metrics.RecordOperationDuration("create", duration)`
   - Record sandbox creation with `metrics.RecordSandboxCreation(runtime, warmPodUsed)`
   - Add detailed logging with structured fields

3. **Instrument `TerminateSandbox` method**:
   - Add timer at start of method
   - Record operation duration at end
   - Record termination with `metrics.RecordSandboxTermination(runtime)`
   - Add detailed logging with structured fields

4. **Instrument `Execute` method**:
   - Record execution attempts, successes, and failures
   - Track execution durations by type (code vs command)
   - Add detailed logging with structured fields

5. **Add metrics for file operations**:
   - Track file upload/download sizes and durations
   - Count file operations by type
   - Record errors with appropriate labels

**Test Cases:**
```go
func TestMetricsRecording(t *testing.T) {
    // Setup mock metrics recorder
    mockMetrics := new(mocks.MockMetricsRecorder)

    // Expect specific metrics to be recorded
    mockMetrics.On("RecordSandboxCreation", "python:3.10", false).Return()
    mockMetrics.On("RecordOperationDuration", "create", mock.Anything).Return()

    // Create service with mock metrics
    svc := NewService(logger, k8sClient, dbService, warmPoolSvc, execSvc, fileSvc, mockMetrics, sessionMgr)

    // Execute operation that should record metrics
    req := types.CreateSandboxRequest{Runtime: "python:3.10"}
    svc.CreateSandbox(context.Background(), req)

    // Verify metrics were recorded
    mockMetrics.AssertExpectations(t)
}

func TestWarmPoolMetrics(t *testing.T) {
    // Similar to above but testing warm pool hit/miss metrics
    // Verify both hit and miss scenarios
}

func TestOperationDurationMetrics(t *testing.T) {
    // Test that all operations record duration metrics
    // Verify correct operation labels
}
```

**Verification:**
- All service methods record appropriate metrics
- Prometheus endpoint exposes all defined metrics
- Metrics have correct labels and values
- Tests verify metrics recording logic

---

## Phase 3: Session Management

**Objective:** Implement robust WebSocket session handling for real-time interaction with sandboxes. This phase focuses on completing the session manager to handle WebSocket connections, message routing, execution streaming, and graceful error handling according to the `interfaces.SessionManager` contract.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/session/session_manager.go` (complete implementation)
- `src/api/internal/services/sandbox/sandbox_service.go` (integrate session management)
- `src/api/internal/services/sandbox/session/session_manager_test.go` (create new file)

**Implementation Tasks:**

1. **Complete `session.Manager` implementation**:
   - Implement all methods of `interfaces.SessionManager` interface
   - Add proper mutex handling for concurrent access
   - Implement session tracking with cleanup for stale sessions
   - Add heartbeat mechanism with configurable intervals
   - Implement execution cancellation support

2. **Implement message handling in `readPump`**:
   - Add support for all message types defined in API.md:
     - `execute` - Run code or commands
     - `cancel` - Cancel running execution
     - `ping` - Heartbeat
     - `file_upload` - Upload file
     - `file_download` - Download file
     - `file_list` - List files
     - `install_packages` - Install packages
   - Add proper error handling for each message type
   - Implement message validation

3. **Implement `writePump` for sending messages**:
   - Add buffered channel for outgoing messages
   - Implement write deadlines
   - Add ping/pong handling for connection health
   - Implement graceful connection closure

4. **Integrate with `sandbox_service.go`**:
   - Implement `CreateSession` method
   - Implement `CloseSession` method
   - Implement `HandleSession` method
   - Add session lifecycle management

5. **Implement execution streaming**:
   - Create execution context with cancellation
   - Stream output in real-time using `types.Message`
   - Handle execution completion and errors
   - Support concurrent executions within a session

**Test Cases:**
```go
// session_manager_test.go

func TestSessionCreation(t *testing.T) {
    // Test creating new session
    // Verify session tracking
    // Test session ID generation
}

func TestMessageHandling(t *testing.T) {
    // Test handling of each message type
    // Verify proper responses
    // Test error handling
}

func TestConcurrentExecutions(t *testing.T) {
    // Test multiple concurrent executions
    // Verify output streaming works correctly
    // Test cancellation works for specific executions
}

func TestSessionExpiration(t *testing.T) {
    // Test session cleanup after inactivity
    // Verify resources are released
}

func TestConnectionErrors(t *testing.T) {
    // Test handling of connection failures
    // Verify cleanup happens correctly
}
```

**Verification:**
- All methods of `interfaces.SessionManager` are implemented
- WebSocket connections handle all message types
- Execution output streams correctly in real-time
- Sessions are properly tracked and cleaned up
- Concurrent executions work correctly
- Error handling is robust

---

## Phase 4: Reconciliation Engine

**Objective:** Implement a robust reconciliation system that ensures sandbox state consistency, handles failures, and enforces timeouts. This phase focuses on completing the `ReconciliationHelper` to detect and resolve inconsistencies between desired and actual sandbox states.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/reconciliation_helper.go` (complete implementation)
- `src/api/internal/services/sandbox/reconciliation_helper_test.go` (enhance tests)
- `src/api/internal/services/sandbox/sandbox_service.go` (integrate reconciliation)

**Implementation Tasks:**

1. **Complete `ReconciliationHelper` implementation**:
   - Implement periodic reconciliation loop with configurable interval
   - Add sandbox state validation logic
   - Implement timeout enforcement for expired sandboxes
   - Add detection and handling of stuck sandboxes
   - Implement pod status synchronization

2. **Implement stuck sandbox detection**:
   - Define criteria for stuck sandboxes (e.g., in Creating state for > 10 minutes)
   - Add logic to mark stuck sandboxes as Failed
   - Implement cleanup of associated resources
   - Add detailed logging and metrics

3. **Implement timeout enforcement**:
   - Check sandbox spec.Timeout against current time
   - Mark expired sandboxes for termination
   - Add grace period for cleanup
   - Record metrics for timeout events

4. **Implement pod status synchronization**:
   - Check if pod exists for each sandbox
   - Update sandbox status based on pod status
   - Handle pod not found scenarios
   - Update resource usage information

5. **Integrate with `sandbox_service.go`**:
   - Start reconciliation loop in service.Start()
   - Stop reconciliation loop in service.Stop()
   - Add configuration options for reconciliation parameters

**Test Cases:**
```go
// reconciliation_helper_test.go

func TestStuckSandboxDetection(t *testing.T) {
    // Create sandbox in Creating state with old timestamp
    // Run reconciliation
    // Verify sandbox is marked as Failed
    // Check for appropriate condition
}

func TestTimeoutEnforcement(t *testing.T) {
    // Create sandbox with timeout and old start time
    // Run reconciliation
    // Verify sandbox is marked for termination
}

func TestPodStatusSync(t *testing.T) {
    // Create sandbox with associated pod
    // Change pod status
    // Run reconciliation
    // Verify sandbox status is updated
}

func TestMissingPodHandling(t *testing.T) {
    // Create sandbox with non-existent pod reference
    // Run reconciliation
    // Verify sandbox is marked as Failed
}

func TestResourceUsageUpdates(t *testing.T) {
    // Create sandbox with running pod
    // Run reconciliation
    // Verify resource usage is updated
}
```

**Verification:**
- Reconciliation loop runs at configured interval
- Stuck sandboxes are detected and handled
- Timeouts are enforced correctly
- Pod status is synchronized accurately
- Resource usage is updated
- All edge cases are handled gracefully

---

## Phase 5: File Operations

**Objective:** Implement secure and efficient file operations for sandboxes, including uploading, downloading, listing, and deleting files. This phase focuses on completing the file operation methods in the sandbox service and ensuring they work correctly with the Kubernetes API.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/sandbox_service.go` (complete file methods)
- `src/api/internal/services/sandbox/file_service.go` (create new file)
- `src/api/internal/services/sandbox/sandbox_service_test.go` (add file tests)

**Implementation Tasks:**

1. **Complete file operation methods in `sandbox_service.go`**:
   - Implement `ListFiles` method
   - Implement `DownloadFile` method
   - Implement `UploadFile` method
   - Implement `DeleteFile` method
   - Add proper error handling and validation

2. **Create `file_service.go` for file operation helpers**:
   - Implement chunking for large file transfers
   - Add checksum validation
   - Implement compression for efficiency
   - Add file type validation
   - Implement quota enforcement

3. **Enhance file operation security**:
   - Add path traversal protection
   - Implement file size limits
   - Add file type restrictions
   - Implement virus scanning integration
   - Add audit logging for file operations

4. **Implement WebSocket file transfer**:
   - Add chunked file upload/download over WebSocket
   - Implement progress reporting
   - Add resume capability for interrupted transfers
   - Implement cancellation support

5. **Add file operation metrics**:
   - Track file sizes and transfer durations
   - Count operations by type
   - Monitor quota usage
   - Track errors with appropriate labels

**Test Cases:**
```go
// sandbox_service_test.go

func TestListFiles(t *testing.T) {
    // Test listing files in different directories
    // Test empty directory handling
    // Test error handling
}

func TestDownloadFile(t *testing.T) {
    // Test downloading existing file
    // Test downloading non-existent file
    // Test downloading directory (should fail)
    // Test downloading large file
}

func TestUploadFile(t *testing.T) {
    // Test uploading new file
    // Test overwriting existing file
    // Test uploading to non-existent directory
    // Test uploading large file
    // Test uploading with invalid path
}

func TestDeleteFile(t *testing.T) {
    // Test deleting existing file
    // Test deleting non-existent file
    // Test deleting directory
    // Test deleting with invalid path
}

func TestFileOperationSecurity(t *testing.T) {
    // Test path traversal attempts
    // Test oversized file uploads
    // Test restricted file types
}
```

**Verification:**
- All file operations work correctly
- Large files transfer efficiently
- Security restrictions are enforced
- Quotas are respected
- Error handling is robust
- Metrics are recorded correctly

---

## Phase 6: Warm Pool Integration

**Objective:** Optimize sandbox creation through integration with the warm pool system. This phase focuses on enhancing the sandbox service to efficiently utilize pre-warmed environments, reducing sandbox creation time and improving user experience.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/sandbox_service.go` (enhance warm pool integration)
- `src/api/internal/services/warmpool/warmpool_service.go` (review and enhance)
- `src/api/internal/services/sandbox/sandbox_service_test.go` (add warm pool tests)

**Implementation Tasks:**

1. **Enhance warm pool integration in `CreateSandbox`**:
   - Improve warm pool availability checking
   - Add runtime and security level matching
   - Implement fallback to regular creation when warm pods unavailable
   - Add detailed logging for warm pool decisions
   - Enhance metrics for warm pool utilization

2. **Implement warm pod assignment optimization**:
   - Add logic to select best matching warm pod
   - Consider resource requirements in matching
   - Implement preloaded package matching
   - Add support for custom initialization

3. **Add warm pool status monitoring**:
   - Implement periodic warm pool status checks
   - Add metrics for warm pool availability
   - Implement alerts for low availability
   - Add detailed logging for warm pool status

4. **Enhance error handling for warm pool integration**:
   - Add graceful fallback when warm pool service unavailable
   - Implement retry logic for transient errors
   - Add circuit breaker for repeated failures
   - Improve error reporting and metrics

5. **Optimize warm pool utilization**:
   - Implement predictive scaling based on usage patterns
   - Add support for priority-based allocation
   - Implement warm pod reservation system
   - Add support for specialized warm pools

**Test Cases:**
```go
func TestWarmPoolIntegration(t *testing.T) {
    // Test sandbox creation with available warm pod
    // Verify warm pod is used correctly
    // Check metrics are recorded
}

func TestWarmPoolFallback(t *testing.T) {
    // Test fallback when warm pool service fails
    // Verify sandbox still creates successfully
    // Check appropriate metrics and logs
}

func TestWarmPodMatching(t *testing.T) {
    // Test matching logic with different requirements
    // Verify best matching pod is selected
    // Test fallback when no exact match
}

func TestWarmPoolMetrics(t *testing.T) {
    // Test metrics recording for hits and misses
    // Verify utilization metrics
    // Check latency improvements
}
```

**Verification:**
- Warm pod utilization improves sandbox creation time by at least 50%
- Fallback mechanisms work reliably
- Matching logic selects optimal warm pods
- Metrics show warm pool utilization
- Error handling is robust

---

## Phase 7: Security Hardening

**Objective:** Enhance the security posture of the sandbox service through comprehensive input validation, network policy enforcement, and resource isolation. This phase focuses on implementing security best practices and ensuring the service is resilient against attacks.

**Files to Modify/Create:**
- `src/api/internal/services/sandbox/validation/validators.go` (enhance validation)
- `src/api/internal/services/sandbox/sandbox_service.go` (add security checks)
- `src/api/internal/services/sandbox/security/security.go` (create new file)
- `src/api/internal/services/sandbox/sandbox_service_test.go` (add security tests)

**Implementation Tasks:**

1. **Enhance input validation in `validators.go`**:
   - Add comprehensive validation for all request fields
   - Implement strict regex patterns for critical fields
   - Add validation for network access rules
   - Implement resource limit validation
   - Add security level validation

2. **Create `security.go` for security helpers**:
   - Implement network policy generation
   - Add seccomp profile selection
   - Implement resource isolation helpers
   - Add security context generation
   - Implement audit logging helpers

3. **Enhance security in `sandbox_service.go`**:
   - Add security checks before operations
   - Implement permission verification
   - Add rate limiting for sensitive operations
   - Implement audit logging for all operations
   - Add security headers and response sanitization

4. **Implement network policy enforcement**:
   - Add support for egress rules
   - Implement domain allowlisting
   - Add port restrictions
   - Implement protocol restrictions
   - Add network isolation between sandboxes

5. **Enhance resource isolation**:
   - Implement CPU and memory limits
   - Add filesystem isolation
   - Implement process isolation
   - Add user namespace isolation
   - Implement capability restrictions

**Test Cases:**
```go
func TestInputValidation(t *testing.T) {
    // Test validation of all request fields
    // Test rejection of malicious inputs
    // Verify proper error messages
}

func TestNetworkPolicyEnforcement(t *testing.T) {
    // Test creation of network policies
    // Verify egress rules are applied
    // Test domain allowlisting
}

func TestResourceIsolation(t *testing.T) {
    // Test CPU and memory limits
    // Verify filesystem isolation
    // Test process isolation
}

func TestSecurityLevels(t *testing.T) {
    // Test different security levels
    // Verify appropriate restrictions
    // Test custom security settings
}

func TestAuditLogging(t *testing.T) {
    // Test audit log generation
    // Verify all sensitive operations are logged
    // Test log format and content
}
```

**Verification:**
- All input is properly validated
- Network policies are correctly enforced
- Resource isolation is effective
- Security levels apply appropriate restrictions
- Audit logging captures all sensitive operations
- Security tests pass with no vulnerabilities

---

## Phase 8: Final Integration and Optimization

**Objective:** Complete the sandbox service implementation with comprehensive testing, optimization, and documentation. This phase focuses on ensuring the service is production-ready, performant, and well-documented.

**Files to Modify/Create:**
- All service files (for optimization)
- `README.md` (create or update)
- `CONTRIBUTING.md` (create or update)
- `API.md` (update with final details)

**Implementation Tasks:**

1. **Perform comprehensive testing**:
   - Add integration tests for all components
   - Implement load testing
   - Add chaos testing for resilience
   - Implement security testing
   - Add performance benchmarks

2. **Optimize performance**:
   - Profile and optimize critical paths
   - Implement caching for frequent operations
   - Add connection pooling
   - Optimize database queries
   - Implement request batching

3. **Enhance error handling and resilience**:
   - Add circuit breakers for external dependencies
   - Implement retry logic with exponential backoff
   - Add graceful degradation for partial failures
   - Implement timeout handling
   - Add comprehensive logging for troubleshooting

4. **Complete documentation**:
   - Update API documentation
   - Add code comments
   - Create architecture diagrams
   - Write troubleshooting guide
   - Add examples and tutorials

5. **Implement monitoring and alerting**:
   - Create Grafana dashboards
   - Set up alerts for critical conditions
   - Add health checks
   - Implement tracing
   - Add SLO/SLI monitoring

**Test Cases:**
```go
func TestEndToEndWorkflow(t *testing.T) {
    // Test complete user workflow
    // Create sandbox, execute code, manage files, terminate
    // Verify all operations work together
}

func TestConcurrentOperations(t *testing.T) {
    // Test multiple concurrent operations
    // Verify thread safety
    // Check performance under load
}

func TestErrorRecovery(t *testing.T) {
    // Test recovery from various error conditions
    // Verify service remains stable
    // Check data consistency after errors
}

func TestPerformanceBenchmarks(t *testing.T) {
    // Benchmark critical operations
    // Verify performance meets requirements
    // Test scaling behavior
}
```

**Verification:**
- All tests pass consistently
- Performance meets or exceeds requirements
- Documentation is complete and accurate
- Monitoring provides comprehensive visibility
- Service is resilient to failures
- Security posture is strong

