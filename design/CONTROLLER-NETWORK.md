# Network Policy Design for LLMSafeSpace

## Overview

This document provides a detailed design for the network policies in LLMSafeSpace, focusing on how network isolation is implemented to secure sandbox environments. Network policies are a critical component of the security model, ensuring that sandboxes can only communicate with authorized endpoints while preventing unauthorized network access.

## Design Goals

1. **Strong Isolation**: Prevent communication between sandboxes
2. **Controlled Egress**: Allow outbound traffic only to authorized destinations
3. **Default Deny**: Implement a default-deny policy for all traffic
4. **Granular Control**: Support fine-grained rules based on domains and ports
5. **Observability**: Enable monitoring and logging of network activity
6. **Flexibility**: Allow customization for different use cases
7. **Performance**: Minimize impact on network performance
8. **Compatibility**: Work across different Kubernetes environments

## Network Policy Architecture

### High-Level Architecture

The network policy architecture consists of multiple layers of protection:

```
┌─────────────────────────────────────────────────────────────┐
│                  Kubernetes Namespace Isolation              │
├─────────────────────────────────────────────────────────────┤
│                  Default Deny Network Policies               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────┐  │
│  │  Ingress    │    │   Egress    │    │  DNS Access     │  │
│  │  Policies   │    │  Policies   │    │    Policies     │  │
│  └─────────────┘    └─────────────┘    └─────────────────┘  │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│                  Service Mesh Integration                    │
└─────────────────────────────────────────────────────────────┘
```

### Components

1. **Namespace Isolation**: Each sandbox can be deployed in its own namespace for strong isolation
2. **Default Deny Policies**: Block all traffic by default
3. **Ingress Policies**: Control inbound traffic to sandboxes
4. **Egress Policies**: Control outbound traffic from sandboxes
5. **DNS Access Policies**: Allow DNS resolution while maintaining security
6. **Service Mesh Integration**: Optional enhanced security with mTLS and traffic monitoring

## Detailed Design

### 1. Default Network Policies

#### Default Deny Policy

Every sandbox namespace has a default deny policy applied automatically:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny
  namespace: ${SANDBOX_NAMESPACE}
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
```

This policy blocks all inbound and outbound traffic for all pods in the namespace, creating a secure baseline.

#### Implementation Details

The NetworkPolicyManager in the controller creates these policies automatically:

```go
// ensureDefaultDenyPolicy creates or updates the default deny policy
func (n *NetworkPolicyManager) ensureDefaultDenyPolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    // Define the default deny policy
    defaultDenyPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: "default-deny",
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "sandbox-id": sandbox.Name,
                "policy-type": "default-deny",
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{},
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
                networkingv1.PolicyTypeEgress,
            },
        },
    }
    
    // Create or update the policy
    _, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), defaultDenyPolicy, metav1.CreateOptions{})
    if err != nil {
        if errors.IsAlreadyExists(err) {
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                context.TODO(), defaultDenyPolicy, metav1.UpdateOptions{})
        }
    }
    
    return err
}
```

### 2. DNS Access Policy

To allow DNS resolution, a specific policy is created to permit UDP and TCP traffic to the cluster's DNS service:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-dns
  namespace: ${SANDBOX_NAMESPACE}
spec:
  podSelector: {}
  policyTypes:
  - Egress
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: kube-system
      podSelector:
        matchLabels:
          k8s-app: kube-dns
    ports:
    - protocol: UDP
      port: 53
    - protocol: TCP
      port: 53
```

#### Implementation Details

```go
// ensureDNSAccessPolicy creates or updates the DNS access policy
func (n *NetworkPolicyManager) ensureDNSAccessPolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    // Define the DNS access policy
    dnsPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: "allow-dns",
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "sandbox-id": sandbox.Name,
                "policy-type": "dns-access",
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{},
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeEgress,
            },
            Egress: []networkingv1.NetworkPolicyEgressRule{
                {
                    To: []networkingv1.NetworkPolicyPeer{
                        {
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "kubernetes.io/metadata.name": "kube-system",
                                },
                            },
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "k8s-app": "kube-dns",
                                },
                            },
                        },
                    },
                    Ports: []networkingv1.NetworkPolicyPort{
                        {
                            Protocol: &[]corev1.Protocol{corev1.ProtocolUDP}[0],
                            Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 53},
                        },
                        {
                            Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                            Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 53},
                        },
                    },
                },
            },
        },
    }
    
    // Create or update the policy
    _, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), dnsPolicy, metav1.CreateOptions{})
    if err != nil {
        if errors.IsAlreadyExists(err) {
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                context.TODO(), dnsPolicy, metav1.UpdateOptions{})
        }
    }
    
    return err
}
```

