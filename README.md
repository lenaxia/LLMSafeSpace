# LLMSafeSpace: Self-Hosted LLM Agent Execution Platform

A Kubernetes-first platform for secure code execution focused on LLM agents, with simplified architecture and easy maintenance.

This repo is located at github.com/lenaxia/llmsafespace

## Architecture Overview

LLMSafeSpace provides a secure, isolated environment for executing code from LLM agents with a focus on security, simplicity, and Kubernetes integration. Now with full OLM (Operator Lifecycle Manager) compatibility for enterprise-grade operator management.

### Core Components

#### `olm-operator` (New)
- Manages operator lifecycle through OLM
- Handles version upgrades and rollbacks
- Provides catalog management for multiple channels
- Implements conversion webhooks for API versions
- Manages operator dependencies and related images

#### `agent-api`
- (Updated) Entry point for all SDK interactions with layered architecture:
  - **HTTP Handlers**: Endpoint controllers (`api/internal/handler/`)
  - **Services**: Business logic (`api/internal/service/`)
  - **K8s Client**: Kubernetes wrappers (`api/internal/k8s/`)
  - **Middleware**: Auth and logging (`api/internal/middleware/`)
  - **Store**: Database access (`api/internal/store/`)
- Key Features:
  - Sandbox lifecycle management
  - Warm pool coordination
  - Kubernetes API integration
  - Versioned API endpoints (`api/internal/version/`)
  - Generated client library (`api/pkg/client/`)

#### `controller`
- Unified Kubernetes operator that manages all custom resources
- Creates and manages sandbox pods with integrated warm pool support
- Implements security policies and resource limits
- Handles template management and caching
- Maintains pools of pre-initialized sandbox environments
- Ensures minimum number of warm pods are always available
- Handles recycling of used pods when appropriate
- Implements auto-scaling based on usage patterns
- Provides seamless coordination between sandbox creation and warm pod allocation

#### `execution-runtime`
- Container image for code execution environments
- Supports multiple language runtimes (Python, JavaScript, etc.)
- Includes pre-installed tools and libraries for LLM agents
- Implements secure execution boundaries

### Supporting Services

- **PostgreSQL**: Stores user data, API keys, and sandbox metadata
- **Redis**: Session management and caching

## Project Structure

### Key Architectural Layers

1. **API Service** (`api/`):
   - `cmd/server/`: Main entrypoint
   - `config/`: Configuration files
   - `internal/`:
     - `handler/`: HTTP request handlers
     - `k8s/`: Kubernetes client wrappers  
     - `middleware/`: HTTP middleware
     - `service/`: Business logic services
     - `store/`: Database access layer
     - `version/`: Version information
   - `pkg/client/`: Generated Go client library

2. **Controller** (`controller/`):
   - `cmd/manager/`: Operator entrypoint
   - `config/`:
     - `crd/`: Custom Resource Definitions
     - `rbac/`: RBAC rules
     - `webhook/`: Webhook configurations
   - `internal/`:
     - `controller/`: Reconciliation logic
     - `manager/`: Manager setup  
     - `webhook/`: Webhook handlers
   - `pkg/admission/`: Admission control

3. **Shared Packages** (`pkg/`):
   - `apis/llmsafespace/`: API Type Definitions (v1, v1alpha1)
   - `client/`: Generated Kubernetes clients
   - `crds/`: CRD manifests
   - `kubernetes/`: K8s utilities
   - `logger/`: Logging implementation

4. **Testing**:
   - `test/`:
     - `mocks/`: Shared mock implementations
       - `interfaces/`: Mock versions of core interfaces
       - `resources/`: Mock CRD resources
     - `e2e/`: End-to-end tests
     - `integration/`: Integration tests
   - Component tests live alongside implementations (`*_test.go`)
   - Mock usage examples in `test/mocks/README.md`

