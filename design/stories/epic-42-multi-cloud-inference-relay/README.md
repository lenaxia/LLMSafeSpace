# Epic 42: Multi-Cloud Inference Relay

**Status:** Planning
**Created:** 2026-06-13
**Depends on:** Epic 26 (Client-Proxied Inference — CF Worker relay shipped), Epic 32 (VPN sidecar patterns — WireGuard reference)
**Supersedes:** None (extends Epic 26's relay architecture from single-cloudflare to multi-cloud)

---

## Problem Statement

### Current State

Epic 26 deployed a single Cloudflare Worker (`workers/inference-relay/`) as a transparent path-secret-authenticated proxy to `opencode.ai/zen/v1`. The relay distributes free-tier LLM traffic across Cloudflare's 300+ edge POPs, avoiding per-IP throttling from the platform's own server IPs.

This is now **broken in production**: `opencode.ai/zen` is IP-blocking Cloudflare's egress ranges. The relay architecture is correct (worklog `0184` confirmed the `public` key itself is not throttled — a laptop can reach Zen fine), but the Cloudflare IP ranges are blocked. Free-tier inference for all workspace pods is dead until we move the relay off Cloudflare.

```
Workspace Pod (opencode) → relay.safespaces.dev → CF Worker → opencode.ai/zen/v1
                                                              ✗ IP-blocked
```

Additionally, the current relay has no self-healing or rotation:
- Single point of failure — one Worker, one IP range family
- No detection of 429s or IP blocks — failures are silent (opencode sees 429, user sees error)
- No automated IP rotation — operator must manually deploy a new Worker and update DNS
- No health monitoring — the controller has no idea if the relay is alive

### Desired State

A **portable relay binary** that runs on OCI and GCP free-tier VMs, connected to the cluster via **WireGuard tunnels**, fronted by an **in-cluster router** that handles sticky session routing, failover, and 429 detection. A **relay controller** (CRD + reconciler) manages the full lifecycle of relay VMs — provisioning, health-checking, IP rotation, and replacement.

The controller maintains **exactly 1 OCI VM and 1 GCP VM** at all times. The router distributes workspace traffic across both with deterministic stickiness, failing over instantly when one goes down.

```
                                  WireGuard mesh (10.42.42.0/24)
  ┌──────────────────────────────────────────────────────────────────────┐
  │                        LLMSafeSpace Cluster                          │
  │                                                                      │
  │  ┌──────────────┐         ┌────────────────────┐                     │
  │  │ Workspace     │  HTTP   │ relay-router        │    wg0: 10.42.42.1 │
  │  │ Pods          │────────→│ (Deployment, 2 rep) │────────────────────┼──┐
  │  │               │         │                     │                    │  │
  │  │ INFERENCE_    │         │ sticky: hash(wsID)  │                    │  │
  │  │ RELAY_BASEURL │         │   % healthyRelays   │                    │  │
  │  │ → router svc  │         │                     │                    │  │
  │  └──────────────┘         │ 429 detection       │                    │  │
  │                            │ drain + failover    │                    │  │
  │                            └────────────────────┘                    │  │
  │                                                                      │  │
  │  ┌──────────────────────────────────────────────────┐               │  │
  │  │ InferenceRelay Controller                         │               │  │
  │  │  - provisions OCI + GCP VMs                       │               │  │
  │  │  - generates WG keypairs, embeds in cloud-init    │               │  │
  │  │  - health-checks VMs over WG                      │               │  │
  │  │  - destroys + recreates on failure/429            │               │  │
  │  │  - pushes healthy relay IPs to router via CRD     │               │  │
  │  └──────────────────────────────────────────────────┘               │  │
  └──────────────────────────────────────────────────────────────────────┘  │
                              │                                              │
                    ┌─────────┴─────────┐                                   │
                    │                   │                                   │
               encrypted UDP       encrypted UDP                             │
                    │                   │                                   │
          ┌─────────┴─────┐   ┌─────────┴─────┐                              │
          │ OCI A1 VM     │   │ GCP e2-micro  │                              │
          │ wg0:10.42.42.2│   │ wg0:10.42.42.3│                              │
          │ relay:8080    │   │ relay:8080    │                              │
          │ (plain HTTP,  │   │ (plain HTTP,  │                              │
          │  WG-only, no  │   │  WG-only, no  │                              │
          │  auth code)   │   │  auth code)   │                              │
          └───────┬───────┘   └───────┬───────┘                              │
                  │                   │                                      │
                  └───────┬───────────┘                                      │
                          ▼                                                  │
                   opencode.ai/zen/v1 ◄──────────────────────────────────────┘
```

---

## Design Principles

1. **WireGuard as the security boundary.** No TLS, no certs, no path-secret, no Caddy. The relay binary is plain HTTP on the WG interface only. Public internet sees one UDP port per VM. Authentication is WG public-key pinning — only the router's WG public key is accepted as a peer.

2. **In-cluster router for routing intelligence.** Workspace pods call a cluster-local Service, not an external hostname. The router handles sticky session assignment, 429 detection, drain/failover, and retry — all without DNS changes, pod restarts, or TTL waits.

3. **Destroy-and-recreate for all rotation.** No in-place key rotation, no IP swapping, no config pushes to running VMs. To rotate an IP, a WG key, or recover from failure: provision a new VM, verify healthy, add to router pool, destroy the old one. The other VM carries traffic during the ~60s window. Relay VMs are stateless — there is nothing to preserve.

4. **OCI-primary, GCP-secondary.** OCI (10 TB egress) carries the majority of traffic. GCP (1 GB egress) is failover and IP diversity. The router prefers OCI for new sessions when both are healthy but weighted toward OCI for capacity.

5. **Free-tier only, verified.** All claims about free-tier limits are verified against provider documentation (see Stated Assumptions). AWS is excluded — the new free tier model (credit-based, 6-month auto-close) does not offer reliable perpetual free compute.

6. **Zero pod-side changes to the interface.** The workspace controller still injects a single `INFERENCE_RELAY_BASEURL` — it now points at the in-cluster router Service instead of an external hostname. Pods don't know about WireGuard, relay VMs, or routing logic.

---

## Architecture

### Component Overview

```
┌─ In-Cluster ──────────────────────────────────────────────────────┐
│                                                                    │
│  Workspace Pods                                                    │
│    └─ INFERENCE_RELAY_BASEURL = http://relay-router:8080           │
│                                                                    │
│  relay-router (Deployment, 2 replicas, anti-affinity)              │
│    └─ Service: relay-router (ClusterIP)                            │
│    └─ WireGuard interface: wg0 (10.42.42.1)                        │
│    └─ Healthy relays list (computed from health checks)            │
│    └─ Session routing: hash(workspaceID) % len(healthyRelays)      │
│                                                                    │
│  InferenceRelay Controller (same binary as workspace controller)   │
│    └─ Watches InferenceRelay CR                                    │
│    └─ OCI driver: provisions/destroys OCI VMs                      │
│    └─ GCP driver: provisions/destroys GCP VMs                      │
│    └─ Generates WG keypairs, writes relay-router ConfigMap         │
│    └─ Health-checks each VM over WG every 15s                      │
│                                                                    │
└────────────────────────────────────────────────────────────────────┘
         │                                          │
    WireGuard UDP 51820                      WireGuard UDP 51820
         │                                          │
┌────────┴──────────┐                    ┌──────────┴────────┐
│ OCI A1 VM         │                    │ GCP e2-micro      │
│ Public IP: x.x.x.x│                    │ Public IP: y.y.y.y│
│ wg0: 10.42.42.2   │                    │ wg0: 10.42.42.3   │
│ relay-proxy:8080  │                    │ relay-proxy:8080  │
│ (plain HTTP)      │                    │ (plain HTTP)      │
│ keepalive daemon  │                    │                   │
└────────┬──────────┘                    └────────┬──────────┘
         │                                        │
         └──────────── opencode.ai/zen/v1 ────────┘
```

### Layer 1: Portable Relay Binary (`cmd/relay-proxy/`)

A standalone Go binary, no external dependencies beyond stdlib. ~40 lines of actual logic. No authentication — WireGuard is the auth. No TLS — WireGuard is the encryption. No path parsing — the router sends clean paths.

```
cmd/relay-proxy/
├── main.go          # HTTP server, env config, health + metrics endpoints
├── proxy.go         # Transparent forward to UPSTREAM_URL
├── proxy_test.go    # Unit tests
├── keepalive.go     # Periodic upstream probe to prevent OCI idle reclamation
└── README.md        # Deployment guide
```

**Endpoints:**
- `GET /healthz` → `200 OK` (no body) — for controller health checks over WG
- `GET /metrics` → Prometheus format — request counts by status code, keepalive counter
- `* /*` → transparent proxy to `UPSTREAM_URL` (default `https://opencode.ai/zen/v1`), streams response back

**Environment:**
- `UPSTREAM_URL` (default: `https://opencode.ai/zen/v1`)
- `LISTEN_ADDR` (default: `10.42.42.2:8080` — WG interface only, not `0.0.0.0`)

**Build:**
```makefile
relay-bin:
	GOOS=linux GOARCH=arm64 go build -o deploy/relay-proxy-arm64 ./cmd/relay-proxy/
	GOOS=linux GOARCH=amd64 go build -o deploy/relay-proxy-amd64 ./cmd/relay-proxy/
```

### Layer 2: WireGuard Mesh

The security layer. Replaces Caddy, TLS certs, CA infrastructure, and path-secret auth with one UDP port per VM.

**Topology:**
```
Router (10.42.42.1) ←── WG tunnel ──→ OCI VM (10.42.42.2)
Router (10.42.42.1) ←── WG tunnel ──→ GCP VM (10.42.42.3)
```

Star topology — router is the hub, relay VMs are spokes. Relay VMs do not peer with each other (no relay-to-relay traffic needed).

**Key management:**
- Controller generates a WireGuard keypair per relay VM during provisioning
- Router has one static keypair (generated at controller startup, stored in a K8s Secret)
- Each relay VM's cloud-init embeds: its private key, router's public key, router's WG endpoint (cluster public IP or NAT-traversed endpoint)
- Router's WG config lists each relay VM's public key as a peer
- **Rotation = destroy VM + provision new one with fresh keypair** (see Design Principle 3)

**Router WireGuard sidecar:**
The relay-router Deployment runs two containers:
1. `wireguard` sidecar — manages the `wg0` interface, requires `NET_ADMIN` capability (same pattern as Epic 32's VPN sidecars)
2. `router` main container — the Go HTTP router, connects to relays via `10.42.42.x:8080`

This follows the established pattern in `design/stories/epic-32-vpn-network-iam/README.md` for WireGuard sidecars with `NET_ADMIN` + `NET_RAW` capabilities.

**Why WireGuard over mTLS/TLS:**
- Eliminates CA, cert generation, cert rotation, Caddy, DNS-for-cert-validation
- WG public-key pinning is stronger auth than a bearer token or path secret
- Relay VMs expose zero attack surface to the public internet (one UDP port, WG rejects unauthenticated packets before any application logic runs)
- Simpler cloud-init: install `wireguard-tools`, write config file, `wg-quick up wg0`
- Key rotation is the same destroy-and-recreate flow as IP rotation — no separate mechanism

### Layer 3: In-Cluster Relay Router (`cmd/relay-router/`)

A Go HTTP server running as a Deployment (2 replicas, pod anti-affinity). This is the only endpoint workspace pods talk to.

**Responsibilities:**

1. **Sticky session routing:** Each workspace is deterministically assigned to a relay:
   ```
   relayIndex = fnv32(workspaceID) % len(healthyRelays)
   ```
   No stored session state — both router replicas compute the same assignment independently. Stickiness is implicit from the hash.

2. **Health checking:** Each router replica independently health-checks each relay every 15s via `GET http://10.42.42.x:8080/healthz` over the WG tunnel. A relay is marked unhealthy after 3 consecutive failures (45s).

3. **429 detection:** The router observes upstream response status codes on proxied requests. If a relay's 429 rate exceeds the threshold (default 50%) over a 5-minute window, the router:
   - Marks the relay as `draining` — stops assigning new sessions to it
   - Writes a condition to the InferenceRelay CR status (or calls a controller endpoint) requesting rotation
   - Existing in-flight streams on the draining relay are left to complete (or fail naturally if the IP is hard-blocked)

4. **Failover:** When a relay transitions from healthy → unhealthy, `healthyRelays` shrinks. The hash modulo changes, so new requests for workspaces previously on the failed relay now route to the surviving relay. In-flight streams to the failed relay break — opencode's retry logic handles this.

5. **Rebalancing:** When a relay rejoins (replacement VM provisioned and healthy), `healthyRelays` grows. The hash modulo changes back. New sessions naturally distribute across both relays. Existing sessions on the surviving relay are NOT force-migrated — they move on their next natural session boundary.

**How the router learns relay IPs:**
The controller writes a ConfigMap (`relay-router-peers`) that the router mounts as a volume and watches (via fsnotify or poll):
```json
{
  "relays": [
    {"id": "oci-1", "wgIP": "10.42.42.2", "provider": "oci", "healthy": true},
    {"id": "gcp-1", "wgIP": "10.42.42.3", "provider": "gcp", "healthy": true}
  ]
}
```
The router independently verifies health — it doesn't trust the ConfigMap's `healthy` field blindly.

**Workspace identification for stickiness:**
The router extracts the workspace ID from the request. Options:
- HTTP header injected by the workspace controller: `X-Workspace-ID: <uid>`
- The existing Basic Auth username (already `opencode` for all pods — not unique enough)
- **Recommended: new header `X-Workspace-ID`** — the workspace controller already sets per-pod env vars; adding one more header to the relay injector is a one-line change

**Relay-router as a reverse proxy:**
The router receives the full request from the pod, selects a relay, rewrites the URL to `http://10.42.42.x:8080/<original-path>`, and streams the response back. It injects `X-Workspace-ID` in neither direction — that header is for the router's internal routing only.

### Layer 4: InferenceRelay Controller

Runs as a new reconciler inside the existing workspace controller binary (gated by a feature flag). Watches a single cluster-scoped `InferenceRelay` CR.

**Lifecycle states:**

```
                         provision
    ┌──────────┐     ──────────────→     ┌──────────────┐
    │ Absent   │                          │ Provisioning │
    └──────────┘                          └──────┬───────┘
                                                 │ health check passes
                                                 ▼
    ┌──────────┐     destroy +       ┌──────────────┐
    │ Draining │←─── reprovision ────│  Healthy     │
    └────┬─────┘     (on 429/fail)   └──────┬───────┘
         │                                    │ health check fails (3x)
         │                                    ▼
         │                              ┌──────────────┐
         └──────────────────────────────│ Unhealthy    │
                                        └──────┬───────┘
                                               │ stays unhealthy >15m
                                               ▼
                                        destroy + reprovision
```

**Reconcile loop:**
1. Read `InferenceRelay` CR spec
2. For each provider in `spec.providers` (always OCI + GCP):
   a. Check if a relay VM exists for this provider
   b. If not, provision one (generate WG keypair, render cloud-init, call provider API)
   c. Health-check existing VM over WG tunnel
   d. If unhealthy for >15m, destroy and reprovision
   e. If router reports 429 rotation needed, mark draining, provision replacement
3. Update ConfigMap `relay-router-peers` with current relay IPs and health status
4. Update CR status with observed state

### Layer 5: InferenceRelay CRD

A cluster-scoped CRD. Singleton — only one instance expected per cluster.

```go
// InferenceRelay represents the managed relay VM fleet. The controller
// provisions, health-checks, and replaces relay VMs on OCI and GCP to
// maintain free-tier inference availability. Workspace pods route through
// the in-cluster relay-router, which distributes traffic across healthy
// relay VMs via WireGuard tunnels.
type InferenceRelay struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   InferenceRelaySpec   `json:"spec,omitempty"`
    Status InferenceRelayStatus `json:"status,omitempty"`
}

type InferenceRelaySpec struct {
    // UpstreamURL is the LLM provider endpoint the relays proxy to.
    // +kubebuilder:default="https://opencode.ai/zen/v1"
    UpstreamURL string `json:"upstreamURL"`

    // Providers configures the relay VMs. Must include exactly one OCI
    // and one GCP provider for the intended 2-VM fleet.
    // +kubebuilder:validation:MinItems=1
    Providers []RelayProviderSpec `json:"providers"`

    // WireGuard configures the mesh between router and relay VMs.
    WireGuard WireGuardConfig `json:"wireGuard,omitempty"`

    // HealthCheck configures active health-checking of relay VMs.
    HealthCheck HealthCheckConfig `json:"healthCheck,omitempty"`

    // Rotation configures automatic destroy-and-recreate on 429 detection.
    Rotation RotationConfig `json:"rotation,omitempty"`
}

type RelayProviderSpec struct {
    // Provider is the cloud provider name.
    // +kubebuilder:validation:Enum=oci;gcp
    Provider string `json:"provider"`

    // Region is the provider region (e.g. "us-ashburn-1", "us-central1-a").
    // OCI: must be the tenancy home region for Always Free eligibility.
    // GCP: must be us-west1, us-central1, or us-east1 for Always Free eligibility.
    Region string `json:"region"`

    // CredentialsRef references a K8s Secret containing provider credentials.
    //   oci: API key (tenancy OCID, user OCID, fingerprint, private key)
    //   gcp: Service account JSON key
    CredentialsRef string `json:"credentialsRef"`

    // Shape overrides the default free-tier shape.
    //   oci default: VM.Standard.A1.Flex (2 OCPU, 12 GB, Arm)
    //   gcp default: e2-micro (shared vCPU, 1 GB)
    // +optional
    Shape string `json:"shape,omitempty"`
}

type WireGuardConfig struct {
    // RouterPrivateKeyRef references a K8s Secret containing the router's
    // WG private key. Auto-generated by the controller if not set.
    // +optional
    RouterPrivateKeyRef string `json:"routerPrivateKeyRef,omitempty"`

    // CIDR is the WireGuard mesh network. Default: 10.42.42.0/24.
    // Router is .1, OCI relay is .2, GCP relay is .3.
    // +kubebuilder:default="10.42.42.0/24"
    CIDR string `json:"cidr,omitempty"`

    // Port is the WireGuard UDP port. Default: 51820.
    // +kubebuilder:default=51820
    Port int `json:"port,omitempty"`

    // RouterEndpoint is the routable address relay VMs connect back to.
    // For clusters behind NAT, this is the public IP or hostname of the
    // node port / load balancer exposing the router's WG port.
    RouterEndpoint string `json:"routerEndpoint"`
}

type HealthCheckConfig struct {
    // Interval between health checks per relay VM.
    // +kubebuilder:default="15s"
    Interval metav1.Duration `json:"interval,omitempty"`

    // Health check request timeout.
    // +kubebuilder:default="5s"
    Timeout metav1.Duration `json:"timeout,omitempty"`

    // Consecutive failures before marking unhealthy.
    // +kubebuilder:default=3
    UnhealthyThreshold int `json:"unhealthyThreshold,omitempty"`

    // Time to stay unhealthy before destroy + reprovision.
    // +kubebuilder:default="15m"
    ReplacementTimeout metav1.Duration `json:"replacementTimeout,omitempty"`
}

type RotationConfig struct {
    // Enabled enables destroy-and-recreate when the router detects 429 storms.
    // +kubebuilder:default=true
    Enabled bool `json:"enabled"`

    // Max429Rate is the 429 fraction (of total responses) that triggers rotation.
    // +kubebuilder:default=0.5
    Max429Rate float64 `json:"max429Rate,omitempty"`

    // DetectionWindow is the rolling window for counting 429s.
    // +kubebuilder:default="5m"
    DetectionWindow metav1.Duration `json:"detectionWindow,omitempty"`

    // Cooldown is the minimum time between rotations on the same provider slot.
    // +kubebuilder:default=30m
    Cooldown metav1.Duration `json:"cooldown,omitempty"`
}

type InferenceRelayStatus struct {
    // Instances is the observed state of all managed relay VMs.
    Instances []RelayInstanceStatus `json:"instances,omitempty"`

    // HealthyReplicas is the count of instances currently passing health checks.
    HealthyReplicas int `json:"healthyReplicas"`

    // Conditions reflects the overall relay fleet health.
    Conditions []metav1.Condition `json:"conditions,omitempty"`

    // LastRotation is the time of the most recent destroy-and-recreate.
    LastRotation *metav1.Time `json:"lastRotation,omitempty"`
}

type RelayInstanceStatus struct {
    ID         string       `json:"id"`
    Provider   string       `json:"provider"`
    Region     string       `json:"region"`
    WgIP       string       `json:"wgIP"`
    PublicIP   string       `json:"publicIP"`
    State      string       `json:"state"` // "provisioning", "healthy", "draining", "unhealthy", "terminated"
    Healthy    bool         `json:"healthy"`
    LastCheck  *metav1.Time `json:"lastCheck,omitempty"`
    Requests429 int         `json:"429Count,omitempty"`
    TotalRequests int       `json:"totalRequests,omitempty"`
}
```

### Layer 6: Cloud Provider Drivers (`controller/internal/relay/`)

Each provider driver implements:

```go
type ProviderDriver interface {
    // Provision creates a relay VM with the given cloud-init userdata
    // and returns the instance ID and public IP.
    Provision(ctx context.Context, name, region, shape, cloudInitData string) (*RelayInstance, error)

    // Destroy terminates a relay VM.
    Destroy(ctx context.Context, instanceID, region string) error

    // GetStatus returns the current VM state.
    GetStatus(ctx context.Context, instanceID, region string) (*RelayStatus, error)

    // ListInstances returns relay VMs managed by this driver.
    ListInstances(ctx context.Context, region string) ([]RelayInstance, error)
}
```

Note: **no `RotateIP` method** — rotation is destroy + provision, not in-place IP swap. This keeps drivers simple (3 methods instead of 4) and matches the destroy-and-recreate principle.

**Drivers:**
```
controller/internal/relay/
├── driver.go           # ProviderDriver interface
├── oci_driver.go       # OCI driver (primary — 10 TB egress, A1 Arm)
├── gcp_driver.go       # GCP driver (secondary — failover, 1 GB egress)
├── cloudinit.go        # Renders cloud-init templates (WG + relay binary + keepalive)
├── wireguard.go        # Keypair generation, config rendering
├── health.go           # Health-checker (GET /healthz over WG)
├── reconciler.go       # InferenceRelay CRD reconciler
└── router_configmap.go # Writes relay-router-peers ConfigMap
```

### Layer 7: Cloud-Init Template

Shared across providers. Renders a single `user-data` script that:

1. Downloads the relay binary from the artifact location (GitHub Release / OCI artifact)
2. Creates the WireGuard interface with the embedded private key and router peer
3. Writes the relay binary's systemd unit (binds to WG IP only)
4. Starts the relay proxy
5. Configures the keepalive daemon (upstream probe every 30s — prevents OCI idle reclamation)
6. Configures UFW: allow SSH (WG-only or disabled), allow UDP 51820 (WG), deny everything else
7. Enables unattended-upgrades

```bash
#!/bin/bash
set -euo pipefail

# Download relay binary
ARCH=$(uname -m)
case "$ARCH" in
  aarch64) BINARY=relay-proxy-arm64 ;;
  x86_64)  BINARY=relay-proxy-amd64 ;;
esac
curl -fsSL "https://github.com/lenaxia/llmsafespace/releases/latest/download/$BINARY" -o /usr/local/bin/relay-proxy
chmod +x /usr/local/bin/relay-proxy

# Configure WireGuard
apt-get update && apt-get install -y wireguard-tools
mkdir -p /etc/wireguard
cat > /etc/wireguard/wg0.conf <<WGEOF
[Interface]
PrivateKey = ${RELAY_WG_PRIVATE_KEY}
Address = ${RELAY_WG_IP}/24

[Peer]
PublicKey = ${ROUTER_WG_PUBLIC_KEY}
Endpoint = ${ROUTER_WG_ENDPOINT}
AllowedIPs = 10.42.42.0/24
PersistentKeepalive = 25
WGEOF
wg-quick up wg0

# Relay proxy systemd service (WG interface only)
cat > /etc/systemd/system/relay-proxy.service <<SVCEOF
[Unit]
Description=LLMSafeSpace Inference Relay Proxy
After=network-online.target wg-quick@wg0.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/relay-proxy
Environment=UPSTREAM_URL=https://opencode.ai/zen/v1
Environment=LISTEN_ADDR=${RELAY_WG_IP}:8080
Restart=always
RestartSec=5
User=nobody

[Install]
WantedBy=multi-user.target
SVCEOF
systemctl enable --now relay-proxy

# Keepalive: probe upstream every 30s to keep network util above OCI's 20% threshold
cat > /etc/cron.d/relay-keepalive <<CRONEOF
* * * * * nobody curl -sf -o /dev/null http://${RELAY_WG_IP}:8080/healthz
CRONEOF

# Firewall
apt-get install -y ufw
ufw default deny incoming
ufw default allow outgoing
ufw allow 51820/udp
ufw --force enable

# Unattended upgrades
apt-get install -y unattended-upgrades
dpkg-reconfigure -f noninteractive unattended-upgrades
```

---

## Stated Assumptions

All free-tier claims below were verified against provider documentation on 2026-06-13. Items marked ⚠️ could not be fully verified from published docs and require live testing before implementation.

| # | Assumption | Status | Source / Verification |
|---|-----------|--------|----------------------|
| A1 | OCI Always Free is for the life of the account, no expiration | ✅ Verified | OCI docs: "free of charge in the home region of the tenancy, for the life of the account" |
| A2 | OCI A1 shape (VM.Standard.A1.Flex) provides 2 OCPU / 12 GB free | ✅ Verified | OCI Always Free docs: "equivalent to 2 OCPUs and 12 GB of memory" |
| A3 | OCI includes 10 TB/month outbound data transfer free | ✅ Verified | OCI Always Free docs: "you get 10 TB per month of outbound data" |
| A4 | OCI Always Free resources must be created in the home region only | ✅ Verified | OCI docs: "You must create the Always Free compute instances in your home region" |
| A5 | OCI A1 instances suffer "out of host capacity" errors requiring retries | ✅ Verified | OCI docs explicitly mention this: "If you receive an 'out of host capacity' error..." |
| A6 | OCI will reclaim idle Always Free compute instances (CPU/network/memory <20% for 7 days) | ✅ Verified — **CRITICAL DESIGN RISK** | OCI docs: "Idle Always Free compute instances may be reclaimed... if, during a 7-day period, CPU utilization for the 95th percentile is less than 20%, Network utilization is less than 20%, Memory utilization is less than 20%" |
| A7 | OCI supports ephemeral and reserved public IPs; ephemeral IPs can be released to get a new IP | ✅ Verified (concept) | OCI Networking Overview: "There are two types of public IPs: ephemeral and reserved." |
| A8 | OCI free-tier limit on number of ephemeral/reserved public IPs | ⚠️ Unverified | OCI docs do not specify the exact IP allocation limit for Always Free tenancies. Must verify empirically. |
| A9 | OCI supports cloud-init / user-data on Linux images (Oracle Linux, Ubuntu) | ⚠️ Unverified from docs | Not explicitly mentioned in Always Free docs. Widely reported to be supported. Verify during US-42.2. |
| A10 | AWS free tier has fundamentally changed to a credit-based model (6-month Free plan, auto-close) | ✅ Verified | AWS Free Tier FAQ: Free plan "expires the earlier of 6-months from the date you opened your AWS account, or once you have exhausted your Free Tier credits." AWS is **excluded** from this epic. |
| A11 | GCP Always Free e2-micro is available in us-west1, us-central1, us-east1 only | ✅ Verified | GCP Free Tier docs: "1 non-preemptible e2-micro VM instance per month in one of the following US regions" |
| A12 | GCP e2-micro includes 1 GB/month outbound data transfer (North America, excl. China/Australia) | ✅ Verified | GCP Free Tier docs: "1 GB of outbound data transfer from North America to all region destinations" |
| A13 | GCP Free Tier has no end date but can be changed with 30 days notice | ✅ Verified | GCP docs: "Google reserves the right to change the offering, including changing or eliminating usage limits, with 30 days' advance notice." |
| A14 | GCP Free Tier requires an active billing account (Paid or Free Trial) | ✅ Verified | GCP docs: "A Google Cloud billing account is required to access the Google Cloud Free Tier." |
| A15 | GCP e2-micro specs (vCPU, memory) | ⚠️ Unverified | GCP machine type docs were not fully scrapable. Must verify at implementation time. |
| A16 | GCP supports startup scripts (equivalent of cloud-init) on VM creation | ✅ Verified | GCP Compute Engine docs reference startup scripts as a standard feature. |
| A17 | OCI A1 shape network bandwidth scales with OCPUs | ✅ Verified | OCI docs: "The network bandwidth and number of VNICs scale proportionately with the number of OCPUs." |
| A18 | OCI E2.1.Micro shape has 50 Mbps bandwidth to internet | ✅ Verified | OCI docs: "up to 50 Mbps network bandwidth via the internet" |
| A19 | WireGuard is available in standard Linux kernels ≥5.6 (no DKMS needed) | ✅ Verified | WireGuard was merged into the Linux kernel in 5.6 (2020-03). OCI Oracle Linux 8/9 and GCP Ubuntu 20.04+ images ship kernels ≥5.6. |
| A20 | WireGuard UDP hole-punching works through cloud NAT (relay VMs behind cloud NAT can maintain persistent connections) | ⚠️ Unverified | `PersistentKeepalive = 25` in the WG config is the standard NAT-traversal mechanism. Should work but needs verification per provider's NAT implementation. |
| A21 | Relay VMs can reach the router's WG endpoint from outside the cluster | ⚠️ Design dependency | Requires a NodePort or LoadBalancer Service exposing UDP 51820 on the router. Cluster's network setup (Traefik ingress, bare-metal Talos) must support UDP load balancing. ⚠️ Verify during US-42.3. |
| A22 | OCI IPs and GCP IPs are not blocked by opencode.ai/zen | ⚠️ Unverified | **Day-one validation gate.** Must deploy a relay VM on each provider and curl `opencode.ai/zen/v1` before building the full controller. |

---

## Design Questions

| # | Question | Answer | Rationale |
|---|----------|--------|-----------|
| DQ1 | How do we prevent OCI from reclaiming idle relay VMs? | **Keepalive daemon.** Cloud-init installs a cron job that curls `localhost:8080/healthz` every minute. The relay binary also runs a goroutine that probes the upstream (`GET opencode.ai/zen/v1/models`) every 30s. Both contribute to network utilization. The Go runtime's memory footprint (>2 GB on a 12 GB VM) keeps memory above 20%. | OCI reclaims Always Free instances with <20% CPU/network/memory utilization over 7 days (A6). The keepalive ensures network + CPU stay measurable. Requires 7-day empirical validation. |
| DQ2 | How does the router expose its WireGuard port to relay VMs? | **NodePort or LoadBalancer Service on UDP 51820.** The router Deployment is fronted by a K8s Service of type NodePort (bare-metal cluster) or LoadBalancer (if MetalLB is available). Relay VMs connect to `<nodeIP>:<nodePort>` as the WG endpoint. | The cluster runs on bare-metal Talos with Traefik ingress. Traefik doesn't handle UDP, so we need a separate UDP path. A NodePort is simplest. |
| DQ3 | How does the router identify which workspace a request belongs to? | **`X-Workspace-ID` header** injected by the workspace controller into the relay injector config. The relay injector already rewrites `agent-config.json` — adding a default header to the `opencode-relay` provider is a one-line change. | The router needs the workspace ID for deterministic hash-based relay assignment. Basic Auth username is not unique per workspace. |
| DQ4 | What happens when both relays are unhealthy? | **Direct fallback.** The router proxies directly to `opencode.ai/zen/v1` (server IPs), accepting the throttle risk. Returns a `X-Relay-Status: fallback` header so the frontend can display a warning. Better than a hard 502. | Free-tier traffic from server IPs may be throttled, but partial availability beats total outage. The controller will be working on reprovisioning in parallel. |
| DQ5 | Destroy-and-recreate vs in-place rotation? | **Always destroy-and-recreate.** No in-place IP swapping, key rotation, or config pushing. Relay VMs are stateless. The other VM carries traffic during the ~60s provisioning window. | Simpler driver interface (no RotateIP), simpler cloud-init (no runtime reconfiguration), identical flow for failure recovery and key/IP rotation. |
| DQ6 | Should the controller run inside the existing workspace controller binary or as a separate deployment? | **Same binary, new reconciler, gated by a feature flag.** | The relay controller and workspace controller are coupled (router URL injection). Same binary simplifies deployment and avoids a second controller pod. |
| DQ7 | Should we weight traffic toward OCI (10 TB egress) over GCP (1 GB egress)? | **Yes — OCI gets 2/3 of new sessions, GCP gets 1/3.** Implemented as weighted consistent hashing: `hash(workspaceID) % 3 < 2 → OCI, else GCP`. | GCP's 1 GB/mo egress would be exhausted quickly if it carried 50% of traffic. OCI's 10 TB can handle the load. GCP is primarily for failover and IP diversity. |
| DQ8 | What happens when GCP egress quota (1 GB/mo) is exhausted? | **Controller detects via GCP billing API or the relay's own byte counter, marks GCP relay as `quota-exhausted`, removes from pool.** All traffic routes to OCI until the monthly reset. | GCP egress resets monthly. The controller can optionally destroy and recreate the GCP VM at month boundary to reset billing counters (though egress is per-billing-account, not per-VM). |

---

## OCI Idle Reclamation Mitigation

OCI will reclaim Always Free instances where CPU utilization (95th percentile), network utilization, and memory utilization are all below 20% for a 7-day window (A6). This is a first-class design risk for relay VMs.

**Mitigation (built into cloud-init, Layer 7):**

1. **Network keepalive:** Cron job runs `curl -sf -o /dev/null http://<wg-ip>:8080/healthz` every minute. Generates consistent small network I/O.
2. **Upstream probe:** Relay binary goroutine performs `GET opencode.ai/zen/v1/models` every 30s. Keeps network utilization measurable and serves as an active upstream-health probe.
3. **Memory:** Go runtime + relay buffers naturally use >2 GB on a 12 GB VM (>20%).
4. **CPU:** The network I/O from keepalive + probe generates CPU work. A lightweight busy-loop goroutine (1% CPU for 1s every 10s) provides additional floor.

**Verification required (US-42.2):** Monitor CPU/network/memory utilization for 7 days after first deployment to confirm all three metrics stay above 20%.

---

## Story Breakdown

| Story | Title | Effort | Depends On |
|-------|-------|--------|------------|
| US-42.1 | Portable relay Go binary (proxy + health + metrics + keepalive) | Small-Medium (1d) | None |
| US-42.2 | Cloud-init template + artifact publishing + **day-one validation** (deploy VM on OCI, curl Zen, verify not blocked — A22) | Small (0.5-1d) | US-42.1 |
| US-42.3 | InferenceRelay CRD + types + deepcopy + RBAC | Medium (1d) | None |
| US-42.4 | WireGuard keypair generation + config rendering | Small (0.5d) | None |
| US-42.5 | OCI provider driver (provision, destroy, status) | Medium (1-2d) | US-42.2, US-42.4 |
| US-42.6 | GCP provider driver (provision, destroy, status) | Medium (1d) | US-42.2, US-42.4 |
| US-42.7 | Relay-router: sticky routing + health checking + 429 detection | Medium-Large (2d) | US-42.3 |
| US-42.8 | Router WireGuard sidecar + NodePort Service | Small-Medium (1d) | US-42.4, US-42.7 |
| US-42.9 | InferenceRelay reconciler (lifecycle: provision, health, destroy+recreate, ConfigMap sync) | Large (2-3d) | US-42.3, US-42.5, US-42.6, US-42.7 |
| US-42.10 | Helm chart integration (CRD, router Deployment+Service, controller flags, WG Secret) | Small (0.5d) | US-42.3, US-42.9 |
| US-42.11 | Fallback mode: direct routing when all relays unhealthy | Small (0.5d) | US-42.7 |

**Total estimated effort:** 11-15 days

**Day-one gate (US-42.2):** Before any controller work, manually deploy a relay VM on OCI and one on GCP, curl `opencode.ai/zen/v1` from each. If either provider's IPs are blocked by Zen, the entire epic premise fails. This is the cheapest possible validation.

---

## Dependency Graph

```
US-42.1 (relay binary) ──────────────┐
                                      ├── US-42.2 (cloud-init + validation GATE)
US-42.4 (WG keypair gen) ─────────┐   │
                                  │   │
US-42.3 (CRD types) ───────────┐  │   │
                               │  │   │
                               │  │   ├── US-42.5 (OCI driver) ─────────┐
                               │  │   ├── US-42.6 (GCP driver) ─────────┤
                               │  │   │                                   │
                               ├── US-42.7 (router) ──────────────────┤  │
                               │  │                                      │  │
                               │  ├── US-42.8 (router WG sidecar) ──────┤  │
                               │                                         │  │
                               └── US-42.9 (reconciler) ◄────────────────┴──┘
                                          │
                                          ├── US-42.10 (Helm)
                                          └── US-42.11 (fallback mode)
```

**Critical path:** US-42.1 → US-42.2 (validation gate) → US-42.5 (OCI driver) → US-42.9 (reconciler) → US-42.10 (Helm)

---

## Execution Strategy

**Phase 0 — Validation gate (day 1):** US-42.1, US-42.2
Port relay binary, deploy on OCI + GCP manually, curl `opencode.ai/zen/v1` from each VM. **If either IP range is blocked, stop and reassess.** This is the cheapest possible de-risking step.

**Phase 1 — Foundation (day 2-3):** US-42.3, US-42.4
CRD types and WG keypair generation. No cloud dependencies — can be fully unit-tested.

**Phase 2 — Router (day 3-5):** US-42.7, US-42.8
Build the relay-router with mock relays. WireGuard sidecar + NodePort. Test stickiness, failover, 429 detection against mock HTTP servers.

**Phase 3 — Provider drivers (day 5-8):** US-42.5, US-42.6
OCI and GCP drivers. Can be developed in parallel. End of phase 3: controller can provision a VM, establish WG tunnel, health-check it.

**Phase 4 — Reconciler + integration (day 8-11):** US-42.9, US-42.10, US-42.11
Full lifecycle management. Helm chart. Fallback mode. End-to-end: `kubectl apply` → VMs provisioned → WG tunnels up → router routing → pods getting free-tier inference.

---

## Out of Scope

| # | What | Why |
|---|------|-----|
| 1 | AWS provider driver | AWS free tier changed to credit-based model with 6-month auto-close (A10). No verified always-free EC2 compute. |
| 2 | Cloudflare Worker as a managed provider | CF Worker IPs are blocked by Zen — that's why this epic exists. |
| 3 | Per-workspace relay assignment | Deterministic hash-based routing is sufficient. No per-workspace state needed. |
| 4 | Relay request/response body logging | Privacy concern. The relay is a dumb byte pipe. Only aggregate counters for 429 detection. |
| 5 | Autoscaling beyond 2 VMs | Free tiers are capacity-limited. The architecture supports N relays but the free-tier fleet is fixed at 1 OCI + 1 GCP. |
| 6 | DNS management for pod routing | The router is in-cluster (ClusterIP Service). No DNS needed for routing. DNS only for the relay binary's upstream (`opencode.ai`). |
| 7 | mTLS / TLS between router and relay | WireGuard replaces all PKI. Adding TLS inside WG would be redundant encryption. |
| 8 | Path-secret authentication | Eliminated by WireGuard. WG public-key pinning is the auth. |
| 9 | Caddy / Let's Encrypt | Eliminated by WireGuard. No public HTTPS endpoints on relay VMs. |
| 10 | In-place IP rotation | All rotation is destroy-and-recreate (DQ5). No driver-level `RotateIP` method. |

---

## CRD Example

```yaml
apiVersion: llmsafespace.dev/v1
kind: InferenceRelay
metadata:
  name: relay-fleet
spec:
  upstreamURL: "https://opencode.ai/zen/v1"
  wireGuard:
    cidr: "10.42.42.0/24"
    port: 51820
    routerEndpoint: "203.0.113.10:31820"  # cluster public IP + NodePort
  providers:
    - provider: oci
      region: us-ashburn-1
      credentialsRef: oci-credentials
    - provider: gcp
      region: us-central1-a
      credentialsRef: gcp-credentials
  healthCheck:
    interval: 15s
    timeout: 5s
    unhealthyThreshold: 3
    replacementTimeout: 15m
  rotation:
    enabled: true
    max429Rate: 0.5
    detectionWindow: 5m
    cooldown: 30m
```

---

## Migration from Epic 26 (Cloudflare Worker)

1. **Deploy relay VMs** (manually at first, then via controller) — verify they work
2. **Deploy relay-router** in-cluster — verify WG tunnels and health checks
3. **Update workspace controller** `inferenceRelayURL` to `http://relay-router:8080`
4. **Delete CF Worker** — `npx wrangler delete`
5. **Keep CF Worker code** in repo for historical reference

No workspace pod restarts needed beyond the normal pod lifecycle — the relay injector reads `INFERENCE_RELAY_BASEURL` at pod startup and the next pod rotation picks up the new URL.

---

## Security Considerations

1. **WireGuard is the only auth.** Relay VMs reject all non-WG traffic. The relay binary listens on the WG interface IP only (`10.42.42.x:8080`), not `0.0.0.0`. Public internet sees one UDP port; unauthenticated packets are dropped by WG before reaching any application code.

2. **No secrets on relay VMs.** The relay proxies `apiKey: "public"` requests — nothing worth stealing. No cluster credentials, no API keys, no user data. Cloud credentials are used only by the controller (in-cluster), never on the VMs.

3. **WG keypairs are per-VM, generated by the controller.** A compromised relay VM's private key compromises only that tunnel. Destroy-and-recreate generates a fresh keypair. The router's private key is in a K8s Secret.

4. **UFW firewall on relay VMs.** Cloud-init configures: deny all incoming, allow UDP 51820 (WG), allow outgoing. SSH is either disabled or restricted to the WG interface.

5. **Router is in-cluster, not exposed to the internet.** Workspace pods reach it via ClusterIP. Only the WG UDP port is exposed (via NodePort) for relay VMs to connect back.

6. **Provider credential rotation.** Cloud credentials (OCI API key, GCP service account JSON) live in K8s Secrets, used only by the controller. Rotating them doesn't affect running VMs — only future provisioning calls.

---

## Open Questions

| # | Question | Notes |
|---|----------|-------|
| OQ1 | What is the exact OCI free-tier limit on ephemeral/reserved public IPs? | Unverified (A8). Must test empirically during US-42.5. Determines feasibility of IP rotation via destroy+recreate (which allocates a new ephemeral IP). |
| OQ2 | Does OCI support cloud-init on Always Free images? | Unverified (A9). If not, fall back to a custom startup script in cloud-init format (OCI supports `user_data` field on instance launch regardless). |
| OQ3 | What are the actual GCP e2-micro specs? | Unverified (A15). Verify at GCP machine type docs during US-42.6. |
| OQ4 | Can the cluster expose a UDP NodePort for WG that relay VMs can reach? | Design dependency (A21). The Talos cluster's network setup must allow incoming UDP on a NodePort. Verify during US-42.8. If not, use a LoadBalancer Service (MetalLB) or a dedicated WG gateway pod. |
| OQ5 | Will OCI's idle reclamation actually trigger for a relay VM with keepalive traffic? | Requires 7-day empirical testing (see "OCI Idle Reclamation Mitigation"). The 20% thresholds are documented but the measurement methodology (95th percentile for CPU) needs validation. |
| OQ6 | Does Zen (opencode.ai) block OCI and GCP IP ranges? | **Day-one validation gate (A22).** Must curl `opencode.ai/zen/v1` from a VM on each provider before building anything. |
| OQ7 | How does the router inject `X-Workspace-ID`? | The relay injector (`cmd/workspace-agentd/relay_injector.go:171-179`) writes the provider config. Adding a `headers` field or a custom header to the `opencode-relay` provider's `options` is a one-line change — but must verify opencode's `@ai-sdk/openai-compatible` package supports custom headers. |
| OQ8 | Should the router proxy streaming responses (SSE) with buffering or true pass-through? | True pass-through (`io.Copy` / `Flush`) — the router must not buffer SSE streams. The existing proxy in `api/internal/handlers/proxy.go` already does this for workspace→opencode traffic; reuse the same pattern. |
