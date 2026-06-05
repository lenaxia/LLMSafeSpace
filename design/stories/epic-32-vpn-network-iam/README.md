# Epic 32: VPN Sidecars, VPC Connectivity & AWS IAM

**Status:** Planning
**Created:** 2026-06-04
**Priority:** High
**Depends on:** Epic 6 (Collapse Sandbox into Workspace — shipped), Epic 9 (Configuration & Settings — shipped), Epic 24 (Self-Healing Lifecycle — shipped)

---

## Rationale

Workspaces are currently network-isolated by default: egress is limited to user-declared FQDNs resolved to public IPs, `AutomountServiceAccountToken` is false, and the pod runs with `drop: ALL` capabilities. This is correct for the base security model, but power users need:

1. **VPN connectivity** to private networks — VPCs, on-premise data centers, or mesh overlays — so the AI agent can reach internal databases, APIs, and services.
2. **AWS IAM roles** so the agent can interact with AWS services (S3, DynamoDB, Bedrock, etc.) using short-lived credentials governed by IAM policies.

This epic adds three VPN provider sidecars (WireGuard, Tailscale, ZeroTier), two AWS IAM mechanisms (IRSA + EKS Pod Identity), and VPC connectivity via Tailscale subnet routing as the primary integration path. All features are admin-gated via existing Tier 2 settings and per-workspace CRD configuration.

---

## Design Principles

1. **Admin-gated by default**: Every VPN type and IAM role has an admin toggle and allowed-list. Users cannot attach arbitrary networks without operator approval.
2. **Sidecar isolation**: VPN processes run in a separate sidecar container, not in the main workspace container. A compromised workspace cannot compromise the VPN tunnel.
3. **No capability escalation**: VPN sidecar containers require `NET_ADMIN` capability. The workspace main container remains `drop: ALL`. The sidecar's only privilege is network configuration — it shares the pod network namespace.
4. **BYO config**: Users bring their own VPN config (WireGuard conf, Tailscale auth key, ZeroTier network ID + token). The platform never generates or manages VPN credentials.
5. **VPC via Tailscale subnet routing**: VPC connectivity is achieved by routing workspace traffic through a Tailscale sidecar connected to a Tailscale network that has a subnet router in the target VPC. This avoids raw VPC peering complexity at the K8s level.
6. **IRSA first, Pod Identity fallback**: AWS IAM uses IRSA (IAM Roles for Service Accounts) where available. If the cluster uses EKS Pod Identity, that takes precedence. The CRD field is the same; the controller detects the mechanism.

---

## Stated Assumptions

| # | Assumption | Verification |
|---|---|---|
| A1 | Workspace pods share a single pod network namespace with their sidecars | K8s pod spec: all containers in a pod share the same network namespace (netns) |
| A2 | `AutomountServiceAccountToken: false` prevents IRSA | IRSA requires a ServiceAccount token to be mounted for the OIDC provider to validate. When IAM is enabled, the controller must use a per-workspace ServiceAccount with `eks.amazonaws.com/role-arn` annotation and `AutomountServiceAccountToken: true` |
| A3 | Tailscale `--accept-routes` flag makes subnet routes available in the pod netns | Verified via Tailscale docs: `--accept-routes` installs subnet routes in the pod's routing table |
| A4 | WireGuard sidecar requires `NET_ADMIN` + `/dev/net/tun` | WireGuard creates a tun interface; `NET_ADMIN` is required for tunnel creation and route manipulation |
| A5 | ZeroTier sidecar requires `NET_ADMIN` + `/dev/net/tun` | Same as WireGuard — ZeroTier creates a `zt*` virtual network interface |
| A6 | Tailscale sidecar can run without `NET_ADMIN` in `--userspace-mode` (userspace networking, no tun device) | True, but userspace mode is slower and lacks some features. For production, `NET_ADMIN` is preferred |
| A7 | IRSA is available on EKS clusters with OIDC provider configured | Standard EKS feature. Non-EKS clusters can use kube2iam or similar |
| A8 | EKS Pod Identity is available on EKS clusters >= 1.24 with Pod Identity Agent addon | Newer EKS feature; not all clusters have it. Controller must detect and fall back to IRSA |
| A9 | VPC peering is unnecessary when Tailscale subnet routing is available | Subnet routing through the Tailscale sidecar achieves the same effect without Cloud-level VPC peering complexity |

---

## Design Questions

| # | Question | Answer | Rationale |
|---|---|---|---|
| DQ1 | Should we support VPC peering at the K8s CNI level? | **No.** VPC peering is an infrastructure-level concern. Tailscale subnet routing achieves the same result without coupling the platform to specific cloud providers. For users who need CNI-level peering, they can configure it at the cluster level outside LLMSafeSpace. | Avoids cloud-provider lock-in and massive infrastructure complexity per workspace. |
| DQ2 | Should VPC config be a separate CRD field or part of VPN config? | **Separate.** `spec.vpc` contains VPC-specific metadata (region, VPC ID, IAM role) that is distinct from VPN tunnel config. The controller uses VPC config only for Tailscale subnet routing setup. | SRP separation of concerns. VPC is a higher-level abstraction that happens to use the VPN sidecar. |
| DQ3 | Should IAM roles force a separate ServiceAccount per workspace? | **Yes.** When `spec.iam.roleARN` is set, the controller creates a ServiceAccount named `workspace-iam-<workspaceName>` with the IRSA annotation, and the pod spec references that SA. Without IAM, the pod uses the default (with `AutomountServiceAccountToken: false`). | Each workspace needs its own IAM role; sharing a single SA would violate least-privilege. |
| DQ4 | ZeroTier: should we support ZeroTier's managed routes (auto-configure) or require manual IP assignment? | **Managed routes.** ZeroTier's `managed routes` feature automatically configures routes on the sidecar when `allowManaged=1` is set. The default config should enable this. | Simpler UX. Users just provide network ID + token. |
| DQ5 | WireGuard: should the sidecar support dynamic peer updates (wg-quick vs wg set)? | **Static config only (wg-quick).** The config Secret is reloaded on pod restart. Dynamic updates add complexity with minimal benefit for the workspace use case. | KISS. If the user needs to change WireGuard peers, they update the Secret and bump `restartGeneration`. |
| DQ6 | Should VPN sidecars be usable simultaneously? | **No (single VPN at a time).** `spec.vpn` is a single optional config, not a list. If a user needs multiple tunnels, they should use Tailscale (which is an overlay mesh, not a point-to-point tunnel). | One VPN at a time avoids routing conflicts and complexity. |
| DQ7 | What is the routing model for VPN traffic? | **Split tunnel by default.** Only traffic to the VPN's declared subnets goes through the tunnel. The default route (0.0.0.0/0) remains through the pod's primary interface. Users can configure `routeAllTraffic: true` to force all traffic through the VPN. | Split tunnel preserves normal internet access. Full tunnel risks breaking workspace connectivity (e.g., the Tailscale DERP relay for initial connection). |
| DQ8 | Should VPC feature require Tailscale specifically? | **Yes.** VPC connectivity is a Tailscale feature using subnet routers. WireGuard and ZeroTier can also connect to VPCs, but Tailscale is the recommended and best-documented path. | Tailscale is built for this use case (mesh VPN with subnet routing). WireGuard requires manual VPC peer config; ZeroTier requires a network controller in the VPC. |
| DQ9 | How does the controller detect IRSA vs Pod Identity? | **Detection at startup.** The controller checks for the `eks.amazonaws.com/pod-identity` webhook or the `amazonaws.com/eks/pod-identity-agent` mutating webhook. If present, use Pod Identity. Otherwise, assume IRSA. A config flag overrides detection. | Automated detection reduces operator config burden. |
| DQ10 | Should VPN sidecar images be configurable per instance? | **Yes, via admin settings.** `vpn.wireguard.image`, `vpn.tailscale.image`, `vpn.zerotier.image` are admin-configurable with hardened defaults (digest-pinned). | Operators may want to use their own hardened images or mirror to private registries. |