```
.
├── api/                              # API Service Component
│   ├── cmd/server/                   # REST API entrypoint
│   ├── internal/                     # Business logic
│   └── pkg/client/                   # Generated SDK
│
├── controller/                       # Operator Core
│   ├── cmd/manager/                  # Operator main
│   ├── config/
│   │   ├── crd/                      # CRD bases
│   │   └── rbac/                     # RBAC rules  
│   └── internal/controller/          # Reconciliation
│
├── bundle/                           # OLM Bundle Artifacts
│   ├── manifests/                    # Generated CSVs/CRDs
│   │   ├── *.clusterserviceversion.yaml
│   │   └── *.crd.yaml
│   ├── metadata/annotations.yaml     # Bundle metadata
│   └── tests/scorecard/              # OLM test configs
│
├── olm/                              # Catalog Management
│   ├── catalog/
│   │   ├── stable/                   # Production channel
│   │   ├── alpha/                    # Pre-release channel  
│   │   └── index.yaml                # Catalog index
│   └── versioned/
│       ├── v0.1.0/                   # Versioned bundles
│       └── v0.2.0/
│
├── pkg/                              # Shared Libraries
│   ├── apis/llmsafespace/            # API Types
│   ├── client/                       # Generated clients
│   └── kubernetes/                   # K8s utilities
│
├── hack/
│   ├── update-bundle.sh              # Bundle generation
│   └── verify-olm.sh                 # OLM validation
│
├── test/
│   ├── e2e/                          # End-to-end tests
│   └── olm/                          # OLM-specific tests
│       ├── upgrade/                  # Version upgrade tests
│       └── bundle/                   # Bundle validation
│
├── Makefile                          # Now includes:
│   ├── bundle-generate               # Generate OLM bundle
│   ├── catalog-build                 # Build catalog index
│   └── olm-install                   # Test installation
│
└── go.mod                            # Updated with:
    ├── operator-framework/api        # OLM dependencies
    └── operator-lib                  # Helper utilities
```

### Key Directories Explained:

1. **OLM Bundle (`bundle/`)**:
   - `manifests/`: ClusterServiceVersion (CSV) and CRDs
   - `metadata/`: Bundle annotations and labels
   - `tests/scorecard/`: OLM validation tests

2. **Catalog Management (`olm/`)**:
   - `catalog/`: Channel definitions (stable/alpha)
   - `versioned/`: Immutable release bundles
   - `overlays/`: Environment-specific customizations

3. **API Service (`api/`)** (Updated):
   - `cmd/server`: Main API server executable
   - `internal/handler`: HTTP request handlers
   - `internal/service`: Core business logic
   - `pkg/client`: Generated client library for external consumers

2. **Controller (`controller/`)**:
   - Follows standard kubebuilder/operator-sdk layout
   - `internal/controller`: Resource reconciliation logic
   - `config/rbac`: Generated RBAC manifests
   - `config/webhook`: Webhook configurations

3. **Shared Packages (`pkg/`)**:
   - `apis/`: CRD type definitions (single source of truth)
   - `client/`: Generated Kubernetes clientsets
   - `crds/`: Generated CRD YAML manifests
   - `kubernetes/`: Shared Kubernetes utilities

4. **Development (`hack/`)**:
   - Code generation scripts
   - Verification tools
   - Maintainer utilities

5. **Testing (`test/`)**:
   - `e2e/`: Full system tests
   - `integration/`: Component integration tests
   - Unit tests live alongside the code they test

This structure follows:
- Kubernetes operator best practices
- Standard Go project layout
- Clean architecture principles
- Production-grade organization patterns

## SDK Usage

