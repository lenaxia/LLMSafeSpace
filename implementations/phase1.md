# Phase 1: Project Setup and Core Interfaces

## Overview
This phase focuses on setting up the project structure and defining the core interfaces and types that will be used throughout the project. This provides the foundation for both the API server and controller components.

## Steps

### 1. Set up the project structure as outlined in the README

**Files for Context:**
- README.md (project structure section)

**Core Task:**
Create the basic directory structure for the project, including:
- `/api` - API server component
- `/controller` - Kubernetes controller component
- `/pkg` - Shared packages
- `/test` - Testing utilities and mocks

**Requirements:**
- Follow Go project layout best practices
- Ensure proper separation of concerns between components
- Set up Go modules and initial dependencies

**Implementation Details:**
```bash
# Create main directories
mkdir -p api/cmd/server
mkdir -p api/config
mkdir -p api/internal/{handler,k8s,middleware,service,store,version}
mkdir -p api/pkg/client

mkdir -p controller/cmd/manager
mkdir -p controller/config/{crd,rbac,webhook}
mkdir -p controller/internal/{controller,manager,webhook}
mkdir -p controller/pkg/admission

mkdir -p pkg/{apis/llmsafespace/v1,client,crds,kubernetes,logger}

mkdir -p test/{mocks/interfaces,e2e,integration}

# Initialize Go modules
go mod init github.com/lenaxia/llmsafespace
```

### 2. Define core interfaces in `pkg/interfaces`

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new files in `pkg/interfaces/`

**Core Task:**
Define the core interfaces that will be used throughout the project:
- `LoggerInterface`: For structured logging
- `KubernetesClient`: For interacting with Kubernetes API
- `CacheService`: For caching and session management

**Requirements:**
- Interfaces should be clean and focused on a single responsibility
- Follow Go interface best practices
- Include comprehensive documentation

**Implementation Details:**
Create the following files:
- `pkg/interfaces/logger.go`: Define the logger interface
- `pkg/interfaces/kubernetes.go`: Define the Kubernetes client interface
- `pkg/interfaces/cache.go`: Define the cache service interface

### 3. Implement mock versions of these interfaces in `test/mocks/interfaces`

**Files for Context:**
- `pkg/interfaces/logger.go`
- `pkg/interfaces/kubernetes.go`
- `pkg/interfaces/cache.go`

**Files to Edit:**
- Create new files in `test/mocks/interfaces/`

**Core Task:**
Implement mock versions of the core interfaces for testing:
- `MockLogger`: Mock implementation of `LoggerInterface`
- `MockKubernetesClient`: Mock implementation of `KubernetesClient`
- `MockCacheService`: Mock implementation of `CacheService`

**Requirements:**
- Use a mocking library like `testify/mock`
- Implement all interface methods
- Add helper methods for common testing scenarios

**Implementation Details:**
Create the following files:
- `test/mocks/interfaces/mock_logger.go`
- `test/mocks/interfaces/mock_kubernetes.go`
- `test/mocks/interfaces/mock_cache.go`

### 4. Define shared types in `pkg/types`

**Files for Context:**
- None (new implementation)

**Files to Edit:**
- Create new file `pkg/types/types.go`

**Core Task:**
Define the core types that will be used throughout the project:
- `Sandbox`: Represents a sandbox environment
- `RuntimeEnvironment`: Represents a runtime environment
- `SandboxProfile`: Represents a security profile for sandboxes
- `WarmPool`: Represents a pool of pre-initialized sandboxes
- `WarmPod`: Represents a pre-initialized pod in a warm pool

**Requirements:**
- Types should be compatible with Kubernetes custom resources
- Include proper validation tags
- Follow Go struct best practices
- Include comprehensive documentation

**Implementation Details:**
Create the file `pkg/types/types.go` with the core type definitions.

### 5. Generate DeepCopy implementations for types

**Files for Context:**
- `pkg/types/types.go`

**Files to Edit:**
- None (generation will create new files)

**Core Task:**
Generate DeepCopy implementations for the core types to support Kubernetes custom resources.

**Requirements:**
- Use Kubernetes code-generator tools
- Ensure generated code is included in version control
- Add generation commands to Makefile

**Implementation Details:**
```bash
# Install code generator tools
go install k8s.io/code-generator/cmd/deepcopy-gen@v0.26.0

# Add generation command to Makefile
echo 'deepcopy:
	deepcopy-gen --input-dirs=github.com/lenaxia/llmsafespace/pkg/types --output-file-base=zz_generated.deepcopy --go-header-file=hack/boilerplate.go.txt
' >> Makefile

# Create boilerplate header file
mkdir -p hack
echo '/*
Copyright 2023 The LLMSafeSpace Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
' > hack/boilerplate.go.txt

# Run generation
make deepcopy
```

## Tests

### Unit Tests

1. **Logger Interface Tests**
   - **File:** `pkg/interfaces/logger_test.go`
   - **Purpose:** Verify that the logger interface is well-defined and can be implemented
   - **Test Cases:**
     - Test that a concrete implementation can satisfy the interface
     - Test that the interface methods have the expected signatures

2. **Kubernetes Client Interface Tests**
   - **File:** `pkg/interfaces/kubernetes_test.go`
   - **Purpose:** Verify that the Kubernetes client interface is well-defined
   - **Test Cases:**
     - Test that a concrete implementation can satisfy the interface
     - Test that the interface methods have the expected signatures

3. **Cache Service Interface Tests**
   - **File:** `pkg/interfaces/cache_test.go`
   - **Purpose:** Verify that the cache service interface is well-defined
   - **Test Cases:**
     - Test that a concrete implementation can satisfy the interface
     - Test that the interface methods have the expected signatures

4. **Mock Logger Tests**
   - **File:** `test/mocks/interfaces/mock_logger_test.go`
   - **Purpose:** Verify that the mock logger works as expected
   - **Test Cases:**
     - Test that method calls are recorded
     - Test that expectations can be set and verified

5. **Mock Kubernetes Client Tests**
   - **File:** `test/mocks/interfaces/mock_kubernetes_test.go`
   - **Purpose:** Verify that the mock Kubernetes client works as expected
   - **Test Cases:**
     - Test that method calls are recorded
     - Test that expectations can be set and verified
     - Test that return values can be customized

6. **Mock Cache Service Tests**
   - **File:** `test/mocks/interfaces/mock_cache_test.go`
   - **Purpose:** Verify that the mock cache service works as expected
   - **Test Cases:**
     - Test that method calls are recorded
     - Test that expectations can be set and verified
     - Test that return values can be customized

7. **Types Tests**
   - **File:** `pkg/types/types_test.go`
   - **Purpose:** Verify that the core types are defined correctly
   - **Test Cases:**
     - Test that types can be instantiated
     - Test that validation tags work as expected
     - Test that DeepCopy methods work correctly

### Integration Tests

1. **Type DeepCopy Integration Test**
   - **File:** `test/integration/types_deepcopy_test.go`
   - **Purpose:** Verify that the generated DeepCopy implementations work correctly in a Kubernetes context
   - **Test Cases:**
     - Test that types can be serialized to and from Kubernetes resources
     - Test that DeepCopy preserves all fields correctly
     - Test that types can be used with the Kubernetes client-go library