### 3. API Service Access Policy

Sandboxes need to communicate with the API service for execution results and file operations:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-api-service
  namespace: ${SANDBOX_NAMESPACE}
spec:
  podSelector: {}
  policyTypes:
  - Egress
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: llmsafespace
      podSelector:
        matchLabels:
          app: llmsafespace
          component: api
    ports:
    - protocol: TCP
      port: 8080
```

#### Implementation Details

```go
// ensureAPIServicePolicy creates or updates the API service access policy
func (n *NetworkPolicyManager) ensureAPIServicePolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    // Define the API service access policy
    apiPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: "allow-api-service",
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "sandbox-id": sandbox.Name,
                "policy-type": "api-access",
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{},
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeEgress,
            },
            Egress: []networkingv1.NetworkPolicyEgressRule{
                {
                    To: []networkingv1.NetworkPolicyPeer{
                        {
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "kubernetes.io/metadata.name": "llmsafespace",
                                },
                            },
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "app": "llmsafespace",
                                    "component": "api",
                                },
                            },
                        },
                    },
                    Ports: []networkingv1.NetworkPolicyPort{
                        {
                            Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                            Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 8080},
                        },
                    },
                },
            },
        },
    }
    
    // Create or update the policy
    _, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), apiPolicy, metav1.CreateOptions{})
    if err != nil {
        if errors.IsAlreadyExists(err) {
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                context.TODO(), apiPolicy, metav1.UpdateOptions{})
        }
    }
    
    return err
}
```

### 4. Egress Filtering Rules

#### Domain-Based Egress Filtering

LLMSafeSpace supports domain-based egress filtering to allow sandboxes to access specific external services:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-egress-pypi
  namespace: ${SANDBOX_NAMESPACE}
spec:
  podSelector: {}
  policyTypes:
  - Egress
  egress:
  - to:
    - ipBlock:
        cidr: ${PYPI_IP_RANGE}
    ports:
    - protocol: TCP
      port: 443
```

Since Kubernetes NetworkPolicy doesn't support domain names directly, the controller resolves domain names to IP addresses and creates appropriate policies.

#### Implementation Details