```python
from llmsafespace import Sandbox, APIError

try:
    # Create sandbox with full configuration
    sandbox = Sandbox(
        runtime="python:3.10",
        api_key="your_api_key",
        timeout=300,
        security_level="high",
        resources={
            "cpu": "1",
            "memory": "1Gi"
        },
        use_warm_pool=True
    )

    # Execute with error handling
    result = sandbox.run_code("""
    import os
    print(os.environ.get('HOSTNAME'))
    """)
    
    print(f"Output: {result.stdout}")
    print(f"Metrics: {sandbox.metrics()}")

except APIError as e:
    print(f"Error {e.code}: {e.message}")
    if e.details:
        print("Details:", e.details)

# Execute code
result = sandbox.run_code("""
import numpy as np
data = np.random.rand(10, 10)
mean = data.mean()
print(f"Mean value: {mean}")
""")

print(result.stdout)  # Access stdout output

# Install packages
sandbox.install("pandas matplotlib")

# Upload files
sandbox.upload_file("data.csv", local_path="./data.csv")

# Execute commands
result = sandbox.run_command("ls -la")

# Stream output in real-time
for output in sandbox.stream_command("python long_task.py"):
    print(output)

# Clean up
sandbox.terminate()
```

## Kubernetes Integration (OLM Enhanced)

LLMSafeSpace uses a comprehensive CRD system with OLM lifecycle management:

### OLM Deployment Example
```yaml
# ClusterServiceVersion (CSV) excerpt
apiVersion: operators.coreos.com/v1alpha1
kind: ClusterServiceVersion
metadata:
  name: llmsafespace-operator.v0.1.0
spec:
  displayName: LLMSafeSpace Operator
  installModes:
    - type: OwnNamespace
      supported: true
  install:
    spec:
      deployments:
      - name: llmsafespace-controller
        spec:
          template:
            spec:
              containers:
              - name: manager
                image: quay.io/llmsafespace/controller:v0.1.0
                args:
                - --leader-elect
                - --operator-namespace=$(OPERATOR_NAMESPACE)
                env:
                - name: OPERATOR_NAMESPACE
                  valueFrom:
                    fieldRef:
                      fieldPath: metadata.namespace
```

### Package Manifest
```yaml
apiVersion: packages.operators.coreos.com/v1
kind: PackageManifest
metadata:
  name: llmsafespace-operator
spec:
  packageName: llmsafespace-operator
  channels:
  - name: stable
    currentCSV: llmsafespace-operator.v0.1.0
  - name: alpha
    currentCSV: llmsafespace-operator.v0.2.0-alpha
  defaultChannel: stable
```

### Core Resources
1. **Runtime Environments**:
   ```yaml
   apiVersion: llmsafespace.dev/v1
   kind: RuntimeEnvironment
   spec:
     image: python:3.10
     language: python
     version: "3.10"
     resources:
       minCpu: "500m"
       minMemory: "512Mi"
   ```

2. **Sandbox Profiles** (security templates):
   ```yaml
   kind: SandboxProfile
   spec:
     language: python
     securityLevel: high
     networkRules:
       - domain: "*.pypi.org"
         ports: [{port: 443, protocol: TCP}]
   ```

3. **Sandboxes** (user instances):
   ```yaml
   kind: Sandbox
   spec:
     runtime: python:3.10
     profileRef:
       name: python-high-security
     resources:
       cpu: "1"
       memory: "1Gi"
   status:
     conditions:
     - type: Ready
       status: "True"
       lastTransitionTime: "2023-07-01T10:00:00Z"
   ```

4. **Warm Pool System**:

```yaml
# Sandbox resource
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: sandbox-12345
  namespace: agent-sandboxes
spec:
  runtime: python:3.10
  resources:
    cpu: "1"
    memory: "1Gi"
  timeout: 300
  user: "user-123"
  securityLevel: "standard"
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  useWarmPool: true
status:
  phase: Running
  startTime: "2023-07-01T10:00:00Z"
  endpoint: "sandbox-12345.agent-sandboxes.svc.cluster.local"
  warmPodRef:
    name: "python-pool-abc123"
    namespace: "warm-pools"
```

