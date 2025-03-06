# Architecture Overview: LLMSafeSpace

## System Components and Interactions

LLMSafeSpace consists of three primary components that work together to provide secure code execution environments:

### Core Components

1. **Agent API Service (`agent-api`)**
   - Serves as the entry point for all SDK interactions
   - Exposes REST API and WebSocket endpoints for client communication
   - Handles authentication, authorization, and request validation
   - Manages sandbox lifecycle (creation, connection, termination)
   - Integrates with Kubernetes API to orchestrate sandbox resources
   - Maintains session state and user context
   - Coordinates warm pool usage for faster sandbox creation

2. **Sandbox Controller (`controller`)**
   - Unified Kubernetes operator that implements control loops for all custom resources
   - Manages the lifecycle of sandboxes, warm pools, and warm pods
   - Enforces security policies, resource limits, and isolation boundaries
   - Handles template management and runtime environment selection
   - Updates status information for all managed resources
   - Implements reconciliation logic for desired vs. actual state
   - Maintains pools of pre-initialized sandbox environments
   - Ensures minimum number of warm pods are always available
   - Handles recycling of used pods when appropriate
   - Implements auto-scaling based on usage patterns
   - Coordinates warm pod allocation to sandboxes

4. **Execution Runtime (`execution-runtime`)**
   - Container images for various language environments (Python, Node.js, etc.)
   - Provides pre-installed tools and libraries commonly used by LLM agents
   - Implements security hardening measures (read-only filesystem, non-root user)
   - Supports different security levels (standard, high) with appropriate configurations
   - Includes monitoring and logging capabilities

### Supporting Services

- **PostgreSQL**: Persistent storage for:
  - User accounts and API keys
  - Sandbox metadata and configuration
  - Usage statistics and billing information
  - Audit logs for compliance
  - Warm pool configuration and status

- **Redis**: In-memory data store for:
  - Session management
  - Caching frequently accessed data
  - Real-time metrics and status information
  - Temporary storage for streaming outputs
  - Warm pod allocation tracking

### Component Interactions

1. **SDK to API Service**:
   - SDKs communicate with the API service via REST and WebSocket protocols
   - Authentication occurs via API keys or OAuth tokens
   - Requests are validated and authorized before processing
   - Real-time output is streamed via WebSockets

2. **API Service to Sandbox Controller**:
   - API service creates/updates Kubernetes custom resources (CRs)
   - Controller watches for changes to these resources
   - Status updates flow back from controller to API service
   - API service requests warm pods when available

3. **Sandbox Controller Internal Coordination**:
   - Unified controller manages all resource types with shared utilities
   - Warm pod allocation is handled internally without cross-controller communication
   - Single work queue processes all resource types with appropriate handlers
   - Shared components handle common tasks like pod creation and security configuration
   - Integrated metrics collection for all resource types

4. **Sandbox Controller to Runtime**:
   - Controller creates pods with appropriate runtime images
   - Security contexts and resource limits are applied
   - Network policies and service accounts are configured
   - Volume mounts are set up for code and data
   - Reuses warm pods when available

5. **Runtime to API Service**:
   - Execution results are sent back to the API service
   - Logs and metrics are collected for monitoring
   - File operations are proxied through the API service

## Deployment Architecture

### Kubernetes-Native Architecture

LLMSafeSpace is designed as a Kubernetes-native application with the following deployment characteristics:

1. **Namespace Isolation**:
   - Core components deployed in dedicated namespace (`llmsafespace`)
   - Sandboxes deployed in separate namespaces for isolation
   - Network policies enforce namespace boundaries

2. **Scalability**:
   - Horizontal scaling of API service based on request load
   - Independent scaling of sandbox controller based on resource count
   - Dynamic provisioning of sandbox pods based on demand

3. **High Availability**:
   - Multiple replicas of API service for redundancy
   - Leader election for sandbox controller to prevent conflicts
   - Stateless design with external state storage

4. **Resource Management**:
   - Resource quotas at namespace level
   - Pod resource limits for predictable performance
   - Priority classes for critical components

### Deployment Topology

