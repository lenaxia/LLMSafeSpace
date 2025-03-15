# Architecture Overview: LLMSafeSpace

This repo is located at github.com/lenaxia/llmsafespace

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

3. **Execution Runtime (`execution-runtime`)**
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

## Package Structure and Dependencies

### Core Package Relationships

```
api/
├── internal/
│   ├── services/
│   │   ├── auth          → pkg/types, pkg/logger
│   │   ├── execution     → pkg/kubernetes, pkg/config
│   │   └── warmpool      → pkg/types, mocks/kubernetes
│   ├── middleware/       → internal/logger, internal/errors
│   └── validation/       → pkg/types, internal/errors
│
controller/
├── internal/
│   ├── resources/        → pkg/types (CRDs)
│   ├── sandbox/          → pkg/kubernetes, pkg/logger
│   ├── warmpod/          → pkg/kubernetes, internal/common
│   └── warmpool/         → pkg/kubernetes, internal/common
│
pkg/
├── kubernetes/           # Shared client logic
│   ├── → controller/internal/resources
│   └── → api/internal/services
├── logger/               # Shared logging implementation
│   ├── → controller
│   └── → api
└── types/                # Cross-component types
    ├── → controller
    └── → api/validation
```

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

## Controller Architecture

### Reconciler Implementation Pattern

```go
// Example SandboxReconciler in controller/internal/sandbox
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch Sandbox instance
    sandbox := &resources.Sandbox{}
    if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. Handle deletion
    if !sandbox.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, sandbox)
    }
    
    // 3. Validate spec
    if err := validateSandboxSpec(sandbox.Spec); err != nil {
        return r.handleValidationError(ctx, sandbox, err)
    }
    
    // 4. Coordinate with WarmPool
    if sandbox.Spec.UseWarmPool {
        return r.handleWarmPoolAssignment(ctx, sandbox)
    }
    
    // 5. Regular reconciliation flow
    return r.standardReconcile(ctx, sandbox)
}
```

### Resource Manager Pattern

```go
// Example PodManager in controller/internal/common
type PodManager struct {
    client    client.Client
    scheme    *runtime.Scheme
    recorder  record.EventRecorder
    logger    logr.Logger
}

func (m *PodManager) CreatePodForSandbox(ctx context.Context, sandbox *resources.Sandbox) (*corev1.Pod, error) {
    // 1. Generate pod specification
    pod, err := m.generatePodSpec(sandbox)
    if err != nil {
        return nil, err
    }
    
    // 2. Set owner reference
    if err := ctrl.SetControllerReference(sandbox, pod, m.scheme); err != nil {
        return nil, err
    }
    
    // 3. Create pod
    if err := m.client.Create(ctx, pod); err != nil {
        return nil, err
    }
    
    // 4. Record event
    m.recorder.Event(sandbox, corev1.EventTypeNormal, "PodCreated", 
        fmt.Sprintf("Created pod %s for sandbox", pod.Name))
    
    return pod, nil
}
```

## API Service Architecture

### Middleware Chain

```go
// Example router setup in api/internal/server/router.go
func NewRouter(services *services.Services, logger logger.Logger) *chi.Mux {
    r := chi.NewRouter()
    
    // Global middleware
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Logger(logger))
    r.Use(middleware.Recoverer)
    r.Use(middleware.Timeout(30 * time.Second))
    r.Use(middleware.SecurityHeaders)
    r.Use(middleware.CORS)
    
    // API routes
    r.Route("/api/v1", func(r chi.Router) {
        // Public routes
        r.Group(func(r chi.Router) {
            r.Post("/auth/login", handlers.Login(services.Auth))
            r.Post("/auth/register", handlers.Register(services.Auth))
        })
        
        // Protected routes
        r.Group(func(r chi.Router) {
            r.Use(middleware.Authenticate(services.Auth))
            
            // Sandbox routes
            r.Route("/sandboxes", func(r chi.Router) {
                r.Post("/", handlers.CreateSandbox(services.Sandbox))
                r.Get("/", handlers.ListSandboxes(services.Sandbox))
                r.Route("/{id}", func(r chi.Router) {
                    r.Get("/", handlers.GetSandbox(services.Sandbox))
                    r.Delete("/", handlers.DeleteSandbox(services.Sandbox))
                    r.Post("/execute", handlers.ExecuteCode(services.Sandbox))
                    r.Post("/upload", handlers.UploadFile(services.Sandbox))
                    r.Get("/files/{path}", handlers.GetFile(services.Sandbox))
                })
            })
            
            // WarmPool routes
            r.Route("/warmpools", func(r chi.Router) {
                r.Get("/", handlers.ListWarmPools(services.WarmPool))
                r.Post("/", handlers.CreateWarmPool(services.WarmPool))
                // Additional routes...
            })
        })
    })
    
    // Swagger documentation
    r.Get("/swagger/*", httpSwagger.Handler())
    
    return r
}
```

### Service Implementation Pattern

