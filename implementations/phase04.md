# Phase 4: Expand API Server Functionality

## Overview
This phase focuses on expanding the functionality of the API server to support more advanced features such as authentication, code execution, file operations, and WebSocket support for streaming execution output.

## Steps

### 1. Implement authentication and authorization middleware

**Files for Context:**
- `api/internal/middleware/` (from Phase 2)

**Files to Edit:**
- Create new file: `api/internal/middleware/auth.go`
- Create new file: `api/internal/service/auth_service.go`

**Core Task:**
Implement authentication and authorization middleware to secure the API server.

**Requirements:**
- Support API key authentication
- Support JWT-based authentication (optional)
- Implement role-based access control
- Secure all API endpoints
- Implement middleware for extracting and validating authentication tokens

**Implementation Details:**
Create the authentication middleware and service to handle authentication and authorization.

### 2. Implement the `/sandboxes/{id}/execute` endpoint for code execution

**Files for Context:**
- `api/internal/handler/sandbox_handler.go` (from Phase 2)
- `api/internal/service/sandbox_service.go` (from Phase 2)

**Files to Edit:**
- Update: `api/internal/handler/sandbox_handler.go`
- Create new file: `api/internal/service/execution_service.go`

**Core Task:**
Implement the `/sandboxes/{id}/execute` endpoint for executing code and commands in sandboxes.

**Requirements:**
- Support executing code in different languages
- Support executing shell commands
- Handle execution timeouts
- Capture and return stdout and stderr
- Handle execution errors

**Implementation Details:**
Create the execution service and update the sandbox handler to support code execution.

### 3. Implement the `/sandboxes/{id}/files` endpoints for file operations

**Files for Context:**
- `api/internal/handler/sandbox_handler.go` (from Step 2)
- `api/internal/service/sandbox_service.go` (from Phase 2)

**Files to Edit:**
- Update: `api/internal/handler/sandbox_handler.go`
- Create new file: `api/internal/service/file_service.go`

**Core Task:**
Implement the `/sandboxes/{id}/files` endpoints for file operations in sandboxes.

**Requirements:**
- Support listing files in a sandbox
- Support uploading files to a sandbox
- Support downloading files from a sandbox
- Support deleting files in a sandbox
- Handle file operation errors

**Implementation Details:**
Create the file service and update the sandbox handler to support file operations.

### 4. Implement the `/runtimes` and `/profiles` endpoints for listing available resources

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new file: `api/internal/handler/runtime_handler.go`
- Create new file: `api/internal/handler/profile_handler.go`
- Create new file: `api/internal/service/runtime_service.go`
- Create new file: `api/internal/service/profile_service.go`

**Core Task:**
Implement the `/runtimes` and `/profiles` endpoints for listing available runtime environments and sandbox profiles.

**Requirements:**
- Support listing all available runtime environments
- Support getting details of a specific runtime environment
- Support listing all available sandbox profiles
- Support getting details of a specific sandbox profile
- Handle errors and return appropriate error responses

**Implementation Details:**
Create the runtime and profile handlers and services to support listing available resources.

### 5. Implement WebSocket support for streaming execution output

**Files for Context:**
- `api/internal/handler/sandbox_handler.go` (from Step 3)
- `api/internal/service/execution_service.go` (from Step 2)

**Files to Edit:**
- Update: `api/internal/handler/sandbox_handler.go`
- Update: `api/internal/service/execution_service.go`
- Create new file: `api/internal/service/session_service.go`

**Core Task:**
Implement WebSocket support for streaming execution output in real-time.

**Requirements:**
- Support WebSocket connections for streaming execution output
- Implement a session management system for WebSocket connections
- Support executing code and commands via WebSocket
- Support cancelling executions
- Handle WebSocket connection errors

**Implementation Details:**
Update the execution service and sandbox handler to support WebSocket connections and streaming execution output.

### 6. Implement error handling and structured error responses

**Files for Context:**
- `api/internal/middleware/` (from Phase 2)

**Files to Edit:**
- Create new file: `api/internal/errors/errors.go`
- Create new file: `api/internal/middleware/error_handler.go`

**Core Task:**
Implement error handling and structured error responses for the API server.