---

## Domains

### Domain 1: Admin Settings
- VPN type toggles and allow-lists
- IAM role pattern allow-list
- VPN sidecar image configuration
- Default VPC config (allowed regions)

### Domain 2: Workspace CRD Extensions
- `spec.vpn` — VPN config (type, configRef, routes, routeAllTraffic)
- `spec.iam` — IAM config (roleARN, mechanism)
- `spec.vpc` — VPC config (region, vpcID, enabled)

### Domain 3: Controller — Pod Builder
- Sidecar container injection per VPN type
- ServiceAccount creation for IAM
- SecurityContext handling (NET_ADMIN for sidecars)
- Volume mounts for VPN config Secrets

### Domain 4: Controller — Network Policy
- VPN UDP port allowances (WireGuard 51820, Tailscale 41641/3478, ZeroTier 9993)
- VPC CIDR allowances when VPC mode is enabled

### Domain 5: Controller — IAM
- ServiceAccount creation with IRSA annotation
- Pod Identity label injection
- `AutomountServiceAccountToken` toggle based on IAM presence

### Domain 6: API Surface
- User endpoints for VPN/VPC/IAM config on workspaces
- Admin endpoints overlap with existing settings API
- Validation webhook checks against admin settings

### Domain 7: Observability
- VPN sidecar health and connectivity status
- IAM role credential expiry monitoring (via IRSA token expiry)
- Metrics for VPN connection state per workspace

---

## Scope

### In Scope

| # | What | Domain |
|---|---|---|
| 1 | Admin settings for VPN types, IAM role patterns, sidecar images | Admin Settings |
| 2 | Admin settings for VPC (allowed regions, default config) | Admin Settings |
| 3 | CRD fields for VPN config (type, configRef, routes, routeAllTraffic) | Workspace CRD |
| 4 | CRD fields for IAM config (roleARN) | Workspace CRD |
| 5 | CRD fields for VPC config (region, vpcID, enabled) | Workspace CRD |
| 6 | WireGuard sidecar injection (linuxserver/wireguard image, config from Secret) | Pod Builder |
| 7 | Tailscale sidecar injection (tailscale/tailscale image, auth key from Secret) | Pod Builder |
| 8 | ZeroTier sidecar injection (zerotier/zerotier image, network ID + token from Secret) | Pod Builder |
| 9 | Split tunnel routing (default) + full tunnel option | Pod Builder |
| 10 | IRSA: per-workspace ServiceAccount with `eks.amazonaws.com/role-arn` annotation | IAM |
| 11 | EKS Pod Identity: label-based pod identity injection | IAM |
| 12 | `AutomountServiceAccountToken` management (on with IAM, off without) | IAM / Pod Builder |
| 13 | NetworkPolicy: allow WireGuard UDP 51820 when VPN enabled | Network Policy |
| 14 | NetworkPolicy: allow Tailscale UDP 41641, 3478 when VPN enabled | Network Policy |
| 15 | NetworkPolicy: allow ZeroTier UDP 9993 when VPN enabled | Network Policy |
| 16 | NetworkPolicy: allow VPC CIDR ranges when VPC configured | Network Policy |
| 17 | Validation webhook: reject VPN types not in admin allow-list | Admin Settings |
| 18 | Validation webhook: reject IAM roles not matching admin-approved patterns | Admin Settings |
| 19 | API endpoints: `PUT /workspaces/:id/vpn`, `PUT /workspaces/:id/iam`, `PUT /workspaces/:id/vpc` | API Surface |
| 20 | CRD schema update + deepcopy regeneration | Workspace CRD |
| 21 | DB migration for new API-side config storage (if needed) | API Surface |
| 22 | VPN sidecar health: controller checks sidecar container readiness | Observability |
| 23 | Status fields: `vpnConnected`, `vpcConnected`, `iamRoleARN` in WorkspaceStatus | Workspace CRD |
| 24 | Frontend: VPN/VPC/IAM config sections in workspace settings | Frontend |

### Out of Scope

| # | What | Why |
|---|---|---|
| 1 | Raw IPsec (strongSwan/libreswan) | Complex, attack surface, compliance-only use case. WireGuard + Tailscale cover 95% of needs. |
| 2 | VPC peering at the K8s CNI level | Infrastructure-level concern; Tailscale subnet routing is the supported path. |
| 3 | Multi-VPN (simultaneous WireGuard + Tailscale) | Single VPN at a time; routing conflicts otherwise. |
| 4 | VPN credential management (generating keys, managing certs) | BYO config only. Platform stores and mounts but never generates. |
| 5 | kube2iam or kiam support | EKS IRSA and Pod Identity cover AWS. Non-EKS clusters can use IRSA with OIDC. Other providers out of scope. |
| 6 | Azure/GCP IAM | This epic is AWS-specific. Cloud-agnostic IAM is a future epic. |
| 7 | VPN connection status monitoring (latency, throughput) | Beyond MVP. Health check is binary (connected/not connected). |
| 8 | WireGuard dynamic peer updates via `wg set` | Static config only; restart to change peers. |