```yaml
# WarmPool resource
apiVersion: llmsafespace.dev/v1
kind: WarmPool
metadata:
  name: python-pool
  namespace: warm-pools
spec:
  runtime: python:3.10
  minSize: 5
  maxSize: 20
  securityLevel: standard
  preloadPackages:
    - numpy
    - pandas
  preloadScripts:
    - name: init-data
      content: |
        import numpy as np
        np.random.seed(42)
        print("Preloaded numpy with fixed seed")
  autoScaling:
    enabled: true
    targetUtilization: 80
status:
  availablePods: 3
  assignedPods: 2
  pendingPods: 0
  lastScaleTime: "2023-07-01T10:00:00Z"
  conditions:
    - type: Ready
      status: "True"
      reason: PoolReady
      message: "Warm pool is ready"
      lastTransitionTime: "2023-07-01T10:05:00Z"
```

```yaml
# WarmPod resource
apiVersion: llmsafespace.dev/v1
kind: WarmPod
metadata:
  name: python-pool-abc123
  namespace: warm-pools
  labels:
    app: llmsafespace
    component: warmpod
    pool: python-pool
spec:
  poolRef:
    name: python-pool
    namespace: warm-pools
  creationTimestamp: "2023-07-01T09:55:00Z"
  lastHeartbeat: "2023-07-01T10:00:00Z"
status:
  phase: Ready
  podName: python-pool-pod-abc123
  podNamespace: warm-pools
```

## Security Features

1. **Application Layer Security**:
   - Token-based authentication with extractor config:
     ```go
     type TokenExtractorConfig struct {
       HeaderName      string
       TokenType       string  
       QueryParamName  string
     }
     ```
   - Sensitive data masking for logs/output:
     ```go
     MaskString("secret-api-key") // Returns "secr...key"
     ```
   - Structured error handling without exposing internals:
     ```go
     type APIError struct {
       Type    ErrorType
       Code    string
       Message string
       Details map[string]interface{}
     }
     ```

2. **Pod Security**:
   - Non-root execution (RunAsUser: 1000)
   - Read-only root filesystem (with writable exceptions)
   - Seccomp profiles (RuntimeDefault or custom)
   - Resource limits with validation:
     ```go
     // +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
     Memory string `json:"memory,omitempty"`
     ```

3. **Network Security**:
   - Egress rules with domain/port validation:
     ```go
     type EgressRule struct {
       Domain string `json:"domain"`
       Ports  []PortRule `json:"ports,omitempty"`
     }
     ```
   - Network policy enforcement
   - Ingress traffic control (default deny)

4. **OLM Security**:
   - RBAC rules generated from operator needs
   - Pod security admission compliance
   - OpenShift Security Context Constraints (SCC)
   - Bundle signature verification
   - Catalog content trust policies

5. **Observability** (Enhanced):
   - OLM health status reporting
   - Operator metrics endpoint (:8080/metrics)
   - Bundle version tracking
   - Catalog update notifications

## LLM Agent Integration Scenarios

### Function Calling with Code Execution

```python
from langchain import OpenAI
from llmsafespace import Sandbox

# Initialize LLM
llm = OpenAI(temperature=0)

# Initialize sandbox
sandbox = Sandbox(runtime="python:3.10", timeout=30)

def execute_code(code):
    """Execute code in sandbox and return result"""
    result = sandbox.run_code(code)
    return {
        "output": result.stdout,
        "error": result.stderr,
        "success": result.exit_code == 0
    }

# LLM generates code
code = llm.generate("Write Python code to calculate the first 10 Fibonacci numbers")

# Execute in sandbox
result = execute_code(code)
print(result["output"])
```

### Interactive Agent with Streaming Output

