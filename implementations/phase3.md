# Phase 3: Expand Controller Functionality

## Overview
This phase focuses on expanding the functionality of the controller to support more advanced features such as security policies, network policies, resource limits, and sandbox lifecycle management.

## Steps

### 1. Implement the `SandboxProfile` CRD and reconciliation loop

**Files for Context:**
- `controller/api/v1/sandbox_types.go` (from Phase 2)
- `pkg/types/types.go` (from Phase 1)

**Files to Edit:**
- Create new file: `controller/api/v1/sandboxprofile_types.go`
- Create new file: `controller/internal/controller/sandboxprofile_controller.go`

**Core Task:**
Define the `SandboxProfile` CRD and implement a reconciliation loop that validates and processes sandbox profiles.

**Requirements:**
- Define the `SandboxProfile` CRD with fields for security policies, network policies, and resource limits
- Implement a reconciliation loop that validates the profile
- Ensure that the profile can be referenced by sandboxes

**Implementation Details:**
```bash
# Create API for SandboxProfile
cd controller
kubebuilder create api --group llmsafespace --version v1 --kind SandboxProfile --resource --controller
```

Update the generated files to implement the `SandboxProfile` CRD and controller.

### 2. Enhance the `Sandbox` reconciliation loop to apply security policies from `SandboxProfile`

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Phase 2)
- `controller/api/v1/sandboxprofile_types.go` (from Step 1)

**Files to Edit:**
- `controller/internal/controller/sandbox_controller.go`

**Core Task:**
Enhance the `Sandbox` reconciliation loop to apply security policies from the referenced `SandboxProfile`.

**Requirements:**
- Retrieve the referenced `SandboxProfile` when reconciling a `Sandbox`
- Apply the security policies from the profile to the pod
- Handle cases where the profile doesn't exist or is invalid

**Implementation Details:**
Update the `Sandbox` controller to apply security policies from the referenced `SandboxProfile`.

### 3. Implement network policy management

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Step 2)

**Files to Edit:**
- Create new file: `controller/internal/common/network_policy_manager.go`
- Update: `controller/internal/controller/sandbox_controller.go`

**Core Task:**
Implement network policy management for sandboxes to control ingress and egress traffic.

**Requirements:**
- Create a `NetworkPolicyManager` that can create and manage network policies
- Apply network policies based on the sandbox and profile specifications
- Support domain-based egress filtering
- Support port-based filtering

**Implementation Details:**
Create a new `NetworkPolicyManager` and integrate it with the `Sandbox` controller.

### 4. Implement resource limit enforcement

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Step 3)

**Files to Edit:**
- Create new file: `controller/internal/common/resource_manager.go`
- Update: `controller/internal/controller/sandbox_controller.go`

**Core Task:**
Implement resource limit enforcement for sandboxes to control CPU, memory, and storage usage.

**Requirements:**
- Create a `ResourceManager` that can apply resource limits to pods
- Apply resource limits based on the sandbox and profile specifications
- Support CPU, memory, and storage limits
- Handle resource validation and defaults

**Implementation Details:**
Create a new `ResourceManager` and integrate it with the `Sandbox` controller.

### 5. Implement sandbox lifecycle management (creating, running, terminating)

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Step 4)

**Files to Edit:**
- Update: `controller/internal/controller/sandbox_controller.go`
- Create new file: `controller/internal/common/lifecycle_manager.go`

**Core Task:**
Implement sandbox lifecycle management to handle the different phases of a sandbox's lifecycle.

**Requirements:**
- Define the lifecycle phases: Pending, Creating, Running, Terminating, Terminated, Failed
- Implement state transitions based on pod status and user actions
- Handle cleanup of resources when a sandbox is terminated
- Implement finalizers to ensure proper cleanup

**Implementation Details:**
Create a new `LifecycleManager` and integrate it with the `Sandbox` controller.

### 6. Implement sandbox status updates and conditions

**Files for Context:**
- `controller/internal/controller/sandbox_controller.go` (from Step 5)

**Files to Edit:**
- Update: `controller/internal/controller/sandbox_controller.go`
- Create new file: `controller/internal/common/status_manager.go`

