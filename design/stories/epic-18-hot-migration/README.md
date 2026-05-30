# Epic 18: Hot Migration & RWX Storage

**Status:** Planning
**Author:** mikekao
**Depends On:** Epic 17 (security baseline), Epic 2 (workspaces)
**Target Environment:** EKS + Graviton Spot + EFS + gVisor

---

## Objective

Implement zero-downtime live migration of workspace pods across nodes, enabling:
- Proactive load balancing (move workspaces off hot nodes before users are impacted)
- Spot instance reclamation handling (2-min warning → graceful migration)
- Node maintenance/upgrades without workspace disruption

---

## Key Decisions (from design session 2026-05-30)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage backend | EFS (not EBS/Longhorn) | RWX native, no share-manager pods, cross-AZ, AWS-managed NFS |
| Volume access mode | RWX | Enables ~100ms cutover; both pods mount simultaneously |
| Sandbox runtime | gVisor | Eliminates container-escape risk that RWX would otherwise widen |
| Tenant isolation | Virtual namespaces + EFS access points | API-level isolation + AWS-enforced root directory per workspace |
| Compute | Graviton Spot (80%) + On-Demand baseline (20%) | 60-70% cost savings; Spot reclamation handled by migration system |
| IAM model | One IRSA role per component, cross-account roles for tenant AWS access with external ID | Scales to 1000+ tenants without per-tenant IAM roles |

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│              Migration Controller                          │
│  Triggers: node pressure, Spot warning, manual            │
└──────────┬───────────────────────────────────┬───────────┘
           │ 1. Start target pod                │ 3. Flip proxy
           ▼                                    ▼
┌─────────────────────┐          ┌──────────────────────────┐
│  Source Pod (Node A) │          │  Target Pod (Node B)      │
│  workspace-agentd    │──2.───── │  workspace-agentd         │
│                      │  session │                           │
└────────┬─────────────┘  state  └────────┬──────────────────┘
         │                                 │
         └────────────┬────────────────────┘
                      ▼
           ┌─────────────────────┐
           │   EFS Access Point   │  ← both pods mount simultaneously
           │   /workspace          │     zero data movement
           └─────────────────────┘
```

**Migration sequence:**
1. Start target pod on destination node (mounts same EFS access point)
2. Transfer session state (agentd snapshot → restore, <500ms)
3. Proxy cutover (~100ms — buffer, switch backend, drain)
4. Terminate source pod (background cleanup)

**Total user-visible disruption: ~100ms** (indistinguishable from network jitter)

---

## Security Model

| Layer | Control |
|-------|---------|
| Container escape prevention | gVisor RuntimeClass on all workspace pods |
| Cross-tenant API isolation | Virtual namespaces (vCluster/Capsule) |
| Cross-tenant storage isolation | EFS access points (AWS-enforced root dir + UID/GID) |
| Tenant AWS account access | Cross-account role assumption with per-tenant external ID |
| Migration safety | Lease-based fencing — only one pod writes at a time during overlap window |
| Spot reclamation | Node termination handler → triggers migration for all pods on condemned node |

---

## Stories (to be fleshed out)

### S18.1 — EFS Storage Class & Access Points
- StorageClass with EFS CSI provisioner
- Dynamic access point creation per workspace
- Root directory enforcement: `/tenants/{tenant_id}/workspaces/{workspace_id}`
- PVC created as RWX with EFS access point

### S18.2 — Migration Controller CRD
- `Migration` CRD: spec (workspaceID, targetNode, reason), status (phase, timing)
- Controller watches for migration triggers
- Implements the 4-step migration sequence

### S18.3 — Session State Snapshot/Restore
- `GET /v1/migrate/snapshot` on workspace-agentd (captures session state)
- `POST /v1/migrate/restore` on target agentd (restores sessions)
- State includes: conversation history refs, working directories, env vars

### S18.4 — Proxy Connection Handoff
- Proxy buffers during cutover window
- SSE streams: client reconnects with last-event-id (already supported)
- WebSocket: brief disconnect, auto-reconnect
- SDK: transparent retry with `Retry-After` header

### S18.5 — Spot Reclamation Handler
- Node termination handler (DaemonSet) catches 2-min Spot warning
- Cordons node, triggers Migration for all workspace pods
- Integrates with Karpenter for replacement node provisioning

### S18.6 — Proactive Load Balancing
- Monitor node CPU/memory pressure
- Trigger migration for least-active workspaces on hot nodes
- Configurable thresholds and cooldown periods

### S18.7 — gVisor RuntimeClass
- Deploy gVisor (runsc) on Graviton nodes
- RuntimeClass `gvisor` applied to all workspace pods
- Validate workspace tooling compatibility (Python, Node, Go, Rust, Java)

### S18.8 — Virtual Namespace per Tenant
- Evaluate vCluster vs Capsule for tenant isolation
- Tenant onboarding creates virtual namespace
- Controller scoped to tenant namespace

### S18.9 — Karpenter NodePool Configuration
- `baseline` pool: On-Demand Graviton, system workloads
- `workspaces` pool: Spot Graviton, diversified instance types
- Consolidation policy for cost optimization

---

## Cost Model (500 concurrent workspaces)

| Component | Monthly |
|-----------|---------|
| Compute (Spot 80% + OD 20%) | ~$5,500 |
| EFS (20TB, moderate throughput) | ~$6,000 |
| EKS control plane | $73 |
| NAT Gateway | ~$500 |
| **Total** | **~$12,000/mo** |

---

## Open Questions

1. vCluster vs Capsule — which fits better for 1000+ tenants?
2. gVisor compatibility with mise-installed runtimes (Java specifically)
3. EFS throughput mode (bursting vs provisioned) at 500 concurrent workspaces
4. Session state size — can we keep snapshot <1MB for fast transfer?
5. Migration SLO — what's the maximum acceptable cutover time to commit to?