```python
from langchain.agents import AgentExecutor, create_react_agent
from langchain.tools import Tool
from langchain.llms import OpenAI
from llmsafespace import Sandbox

# Initialize sandbox
sandbox = Sandbox(runtime="python:3.10", timeout=300)

# Tool for code execution
def run_python_code(code):
    outputs = []
    for output in sandbox.stream_code(code):
        print(output)  # Show real-time output
        outputs.append(output)
    return "".join(outputs)

# Create agent with tools
tools = [
    Tool(
        name="PythonREPL",
        func=run_python_code,
        description="Execute Python code in a secure sandbox"
    )
]

agent = create_react_agent(OpenAI(temperature=0), tools, "You are a helpful AI assistant.")
agent_executor = AgentExecutor(agent=agent, tools=tools, verbose=True)

# Run agent
agent_executor.run("Analyze this dataset and create a visualization")
```

## Alternative Deployment (Non-OLM)

For development/testing without Kubernetes, LLMSafeSpace can run as a Docker Compose stack:

```yaml
version: '3'
services:
  agent-api:
    image: llmsafespace/api:latest
    ports:
      - "8080:8080"
    environment:
      - SANDBOX_MODE=docker
      - MAX_CONCURRENT_SANDBOXES=5
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock

  postgres:
    image: postgres:14
    volumes:
      - postgres-data:/var/lib/postgresql/data

  redis:
    image: redis:7
    volumes:
      - redis-data:/data

volumes:
  postgres-data:
  redis-data:
```

## Development Patterns

### Configuration Management
- Structured config loading matching file tree:
  ```go
  // api/config/config.go
  type Config struct {
    Server struct {
      Host string `mapstructure:"host"`
      Port int    `mapstructure:"port"`
    } `mapstructure:"server"`
    
    Kubernetes k8sconfig.KubernetesConfig `mapstructure:"kubernetes"`
  }
  
  // pkg/config/kubernetes_config.go 
  type KubernetesConfig struct {
    Kubeconfig string `mapstructure:"kubeconfig"`
    Namespace  string `mapstructure:"namespace"`
  }
  ```

### Error Handling
- Structured errors matching `api/internal/errors/`:
  ```go
  // errors/errors.go
  type APIError struct {
    Type    ErrorType           `json:"-"`
    Code    string              `json:"code"`
    Message string              `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
    Err     error               `json:"-"`
  }
  
  // Usage example:
  return &errors.APIError{
    Code:    "invalid_input",
    Message: "Invalid sandbox spec",
    Details: map[string]interface{}{
      "field":   "runtime",
      "allowed": []string{"python:3.10", "nodejs:18"},
    },
  }
  ```

### Testing Patterns

#### Shared Mocks
Reusable mock implementations live in `test/mocks/`:

```go
// Example usage in api tests
import (
	"testing"
	"github.com/lenaxia/llmsafespace/test/mocks/interfaces"
)

func TestHandler(t *testing.T) {
	mockLogger := interfaces.NewMockLogger()
	mockLogger.On("Info", "starting", []interface{}{"component", "test"}).Return()
	
	// Test implementation...
}
```

#### Mock Types Available:
1. **Core Interfaces** (`test/mocks/interfaces/`):
   - `MockLogger` - Implements `LoggerInterface`
   - `MockKubernetesClient` - Implements `KubernetesClient`
   - `MockCacheService` - Implements `CacheService`

2. **Resources** (`test/mocks/resources/`):
   - `NewMockSandbox()` - Pre-configured Sandbox CRD
   - `NewMockWarmPool()` - WarmPool with test defaults

#### Best Practices:
- Use shared mocks for cross-component testing
- Extend mocks in local `*_test.go` files when needed
- Never import mocks in production code
- Keep mock behavior consistent across tests

### Mocking Examples
```go
// Controller test using shared mocks
func TestReconcile(t *testing.T) {
	// Initialize shared mocks
	mockClient := interfaces.NewMockKubernetesClient()
	mockLogger := interfaces.NewMockLogger()

	// Set expectations
	mockClient.On("GetSandbox", mock.Anything, "test-sandbox").
		Return(testmocks.NewMockSandbox(), nil)
	
	mockLogger.On("Info", "Reconciling", mock.Anything).Return()

	// Run test...
}