---

## CRD Changes

### New fields on `WorkspaceSpec`

```go
// WorkspaceVPNConfig defines VPN attachment for a workspace.
type WorkspaceVPNConfig struct {
    // Type is the VPN provider: "wireguard", "tailscale", "zerotier".
    // +kubebuilder:validation:Enum=wireguard;tailscale;zerotier
    Type string `json:"type"`

    // ConfigRef is a K8s Secret reference containing provider-specific config:
    //   wireguard: wg0.conf (WireGuard config file)
    //   tailscale: authkey  (TS auth key, one-off or reusable)
    //   zerotier:  networkid (ZeroTier network ID) + token (API token)
    ConfigRef string `json:"configRef"`

    // RouteAllTraffic forces ALL traffic through the VPN tunnel.
    // Default: false (split tunnel — only VPN-declared subnets)
    RouteAllTraffic bool `json:"routeAllTraffic,omitempty"`

    // Routes are additional CIDRs to route through the VPN tunnel
    // (in addition to provider-declared routes).
    Routes []string `json:"routes,omitempty"`
}

// WorkspaceIAMConfig defines AWS IAM role configuration for a workspace.
type WorkspaceIAMConfig struct {
    // RoleARN is the AWS IAM role ARN to assume (e.g. arn:aws:iam::123456789012:role/MyRole).
    // +kubebuilder:validation:Pattern=^arn:aws:iam::\d{12}:role/.+$
    RoleARN string `json:"roleARN"`
}

// WorkspaceVPCConfig defines VPC connectivity for a workspace.
// VPC connectivity is achieved via Tailscale subnet routing —
// the Tailscale sidecar connects to a tailnet that has a subnet
// router in the target VPC.
type WorkspaceVPCConfig struct {
    // Enabled enables VPC connectivity for this workspace.
    // Requires spec.vpn.type == "tailscale".
    Enabled bool `json:"enabled"`

    // Region is the AWS region where the target VPC resides (e.g. "us-east-1").
    Region string `json:"region,omitempty"`

    // VPCID is the target VPC ID (e.g. "vpc-0123456789abcdef0").
    VPCID string `json:"vpcId,omitempty"`
}

// Extended WorkspaceSpec with new optional fields.
type WorkspaceSpec struct {
    // ... existing fields ...

    // VPN configures a VPN sidecar for the workspace.
    VPN *WorkspaceVPNConfig `json:"vpn,omitempty"`

    // IAM configures AWS IAM role for the workspace.
    IAM *WorkspaceIAMConfig `json:"iam,omitempty"`

    // VPC configures VPC connectivity for the workspace.
    VPC *WorkspaceVPCConfig `json:"vpc,omitempty"`
}
```

### New fields on `WorkspaceStatus`

```go
type WorkspaceStatus struct {
    // ... existing fields ...

    // VPNConnected indicates whether the VPN sidecar has established a connection.
    VPNConnected bool `json:"vpnConnected,omitempty"`

    // VPCConnected indicates whether VPC connectivity (via Tailscale) is active.
    VPCConnected bool `json:"vpcConnected,omitempty"`

    // IAMRoleARN is the actual IAM role ARN configured (mirrored from spec for status display).
    IAMRoleARN string `json:"iamRoleARN,omitempty"`
}
```

### New ServiceAccount for IAM

When `spec.iam.roleARN` is set, the controller creates a ServiceAccount:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: workspace-iam-<workspaceName>
  namespace: <workspace-namespace>
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/MyRole"
    # OR (for EKS Pod Identity):
    # eks.amazonaws.com/audience: "sts.amazonaws.com"