```go
// ensureEgressPolicies creates or updates egress policies based on sandbox configuration
func (n *NetworkPolicyManager) ensureEgressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    if sandbox.Spec.NetworkAccess == nil || len(sandbox.Spec.NetworkAccess.Egress) == 0 {
        return nil
    }
    
    // Process each egress rule
    for i, rule := range sandbox.Spec.NetworkAccess.Egress {
        // Resolve domain to IP addresses
        ips, err := n.resolveDomain(rule.Domain)
        if err != nil {
            n.recorder.Event(sandbox, corev1.EventTypeWarning, "EgressResolutionFailed", 
                fmt.Sprintf("Failed to resolve domain %s: %v", rule.Domain, err))
            continue
        }
        
        // Create CIDR blocks for each IP
        var ipBlocks []networkingv1.IPBlock
        for _, ip := range ips {
            // Add /32 for individual IPs
            ipBlocks = append(ipBlocks, networkingv1.IPBlock{
                CIDR: fmt.Sprintf("%s/32", ip),
            })
        }
        
        // Create ports configuration
        var ports []networkingv1.NetworkPolicyPort
        if len(rule.Ports) > 0 {
            for _, p := range rule.Ports {
                port := intstr.FromInt(int(p.Port))
                protocol := corev1.Protocol(p.Protocol)
                ports = append(ports, networkingv1.NetworkPolicyPort{
                    Protocol: &protocol,
                    Port:     &port,
                })
            }
        } else {
            // Default to HTTPS (443) if no ports specified
            port := intstr.FromInt(443)
            protocol := corev1.ProtocolTCP
            ports = append(ports, networkingv1.NetworkPolicyPort{
                Protocol: &protocol,
                Port:     &port,
            })
        }
        
        // Create network policy for this domain
        policyName := fmt.Sprintf("allow-egress-%s-%d", sanitizeDomainForName(rule.Domain), i)
        policy := &networkingv1.NetworkPolicy{
            ObjectMeta: metav1.ObjectMeta{
                Name: policyName,
                Namespace: namespace,
                Labels: map[string]string{
                    "app": "llmsafespace",
                    "sandbox-id": sandbox.Name,
                    "policy-type": "egress",
                    "domain": sanitizeDomainForLabel(rule.Domain),
                },
                Annotations: map[string]string{
                    "llmsafespace.dev/original-domain": rule.Domain,
                },
                OwnerReferences: []metav1.OwnerReference{
                    *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
                },
            },
            Spec: networkingv1.NetworkPolicySpec{
                PodSelector: metav1.LabelSelector{},
                PolicyTypes: []networkingv1.PolicyType{
                    networkingv1.PolicyTypeEgress,
                },
                Egress: []networkingv1.NetworkPolicyEgressRule{
                    {
                        To: func() []networkingv1.NetworkPolicyPeer {
                            peers := make([]networkingv1.NetworkPolicyPeer, len(ipBlocks))
                            for j, block := range ipBlocks {
                                peers[j] = networkingv1.NetworkPolicyPeer{
                                    IPBlock: &block,
                                }
                            }
                            return peers
                        }(),
                        Ports: ports,
                    },
                },
            },
        }
        
        // Create or update the policy
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
            context.TODO(), policy, metav1.CreateOptions{})
        if err != nil {
            if errors.IsAlreadyExists(err) {
                _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                    context.TODO(), policy, metav1.UpdateOptions{})
            }
            if err != nil {
                return err
            }
        }
    }
    
    return nil
}

// resolveDomain resolves a domain name to IP addresses
func (n *NetworkPolicyManager) resolveDomain(domain string) ([]string, error) {
    // Check cache first
    if ips, found := n.domainCache.Get(domain); found {
        return ips.([]string), nil
    }
    
    // Perform DNS lookup
    ips, err := net.LookupIP(domain)
    if err != nil {
        return nil, err
    }
    
    // Extract IPv4 addresses
    var ipStrings []string
    for _, ip := range ips {
        if ipv4 := ip.To4(); ipv4 != nil {
            ipStrings = append(ipStrings, ipv4.String())
        }
    }
    
    if len(ipStrings) == 0 {
        return nil, fmt.Errorf("no IPv4 addresses found for domain %s", domain)
    }
    
    // Cache the results
    n.domainCache.Set(domain, ipStrings, cache.DefaultExpiration)
    
    return ipStrings, nil
}

// sanitizeDomainForName converts a domain to a valid Kubernetes resource name
func sanitizeDomainForName(domain string) string {
    // Replace dots and other invalid chars with dashes
    name := strings.ReplaceAll(domain, ".", "-")
    name = strings.ReplaceAll(name, "*", "wildcard")
    
    // Truncate if too long
    if len(name) > 63 {
        name = name[:63]
    }
    
    return name
}

// sanitizeDomainForLabel converts a domain to a valid Kubernetes label value
func sanitizeDomainForLabel(domain string) string {
    // Replace dots and other invalid chars with dashes
    label := strings.ReplaceAll(domain, ".", "-")
    label = strings.ReplaceAll(label, "*", "wildcard")
    
    // Truncate if too long
    if len(label) > 63 {
        label = label[:63]
    }
    
    return label
}
```

#### Domain Resolution Refresh

To handle IP address changes for domains, the controller periodically refreshes the domain resolutions:

