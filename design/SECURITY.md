# LLMSafeSpace Security Model

This document outlines the security architecture and configuration options for LLMSafeSpace, a self-hosted platform for secure code execution focused on LLM agents.

## Security Architecture Overview

LLMSafeSpace is designed with a defense-in-depth approach to provide strong isolation for untrusted code execution while maintaining the simplicity of a container-based architecture. The platform leverages Kubernetes security features to create a robust security boundary around each sandbox.

## Container Security Features

### Kernel Isolation

#### gVisor Runtime

LLMSafeSpace supports the gVisor container runtime, which provides a user-space kernel that intercepts system calls, significantly reducing the attack surface:

```yaml
# Example sandbox with gVisor runtime
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: secure-sandbox
spec:
  runtime: python:3.10
  securityLevel: high  # Enables gVisor runtime
```

When `securityLevel: high` is specified, the sandbox controller automatically applies the gVisor runtime class to the pod.

#### Seccomp Profiles

LLMSafeSpace applies restrictive seccomp profiles to limit available system calls:

- **Default Profile**: A restrictive profile that blocks dangerous syscalls
- **Language-Specific Profiles**: Optimized profiles for Python, Node.js, etc.
- **Custom Profiles**: Support for user-defined seccomp profiles

```yaml
# Example custom seccomp profile configuration
apiVersion: llmsafespace.dev/v1
kind: SandboxProfile
metadata:
  name: custom-python
spec:
  language: python
  seccompProfile: profiles/python-restricted.json
```

### Resource Isolation

#### CPU and Memory Limits

All sandboxes have strict resource limits enforced:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: resource-limited-sandbox
spec:
  runtime: python:3.10
  resources:
    cpu: "1"
    memory: "1Gi"
    ephemeralStorage: "5Gi"
```

#### CPU Pinning

For high-security workloads, LLMSafeSpace supports CPU pinning to reduce side-channel risks:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: cpu-pinned-sandbox
spec:
  runtime: python:3.10
  resources:
    cpu: "2"
    cpuPinning: true  # Enables CPU pinning
```

### Network Isolation

#### Network Policies

LLMSafeSpace applies default-deny network policies with specific allowances:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: network-restricted-sandbox
spec:
  runtime: python:3.10
  networkAccess:
    egress:
      - domain: "pypi.org"
      - domain: "files.pythonhosted.org"
    ingress: false
```

#### Service Mesh Integration

For enterprise deployments, LLMSafeSpace integrates with service mesh solutions like Istio to provide:

- mTLS encryption between services
- Fine-grained access control
- Traffic monitoring and anomaly detection

### Filesystem Security

#### Read-Only Root Filesystem

All sandbox containers run with read-only root filesystems by default:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: filesystem-secure-sandbox
spec:
  runtime: python:3.10
  filesystem:
    readOnlyRoot: true
    writablePaths:
      - /tmp
      - /workspace
```

#### Ephemeral Storage

Sandbox storage is ephemeral by default, ensuring that data doesn't persist between sessions unless explicitly configured:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: persistent-sandbox
spec:
  runtime: python:3.10
  storage:
    persistent: true
    volumeSize: "10Gi"
```

### User Isolation

#### Non-Root Execution

All code executes as a non-root user with minimal privileges:

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: user-secure-sandbox
spec:
  runtime: python:3.10
  securityContext:
    runAsUser: 1000
    runAsGroup: 1000
```

#### User Namespaces

LLMSafeSpace leverages user namespaces for additional isolation between the container user and host user.

## Security Levels

LLMSafeSpace provides predefined security levels to simplify configuration:

- **Standard**: Balanced security and performance
- **High**: Enhanced security with gVisor and stricter policies
- **Custom**: User-defined security settings

```yaml
apiVersion: llmsafespace.dev/v1
kind: Sandbox
metadata:
  name: high-security-sandbox
spec:
  runtime: python:3.10
  securityLevel: high
```

## Monitoring and Auditing

### Security Monitoring

LLMSafeSpace includes comprehensive security monitoring:

- Runtime anomaly detection
- Resource usage monitoring
- Network traffic analysis
- System call auditing

### Audit Logging

All sandbox activities are logged for audit purposes:

- Creation and termination events
- Code execution
- File system access
- Network connections

## Comparison with VM-based Isolation

While LLMSafeSpace's container-based approach doesn't provide the same level of isolation as Firecracker VMs, the combination of security features significantly narrows the gap:

| Feature | LLMSafeSpace Containers | Firecracker VMs |
|---------|------------------------|-----------------|
| Kernel Isolation | Partial (with gVisor) | Complete |
| Memory Isolation | Strong | Stronger |
| CPU Isolation | Good (with pinning) | Better |
| Startup Time | Very Fast (ms) | Fast (sub-second) |
| Resource Efficiency | Higher | Lower |
| Implementation Complexity | Lower | Higher |
| Deployment Simplicity | Higher | Lower |

## Security Best Practices

### Recommended Configuration

For production deployments, we recommend:

1. Enable gVisor runtime with `securityLevel: high`
2. Apply strict network policies
3. Use CPU pinning for sensitive workloads
4. Enable audit logging
5. Set appropriate resource limits
6. Configure sandbox timeouts

### Regular Updates

Keep LLMSafeSpace and its dependencies updated:

```bash
# Update LLMSafeSpace using Helm
helm upgrade llmsafespace llmsafespace/llmsafespace --namespace llmsafespace
```

## Enterprise Security Features

Enterprise edition includes additional security features:

- Advanced threat detection
- Integration with enterprise SIEM systems
- Custom security policies
- Compliance reporting
- Enhanced isolation options
- Dedicated warm pools with custom security profiles

## Warm Pool Security Considerations

The warm pool functionality introduces specific security considerations:

### Pod Recycling Security

- **Isolation Verification**: Before recycling pods, the system verifies that no sensitive data remains
- **Filesystem Reset**: All writable directories are cleaned between uses
- **Package Verification**: Installed packages are verified against an allowlist before recycling
- **Time-Based Expiry**: Pods older than a configurable threshold are terminated rather than recycled
- **Security Event Monitoring**: Pods that trigger security events are never recycled

### Preloaded Package Security

- **Package Scanning**: All preloaded packages are scanned for vulnerabilities
- **Version Pinning**: Package versions are pinned to prevent supply chain attacks
- **Integrity Verification**: Package integrity is verified during pod initialization
- **Allowlist Enforcement**: Only approved packages can be preloaded

## Conclusion

LLMSafeSpace's security model provides robust protection for code execution environments while maintaining the simplicity and efficiency of a container-based architecture. By leveraging Kubernetes security features and adding specialized isolation mechanisms, LLMSafeSpace delivers a secure platform suitable for most LLM agent execution scenarios.

The warm pool functionality significantly improves startup performance without compromising security, thanks to careful pod lifecycle management and security verification during recycling.

For use cases requiring the absolute highest level of isolation, consider deploying LLMSafeSpace with a VM-based runtime or exploring our enterprise offerings with enhanced security features.