```

### New Conditions

```go
const (
    WorkspaceConditionVPNConnected WorkspaceConditionType = "VPNConnected"
    WorkspaceConditionVPCConnected WorkspaceConditionType = "VPCConnected"
    WorkspaceConditionIAMReady    WorkspaceConditionType = "IAMReady"
)
```

---

## Admin Settings (New Tier 2)

Increment `SchemaVersion` to 2. Add to `InstanceSettings()` in `pkg/settings/schema.go`:

| Key | Default | Type | Valid Values | Category | Description |
|-----|---------|------|-------------|----------|-------------|
| `vpn.enabled` | `false` | bool | | VPN | Master toggle for all VPN features |
| `vpn.allowedTypes` | `["tailscale"]` | strings | `wireguard`, `tailscale`, `zerotier` | VPN | VPN providers users can enable |
| `vpn.allowUserManagedConfig` | `false` | bool | | VPN | Users can bring their own VPN config |
| `vpn.wireguard.image` | `ghcr.io/lenaxia/sidecars/wireguard@sha256:...` | string | container image ref | VPN | WireGuard sidecar image |
| `vpn.tailscale.image` | `ghcr.io/lenaxia/sidecars/tailscale@sha256:...` | string | container image ref | VPN | Tailscale sidecar image |
| `vpn.zerotier.image` | `ghcr.io/lenaxia/sidecars/zerotier@sha256:...` | string | container image ref | VPN | ZeroTier sidecar image |
| `vpn.wireguard.port` | `51820` | int | 1024–65535 | VPN | WireGuard UDP port |
| `vpn.tailscale.port` | `41641` | int | 1024–65535 | VPN | Tailscale UDP port |
| `vpn.zerotier.port` | `9993` | int | 1024–65535 | VPN | ZeroTier UDP port |
| `iam.enabled` | `false` | bool | | IAM | Master toggle for IAM role support |
| `iam.allowedRolePatterns` | `["arn:aws:iam::*:role/llmsafespace-*"]` | strings | regex patterns | IAM | IAM role ARNs matching any pattern are allowed |
| `iam.podIdentity.enabled` | `false` | bool | | IAM | Prefer EKS Pod Identity over IRSA |
| `vpc.enabled` | `false` | bool | | VPC | Master toggle for VPC connectivity |
| `vpc.allowedRegions` | `["us-east-1", "us-west-2"]` | strings | AWS region codes | VPC | Regions where VPC connectivity is allowed |

---

## Sidecar Design

### Shared properties (all VPN types)

- **Network namespace**: `nil` for `PodSpec.ShareProcessNamespace` (default — containers share netns but not pidns)
- **Security context**: `NET_ADMIN` capability added, `NET_RAW` added (for raw socket operations like ICMP).
  - `drop: ALL` is REPLACED with `drop: [ALL]` and `add: [NET_ADMIN, NET_RAW]` — we must drop everything then add back
  - Still `runAsNonRoot: true`, `readOnlyRootFilesystem: true`
- **Resource requests**: 100m CPU, 64Mi memory (sidecars are lightweight)
- **Resource limits**: 500m CPU, 256Mi memory
- **Readiness probe**: HTTP endpoint exposed by the sidecar or custom script
- **Config volume**: Secret volume mount from `spec.vpn.configRef`
- **Init container order**: VPN sidecar has NO init container dependency. The VPN config Secret is mounted and the sidecar starts in parallel with the workspace container. The workspace container's readiness probe may fail until the VPN is up (acceptable — agentd health endpoint doesn't depend on VPN).
- **Restart policy**: If the VPN sidecar crashes, the pod restarts (sidecar restart policy is part of pod lifecycle). User bumps `restartGeneration` after fixing config.

### WireGuard sidecar

```go
func (r *WorkspaceReconciler) buildWireGuardSidecar(ws *v1.Workspace) corev1.Container {
    return corev1.Container{
        Name:  "wireguard",
        Image: r.getSidecarImage("vpn.wireguard.image"), // from admin settings
        Command: []string{"/bin/sh", "-c", `
            mkdir -p /etc/wireguard
            cp /vpn-config/wg0.conf /etc/wireguard/wg0.conf
            chmod 600 /etc/wireguard/wg0.conf
            wg-quick up wg0
            # Keep container alive
            while true; do sleep 3600; done
        `},
        SecurityContext: &corev1.SecurityContext{
            ReadOnlyRootFilesystem:   &falseVal, // needs /etc/wireguard writable
            RunAsNonRoot:             &trueVal,
            AllowPrivilegeEscalation: &trueVal, // needs NET_ADMIN privilege escalation
            Capabilities: &corev1.Capabilities{
                Drop: []corev1.Capability{"ALL"},
                Add:  []corev1.Capability{"NET_ADMIN", "NET_RAW"},
            },
        },
        VolumeMounts: []corev1.VolumeMount{
            {Name: "vpn-config", MountPath: "/vpn-config", ReadOnly: true},
        },
        ReadinessProbe: &corev1.Probe{
            ProbeHandler: corev1.ProbeHandler{
                Exec: &corev1.ExecAction{
                    Command: []string{"wg", "show", "wg0"},
                },
            },
            InitialDelaySeconds: 5, PeriodSeconds: 15, TimeoutSeconds: 5, FailureThreshold: 3,
        },
        Resources: vpnSidecarResourceRequirements(),
    }
}
```

### Tailscale sidecar

```go
func (r *WorkspaceReconciler) buildTailscaleSidecar(ws *v1.Workspace) corev1.Container {
    hostname := fmt.Sprintf("ws-%s", ws.Name)
    acceptRoutes := "true" // needed for VPC subnet routing

    return corev1.Container{
        Name:  "tailscale",
        Image: r.getSidecarImage("vpn.tailscale.image"),
        Env: []corev1.EnvVar{
            {Name: "TS_AUTHKEY", ValueFrom: &corev1.EnvVarSource{
                SecretKeyRef: &corev1.SecretKeySelector{
                    LocalObjectReference: corev1.LocalObjectReference{
                        Name: ws.Spec.VPN.ConfigRef,
                    },
                    Key: "authkey",
                },
            }},
            {Name: "TS_HOSTNAME", Value: hostname},
            {Name: "TS_ACCEPT_ROUTES", Value: acceptRoutes},
            {Name: "TS_ROUTES", Value: vpnRoutesString(ws)}, // VPC CIDRs + user routes
            {Name: "TS_USERSPACE", Value: "false"}, // kernel mode (requires NET_ADMIN)
            {Name: "TS_STATE_DIR", Value: "/var/lib/tailscale"},
        },
        VolumeMounts: []corev1.VolumeMount{
            {Name: "tailscale-state", MountPath: "/var/lib/tailscale"},
        },
        SecurityContext: &corev1.SecurityContext{
            ReadOnlyRootFilesystem:   &falseVal,
            RunAsNonRoot:             &trueVal,
            AllowPrivilegeEscalation: &trueVal,
            Capabilities: &corev1.Capabilities{
                Drop: []corev1.Capability{"ALL"},
                Add:  []corev1.Capability{"NET_ADMIN", "NET_RAW"},
            },
        },
        ReadinessProbe: &corev1.Probe{
            ProbeHandler: corev1.ProbeHandler{
                Exec: &corev1.ExecAction{
                    Command: []string{"tailscale", "status"},
                },
            },
            InitialDelaySeconds: 5, PeriodSeconds: 15, TimeoutSeconds: 5, FailureThreshold: 6,
        },
        Resources: vpnSidecarResourceRequirements(),
    }
}
```

### ZeroTier sidecar

```go
func (r *WorkspaceReconciler) buildZeroTierSidecar(ws *v1.Workspace) corev1.Container {
    return corev1.Container{
        Name:  "zerotier",
        Image: r.getSidecarImage("vpn.zerotier.image"),
        Env: []corev1.EnvVar{
            {Name: "ZT_NETWORK", ValueFrom: &corev1.EnvVarSource{
                SecretKeyRef: &corev1.SecretKeySelector{
                    LocalObjectReference: corev1.LocalObjectReference{
                        Name: ws.Spec.VPN.ConfigRef,
                    },
                    Key: "networkid",
                },
            }},
            {Name: "ZT_API_TOKEN", ValueFrom: &corev1.EnvVarSource{
                SecretKeyRef: &corev1.SecretKeySelector{
                    LocalObjectReference: corev1.LocalObjectReference{
                        Name: ws.Spec.VPN.ConfigRef,
                    },
                    Key: "token",
                },
            }},
            {Name: "ZT_ALLOW_MANAGED", Value: "1"},
        },
        SecurityContext: &corev1.SecurityContext{
            ReadOnlyRootFilesystem:   &falseVal,
            RunAsNonRoot:             &trueVal,
            AllowPrivilegeEscalation: &trueVal,
            Capabilities: &corev1.Capabilities{
                Drop: []corev1.Capability{"ALL"},
                Add:  []corev1.Capability{"NET_ADMIN", "NET_RAW"},
            },
        },
        ReadinessProbe: &corev1.Probe{
            ProbeHandler: corev1.ProbeHandler{
                Exec: &corev1.ExecAction{
                    Command: []string{"zerotier-cli", "listnetworks"},
                },
            },
            InitialDelaySeconds: 10, PeriodSeconds: 15, TimeoutSeconds: 5, FailureThreshold: 6,
        },
        Resources: vpnSidecarResourceRequirements(),
    }
}
```

### Pod spec changes

When any of `spec.vpn`, `spec.iam`, `spec.vpc` are set:

```go
// Modified pod construction in buildPod():
func (r *WorkspaceReconciler) buildPod(ctx context.Context, workspace *v1.Workspace) (*corev1.Pod, error) {
    // ... existing init container and main container logic ...

    containers := []corev1.Container{mainContainer}
    volumes := // ... existing volumes ...

    // Add VPN sidecar container.
    if workspace.Spec.VPN != nil && workspace.Spec.VPN.ConfigRef != "" {
        sidecar, vol, err := r.buildVPNSidecar(workspace)
        if err != nil {
            return nil, fmt.Errorf("building VPN sidecar: %w", err)
        }
        containers = append(containers, sidecar)
        volumes = append(volumes, *vol)
    }

    // Determine automount and service account.
    automountSA := false
    var serviceAccountName string
    if workspace.Spec.IAM != nil && workspace.Spec.IAM.RoleARN != "" {
        automountSA = true
        serviceAccountName = fmt.Sprintf("workspace-iam-%s", workspace.Name)
        if err := r.ensureIAMServiceAccount(ctx, workspace); err != nil {
            return nil, fmt.Errorf("ensuring IAM service account: %w", err)
        }
    }

    pod := &corev1.Pod{
        // ... existing fields ...
        Spec: corev1.PodSpec{
            InitContainers:        initContainers,
            Containers:            containers,
            Volumes:               volumes,
            AutomountServiceAccountToken: &automountSA,
            ServiceAccountName:    serviceAccountName,
            // ... remaining fields ...
        },
    }
    return pod, nil
}
```

---

## IAM Implementation

### IRSA Path

1. User sets `spec.iam.roleARN: "arn:aws:iam::123456789012:role/MyRole"`
2. Controller creates/normalizes a ServiceAccount `workspace-iam-<name>` with annotation `eks.amazonaws.com/role-arn`
3. Controller sets `spec.serviceAccountName` and `automountServiceAccountToken: true` on the pod
4. The EKS OIDC provider and `amazon-eks-pod-identity-webhook` mutate the pod to project the AWS credentials
5. AWS SDKs in the workspace container automatically assume the role

### Pod Identity Path

1. Same user-facing `spec.iam.roleARN` field
2. Controller detects Pod Identity webhook at startup (via config flag or auto-detection)
3. Controller adds label `eks.amazonaws.com/pod-identity: "true"` or configures a `PodIdentityAssociation` resource
4. The Pod Identity Agent injects credentials into the pod
5. No ServiceAccount changes needed (simpler than IRSA)

### IAM ServiceAccount reconciliation

```go
func (r *WorkspaceReconciler) ensureIAMServiceAccount(ctx context.Context, ws *v1.Workspace) error {
    name := fmt.Sprintf("workspace-iam-%s", ws.Name)
    sa := &corev1.ServiceAccount{
        ObjectMeta: metav1.ObjectMeta{
            Name:      name,
            Namespace: ws.Namespace,
            Annotations: map[string]string{
                "eks.amazonaws.com/role-arn": ws.Spec.IAM.RoleARN,
            },
        },
    }
    // Set controller reference for GC.
    if err := controllerutil.SetControllerReference(ws, sa, r.Scheme); err != nil {
        return err
    }
    // Create-or-update.
    existing := &corev1.ServiceAccount{}
    if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: ws.Namespace}, existing); err != nil {
        if apierrors.IsNotFound(err) {
            return r.Create(ctx, sa)
        }
        return err
    }
    existing.Annotations = sa.Annotations
    return r.Update(ctx, existing)
}
```

---

## Network Policy Changes

### VPN UDP port allowances

When `spec.vpn` is set, the controller adds UDP egress rules to the per-workspace NetworkPolicy:

```go
func (r *WorkspaceReconciler) buildVPNNetworkPolicyRules(ws *v1.Workspace) []networkingv1.NetworkPolicyEgressRule {
    if ws.Spec.VPN == nil {
        return nil
    }
    udp := corev1.ProtocolUDP
    var rules []networkingv1.NetworkPolicyEgressRule

    switch ws.Spec.VPN.Type {
    case "wireguard":
        port := intstr.FromInt(r.getVPNAppPort("vpn.wireguard.port", 51820))
        rules = append(rules, networkingv1.NetworkPolicyEgressRule{
            Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &port}},
        })
    case "tailscale":
        port1 := intstr.FromInt(r.getVPNAppPort("vpn.tailscale.port", 41641))
        port2 := intstr.FromInt(3478) // STUN
        rules = append(rules, networkingv1.NetworkPolicyEgressRule{
            Ports: []networkingv1.NetworkPolicyPort{
                {Protocol: &udp, Port: &port1},
                {Protocol: &udp, Port: &port2},
            },
        })
    case "zerotier":
        port := intstr.FromInt(r.getVPNAppPort("vpn.zerotier.port", 9993))
        rules = append(rules, networkingv1.NetworkPolicyEgressRule{
            Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &port}},
        })
    }
    return rules
}
```

### VPC CIDR allowances

When `spec.vpc.enabled` is true and an admin-approved region+VPCID is set, the controller adds egress rules for the VPC CIDRs. The VPC CIDRs are resolved from the `spec.vpc.vpcID` via AWS API (or can be user-provided as `spec.vpc.cidrs`).

Since the controller may not have AWS API access, VPC CIDRs are user-declared in the CRD or resolved via an optional AWS client. The initial implementation uses user-declared CIDRs validated by the admin settings.

---

## API Surface

### User endpoints

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/api/v1/workspaces/:id/vpn` | Set/update VPN config |
| `DELETE` | `/api/v1/workspaces/:id/vpn` | Remove VPN config |
| `GET` | `/api/v1/workspaces/:id/vpn` | Get current VPN config |
| `PUT` | `/api/v1/workspaces/:id/iam` | Set IAM role ARN |
| `DELETE` | `/api/v1/workspaces/:id/iam` | Remove IAM role |
| `GET` | `/api/v1/workspaces/:id/iam` | Get current IAM config |
| `PUT` | `/api/v1/workspaces/:id/vpc` | Set VPC config |
| `DELETE` | `/api/v1/workspaces/:id/vpc` | Remove VPC config |
| `GET` | `/api/v1/workspaces/:id/vpc` | Get current VPC config |

