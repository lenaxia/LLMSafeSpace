# SecureAgent: Self-Hosted LLM Agent Execution Platform

A Kubernetes-first platform for secure code execution focused on LLM agents, with simplified architecture and easy maintenance.

## Architecture Overview

SecureAgent provides a secure, isolated environment for executing code from LLM agents with a focus on security, simplicity, and Kubernetes integration.

### Core Components

#### `agent-api`
- Entry point for all SDK interactions
- Handles authentication, RBAC, and request validation
- Manages sandbox lifecycle (create, connect, terminate)
- Exposes REST API and WebSocket endpoints
- Integrates with Kubernetes API for sandbox orchestration
- Coordinates warm pool usage for faster sandbox creation

#### `sandbox-controller`
- Kubernetes operator that manages sandbox resources
- Creates and manages pods for code execution
- Implements security policies and resource limits
- Handles template management and caching
- Manages warm pools for improved startup performance

#### `warm-pool-controller`
- Maintains pools of pre-initialized sandbox environments
- Ensures minimum number of warm pods are always available
- Handles recycling of used pods when appropriate
- Implements auto-scaling based on usage patterns
- Coordinates with Sandbox Controller for pod assignment

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
src/
├── api/                      # API service for SDK interactions
│   ├── Dockerfile
│   ├── main.go
│   └── internal/
│       ├── auth/             # Authentication and authorization
│       ├── handlers/         # API endpoint handlers
│       └── k8s/              # Kubernetes integration
├── controller/               # Kubernetes operator for sandbox management
│   ├── Dockerfile
│   ├── main.go
│   └── internal/
│       ├── controller/       # Custom resource controller
│       └── resources/        # CRD definitions
├── runtimes/                 # Execution environment images
│   ├── base/                 # Base image with common tools
│   ├── python/               # Python runtime
│   └── nodejs/               # Node.js runtime
├── sdk/                      # Client SDKs
│   ├── python/               # Python SDK
│   ├── js/                   # JavaScript/TypeScript SDK
│   └── go/                   # Go SDK
├── charts/                   # Helm charts for deployment
│   ├── llmsafespace/          # Main chart
│   └── templates/            # Kubernetes manifests
├── docker/                   # Docker Compose setup
│   └── docker-compose.yaml
├── docs/                     # Documentation
│   ├── api.md                # API reference
│   └── deployment.md         # Deployment guide
├── examples/                 # Example usage
│   ├── python/
│   └── javascript/
├── scripts/                  # Utility scripts
│   ├── build.sh
│   └── deploy.sh
├── Makefile                  # Build and deployment commands
├── go.mod                    # Go module definition
└── README.md                 # Project overview
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

SecureAgent uses custom resources to manage sandboxes and warm pools:

```yaml
# Sandbox resource
apiVersion: agent.example.com/v1
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
apiVersion: agent.example.com/v1
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
  autoScaling:
    enabled: true
    targetUtilization: 80
    scaleDownDelay: 300
status:
  availablePods: 3
  assignedPods: 2
  pendingPods: 0
  lastScaleTime: "2023-07-01T10:00:00Z"
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

For non-Kubernetes deployments, SecureAgent can run as a Docker Compose stack:

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

## Areas for Improvement

### 1. Persistent Storage Options

Current sandboxes are ephemeral by default, which is appropriate for most LLM agent use cases. However, some agents need to maintain state between sessions or store larger datasets.

**Planned Enhancements:**
- Optional persistent volume claims for sandboxes
- Configurable storage retention policies
- Shared volumes between related sandboxes
- Integration with object storage for larger datasets

### 2. Warm Pool Optimizations

While the current warm pool implementation significantly improves startup times, there are several optimizations planned:

**Planned Enhancements:**
- Predictive scaling based on usage patterns
- More sophisticated pod recycling strategies
- Custom image preloading for specific use cases
- Warm pool metrics and analytics dashboard
- Multi-region warm pool distribution

### 3. Inter-Sandbox Communication

For complex multi-agent systems, enabling secure communication between sandboxes would allow for collaborative problem-solving.

**Planned Enhancements:**
- Secure message passing between sandboxes
- Shared memory spaces with access controls
- Event-based communication patterns
- Agent-to-agent authentication

### 3. Specialized ML Runtime

While the current runtimes support ML libraries, dedicated ML environments would improve performance for specialized tasks.

**Planned Enhancements:**
- Pre-configured environments with popular ML frameworks
- GPU support for accelerated computation
- Optimized containers for specific ML workloads
- Cached model repositories

### 4. Agent-Specific Monitoring

Current monitoring is comprehensive but could be enhanced with LLM agent-specific metrics and visualizations.

**Planned Enhancements:**
- Agent behavior analytics dashboard
- Code quality metrics for generated code
- Pattern recognition for problematic code
- Integration with LLM observability tools
- Execution path visualization

### 5. Feedback Mechanisms

Adding mechanisms to provide execution feedback to the LLM would help improve code generation over time.

**Planned Enhancements:**
- Structured execution feedback format
- Integration with LLM fine-tuning pipelines
- Automated code quality assessment
- Performance benchmarking for generated code
- Error categorization for better prompting

## Getting Started

### Prerequisites
- Kubernetes cluster (v1.20+)
- Helm (v3.0+)
- kubectl

### Installation

```bash
# Add the SecureAgent Helm repository
helm repo add llmsafespace https://charts.llmsafespace.dev

# Install SecureAgent
helm install llmsafespace llmsafespace/llmsafespace \
  --namespace llmsafespace \
  --create-namespace \
  --set apiKey.create=true
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

SecureAgent is licensed under the Apache License 2.0. See [LICENSE](LICENSE) for the full license text.