```go
// StartDomainRefreshLoop starts a goroutine to periodically refresh domain resolutions
func (n *NetworkPolicyManager) StartDomainRefreshLoop(stopCh <-chan struct{}) {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            n.refreshAllDomainPolicies()
        case <-stopCh:
            return
        }
    }
}

// refreshAllDomainPolicies refreshes all domain-based egress policies
func (n *NetworkPolicyManager) refreshAllDomainPolicies() {
    // List all sandboxes
    sandboxes, err := n.sandboxLister.List(labels.Everything())
    if err != nil {
        klog.Errorf("Failed to list sandboxes for domain refresh: %v", err)
        return
    }
    
    // Process each sandbox
    for _, sandbox := range sandboxes {
        if sandbox.Spec.NetworkAccess == nil || len(sandbox.Spec.NetworkAccess.Egress) == 0 {
            continue
        }
        
        // Determine namespace
        namespace := sandbox.Namespace
        if n.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
        
        // Re-create egress policies
        if err := n.ensureEgressPolicies(sandbox, namespace); err != nil {
            klog.Errorf("Failed to refresh egress policies for sandbox %s: %v", sandbox.Name, err)
        }
    }
}
```

### 5. Ingress Policies

By default, sandboxes do not accept any ingress traffic. However, if specified in the sandbox configuration, ingress can be enabled:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-ingress
  namespace: ${SANDBOX_NAMESPACE}
spec:
  podSelector:
    matchLabels:
      app: llmsafespace
      sandbox-id: ${SANDBOX_ID}
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: llmsafespace
    ports:
    - protocol: TCP
      port: 8080
```

#### Implementation Details

```go
// ensureIngressPolicies creates or updates ingress policies based on sandbox configuration
func (n *NetworkPolicyManager) ensureIngressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    if sandbox.Spec.NetworkAccess == nil || !sandbox.Spec.NetworkAccess.Ingress {
        return nil
    }
    
    // Define the ingress policy
    ingressPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: "allow-ingress",
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "sandbox-id": sandbox.Name,
                "policy-type": "ingress",
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    "app": "llmsafespace",
                    "sandbox-id": sandbox.Name,
                },
            },
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
            },
            Ingress: []networkingv1.NetworkPolicyIngressRule{
                {
                    From: []networkingv1.NetworkPolicyPeer{
                        {
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "kubernetes.io/metadata.name": "llmsafespace",
                                },
                            },
                        },
                    },
                    Ports: []networkingv1.NetworkPolicyPort{
                        {
                            Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                            Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 8080},
                        },
                    },
                },
            },
        },
    }
    
    // Create or update the policy
    _, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), ingressPolicy, metav1.CreateOptions{})
    if err != nil {
        if errors.IsAlreadyExists(err) {
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                context.TODO(), ingressPolicy, metav1.UpdateOptions{})
        }
    }
    
    return err
}
```

### 6. Service Mesh Integration

For enhanced security and observability, LLMSafeSpace can integrate with service mesh solutions like Istio.

#### Istio Integration

When Istio is enabled, additional security features are available:

1. **mTLS Communication**: All pod-to-pod communication is encrypted
2. **Fine-grained Access Control**: Using AuthorizationPolicy resources
3. **Traffic Monitoring**: Detailed metrics and logs for all network traffic
4. **Circuit Breaking**: Prevent cascading failures
5. **Request Tracing**: End-to-end tracing of requests

#### Example Istio Authorization Policy

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: sandbox-policy
  namespace: ${SANDBOX_NAMESPACE}
spec:
  selector:
    matchLabels:
      app: llmsafespace
      sandbox-id: ${SANDBOX_ID}
  action: ALLOW
  rules:
  - from:
    - source:
        namespaces: ["llmsafespace"]
        principals: ["cluster.local/ns/llmsafespace/sa/agent-api"]
    to:
    - operation:
        methods: ["GET", "POST"]
        paths: ["/api/*"]
```

#### Implementation Details