All endpoints are gated by admin settings:
- VPN endpoints return 403 if `vpn.enabled` is false or the requested type not in `vpn.allowedTypes`
- IAM endpoints return 403 if `iam.enabled` is false or the role ARN doesn't match `iam.allowedRolePatterns`
- VPC endpoints return 403 if `vpc.enabled` is false or the region not in `vpc.allowedRegions`

### Workspace status endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/workspaces/:id/status` | Includes `vpnConnected`, `vpcConnected`, `iamRoleARN` status fields |

---

## User Stories

| Story | Title | Domain | Depends On | Key Acceptance Criteria |
|---|---|---|---|---|
| US-30.1 | Admin settings for VPN, IAM, VPC | Admin Settings | US-9.2 | New settings defined in `InstanceSettings()`. Validated by schema. Accessible via existing `PUT /admin/settings/:key`. |
| US-30.2 | CRD schema: VPN, IAM, VPC fields on WorkspaceSpec | CRD | US-30.1 | New fields on `WorkspaceSpec`. Deepcopy regenerated. CRD YAML updated. Webhook validates against admin settings. |
| US-30.3 | WireGuard sidecar injection in pod builder | Pod Builder | US-30.2 | `buildPod` adds WireGuard sidecar when `spec.vpn.type == "wireguard"`. Config mounted from Secret. `NET_ADMIN` in security context. Readiness probe checks `wg show wg0`. |
| US-30.4 | Tailscale sidecar injection in pod builder | Pod Builder | US-30.2 | `buildPod` adds Tailscale sidecar when `spec.vpn.type == "tailscale"`. Auth key from Secret. `TS_ACCEPT_ROUTES=true` for VPC subnet routing. Readiness probe checks `tailscale status`. |
| US-30.5 | ZeroTier sidecar injection in pod builder | Pod Builder | US-30.2 | `buildPod` adds ZeroTier sidecar when `spec.vpn.type == "zerotier"`. Network ID + token from Secret. Readiness probe checks `zerotier-cli listnetworks`. |
| US-30.6 | IAM: IRSA ServiceAccount reconciliation | IAM | US-30.2 | Controller creates ServiceAccount with `eks.amazonaws.com/role-arn` annotation. Pod references it with `automountServiceAccountToken: true`. SA garbage-collected with workspace. |
| US-30.7 | IAM: EKS Pod Identity support | IAM | US-30.2 | Controller detects Pod Identity mode. Adds labels/config for Pod Identity Agent. Same user-facing API as IRSA. |
| US-30.8 | IAM: `AutomountServiceAccountToken` management | IAM | US-30.6 | `automountServiceAccountToken: true` when IAM configured. `false` otherwise. No token leak for non-IAM workspaces. |
| US-30.9 | NetworkPolicy: VPN UDP port allowances | Network Policy | US-30.2 | Per-workspace egress NetPol includes UDP rules for WireGuard/Tailscale/ZeroTier ports when VPN is configured. Rules removed when VPN is removed. |
| US-30.10 | NetworkPolicy: VPC CIDR allowances | Network Policy | US-30.2 | Per-workspace egress NetPol includes TCP rules for VPC CIDRs when VPC is configured. CIDRs validated against admin region allow-list. |
| US-30.11 | Validation webhook: admin policy enforcement | Admin Settings | US-30.2 | Webhook rejects VPN types not in `vpn.allowedTypes`. Rejects IAM roles not matching `iam.allowedRolePatterns`. Rejects VPC regions not in `vpc.allowedRegions`. Rejects VPC without Tailscale VPN. |
| US-30.12 | Validation webhook: VPN config Secret validation | Admin Settings | US-30.2 | Webhook checks that `spec.vpn.configRef` Secret exists and contains required keys (`wg0.conf` for wireguard, `authkey` for tailscale, `networkid`+`token` for zerotier). |
| US-30.13 | API endpoints for VPN/IAM/VPC config | API Surface | US-30.2 | PUT/DELETE/GET endpoints for each config type. 403 when admin-gated. Validation mirrors webhook. |
| US-30.14 | Status fields: VPN/VPC/IAM connectivity | Workspace CRD | US-30.2 | Controller sets `vpnConnected`/`vpcConnected` based on sidecar readiness. `iamRoleARN` mirrors spec. Conditions set on CRD. |
| US-30.15 | Split-tunnel routing | Pod Builder | US-30.3 | Default: only VPN-declared subnets go through tunnel. `routeAllTraffic: true` adds default route through VPN. Tailscale `TS_ROUTES` set from VPC CIDRs + user routes. |
| US-30.16 | VPC connectivity via Tailscale subnet routing | VPC | US-30.4, US-30.15 | When `spec.vpc.enabled` + `spec.vpn.type == "tailscale"`: sidecar advertises VPC routes. Controller adds VPC CIDRs to `TS_ROUTES`. Pod can reach VPC resources by private IP. |
| US-30.17 | Frontend: VPN/IAM/VPC config in workspace settings | Frontend | US-30.13 | Schema-driven config forms for VPN type/Secret, IAM role ARN, VPC config. Admin settings read-only for non-admin. |
| US-30.18 | Frontend: VPN/VPC status indicators | Frontend | US-30.14 | Icons/badges in workspace list and detail view showing VPN/VPC/IAM connection status. |

