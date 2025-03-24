# LLMSafeSpace: Self-Hosted LLM Agent Execution Platform

A Kubernetes-first platform for secure code execution focused on LLM agents, with simplified architecture and easy maintenance.

This repo is located at github.com/lenaxia/llmsafespace

## Architecture Overview

LLMSafeSpace provides a secure, isolated environment for executing code from LLM agents with a focus on security, simplicity, and Kubernetes integration.

### Core Components

#### `agent-api`
- Entry point for all SDK interactions with layered architecture:
  - **Handlers**: HTTP endpoint controllers
  - **Services**: Core business logic (sandbox, metrics)
  - **Stores**: Database access layer
  - **Middleware**: Auth, logging, error handling
- Implements:
  - Structured error handling (`APIError` with codes/details)
  - Token extraction from headers/query params
  - Metrics collection (request counters, durations)
  - Configurable logging with Zap integration
- Features:
  - Sandbox lifecycle management
  - Warm pool coordination
  - Kubernetes API integration
  - WebSocket streaming support

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

1. **API Layer** (`api/`):
   - `internal/handler`: HTTP controllers
   - `internal/service`: Business logic
     - `sandbox`: Lifecycle management
     - `metrics`: Instrumentation
   - `internal/middleware`: Auth, logging
   - `internal/errors`: Structured errors
   - `internal/utilities`: Token handling

2. **Controller** (`controller/`):
   - `internal/resources`: CRD types
     - `sandbox`, `warmpool`, `runtimeenvironment`
   - `internal/common`: Shared utilities
     - Metrics, leader election

3. **Shared Packages** (`pkg/`):
   - `types`: Core type definitions
   - `interfaces`: Logger, services
   - `utilities`: Masking, helpers
   - `config`: Structured configuration

4. **Testing**:
   - `mocks/`: Generated mock implementations
   - Component tests alongside packages
   - Integration test suites

```
.
├── api/                              # API Service Component
│   ├── cmd/
│   │   └── server/                   # Main entrypoint
│   ├── config/                       # Configuration files
│   ├── internal/
│   │   ├── handler/                  # HTTP handlers
│   │   ├── k8s/                      # Kubernetes client wrappers
│   │   ├── middleware/               # HTTP middleware
│   │   ├── service/                  # Business logic
│   │   ├── store/                    # Database access
│   │   └── version/                  # Version info
│   └── pkg/
│       └── client/                   # Go client library
│
├── controller/                       # Kubernetes Operator
│   ├── cmd/
│   │   └── manager/                  # Operator entrypoint
│   ├── config/
│   │   ├── crd/                      # CRD patches
│   │   ├── rbac/                     # RBAC rules
│   │   └── webhook/                  # Webhook configs
│   ├── internal/
│   │   ├── controller/               # Reconciliation logic
│   │   ├── manager/                  # Manager setup
│   │   └── webhook/                  # Webhook handlers
│   └── pkg/
│       └── admission/                # Admission control
│
├── pkg/                              # Shared Packages
│   ├── apis/                         # API Type Definitions
│   │   └── llmsafespace/
│   │       ├── v1/                   # Stable API
│   │       └── v1alpha1/             # Experimental API
│   ├── client/                       # Generated Clients
│   ├── crds/                         # CRD Manifests
│   ├── kubernetes/                   # K8s Utilities
│   └── logger/                       # Logging
│
├── hack/                             # Development Tools
│   ├── boilerplate.go.txt            # License header
│   ├── update-codegen.sh             # Types generation
│   └── verify-codegen.sh             # CI verification
│
├── test/                             # Testing
│   ├── e2e/                          # End-to-End
│   └── integration/                  # Integration
│
├── Makefile                          # Build automation
├── go.mod                            # Go modules
└── go.sum                            # Go dependencies
```

### Key Directories Explained:

1. **API Service (`api/`)**:
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

## Kubernetes Integration

LLMSafeSpace uses a comprehensive CRD system:

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

4. **Observability**:
   - Secure logging with field masking
   - Metrics collection for security events
   - Audit trails for sandbox operations

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

## Docker Compatibility

For non-Kubernetes deployments, LLMSafeSpace can run as a Docker Compose stack:

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
- Layered config loading with `mapstructure` tags:
  ```go
  type Config struct {
    Server struct {
      Host string `mapstructure:"host"`
      Port int    `mapstructure:"port"`
    } `mapstructure:"server"`
  }
  ```
- Supports multiple sources (files, env vars, flags)

### Error Handling
- Structured error types with codes and details:
  ```go
  return &APIError{
    Type:    ErrInvalidInput,
    Code:    "invalid_request",
    Message: "Invalid sandbox parameters",
    Details: map[string]interface{}{"field": "runtime"},
  }
  ```

### Testing
- Mock implementations for all core interfaces:
  ```go
  type MockLogger struct {
    mock.Mock
  }
  func (m *MockLogger) Info(msg string, keysAndValues ...interface{}) {
    m.Called(msg, keysAndValues)
  }
  ```
- Table-driven tests for error cases
- Integration test suites

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

## Development Workflow

### Prerequisites
- Go 1.20+
- Docker and Docker Compose
- kubectl and a Kubernetes cluster (for full testing)

### Local Development

```bash
# Clone the repository
git clone https://github.com/lenaxia/llmsafespace.git
cd llmsafespace

# Install dependencies
go mod download

# Run tests
make test

# Build the API service
cd api
make build

# Run the API service locally
./llmsafespace
```

### Controller Development

```bash
# Build the controller
cd controller
make build

# Install CRDs in your cluster
make install-crds

# Run the controller locally (against your current kubeconfig)
./bin/manager
```

## Code Generation

When modifying API types (in `pkg/types`), you must regenerate the DeepCopy implementations:

```bash
# Install code generator tools
go install k8s.io/code-generator/cmd/deepcopy-gen@v0.26.0

# Run generation (from project root)
make deepcopy

# Verify and commit generated changes
git add pkg/types/zz_generated.deepcopy.go
git commit -m "Update generated code"
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

LLMSafeSpace is licensed under the Apache License 2.0. See [LICENSE](LICENSE) for the full license text.
