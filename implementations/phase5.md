# Phase 5: Warm Pool Integration

## Overview
This phase focuses on implementing the warm pool system to improve sandbox startup times. This includes implementing the `WarmPool` and `WarmPod` CRDs, reconciliation loops, and integration with the sandbox system.

## Steps

### 1. Controller: Implement the `WarmPool` and `WarmPod` CRDs

**Files for Context:**
- `controller/api/v1/sandbox_types.go` (from Phase 3)
- `pkg/types/types.go` (from Phase 1)

**Files to Edit:**
- Create new file: `controller/api/v1/warmpool_types.go`
- Create new file: `controller/api/v1/warmpod_types.go`

**Core Task:**
Define the `WarmPool` and `WarmPod` CRDs to support the warm pool system.

**Requirements:**
- Define the `WarmPool` CRD with fields for runtime, size, and auto-scaling
- Define the `WarmPod` CRD with fields for pool reference and status
- Ensure compatibility with the types defined in `pkg/types`
- Add kubebuilder annotations for validation

**Implementation Details:**
```bash
# Create API for WarmPool
cd controller
kubebuilder create api --group llmsafespace --version v1 --kind WarmPool --resource --controller

# Create API for WarmPod
kubebuilder create api --group llmsafespace --version v1 --kind WarmPod --resource --controller
```

Update the generated files to implement the `WarmPool` and `WarmPod` CRDs.

### 2. Controller: Implement the reconciliation loops for `WarmPool` and `WarmPod`

**Files for Context:**
- `controller/api/v1/warmpool_types.go` (from Step 1)
- `controller/api/v1/warmpod_types.go` (from Step 1)

**Files to Edit:**
- Create new file: `controller/internal/controller/warmpool_controller.go`
- Create new file: `controller/internal/controller/warmpod_controller.go`

**Core Task:**
Implement the reconciliation loops for the `WarmPool` and `WarmPod` resources.

**Requirements:**
- Implement a reconciliation loop for `WarmPool` that maintains the desired number of warm pods
- Implement a reconciliation loop for `WarmPod` that creates and manages the underlying pod
- Handle pod lifecycle events and update the warm pod status
- Implement auto-scaling based on usage patterns

**Implementation Details:**
Update the generated controllers to implement the reconciliation logic for warm pools and warm pods.

### 3. Controller: Integrate warm pod allocation with the `Sandbox` reconciliation loop

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Phase 3)
- `controller/internal/controller/warmpool_controller.go` (from Step 2)
- `controller/internal/controller/warmpod_controller.go` (from Step 2)

**Files to Edit:**
- Update: `controller/internal/controller/sandbox_controller.go`
- Create new file: `controller/internal/common/warm_pod_allocator.go`

**Core Task:**
Integrate warm pod allocation with the `Sandbox` reconciliation loop to use warm pods when available.

**Requirements:**
- Implement a `WarmPodAllocator` that can find and allocate warm pods for sandboxes
- Update the `Sandbox` controller to check for available warm pods when creating a sandbox
- Handle the case where no warm pods are available
- Update the sandbox status with information about the warm pod used

**Implementation Details:**
Create a new `WarmPodAllocator` and integrate it with the `Sandbox` controller.

### 4. Controller: Implement warm pool auto-scaling and management

**Files for Context:**
- `controller/internal/controller/warmpool_controller.go` (from Step 2)

**Files to Edit:**
- Update: `controller/internal/controller/warmpool_controller.go`
- Create new file: `controller/internal/common/warm_pool_manager.go`

**Core Task:**
Implement warm pool auto-scaling and management to optimize resource usage.

**Requirements:**
- Implement auto-scaling based on usage patterns
- Implement TTL-based cleanup of unused warm pods
- Implement metrics collection for warm pool usage
- Handle scale-up and scale-down events

**Implementation Details:**
Create a new `WarmPoolManager` and integrate it with the `WarmPool` controller.

### 5. API Server: Implement a `WarmPoolService` for coordinating with warm pools

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new file: `api/internal/service/warmpool_service.go`

**Core Task:**
Implement a `WarmPoolService` for coordinating with warm pools from the API server.

**Requirements:**
- Implement methods for getting warm pool status
- Implement methods for checking warm pod availability
- Implement methods for requesting warm pods for sandboxes
- Handle errors and return appropriate error types

**Implementation Details:**
Create the `WarmPoolService` with methods for coordinating with warm pools.

### 6. API Server: Integrate warm pool support in the `SandboxService`

**Files for Context:**
- `api/internal/service/sandbox_service.go` (from Phase 4)
- `api/internal/service/warmpool_service.go` (from Step 5)

**Files to Edit:**
- Update: `api/internal/service/sandbox_service.go`

**Core Task:**
Integrate warm pool support in the `SandboxService` to use warm pods when creating sandboxes.

**Requirements:**
- Update the sandbox creation method to check for available warm pods
- Add an option to use warm pods when creating a sandbox
- Handle the case where no warm pods are available
- Update the sandbox response with information about the warm pod used

**Implementation Details:**
Update the `SandboxService` to integrate with the `WarmPoolService`.

### 7. API Server: Implement endpoints for managing warm pools (`/warmpools`)

**Files for Context:**
- `api/internal/service/warmpool_service.go` (from Step 5)