```
                                  ┌─────────────────┐
                                  │   Client SDKs   │
                                  └────────┬────────┘
                                           │
                                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                           Kubernetes Cluster                        │
│                                                                     │
│  ┌─────────────────┐      ┌─────────────────┐    ┌──────────────┐   │
│  │                 │      │                 │    │              │   │
│  │   Agent API     │◄────►│    Combined     │◄──►│  PostgreSQL  │   │
│  │   Service       │      │   Controller    │    │              │   │
│  │                 │◄────►│                 │    └──────────────┘   │
│  └────────┬────────┘      └────────┬────────┘                      │
│           │                        │           ┌──────────────┐    │
│           │                        │           │              │    │
│           │                        └──────────►│    Redis     │    │
│           │                        │           │              │    │
│           │                        │           └──────────────┘    │
│           │                        │                               │
│           │                        ▼                               │
│           │            ┌─────────────────────┐                     │
│           │            │   Warm Pod Pools    │                     │
│           │            │                     │                     │
│           │            │  ┌───┐ ┌───┐ ┌───┐  │                     │
│           │            │  │Pod│ │Pod│ │Pod│  │                     │
│           │            │  └───┘ └───┘ └───┘  │                     │
│           │            │                     │                     │
│           │            └─────────────────────┘                     │
│           │                                                        │
│           ▼                                                        │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                      Sandbox Namespace                        │  │
│  │                                                               │  │
│  │  ┌─────────────┐   ┌─────────────┐   ┌─────────────┐          │  │
│  │  │  Sandbox    │   │  Sandbox    │   │  Sandbox    │          │  │
│  │  │  Pod 1      │   │  Pod 2      │   │  Pod 3      │  ...     │  │
│  │  │             │   │             │   │             │          │  │
│  │  └─────────────┘   └─────────────┘   └─────────────┘          │  │
│  │                                                               │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Docker Compatibility

For non-Kubernetes environments, LLMSafeSpace supports deployment via Docker Compose:

- API service runs as a container with access to Docker socket
- Sandboxes are created as Docker containers instead of Kubernetes pods
- Supporting services (PostgreSQL, Redis) run as containers
- Limited security features compared to Kubernetes deployment

## Security Model Overview

LLMSafeSpace implements a defense-in-depth security approach with multiple layers of protection:

### 1. Container Isolation

- **Kernel Isolation**:
  - Optional gVisor runtime for enhanced system call filtering
  - Restrictive seccomp profiles tailored to language runtimes
  - Limited Linux capabilities and dropped privileges

- **Resource Isolation**:
  - CPU and memory limits enforced at container level
  - Optional CPU pinning for sensitive workloads
  - Storage quotas to prevent disk space abuse
  - Warm pool resource management for efficient utilization

- **Network Isolation**:
  - Default-deny network policies
  - Configurable egress filtering by domain
  - No ingress traffic to sandboxes by default
  - Optional service mesh integration for mTLS

### 2. Filesystem Security

- Read-only root filesystem
- Limited writable paths (/tmp, /workspace)
- Ephemeral storage by default
- Optional persistent storage with quotas

### 3. User Isolation

- Non-root execution for all code
- User namespaces for additional isolation
- No privilege escalation allowed
- No host mounts or sensitive paths

### 4. Authentication and Authorization

- API key authentication for SDK access
- Role-based access control for API endpoints
- Resource ownership validation
- Sandbox-specific access tokens

### 5. Monitoring and Auditing

- Comprehensive audit logging of all operations
- Resource usage monitoring and alerting
- Security event detection and notification
- System call auditing for suspicious activity

### Security Levels

LLMSafeSpace provides predefined security configurations:

- **Standard**: Balanced security and performance
- **High**: Enhanced security with gVisor and stricter policies
- **Custom**: User-defined security settings

## Data Flow

1. **Code Execution Flow**:
   - SDK sends code to API service
   - API service validates and forwards to appropriate sandbox
   - Sandbox executes code in isolated environment
   - Results are returned to API service
   - API service streams results back to SDK

2. **File Operation Flow**:
   - SDK uploads file to API service
   - API service validates file and stores temporarily
   - File is transferred to sandbox via volume mount or copy
   - File operations occur within sandbox
   - Results/modified files can be downloaded via API service

3. **Package Installation Flow**:
   - SDK sends package installation request
   - API service validates package names and versions
   - Installation command is executed in sandbox
   - Network policies allow access to package repositories
   - Installation results are returned to SDK

4. **Warm Pool Flow**:
   - Sandbox Controller maintains pools of pre-initialized pods
   - When SDK requests a sandbox, API service checks for matching warm pods
   - If available, warm pod is assigned to the sandbox
   - Sandbox Controller adopts the warm pod and configures it for the specific sandbox
   - When sandbox is terminated, pod may be recycled back to warm pool if appropriate

## Conclusion

LLMSafeSpace's architecture provides a robust, scalable, and secure platform for LLM agent code execution. By leveraging Kubernetes native concepts and implementing multiple layers of security, it achieves strong isolation while maintaining flexibility and ease of use. The warm pool functionality significantly improves startup performance for common runtime environments, making the platform more responsive for interactive use cases. The modular design allows for future enhancements in areas like persistent storage, inter-sandbox communication, and specialized ML runtimes.