### Dependency Graph

```
US-30.1 (admin settings) ──┬── US-30.2 (CRD schema) ──┬── US-30.3 (WireGuard)
                            │                            ├── US-30.4 (Tailscale)
                            │                            ├── US-30.5 (ZeroTier)
                            │                            ├── US-30.6 (IRSA)
                            │                            ├── US-30.7 (Pod Identity)
                            │                            ├── US-30.9 (NetPol: VPN)
                            │                            ├── US-30.10 (NetPol: VPC)
                            │                            ├── US-30.11 (webhook: admin policy)
                            │                            ├── US-30.12 (webhook: config validation)
                            │                            ├── US-30.13 (API)
                            │                            ├── US-30.14 (status fields)
                            │                            └── US-30.15 (routing)
                            │
                            ├── US-30.16 (VPC) ── depends on US-30.4 + US-30.15
                            ├── US-30.17 (frontend config) ── depends on US-30.13
                            └── US-30.18 (frontend status) ── depends on US-30.14

US-30.8 (automount toggle) ── depends on US-30.6
```

### Critical Path

```
Phase 1: US-30.1 → US-30.2 → US-30.3 + US-30.4 (core sidecar injection)
Phase 2: US-30.6 + US-30.7 + US-30.8 (IAM — high value, independent of VPN)
Phase 3: US-30.5 (ZeroTier — lower priority, can ship after WireGuard + Tailscale)
Phase 4: US-30.9 + US-30.10 (NetPol integration)
Phase 5: US-30.11 + US-30.12 (webhook enforcement)
Phase 6: US-30.13 + US-30.14 (API + status)
Phase 7: US-30.15 + US-30.16 (VPC routing)
Phase 8: US-30.17 + US-30.18 (frontend)
```