// API test with custom mock behavior
func TestAPIEndpoint(t *testing.T) {
	mockCache := interfaces.NewMockCacheService().
		WithGetBehavior(func(key string) (string, error) {
			if key == "special" {
				return "cached-value", nil 
			}
			return "", errors.New("not found")
		})
	
	// Test implementation...
}
```

### Logging
- Structured logging with Zap:
  ```go
  logger.Info("Request processed",
    "method", r.Method,
    "path", r.URL.Path,
    "duration", duration,
  )
  ```
- Field masking for sensitive data
- Component-scoped loggers

## Development Workflow (OLM Enhanced)

### New Prerequisites
- operator-sdk v1.28.0+
- opm v1.26.2+
- Kubernetes 1.25+ with OLM installed

### OLM-Specific Commands
```bash
# Generate OLM bundle
make bundle IMG=quay.io/llmsafespace/controller:v0.1.0

# Build catalog index
make catalog-build CATALOG_IMG=quay.io/llmsafespace/catalog:latest

# Deploy using OLM
make olm-deploy BUNDLE_IMG=quay.io/llmsafespace/bundle:v0.1.0

# Run scorecard tests
operator-sdk scorecard ./bundle
```

### Updated Prerequisites
- Go 1.20+
- Docker and Docker Compose
- kubectl and a Kubernetes cluster with OLM

### Local Development (OLM Environment)

```bash
# Ensure OLM is installed in your cluster
operator-sdk olm install

# Deploy the operator via OLM
make olm-deploy

# Verify operator installation
kubectl get subscriptions.operators.coreos.com -n llmsafespace-system

# Clone the repository
git clone https://github.com/lenaxia/llmsafespace.git
cd llmsafespace

# Install dependencies
go mod download

# Run tests
make test # Runs unit, integration and e2e tests

# Generate test coverage
make cover

# Update golden files in tests
make update-golden

# Run with specific mock implementations
go test -v ./... -mock-mode=strict

# Build the API service
cd api
make build

# Run the API service locally
./llmsafespace
```

### Controller Development (OLM-aware)

```bash
# Generate updated bundle after changes
make bundle

# Deploy updated operator
make olm-deploy

# Verify new version
kubectl get clusterserviceversions -n llmsafespace-system
cd controller
make build

# Install CRDs in your cluster
make install-crds

# Run the controller locally (against your current kubeconfig)
./bin/manager
```

## OLM Lifecycle Management

### Upgrade Strategy
```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: ClusterServiceVersion
metadata:
  name: llmsafespace-operator.v0.2.0
spec:
  replaces: llmsafespace-operator.v0.1.0
  links:
  - name: Upgrade Documentation
    url: https://llmsafespace.dev/docs/upgrade-0.2
```

### Catalog Management
```bash
# Add bundle to catalog
opm index add \
  --bundles quay.io/llmsafespace/bundle:v0.1.0 \
  --tag quay.io/llmsafespace/catalog:latest \
  --mode semver

# Prune old versions
opm index prune \
  --tag quay.io/llmsafespace/catalog:latest \
  --keep-replace 3
```

### Monitoring Operator Health
```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: llmsafespace-operator
spec:
  endpoints:
  - port: metrics
    interval: 30s
  selector:
    matchLabels:
      app.kubernetes.io/name: llmsafespace-operator
```

## Code & Bundle Generation

When modifying API types or OLM configurations:

1. Regenerate DeepCopy implementations:

```bash
# Install code generator tools
go install k8s.io/code-generator/cmd/deepcopy-gen@v0.26.0

# Run generation (from project root)
make deepcopy

2. Generate OLM bundle artifacts:
make bundle

3. Validate bundle format:
operator-sdk bundle validate ./bundle

# Verify and commit generated changes
git add pkg/types/zz_generated.deepcopy.go bundle/
git commit -m "Update generated code and OLM bundle"
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

LLMSafeSpace is licensed under the Apache License 2.0. See [LICENSE](LICENSE) for the full license text.