**Files to Edit:**
- Create new file: `api/internal/handler/warmpool_handler.go`

**Core Task:**
Implement endpoints for managing warm pools (`/warmpools`).

**Requirements:**
- Implement a `GET /warmpools` endpoint for listing warm pools
- Implement a `POST /warmpools` endpoint for creating warm pools
- Implement a `GET /warmpools/{id}` endpoint for getting warm pool details
- Implement a `DELETE /warmpools/{id}` endpoint for deleting warm pools
- Implement a `GET /warmpools/{id}/status` endpoint for getting warm pool status
- Handle errors and return appropriate error responses

**Implementation Details:**
Create the warm pool handler with endpoints for managing warm pools.

## Tests

### Unit Tests

1. **WarmPool Controller Tests**
   - **File:** `controller/internal/controller/warmpool_controller_test.go`
   - **Purpose:** Verify that the warm pool controller correctly manages warm pools
   - **Test Cases:**
     - Test that the controller creates warm pods based on the warm pool specification
     - Test that the controller scales the number of warm pods up and down
     - Test that the controller updates the warm pool status correctly
     - Test that the controller handles auto-scaling correctly

2. **WarmPod Controller Tests**
   - **File:** `controller/internal/controller/warmpod_controller_test.go`
   - **Purpose:** Verify that the warm pod controller correctly manages warm pods
   - **Test Cases:**
     - Test that the controller creates a pod for a warm pod
     - Test that the controller updates the warm pod status based on the pod status
     - Test that the controller handles pod deletion correctly
     - Test that the controller handles errors correctly

3. **WarmPodAllocator Tests**
   - **File:** `controller/internal/common/warm_pod_allocator_test.go`
   - **Purpose:** Verify that the warm pod allocator correctly allocates warm pods
   - **Test Cases:**
     - Test that the allocator finds a matching warm pod
     - Test that the allocator handles the case where no warm pods are available
     - Test that the allocator correctly claims a warm pod
     - Test that the allocator handles concurrent allocation correctly

4. **WarmPoolManager Tests**
   - **File:** `controller/internal/common/warm_pool_manager_test.go`
   - **Purpose:** Verify that the warm pool manager correctly manages warm pools
   - **Test Cases:**
     - Test that the manager scales the number of warm pods up and down
     - Test that the manager handles TTL-based cleanup correctly
     - Test that the manager collects metrics correctly
     - Test that the manager handles errors correctly

5. **WarmPoolService Tests**
   - **File:** `api/internal/service/warmpool_service_test.go`
   - **Purpose:** Verify that the warm pool service correctly coordinates with warm pools
   - **Test Cases:**
     - Test that the service gets warm pool status correctly
     - Test that the service checks warm pod availability correctly
     - Test that the service requests warm pods correctly
     - Test that the service handles errors correctly

6. **SandboxService Warm Pool Integration Tests**
   - **File:** `api/internal/service/sandbox_service_warmpool_test.go`
   - **Purpose:** Verify that the sandbox service correctly integrates with warm pools
   - **Test Cases:**
     - Test that the service uses a warm pod when available
     - Test that the service falls back to creating a new pod when no warm pods are available
     - Test that the service updates the sandbox response with warm pod information
     - Test that the service handles errors correctly

7. **WarmPool Handler Tests**
   - **File:** `api/internal/handler/warmpool_handler_test.go`
   - **Purpose:** Verify that the warm pool handler correctly processes HTTP requests
   - **Test Cases:**
     - Test that a valid GET request returns a list of warm pools
     - Test that a valid POST request creates a warm pool
     - Test that a valid GET request returns warm pool details
     - Test that a valid DELETE request deletes a warm pool
     - Test that a valid GET request returns warm pool status
     - Test that invalid requests return appropriate error responses

### Integration Tests

1. **Warm Pool Creation Test**
   - **File:** `test/e2e/warmpool_test.go`
   - **Purpose:** Verify that warm pools can be created and managed
   - **Test Cases:**
     - Test that a warm pool can be created via the API
     - Test that the controller creates warm pods for the warm pool
     - Test that the warm pool status is updated correctly

2. **Warm Pod Allocation Test**
   - **File:** `test/e2e/warmpod_allocation_test.go`
   - **Purpose:** Verify that warm pods are allocated correctly
   - **Test Cases:**
     - Test that a sandbox can use a warm pod
     - Test that the warm pod is correctly assigned to the sandbox
     - Test that the sandbox status is updated with warm pod information

3. **Warm Pool Auto-Scaling Test**
   - **File:** `test/e2e/warmpool_autoscaling_test.go`
   - **Purpose:** Verify that warm pool auto-scaling works correctly
   - **Test Cases:**
     - Test that the warm pool scales up when utilization is high
     - Test that the warm pool scales down when utilization is low
     - Test that the warm pool respects the minimum and maximum size limits

4. **Warm Pool API Test**
   - **File:** `test/e2e/warmpool_api_test.go`
   - **Purpose:** Verify that the warm pool API endpoints work correctly
   - **Test Cases:**
     - Test that warm pools can be listed via the API
     - Test that warm pool details can be retrieved via the API
     - Test that warm pool status can be retrieved via the API
     - Test that warm pools can be deleted via the API