```go
// ensureIstioResources creates or updates Istio resources if Istio is enabled
func (n *NetworkPolicyManager) ensureIstioResources(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    if !n.istioEnabled {
        return nil
    }
    
    // Create AuthorizationPolicy
    authPolicy := &securityv1beta1.AuthorizationPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "sandbox-id": sandbox.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: securityv1beta1.AuthorizationPolicySpec{
            Selector: &typev1beta1.WorkloadSelector{
                MatchLabels: map[string]string{
                    "app": "llmsafespace",
                    "sandbox-id": sandbox.Name,
                },
            },
            Action: securityv1beta1.AuthorizationPolicy_ALLOW,
            Rules: []*securityv1beta1.Rule{
                {
                    From: []*securityv1beta1.Rule_From{
                        {
                            Source: &securityv1beta1.Source{
                                Namespaces: []string{"llmsafespace"},
                                Principals: []string{"cluster.local/ns/llmsafespace/sa/agent-api"},
                            },
                        },
                    },
                    To: []*securityv1beta1.Rule_To{
                        {
                            Operation: &securityv1beta1.Operation{
                                Methods: []string{"GET", "POST"},
                                Paths:   []string{"/api/*"},
                            },
                        },
                    },
                },
            },
        },
    }
    
    // Create or update the AuthorizationPolicy
    _, err := n.istioClient.SecurityV1beta1().AuthorizationPolicies(namespace).Create(
        context.TODO(), authPolicy, metav1.CreateOptions{})
    if err != nil {
        if errors.IsAlreadyExists(err) {
            _, err = n.istioClient.SecurityV1beta1().AuthorizationPolicies(namespace).Update(
                context.TODO(), authPolicy, metav1.UpdateOptions{})
        }
    }
    
    return err
}
```

### 7. NetworkPolicyManager Interface

The NetworkPolicyManager provides a unified interface for managing all network policies:

```go
// NetworkPolicyManager handles the creation, update, and deletion of network policies
type NetworkPolicyManager struct {
    kubeClient      kubernetes.Interface
    istioClient     istioclientset.Interface
    sandboxLister   listers.SandboxLister
    config          *config.Config
    recorder        record.EventRecorder
    domainCache     *cache.Cache
    istioEnabled    bool
}

// NewNetworkPolicyManager creates a new NetworkPolicyManager
func NewNetworkPolicyManager(
    kubeClient kubernetes.Interface,
    istioClient istioclientset.Interface,
    sandboxLister listers.SandboxLister,
    config *config.Config,
    recorder record.EventRecorder,
) *NetworkPolicyManager {
    manager := &NetworkPolicyManager{
        kubeClient:     kubeClient,
        istioClient:    istioClient,
        sandboxLister:  sandboxLister,
        config:         config,
        recorder:       recorder,
        domainCache:    cache.New(1*time.Hour, 10*time.Minute),
        istioEnabled:   istioClient != nil,
    }
    
    // Start domain refresh loop if needed
    if config.EnableDomainRefresh {
        go manager.StartDomainRefreshLoop(make(chan struct{}))
    }
    
    return manager
}

// EnsureNetworkPolicies ensures that all required network policies exist for a sandbox
func (n *NetworkPolicyManager) EnsureNetworkPolicies(sandbox *llmsafespacev1.Sandbox, podNamespace string) error {
    // Determine namespace
    namespace := podNamespace
    if namespace == "" {
        namespace = sandbox.Namespace
        if n.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
    }
    
    // Create default deny policy
    if err := n.ensureDefaultDenyPolicy(sandbox, namespace); err != nil {
        return fmt.Errorf("failed to ensure default deny policy: %v", err)
    }
    
    // Create API service access policy
    if err := n.ensureAPIServicePolicy(sandbox, namespace); err != nil {
        return fmt.Errorf("failed to ensure API service access policy: %v", err)
    }
    
    // Create DNS access policy
    if err := n.ensureDNSAccessPolicy(sandbox, namespace); err != nil {
        return fmt.Errorf("failed to ensure DNS access policy: %v", err)
    }
    
    // Create egress policies if specified
    if sandbox.Spec.NetworkAccess != nil && len(sandbox.Spec.NetworkAccess.Egress) > 0 {
        if err := n.ensureEgressPolicies(sandbox, namespace); err != nil {
            return fmt.Errorf("failed to ensure egress policies: %v", err)
        }
    } else {
        // Delete any existing egress policies if no egress is specified
        if err := n.deleteEgressPolicies(sandbox, namespace); err != nil {
            return fmt.Errorf("failed to delete egress policies: %v", err)
        }
    }
    
    // Create ingress policies if specified
    if sandbox.Spec.NetworkAccess != nil && sandbox.Spec.NetworkAccess.Ingress {
        if err := n.ensureIngressPolicies(sandbox, namespace); err != nil {
            return fmt.Errorf("failed to ensure ingress policies: %v", err)
        }
    } else {
        // Delete any existing ingress policies if ingress is not enabled
        if err := n.deleteIngressPolicies(sandbox, namespace); err != nil {
            return fmt.Errorf("failed to delete ingress policies: %v", err)
        }
    }
    
    // Create Istio resources if enabled
    if n.istioEnabled {
        if err := n.ensureIstioResources(sandbox, namespace); err != nil {
            return fmt.Errorf("failed to ensure Istio resources: %v", err)
        }
    }
    
    return nil
}

// DeleteNetworkPolicies deletes all network policies for a sandbox
func (n *NetworkPolicyManager) DeleteNetworkPolicies(sandbox *llmsafespacev1.Sandbox) error {
    // Determine namespace
    namespace := sandbox.Namespace
    if n.config.NamespaceIsolation {
        namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
    }
    
    // List all network policies in the namespace
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(),
        metav1.ListOptions{
            LabelSelector: fmt.Sprintf("sandbox-id=%s", sandbox.Name),
        },
    )
    if err != nil {
        return err
    }
    
    // Delete each policy
    for _, policy := range policies.Items {
        err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Delete(
            context.TODO(),
            policy.Name,
            metav1.DeleteOptions{},
        )
        if err != nil && !errors.IsNotFound(err) {
            return err
        }
    }
    
    // Delete Istio resources if enabled
    if n.istioEnabled {
        err := n.istioClient.SecurityV1beta1().AuthorizationPolicies(namespace).Delete(
            context.TODO(),
            fmt.Sprintf("sandbox-%s", sandbox.Name),
            metav1.DeleteOptions{},
        )
        if err != nil && !errors.IsNotFound(err) {
            return err
        }
    }
    
    return nil
}
```

