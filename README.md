# LLMSafeSpace: Self-Hosted LLM Agent Execution Platform

A Kubernetes-first platform for secure code execution focused on LLM agents, with simplified architecture and easy maintenance.

This repo is located at github.com/lenaxia/llmsafespace

## Architecture Overview

LLMSafeSpace provides a secure, isolated environment for executing code from LLM agents with a focus on security, simplicity, and Kubernetes integration.

### Core Components

#### `agent-api`
- Entry point for all SDK interactions
- Handles authentication, RBAC, and request validation
- Manages sandbox lifecycle (create, connect, terminate)
- Exposes REST API and WebSocket endpoints
- Integrates with Kubernetes API for sandbox orchestration
- Coordinates warm pool usage for faster sandbox creation

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

```
.
├── api/                     # API service for SDK interactions
│   ├── cmd/                 # Command-line entrypoints
│   │   └── api/             # API server entrypoint
│   │       └── main.go
│   ├── config/              # Configuration files
│   │   └── config.yaml
│   ├── internal/            # Internal implementation
│   │   ├── app/             # Application bootstrap
│   │   │   └── app.go
│   │   ├── config/          # Configuration handling
│   │   │   ├── config.go
│   │   │   └── config_test.go
│   │   ├── docs/            # API documentation
│   │   │   └── swagger.go
│   │   ├── errors/          # Error definitions
│   │   │   └── errors.go
│   │   ├── interfaces/      # Service interfaces
│   │   │   └── interfaces.go
│   │   ├── logger/          # Logging implementation
│   │   │   ├── logger.go
│   │   │   └── logger_test.go
│   │   ├── middleware/      # HTTP middleware components
│   │   │   ├── auth.go      # Authentication middleware
│   │   │   ├── cors.go      # CORS handling
│   │   │   ├── error_handler.go
│   │   │   ├── logging.go   # Request logging
│   │   │   ├── metrics.go   # Prometheus metrics
│   │   │   ├── rate_limit.go
│   │   │   ├── recovery.go  # Panic recovery
│   │   │   ├── request_id.go
│   │   │   ├── security.go  # Security headers
│   │   │   ├── tracing.go   # Distributed tracing
│   │   │   └── validation.go
│   │   ├── mocks/           # Service mocks for testing
│   │   │   ├── database.go
│   │   │   ├── execution.go
│   │   │   ├── file.go
│   │   │   ├── metrics.go
│   │   │   ├── session.go
│   │   │   └── warmpool.go
│   │   ├── server/          # HTTP server implementation
│   │   │   └── router.go    # API route definitions
│   │   ├── services/        # Core business logic
│   │   │   ├── auth/        # Authentication service
│   │   │   │   ├── auth.go
│   │   │   │   └── auth_test.go
│   │   │   ├── cache/       # Redis cache service
│   │   │   │   ├── cache.go
│   │   │   │   └── cache_test.go
│   │   │   ├── database/    # Database access
│   │   │   │   ├── database.go
│   │   │   │   └── database_test.go
│   │   │   ├── execution/   # Code execution service
│   │   │   │   ├── execution.go
│   │   │   │   └── execution_test.go
│   │   │   ├── file/        # File operations service
│   │   │   │   ├── file.go
│   │   │   │   ├── file_test.go
│   │   │   │   └── mock_kubernetes.go
│   │   │   ├── kubernetes/  # K8s client wrapper
│   │   │   │   └── kubernetes.go
│   │   │   ├── metrics/     # Metrics collection
│   │   │   │   └── metrics.go
│   │   │   ├── sandbox/     # Sandbox management
│   │   │   │   ├── DESIGN.md
│   │   │   │   └── tests/
│   │   │   │       └── sandbox_tests.go
│   │   │   ├── services.go  # Service initialization
│   │   │   ├── services_test.go
│   │   │   └── warmpool/    # Warm pool integration
│   │   │       └── warmpool_service.go
│   │   └── validation/      # Request validation
│   │       ├── sandbox.go
│   │       ├── validation.go
│   │       └── warmpool.go
│   ├── migrations/          # Database migrations
│   │   ├── 000001_initial_schema.down.sql
│   │   ├── 000001_initial_schema.up.sql
│   │   ├── 001_initial_schema_rollback.sql
│   │   └── 001_initial_schema.sql
│   └── scripts/             # Operational scripts
│       ├── health-check.sh
│       ├── init-db.sh
│       └── migrate.sh
├── controller/              # Kubernetes operator
│   ├── bin/                 # Build artifacts
│   │   └── manager
│   ├── config/              # Operator configuration
│   │   └── manager/
│   │       └── manager.yaml
│   ├── examples/            # Example CRD manifests
│   │   ├── runtimeenvironment.yaml
│   │   ├── sandboxprofile.yaml
│   │   ├── sandbox.yaml
│   │   ├── test-sandbox.yaml
│   │   ├── test-warmpool.yaml
│   │   └── warmpool.yaml
│   ├── internal/            # Controller implementation
│   │   ├── common/          # Shared utilities
│   │   │   ├── condition_adapter.go
│   │   │   ├── constants.go
│   │   │   ├── leader_election.go
│   │   │   ├── metrics.go
│   │   │   ├── network_policy_manager.go
│   │   │   ├── pod_manager.go
│   │   │   ├── service_manager.go
│   │   │   └── utils.go
│   │   ├── controller/      # Main controller logic
│   │   │   ├── controller.go
│   │   │   └── setup.go
│   │   ├── metrics/         # Controller metrics
│   │   │   └── metrics.go
│   │   ├── resources/       # CRD type definitions
│   │   │   ├── register.go
│   │   │   ├── runtimeenvironment_deepcopy.go
│   │   │   ├── runtimeenvironment_types.go
│   │   │   ├── runtimeenvironment_webhook.go
│   │   │   ├── sandbox_deepcopy.go
│   │   │   ├── sandboxprofile_deepcopy.go
│   │   │   ├── sandboxprofile_types.go
│   │   │   ├── sandboxprofile_webhook.go
│   │   │   ├── sandbox_types.go
│   │   │   ├── sandbox_webhook.go
│   │   │   ├── warmpod_deepcopy.go
│   │   │   ├── warmpod_types.go
│   │   │   ├── warmpod_webhook.go
│   │   │   ├── warmpool_deepcopy.go
│   │   │   ├── warmpool_types.go
│   │   │   └── warmpool_webhook.go
│   │   ├── sandbox/         # Sandbox reconciler
│   │   │   └── controller.go
│   │   ├── warmpod/         # WarmPod reconciler
│   │   │   └── controller.go
│   │   └── warmpool/        # WarmPool reconciler
│   │       └── controller.go
│   ├── scripts/             # Controller scripts
│   │   ├── install-crds.sh
│   │   └── test-controller.sh
│   └── main.go              # Controller entrypoint
├── mocks/                   # Mock implementations
│   ├── kubernetes/          # K8s client mocks
│   │   ├── kubernetes_client.go
│   │   ├── llmsafespace_v1.go
│   │   ├── runtimeenvironment.go
│   │   ├── sandbox.go
│   │   ├── sandboxprofile.go
│   │   ├── warmpod.go
│   │   ├── warmpool.go
│   │   └── watch.go
│   ├── logger/              # Logger mocks
│   │   └── logger.go
│   └── types/               # Type mocks
│       ├── session.go
│       └── wsconnection.go
└── pkg/                     # Shared packages
    ├── config/              # Configuration types
    │   └── kubernetes_config.go
    ├── crds/                # CRD YAML definitions
    │   ├── runtimeenvironment_crd.yaml
    │   ├── sandbox_crd.yaml
    │   ├── sandboxprofile_crd.yaml
    │   ├── warmpod_crd.yaml
    │   └── warmpool_crd.yaml
    ├── interfaces/          # Common interfaces
    │   ├── kubernetes.go
    │   └── logger.go
    ├── kubernetes/          # K8s client utilities
    │   ├── client_crds.go
    │   ├── client.go
    │   ├── client_test.go
    │   ├── informers.go
    │   ├── kubernetes_operations.go
    │   └── tests/           # K8s client tests
    │       ├── client_crds_test.go
    │       ├── client_test.go
    │       ├── informers_test.go
    │       ├── kubernetes_operations_test.go
    │       ├── main_test.go
    │       ├── mocks_test.go
    │       ├── run_tests.sh
    │       └── test_helpers.go
    ├── logger/              # Logger implementation
    │   ├── logger.go
    │   └── mock_test.go
    └── types/               # Domain types
        ├── deepcopy.go
        ├── doc.go
        └── types.go
```

## SDK Usage

```python
from llmsafespace import Sandbox

# Create a new sandbox with Python runtime
sandbox = Sandbox(
    runtime="python:3.10",
    api_key="your_api_key",
    timeout=300,  # seconds
    use_warm_pool=True  # Use pre-initialized environments for faster startup
)

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

LLMSafeSpace uses custom resources to manage sandboxes and warm pools:

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

1. **Pod Security**:
   - Non-root user execution
   - Read-only file system where possible
   - Limited capabilities
   - Resource quotas and limits

2. **Network Security**:
   - Network policies to restrict pod communication
   - Egress filtering for external access
   - Internal service mesh for secure communication

3. **RBAC Integration**:
   - Kubernetes RBAC for API access
   - User-based access controls for sandboxes
   - Namespace isolation for multi-tenancy

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