```go
// Example SandboxService in api/internal/services/sandbox
type Service struct {
    logger     logger.Logger
    k8sClient  kubernetes.Interface
    fileService file.ServiceInterface
    execService execution.ServiceInterface
    warmPoolService warmpool.ServiceInterface
    db         database.Interface
    cache      cache.Interface
}

func (s *Service) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.Sandbox, error) {
    // 1. Validate request
    if err := validation.ValidateCreateSandboxRequest(req); err != nil {
        return nil, err
    }
    
    // 2. Check for warm pool availability if requested
    var warmPod *types.WarmPod
    if req.UseWarmPool {
        var err error
        warmPod, err = s.warmPoolService.GetAvailableWarmPod(ctx, req.Runtime)
        if err != nil {
            s.logger.Warn("Failed to get warm pod", "error", err)
            // Continue without warm pod
        }
    }
    
    // 3. Create sandbox resource
    sandbox := &types.Sandbox{
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: "sandbox-",
            Namespace:    s.config.SandboxNamespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "user": req.UserID,
            },
        },
        Spec: types.SandboxSpec{
            Runtime:       req.Runtime,
            Timeout:       req.Timeout,
            SecurityLevel: req.SecurityLevel,
            Resources:     req.Resources,
            UseWarmPool:   req.UseWarmPool,
        },
    }
    
    // 4. Apply warm pod reference if available
    if warmPod != nil {
        sandbox.Spec.WarmPodRef = &types.ProfileReference{
            Name:      warmPod.Name,
            Namespace: warmPod.Namespace,
        }
    }
    
    // 5. Create sandbox in Kubernetes
    if err := s.k8sClient.Create(ctx, sandbox); err != nil {
        return nil, fmt.Errorf("failed to create sandbox: %w", err)
    }
    
    // 6. Store metadata in database
    if err := s.db.CreateSandboxMetadata(ctx, &database.SandboxMetadata{
        ID:        sandbox.Name,
        UserID:    req.UserID,
        CreatedAt: time.Now(),
        Runtime:   req.Runtime,
    }); err != nil {
        s.logger.Error("Failed to store sandbox metadata", err, "sandboxID", sandbox.Name)
        // Continue despite database error
    }
    
    return sandbox, nil
}
```

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

### Security Implementation Examples

1. **API Layer Security**:
```go
// api/internal/middleware/security.go
func SecurityMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Content-Security-Policy", 
            "default-src 'self'; script-src 'none'")
        
        if r.TLS == nil && !strings.HasPrefix(r.Host, "localhost") {
            http.Error(w, "SSL required", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

2. **Pod Security Context**:
```go
// controller/internal/common/pod_manager.go
func (m *PodManager) generateSecurityContext(securityLevel string) *corev1.SecurityContext {
    sc := &corev1.SecurityContext{
        AllowPrivilegeEscalation: ptr.Bool(false),
        ReadOnlyRootFilesystem:   ptr.Bool(true),
        RunAsNonRoot:             ptr.Bool(true),
        RunAsUser:                ptr.Int64(1000),
        RunAsGroup:               ptr.Int64(1000),
        Capabilities: &corev1.Capabilities{
            Drop: []corev1.Capability{"ALL"},
        },
        SeccompProfile: &corev1.SeccompProfile{
            Type: corev1.SeccompProfileTypeRuntimeDefault,
        },
    }
    
    // Apply additional restrictions for high security level
    if securityLevel == "high" {
        sc.Capabilities.Add = nil
        // Additional hardening...
    }
    
    return sc
}
```

3. **Network Policy**:
```go
// controller/internal/common/network_policy_manager.go
func (m *NetworkPolicyManager) CreateNetworkPolicy(ctx context.Context, sandbox *resources.Sandbox) error {
    policy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-network-policy", sandbox.Name),
            Namespace: sandbox.Namespace,
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, resources.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    "sandbox": sandbox.Name,
                },
            },
            Ingress: []networkingv1.NetworkPolicyIngressRule{
                // Allow ingress only from API service if enabled
                {
                    From: []networkingv1.NetworkPolicyPeer{
                        {
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "app": "llmsafespace-api",
                                },
                            },
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "name": "llmsafespace",
                                },
                            },
                        },
                    },
                },
            },
            Egress: generateEgressRules(sandbox.Spec.NetworkAccess),
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
                networkingv1.PolicyTypeEgress,
            },
        },
    }
    
    return m.client.Create(ctx, policy)
}
```

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

## Implementation Recommendations

1. **API Service Implementation Order**:
   - Core infrastructure (logger, config, errors)
   - Middleware components (request ID, recovery, security)
   - Core services (database, cache, auth)
   - Domain services (file, execution, sandbox)
   - API layer (validation, handlers, router)
   - Application bootstrap (services manager, app)

2. **Controller Implementation Order**:
   - CRD definitions and validation
   - Common utilities (pod manager, network policy manager)
   - Individual reconcilers (sandbox, warmpool, warmpod)
   - Main controller setup and initialization
   - Metrics and monitoring

3. **Testing Strategy**:
   - Unit tests for all components
   - Integration tests for service interactions
   - End-to-end tests for complete workflows
   - Performance tests for warm pool optimization
   - Security tests for isolation boundaries

## Conclusion

LLMSafeSpace's architecture provides a robust, scalable, and secure platform for LLM agent code execution. By leveraging Kubernetes native concepts and implementing multiple layers of security, it achieves strong isolation while maintaining flexibility and ease of use. The warm pool functionality significantly improves startup performance for common runtime environments, making the platform more responsive for interactive use cases. The modular design allows for future enhancements in areas like persistent storage, inter-sandbox communication, and specialized ML runtimes.