## Network Policy Monitoring

### Metrics Collection

The NetworkPolicyManager collects metrics to monitor the effectiveness of network policies:

```go
var (
    networkPolicyOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_policy_operations_total",
            Help: "Total number of network policy operations",
        },
        []string{"operation", "status"},
    )
    
    networkPolicyOperationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_network_policy_operation_duration_seconds",
            Help: "Duration of network policy operations in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"operation"},
    )
    
    networkViolationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_violations_total",
            Help: "Total number of network policy violations",
        },
        []string{"direction", "destination", "runtime"},
    )
    
    domainResolutionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_domain_resolutions_total",
            Help: "Total number of domain resolutions",
        },
        []string{"status"},
    )
    
    domainResolutionDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_domain_resolution_duration_seconds",
            Help: "Duration of domain resolutions in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{},
    )
)

func init() {
    // Register metrics
    prometheus.MustRegister(networkPolicyOperationsTotal)
    prometheus.MustRegister(networkPolicyOperationDurationSeconds)
    prometheus.MustRegister(networkViolationsTotal)
    prometheus.MustRegister(domainResolutionsTotal)
    prometheus.MustRegister(domainResolutionDurationSeconds)
}
```

### Network Policy Violation Detection

To detect network policy violations, LLMSafeSpace integrates with Kubernetes audit logs and network flow logs:

```go
// NetworkViolationDetector monitors for network policy violations
type NetworkViolationDetector struct {
    kubeClient kubernetes.Interface
    recorder   record.EventRecorder
    logger     *zap.Logger
}

// DetectViolations processes network flow logs to detect violations
func (d *NetworkViolationDetector) DetectViolations(flowLogs []FlowLog) {
    for _, log := range flowLogs {
        if log.Verdict == "DROPPED" {
            // Extract sandbox information from source or destination
            sandboxID := extractSandboxID(log)
            if sandboxID == "" {
                continue
            }
            
            // Get sandbox
            sandbox, err := d.kubeClient.LlmsafespaceV1().Sandboxes("").Get(
                context.TODO(), sandboxID, metav1.GetOptions{})
            if err != nil {
                d.logger.Error("Failed to get sandbox for violation",
                    zap.String("sandboxID", sandboxID),
                    zap.Error(err))
                continue
            }
            
            // Record violation metric
            networkViolationsTotal.WithLabelValues(
                log.Direction,
                log.Destination,
                sandbox.Spec.Runtime,
            ).Inc()
            
            // Record event
            d.recorder.Event(sandbox, corev1.EventTypeWarning, "NetworkPolicyViolation",
                fmt.Sprintf("Network policy violation: %s traffic to %s was blocked",
                    log.Direction, log.Destination))
            
            // Log violation
            d.logger.Warn("Network policy violation detected",
                zap.String("sandboxID", sandboxID),
                zap.String("direction", log.Direction),
                zap.String("destination", log.Destination),
                zap.String("sourceIP", log.SourceIP),
                zap.String("destinationIP", log.DestinationIP),
                zap.Int("sourcePort", log.SourcePort),
                zap.Int("destinationPort", log.DestinationPort),
                zap.String("protocol", log.Protocol))
        }
    }
}
```