---

## Test Plan

### Unit Tests

| Story | Test | What It Proves |
|---|---|---|
| US-30.1 | `TestVPNSettings_Defaults` | Default VPN settings are sane (enabled=false, allowed=[tailscale]) |
| US-30.1 | `TestIAMSettings_Defaults` | Default IAM settings are sane (enabled=false, allowedPattern=[...]) |
| US-30.2 | `TestWorkspaceSpec_VPNFields` | VPN fields on WorkspaceSpec marshal/unmarshal correctly |
| US-30.2 | `TestWorkspaceSpec_IAMFields` | IAM fields on WorkspaceSpec marshal/unmarshal correctly |
| US-30.2 | `TestWorkspaceSpec_VPCFields` | VPC fields on WorkspaceSpec marshal/unmarshal correctly |
| US-30.3 | `TestBuildPod_WireGuardSidecar` | WireGuard sidecar added when spec.vpn.type=wireguard |
| US-30.3 | `TestBuildPod_WireGuard_NoConfigRef_Error` | Missing configRef returns error |
| US-30.3 | `TestBuildPod_WireGuard_SecurityContext` | NET_ADMIN, NET_RAW added; runAsNonRoot true |
| US-30.4 | `TestBuildPod_TailscaleSidecar` | Tailscale sidecar added with proper env vars |
| US-30.4 | `TestBuildPod_Tailscale_TS_ROUTES` | TS_ROUTES includes VPC CIDRs when VPC enabled |
| US-30.5 | `TestBuildPod_ZeroTierSidecar` | ZeroTier sidecar added with proper env vars |
| US-30.6 | `TestEnsureIAMServiceAccount_Creates` | ServiceAccount created with correct annotation |
| US-30.6 | `TestEnsureIAMServiceAccount_Updates` | Existing SA updated on roleARN change |
| US-30.6 | `TestEnsureIAMServiceAccount_GC` | SA deleted with workspace (owner reference) |
| US-30.7 | `TestPodIdentity_Detection` | Controller detects Pod Identity webhook |
| US-30.8 | `TestAutomount_WithIAM` | automount=true when IAM configured |
| US-30.8 | `TestAutomount_WithoutIAM` | automount=false when IAM not configured |
| US-30.9 | `TestNetworkPolicy_VPNPorts_WireGuard` | NetPol includes UDP 51820 when WireGuard configured |
| US-30.9 | `TestNetworkPolicy_VPNPorts_Tailscale` | NetPol includes UDP 41641 when Tailscale configured |
| US-30.9 | `TestNetworkPolicy_VPNPorts_ZeroTier` | NetPol includes UDP 9993 when ZeroTier configured |
| US-30.9 | `TestNetworkPolicy_VPNPorts_RemovedOnVPNRemoval` | NetPol cleaned up when VPN removed |
| US-30.10 | `TestNetworkPolicy_VPCCIDRs` | VPC CIDRs added to NetPol when VPC configured |
| US-30.10 | `TestNetworkPolicy_VPCCIDRs_RegionNotAllowed` | VPC rejected if region not in vpc.allowedRegions |
| US-30.11 | `TestWebhook_VPNType_NotAllowed` | webhook rejects VPN type not in admin allow-list |
| US-30.11 | `TestWebhook_IAMRole_NotMatchingPattern` | webhook rejects IAM role not matching pattern |
| US-30.11 | `TestWebhook_VPCRegion_NotAllowed` | webhook rejects VPC region not in allowed list |
| US-30.11 | `TestWebhook_VPCWithoutTailscale_Rejected` | webhook rejects VPC without Tailscale VPN |
| US-30.12 | `TestWebhook_VPNSecret_MissingConfigRef` | webhook rejects missing configRef Secret |
| US-30.12 | `TestWebhook_VPNSecret_WrongKeys` | webhook rejects Secret without required keys |
| US-30.14 | `TestStatus_VPNConnected` | vpnConnected set based on sidecar readiness |
| US-30.14 | `TestStatus_VPCConnected` | vpcConnected set based on sidecar + VPC config |
| US-30.15 | `TestSplitTunnel_Default` | Only VPN subnets routed through tunnel |
| US-30.15 | `TestSplitTunnel_RouteAll` | All traffic routed through tunnel when routeAllTraffic=true |

### Integration Tests (envtest)

