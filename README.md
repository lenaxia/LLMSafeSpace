# LLMSafeSpace: Self-Hosted LLM Agent Execution Platform

A Kubernetes-first platform for secure code execution focused on LLM agents, with simplified architecture and easy maintenance.

This repo is located at github.com/lenaxia/llmsafespace

## Architecture Overview

[... rest of existing architecture content ...]

## Project Structure

[... rest of existing project structure content ...]

## Getting Started

### Prerequisites
- Kubernetes cluster (v1.20+)
- Helm (v3.0+)
- kubectl

### Code Generation

The project uses Kubernetes code generation for creating DeepCopy methods. To update the generated code:

1. Install the code-generator tools:
```bash
go get k8s.io/code-generator@v0.26.0
```

2. Run the generation script:
```bash
make deepcopy
```

This will generate/update the `zz_generated.deepcopy.go` files in the `src/api/internal/types` package.

### Installation

```bash
# Add the LLMSafeSpace Helm repository
helm repo add llmsafespace https://charts.llmsafespace.dev

# Install LLMSafeSpace
helm install llmsafespace llmsafespace/llmsafespace \
  --namespace llmsafespace \
  --create-namespace \
  --set apiKey.create=true
```

[... rest of existing README content ...]