## Advanced Network Policy Features

### 1. Cilium Integration

For Kubernetes clusters with Cilium CNI, LLMSafeSpace can leverage advanced network policy features:

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: sandbox-policy
  namespace: ${SANDBOX_NAMESPACE}
spec:
  endpointSelector:
    matchLabels:
      app: llmsafespace
      sandbox-id: ${SANDBOX_ID}
  egress:
  - toFQDNs:
    - matchName: "pypi.org"
    - matchName: "files.pythonhosted.org"
    toPorts:
    - ports:
      - port: "443"
        protocol: TCP
```

Cilium policies support direct FQDN matching, eliminating the need for manual IP resolution.

### 2. Network Policy Visualization

To help users understand the network policies applied to their sandboxes, LLMSafeSpace provides a visualization tool:

```go
// GenerateNetworkPolicyGraph generates a visualization of network policies
func (n *NetworkPolicyManager) GenerateNetworkPolicyGraph(sandbox *llmsafespacev1.Sandbox) ([]byte, error) {
    // Determine namespace
    namespace := sandbox.Namespace
    if n.config.NamespaceIsolation {
        namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
    }
    
    // List all network policies for this sandbox
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(),
        metav1.ListOptions{
            LabelSelector: fmt.Sprintf("sandbox-id=%s", sandbox.Name),
        },
    )
    if err != nil {
        return nil, err
    }
    
    // Generate DOT graph
    graph := "digraph NetworkPolicies {\n"
    graph += "  rankdir=LR;\n"
    graph += "  node [shape=box];\n"
    
    // Add sandbox node
    graph += fmt.Sprintf("  sandbox [label=\"Sandbox\\n%s\"];\n", sandbox.Name)
    
    // Process each policy
    for _, policy := range policies.Items {
        switch {
        case strings.HasPrefix(policy.Name, "default-deny"):
            graph += "  internet [label=\"Internet\"];\n"
            graph += "  sandbox -> internet [label=\"Blocked by default\", color=red, style=dashed];\n"
            graph += "  internet -> sandbox [label=\"Blocked by default\", color=red, style=dashed];\n"
            
        case strings.HasPrefix(policy.Name, "allow-dns"):
            graph += "  dns [label=\"DNS Service\"];\n"
            graph += "  sandbox -> dns [label=\"UDP/TCP 53\", color=green];\n"
            
        case strings.HasPrefix(policy.Name, "allow-api-service"):
            graph += "  api [label=\"API Service\"];\n"
            graph += "  sandbox -> api [label=\"TCP 8080\", color=green];\n"
            
        case strings.HasPrefix(policy.Name, "allow-egress"):
            // Extract domain from annotations
            domain := policy.Annotations["llmsafespace.dev/original-domain"]
            if domain == "" {
                domain = policy.Name
            }
            
            // Create node for this domain
            nodeID := fmt.Sprintf("domain_%d", len(graph))
            graph += fmt.Sprintf("  %s [label=\"%s\"];\n", nodeID, domain)
            
            // Add connection
            ports := []string{}
            for _, rule := range policy.Spec.Egress {
                for _, port := range rule.Ports {
                    ports = append(ports, fmt.Sprintf("%s %d", *port.Protocol, port.Port.IntVal))
                }
            }
            
            portLabel := strings.Join(ports, ",")
            graph += fmt.Sprintf("  sandbox -> %s [label=\"%s\", color=green];\n", nodeID, portLabel)
            
        case strings.HasPrefix(policy.Name, "allow-ingress"):
            graph += "  api [label=\"API Service\"];\n"
            graph += "  api -> sandbox [label=\"TCP 8080\", color=green];\n"
        }
    }
    
    graph += "}\n"
    
    // Convert DOT to SVG using Graphviz
    cmd := exec.Command("dot", "-Tsvg")
    cmd.Stdin = strings.NewReader(graph)
    var out bytes.Buffer
    cmd.Stdout = &out
    
    if err := cmd.Run(); err != nil {
        return nil, err
    }
    
    return out.Bytes(), nil
}
```

## Network Policy Testing

### 1. Connectivity Testing

LLMSafeSpace includes tools to test network policies:

```go
// TestNetworkConnectivity tests network connectivity from a sandbox
func (n *NetworkPolicyManager) TestNetworkConnectivity(sandbox *llmsafespacev1.Sandbox, target string) (bool, error) {
    // Determine namespace and pod name
    namespace := sandbox.Status.PodNamespace
    podName := sandbox.Status.PodName
    
    if namespace == "" || podName == "" {
        return false, fmt.Errorf("sandbox pod not found")
    }
    
    // Create exec request
    req := n.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(podName).
        Namespace(namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "5", target},
            Stdout:  true,
            Stderr:  true,
        }, scheme.ParameterCodec)
    
    // Execute command
    exec, err := remotecommand.NewSPDYExecutor(n.config.RestConfig, "POST", req.URL())
    if err != nil {
        return false, err
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        return false, err
    }
    
    // Check result
    statusCode := stdout.String()
    if statusCode == "200" || statusCode == "301" || statusCode == "302" {
        return true, nil
    }
    
    return false, fmt.Errorf("connection failed: %s", stderr.String())
}
```

### 2. Policy Validation

The controller validates network policies before applying them:

```go
// validateNetworkPolicies validates network policies in a sandbox spec
func (c *Controller) validateNetworkPolicies(sandbox *llmsafespacev1.Sandbox) error {
    if sandbox.Spec.NetworkAccess == nil {
        return nil
    }
    
    // Validate egress rules
    for i, rule := range sandbox.Spec.NetworkAccess.Egress {
        // Check if domain is valid
        if rule.Domain == "" {
            return fmt.Errorf("egress rule %d: domain is required", i)
        }
        
        // Check if domain is in allowlist if enabled
        if c.config.EnableDomainAllowlist && !c.isDomainAllowed(rule.Domain) {
            return fmt.Errorf("egress rule %d: domain %s is not in the allowlist", i, rule.Domain)
        }
        
        // Validate ports
        for j, port := range rule.Ports {
            if port.Port < 1 || port.Port > 65535 {
                return fmt.Errorf("egress rule %d, port %d: invalid port number %d", i, j, port.Port)
            }
            
            if port.Protocol != "TCP" && port.Protocol != "UDP" {
                return fmt.Errorf("egress rule %d, port %d: invalid protocol %s", i, j, port.Protocol)
            }
        }
    }
    
    return nil
}

