# Phase 7: Persistent Storage and Caching

## Overview
This phase focuses on implementing persistent storage and caching to store user data, sandbox metadata, and other information that needs to be persisted across restarts. This includes integration with PostgreSQL for persistent storage and Redis for caching and session management.

## Steps

### 1. Integrate with PostgreSQL for persistent storage

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new file: `api/internal/store/database.go`
- Create new file: `api/internal/store/models.go`
- Create new file: `api/internal/config/database.go`
- Update: `api/cmd/server/main.go`

**Core Task:**
Integrate with PostgreSQL for persistent storage of user data, sandbox metadata, and other information.

**Requirements:**
- Implement a database connection manager
- Define database models for users, sandboxes, and other entities
- Implement CRUD operations for each model
- Handle database migrations
- Support connection pooling and retry logic
- Handle database errors gracefully

**Implementation Details:**
Create a new database implementation for the API server and integrate it with the server.

### 2. Integrate with Redis for caching and session management

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new file: `api/internal/store/cache.go`
- Create new file: `api/internal/config/cache.go`
- Update: `api/cmd/server/main.go`

**Core Task:**
Integrate with Redis for caching and session management to improve performance and handle WebSocket sessions.

**Requirements:**
- Implement a Redis connection manager
- Implement caching for frequently accessed data
- Implement session management for WebSocket connections
- Support connection pooling and retry logic
- Handle Redis errors gracefully

**Implementation Details:**
Create a new cache implementation for the API server and integrate it with the server.

### 3. Implement a `DatabaseService` and `CacheService` in the API server

**Files for Context:**
- `api/internal/store/database.go` (from Step 1)
- `api/internal/store/cache.go` (from Step 2)
- `pkg/interfaces/cache.go` (from Phase 1)

**Files to Edit:**
- Create new file: `api/internal/service/database_service.go`
- Create new file: `api/internal/service/cache_service.go`
- Update: `api/internal/service/sandbox_service.go` (from Phase 5)
- Update: `api/internal/service/session_service.go` (from Phase 4)

**Core Task:**
Implement a `DatabaseService` and `CacheService` in the API server to provide a clean interface for database and cache operations.

**Requirements:**
- Implement the `DatabaseService` interface with methods for CRUD operations
- Implement the `CacheService` interface from Phase 1
- Integrate the services with the existing API server components
- Handle errors and return appropriate error types
- Support transactions and batch operations

**Implementation Details:**
Create new service implementations for database and cache operations and integrate them with the API server.

### 4. Implement persistent storage for sandbox files (optional)

**Files for Context:**
- `api/internal/service/file_service.go` (from Phase 4)

**Files to Edit:**
- Update: `api/internal/service/file_service.go`
- Create new file: `api/internal/store/file_store.go`

**Core Task:**
Implement persistent storage for sandbox files to allow files to persist across sandbox restarts.

**Requirements:**
- Implement a file storage system that can persist files across sandbox restarts
- Support different storage backends (local filesystem, object storage)
- Implement methods for storing, retrieving, and deleting files
- Handle file storage errors gracefully
- Support file metadata and versioning

**Implementation Details:**
Update the file service to support persistent storage for sandbox files.

## Tests

### Unit Tests

1. **Database Connection Tests**
   - **File:** `api/internal/store/database_test.go`
   - **Purpose:** Verify that the database connection manager works correctly
   - **Test Cases:**
     - Test that a connection can be established
     - Test that connection pooling works correctly
     - Test that retry logic works correctly
     - Test that errors are handled gracefully

2. **Database Model Tests**
   - **File:** `api/internal/store/models_test.go`
   - **Purpose:** Verify that the database models are defined correctly
   - **Test Cases:**
     - Test that models can be created and saved
     - Test that models can be retrieved
     - Test that models can be updated
     - Test that models can be deleted
     - Test that model validation works correctly

3. **Cache Connection Tests**
   - **File:** `api/internal/store/cache_test.go`
   - **Purpose:** Verify that the cache connection manager works correctly
   - **Test Cases:**
     - Test that a connection can be established
     - Test that connection pooling works correctly
     - Test that retry logic works correctly
     - Test that errors are handled gracefully

4. **DatabaseService Tests**
   - **File:** `api/internal/service/database_service_test.go`
   - **Purpose:** Verify that the database service works correctly
   - **Test Cases:**
     - Test that CRUD operations work correctly
     - Test that transactions work correctly
     - Test that batch operations work correctly
     - Test that errors are handled correctly

5. **CacheService Tests**
   - **File:** `api/internal/service/cache_service_test.go`
   - **Purpose:** Verify that the cache service works correctly
   - **Test Cases:**
     - Test that values can be stored and retrieved
     - Test that values can be deleted
     - Test that expiration works correctly
     - Test that errors are handled correctly

6. **File Store Tests**
   - **File:** `api/internal/store/file_store_test.go`
   - **Purpose:** Verify that the file store works correctly
   - **Test Cases:**
     - Test that files can be stored
     - Test that files can be retrieved
     - Test that files can be deleted
     - Test that file metadata can be stored and retrieved
     - Test that errors are handled correctly

### Integration Tests

1. **Database Integration Test**
   - **File:** `test/e2e/database_test.go`
   - **Purpose:** Verify that the database integration works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that user data can be stored and retrieved
     - Test that sandbox metadata can be stored and retrieved
     - Test that data persists across server restarts
     - Test that database migrations work correctly

2. **Cache Integration Test**
   - **File:** `test/e2e/cache_test.go`
   - **Purpose:** Verify that the cache integration works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that frequently accessed data is cached
     - Test that session management works correctly
     - Test that cache expiration works correctly
     - Test that the system works correctly when Redis is unavailable

3. **Persistent File Storage Test**
   - **File:** `test/e2e/file_storage_test.go`
   - **Purpose:** Verify that persistent file storage works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that files persist across sandbox restarts
     - Test that file metadata is preserved
     - Test that files can be accessed from different sandboxes
     - Test that file storage quotas are enforced