**Requirements:**
- Define a structured error response format
- Implement error types for different error scenarios
- Implement middleware for handling and formatting errors
- Ensure all API endpoints return consistent error responses
- Include appropriate HTTP status codes for different error types

**Implementation Details:**
Create the error handling system and integrate it with the API server.

## Tests

### Unit Tests

1. **Authentication Middleware Tests**
   - **File:** `api/internal/middleware/auth_test.go`
   - **Purpose:** Verify that the authentication middleware correctly authenticates requests
   - **Test Cases:**
     - Test that a valid API key is accepted
     - Test that an invalid API key is rejected
     - Test that a missing API key is rejected
     - Test that the middleware sets the user ID in the context

2. **Authorization Middleware Tests**
   - **File:** `api/internal/middleware/auth_test.go`
   - **Purpose:** Verify that the authorization middleware correctly authorizes requests
   - **Test Cases:**
     - Test that a user can access their own resources
     - Test that a user cannot access another user's resources
     - Test that an admin can access any resources
     - Test that the middleware rejects unauthorized requests

3. **Execution Service Tests**
   - **File:** `api/internal/service/execution_service_test.go`
   - **Purpose:** Verify that the execution service correctly executes code and commands
   - **Test Cases:**
     - Test that code execution works correctly
     - Test that command execution works correctly
     - Test that execution timeouts are handled correctly
     - Test that execution errors are handled correctly

4. **File Service Tests**
   - **File:** `api/internal/service/file_service_test.go`
   - **Purpose:** Verify that the file service correctly handles file operations
   - **Test Cases:**
     - Test that listing files works correctly
     - Test that uploading files works correctly
     - Test that downloading files works correctly
     - Test that deleting files works correctly
     - Test that file operation errors are handled correctly

5. **Runtime Service Tests**
   - **File:** `api/internal/service/runtime_service_test.go`
   - **Purpose:** Verify that the runtime service correctly lists available runtime environments
   - **Test Cases:**
     - Test that listing all runtime environments works correctly
     - Test that getting a specific runtime environment works correctly
     - Test that errors are handled correctly

6. **Profile Service Tests**
   - **File:** `api/internal/service/profile_service_test.go`
   - **Purpose:** Verify that the profile service correctly lists available sandbox profiles
   - **Test Cases:**
     - Test that listing all sandbox profiles works correctly
     - Test that getting a specific sandbox profile works correctly
     - Test that errors are handled correctly

7. **Session Service Tests**
   - **File:** `api/internal/service/session_service_test.go`
   - **Purpose:** Verify that the session service correctly manages WebSocket sessions
   - **Test Cases:**
     - Test that creating a session works correctly
     - Test that closing a session works correctly
     - Test that sending messages to a session works correctly
     - Test that receiving messages from a session works correctly

8. **Error Handling Tests**
   - **File:** `api/internal/errors/errors_test.go`
   - **Purpose:** Verify that the error handling system works correctly
   - **Test Cases:**
     - Test that error types are correctly defined
     - Test that error responses are correctly formatted
     - Test that HTTP status codes are correctly assigned

### Integration Tests

1. **Authentication Integration Test**
   - **File:** `test/e2e/auth_test.go`
   - **Purpose:** Verify that authentication works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that a valid API key can access protected endpoints
     - Test that an invalid API key cannot access protected endpoints
     - Test that a user can only access their own resources

2. **Code Execution Integration Test**
   - **File:** `test/e2e/execution_test.go`
   - **Purpose:** Verify that code execution works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that code can be executed in a sandbox
     - Test that execution results are returned correctly
     - Test that execution timeouts are handled correctly

3. **File Operation Integration Test**
   - **File:** `test/e2e/file_test.go`
   - **Purpose:** Verify that file operations work correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that files can be uploaded to a sandbox
     - Test that files can be downloaded from a sandbox
     - Test that files can be listed in a sandbox
     - Test that files can be deleted from a sandbox

4. **WebSocket Integration Test**
   - **File:** `test/e2e/websocket_test.go`
   - **Purpose:** Verify that WebSocket support works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that a WebSocket connection can be established
     - Test that execution output is streamed in real-time
     - Test that executions can be cancelled
     - Test that the connection is closed correctly