// isDomainAllowed checks if a domain is in the allowlist
func (c *Controller) isDomainAllowed(domain string) bool {
    // Check exact match
    for _, allowed := range c.config.DomainAllowlist {
        if allowed == domain {
            return true
        }
        
        // Check wildcard match
        if strings.HasPrefix(allowed, "*.") {
            suffix := allowed[1:] // Remove *
            if strings.HasSuffix(domain, suffix) {
                return true
            }
        }
    }
    
    return false
}
```

## Conclusion

The network policy design for LLMSafeSpace provides a robust security boundary around sandbox environments, ensuring that code execution occurs in a controlled and isolated environment. By implementing a defense-in-depth approach with multiple layers of network security, LLMSafeSpace achieves strong isolation while maintaining the flexibility needed for various use cases.

Key features of the network policy design include:

1. **Default Deny Policies**: Establish a secure baseline by blocking all traffic by default
2. **Domain-Based Egress Filtering**: Allow access to specific external services while blocking all other traffic
3. **DNS and API Service Access**: Enable essential services while maintaining security
4. **Service Mesh Integration**: Enhance security with mTLS and fine-grained access control
5. **Monitoring and Violation Detection**: Track and alert on network policy violations
6. **Advanced Features**: Support for Cilium and other CNI-specific enhancements

This design ensures that sandboxes can only communicate with authorized endpoints, preventing both lateral movement between sandboxes and unauthorized external communication, while still allowing legitimate network access needed for code execution.