**Core Task:**
Implement sandbox status updates and conditions to provide detailed information about the sandbox state.

**Requirements:**
- Define conditions for different aspects of the sandbox: Ready, PodReady, NetworkReady, etc.
- Update conditions based on the state of the sandbox and its resources
- Provide detailed status information in the sandbox status
- Implement status updates for all reconciliation actions

**Implementation Details:**
Create a new `StatusManager` and integrate it with the `Sandbox` controller.

## Tests

### Unit Tests

1. **SandboxProfile Controller Tests**
   - **File:** `controller/internal/controller/sandboxprofile_controller_test.go`
   - **Purpose:** Verify that the sandbox profile controller correctly validates and processes profiles
   - **Test Cases:**
     - Test that a valid profile is accepted
     - Test that an invalid profile is rejected
     - Test that the controller updates the profile status correctly

2. **Sandbox Controller Security Policy Tests**
   - **File:** `controller/internal/controller/sandbox_controller_security_test.go`
   - **Purpose:** Verify that the sandbox controller correctly applies security policies
   - **Test Cases:**
     - Test that security policies from a profile are applied to a pod
     - Test that default security policies are applied when no profile is specified
     - Test that the controller handles invalid security policies correctly

3. **Network Policy Manager Tests**
   - **File:** `controller/internal/common/network_policy_manager_test.go`
   - **Purpose:** Verify that the network policy manager correctly creates and manages network policies
   - **Test Cases:**
     - Test that network policies are created based on sandbox specifications
     - Test that domain-based egress filtering works correctly
     - Test that port-based filtering works correctly
     - Test that the manager handles updates to network policies correctly

4. **Resource Manager Tests**
   - **File:** `controller/internal/common/resource_manager_test.go`
   - **Purpose:** Verify that the resource manager correctly applies resource limits
   - **Test Cases:**
     - Test that resource limits are applied based on sandbox specifications
     - Test that default resource limits are applied when not specified
     - Test that the manager handles invalid resource specifications correctly

5. **Lifecycle Manager Tests**
   - **File:** `controller/internal/common/lifecycle_manager_test.go`
   - **Purpose:** Verify that the lifecycle manager correctly handles sandbox lifecycle
   - **Test Cases:**
     - Test that state transitions work correctly
     - Test that resources are cleaned up when a sandbox is terminated
     - Test that finalizers are added and removed correctly

6. **Status Manager Tests**
   - **File:** `controller/internal/common/status_manager_test.go`
   - **Purpose:** Verify that the status manager correctly updates sandbox status
   - **Test Cases:**
     - Test that conditions are updated based on sandbox state
     - Test that status information is accurate
     - Test that status updates are applied correctly

### Integration Tests

1. **SandboxProfile Integration Test**
   - **File:** `test/e2e/sandboxprofile_test.go`
   - **Purpose:** Verify that sandbox profiles work correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that a sandbox can reference a profile
     - Test that security policies from a profile are applied to a sandbox
     - Test that changes to a profile are reflected in sandboxes that reference it

2. **Network Policy Integration Test**
   - **File:** `test/e2e/network_policy_test.go`
   - **Purpose:** Verify that network policies work correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that network policies are created for a sandbox
     - Test that egress filtering works correctly
     - Test that ingress filtering works correctly

3. **Resource Limit Integration Test**
   - **File:** `test/e2e/resource_limit_test.go`
   - **Purpose:** Verify that resource limits work correctly in an end-to-end scenario
   - **Test Cases:**
     - Test that resource limits are applied to a sandbox
     - Test that a sandbox cannot exceed its resource limits
     - Test that resource limits can be updated

4. **Sandbox Lifecycle Integration Test**
   - **File:** `test/e2e/sandbox_lifecycle_test.go`
   - **Purpose:** Verify that sandbox lifecycle management works correctly in an end-to-end scenario
   - **Test Cases:**
     - Test the complete lifecycle of a sandbox from creation to termination
     - Test that resources are cleaned up when a sandbox is terminated
     - Test that a sandbox can be restarted after failure