| # | Test | What It Proves |
|---|---|---|
| I1 | Create workspace with WireGuard config → pod has sidecar, NetPol includes UDP 51820 | Sidecar injection + NetPol work end-to-end |
| I2 | Create workspace with Tailscale config → pod has sidecar with auth key from Secret | Tailscale sidecar wired correctly |
| I3 | Create workspace with IAM role → ServiceAccount created with annotation | IRSA integration works |
| I4 | Create workspace with VPC + Tailscale → TS_ROUTES has VPC CIDRs | VPC routing configured |
| I5 | Update workspace VPN type → old NetPol cleaned up, new one created | NetPol lifecycle correct |
| I6 | Workspace with VPN deleted → NetPol, sidecar, config volumes cleaned up | Full GC on delete |
| I7 | Webhook rejects VPN type not in admin settings → CREATE fails | Admin policy enforced at CRD level |
| I8 | Webhook rejects IAM role not matching pattern → CREATE fails | IAM policy enforced |
| I9 | Webhook rejects VPC region not in allow-list → CREATE fails | VPC policy enforced |
| I10 | Disable VPN master toggle → existing workspaces unaffected, new ones rejected | Toggle doesn't break running workspaces |

### E2E Tests (real cluster or kind)

| # | Test | What It Proves |
|---|---|---|
| E1 | Deploy workspace with WireGuard config → VPN sidecar connects → agent can reach resources through tunnel | WireGuard works end-to-end |
| E2 | Deploy workspace with Tailscale config → sidecar connects to tailnet → agent can reach tailnet nodes | Tailscale works end-to-end |
| E3 | Deploy workspace with IAM role → `aws sts get-caller-identity` from workspace returns the role | IRSA works end-to-end |
| E4 | Deploy workspace with VPC + Tailscale → agent can reach RDS instance by private IP in VPC | VPC connectivity works |

---

## Observability: New Metrics

```go
// Gauges
llmsafespace_workspace_vpn_enabled_total{vpn_type="wireguard|tailscale|zerotier"}
llmsafespace_workspace_vpn_connected_total{vpn_type}
llmsafespace_workspace_iam_enabled_total
llmsafespace_workspace_vpc_enabled_total
llmsafespace_workspace_vpc_connected_total

// Counters
llmsafespace_workspace_vpn_config_error_total{vpn_type, error="invalid_config|secret_not_found|connection_failed"}
llmsafespace_workspace_iam_config_error_total{error="invalid_role|sa_create_failed|webhook_not_ready"}
```

---

## Rollout Plan

### Phase 1: Foundation (US-30.1, US-30.2, US-30.8)
- Admin settings for VPN/IAM/VPC
- CRD schema changes + deepcopy
- `AutomountServiceAccountToken` management
- **Impact**: No user-facing change, enables all downstream stories

### Phase 2: Core VPN (US-30.3, US-30.4, US-30.15)
- WireGuard and Tailscale sidecar injection
- Split-tunnel routing
- **Impact**: Users can attach VPNs to workspaces (admin-gated)

### Phase 3: IAM (US-30.6, US-30.7)
- IRSA ServiceAccount reconciliation
- EKS Pod Identity support
- **Impact**: Users can grant AWS access to workspaces

### Phase 4: Network Policy (US-30.9, US-30.10)
- VPN port allowances
- VPC CIDR allowances
- **Impact**: Network policies respect VPN/VPC config

### Phase 5: Webhooks + API (US-30.11, US-30.12, US-30.13, US-30.14)
- Validation webhooks
- REST API endpoints
- Status fields
- **Impact**: Full API surface for VPN/IAM/VPC management

### Phase 6: VPC + ZeroTier (US-30.5, US-30.16)
- ZeroTier sidecar (if demand warrants)
- VPC connectivity via Tailscale subnet routing
- **Impact**: VPC connectivity for workspaces

### Phase 7: Frontend (US-30.17, US-30.18)
- UI for VPN/IAM/VPC config
- Status indicators
- **Impact**: Full user experience

---

## Risks

| Risk | Mitigation |
|---|---|
| VPN sidecar with NET_ADMIN could be compromised and used to attack the cluster | Sidecar runs with `runAsNonRoot`, `readOnlyRootFilesystem: false` (needs /etc/wireguard writable). The workspace container itself still has `drop: ALL`. NetworkPolicy limits egress even for the sidecar. Additional mitigation: use separate `securityContext` per container (not pod-level). |
| IAM ServiceAccount token could be exfiltrated from the pod | `automountServiceAccountToken` is false for non-IAM workspaces. For IAM workspaces, the SA token has a 24-hour expiry (IRSA default) and is projected as a time-limited STS credential. Audit logging on SA creation. |
| Tailscale auth key could be stolen from the Secret | Auth keys are one-time-use or reusable with expiry. The Secret is cluster-scoped (not readable from inside the pod without explicit RBAC). The sidecar reads the key at startup. |
| VPN config Secret could leak via logs or snapshot | Secret data is volume-mounted, not passed as env vars (except Tailscale auth key). `pkg/redact` should be updated with rules for VPN config patterns (wireguard private keys, Tailscale auth keys, ZeroTier API tokens). |
| VPC routing conflicts when multiple workspaces target the same VPC | Each workspace gets its own Tailscale identity. Subnet routes are per-identity. No conflict. |
| VPC subnet route overlap with pod network CIDR | Pod network CIDRs are in RFC1918 space. Tailscale subnet routes could overlap. Warn in webhook if VPC CIDR conflicts with known pod CIDR. |
| EKS Pod Identity webhook not installed silently degrades to IRSA | Auto-detection at controller startup. Log warning if detection fails or if configured mode not available. |
| VPN sidecar image with known vulnerabilities | Images pinned by SHA digest in admin settings. Contour-registry scanning is orthogonal. |

---

## Appendix: Config Secret Schemas

### WireGuard Secret (`spec.vpn.configRef`)
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-wireguard-config
type: Opaque
stringData:
  wg0.conf: |
    [Interface]
    PrivateKey = <base64-private-key>
    Address = 10.0.0.2/32
    DNS = 10.0.0.1

    [Peer]
    PublicKey = <base64-public-key>
    Endpoint = vpn.example.com:51820
    AllowedIPs = 10.0.0.0/8, 172.16.0.0/12
```

### Tailscale Secret (`spec.vpn.configRef`)
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-tailscale-config
type: Opaque
stringData:
  authkey: "tskey-auth-xxxxx-xxxxx"
  # Optionally, for OAuth clients:
  # clientid: "..."
  # clientsecret: "..."
```

### ZeroTier Secret (`spec.vpn.configRef`)
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-zerotier-config
type: Opaque
stringData:
  networkid: "8056c2e21c000001"
  token: "xxxxxxxxxxxxxxxxxxxxxxxx"
```
