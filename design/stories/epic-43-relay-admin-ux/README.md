# Epic 43: Relay Admin UX

**Status:** Implemented
**Created:** 2026-06-14
**Depends on:** Epic 42 (Multi-Cloud Inference Relay — relay binary, router, CRD types, controller)

> **Correction note:** The original design referenced AWS as a provider. The
> implementation aligns with the InferenceRelay CRD which defines
> `+kubebuilder:validation:Enum=oci;gcp`. AWS is NOT a supported provider —
> the fleet uses OCI (Always Free, primary, 10 TB egress) and GCP (Always Free,
> failover, 1 GB egress). All cost tracking is $0. There is no PKI/cert-manager
> dependency (Epic 42 uses WireGuard as the security boundary).
**Supersedes:** None

---

## Problem Statement

### Current State

Epic 42 delivers the relay infrastructure as Go code (relay binary, router, CRD types, WireGuard utilities) but provides no operator-facing UI. To configure, deploy, and monitor the relay fleet, an operator must:

1. Read the design doc and manually run 15+ CLI commands across AWS, OCI, and kubectl
2. Export certificates, create IAM Roles Anywhere trust anchors, create profiles, feed IDs back into Helm
3. Create Kubernetes Secrets by hand
4. Monitor relay health by running `kubectl describe inferencerelay` and scraping Prometheus metrics
5. Diagnose failures by reading CR conditions and controller logs

There is no visibility into relay status, cost, or health from the existing admin UI. The operator cannot tell if inference is flowing, which relay is primary, or when a relay was last rotated — without CLI access.

### Desired State

A **Setup Wizard** (one-time, then hidden) that walks the operator through AWS IAM Roles Anywhere + OCI credential configuration with copy-paste commands, prerequisite checks, and a test-connection button.

A **Status Dashboard** (ongoing) that shows real-time fleet health, per-relay metrics, cost tracking, and recent events — replacing `kubectl describe` and Prometheus queries with a single page.

---

## Design Principles

1. **Setup is a wizard, not a wall of YAML.** The operator follows numbered steps with copy-paste commands, status checks, and validation. The wizard tracks progress and hides once complete.

2. **Status is live, not a snapshot.** The dashboard polls the InferenceRelay CR status and router `/metrics` every 15s. No manual refresh needed.

3. **Secrets are never shown.** The UI displays secret names and "configured" badges, never values. Certificates are downloadable (public cert only) but private keys are never exposed in the browser.

4. **Cost is first-class.** AWS is a paid provider (~$7/month). The operator should see actual spend and projected monthly cost at a glance.

5. **Actions are explicit.** Manual rotation, pause, and redeploy are buttons with confirmation dialogs — never automatic changes triggered by viewing the page.

6. **Failures are actionable.** When a relay is unhealthy or provisioning fails, the UI shows the error message from the CR condition and links to the relevant troubleshooting step — not just a red badge.

---

## Architecture

### Component Overview

```
Frontend (React)
  SettingsPage.tsx
    └── "Relay" tab (admin only)
         ├── RelaySetupWizard.tsx     (one-time setup)
         └── RelayStatusDashboard.tsx  (ongoing monitoring)

API Server (Go/Gin)
  /api/v1/admin/relay/
    ├── GET    status         → fleet health, per-relay state, metrics
    ├── GET    setup          → prerequisite checklist state
    ├── GET    ca             → root CA cert download (setup step)
    ├── POST   aws-config     → save trustAnchorId, profileId, roleArn
    ├── POST   oci-creds      → save OCI credentials (writes K8s Secret)
    ├── POST   deploy         → create/update InferenceRelay CR
    ├── POST   rotate/:id     → trigger manual relay rotation
    └── GET    events         → recent relay events (audit log)

Controller (reads CR status, writes to CR)
  InferenceRelay CR status → API scrapes on each GET /status request
  Router /metrics → API scrapes for live traffic/stream data
```

### Data Flow

```
┌─────────────┐     GET /admin/relay/status     ┌──────────────┐
│  Browser    │ ──────────────────────────────→ │  API Server   │
│  (15s poll) │ ←────────────────────────────── │               │
└─────────────┘     JSON: fleet status          └───┬───────┬──┘
                                                        │       │
                                          K8s API ─────┘       └── HTTP ──→ relay-router:8080/metrics
                                          ↓                                    ↓
                                          InferenceRelay CR                    Prometheus text
                                          .status.instances                    parse
                                          .status.conditions
```

The API server is a read aggregator: it fetches the InferenceRelay CR from K8s and scrapes the relay-router's `/metrics` endpoint, then returns a unified JSON response. No data is stored in PostgreSQL — the CRD status and router metrics are the source of truth.

---

## UI Design

### Setup Wizard (`RelaySetupWizard.tsx`)

Shown when no InferenceRelay CR exists or setup is incomplete. Hidden once the fleet is deployed.

```
┌─ Relay Setup ────────────────────────────────────────────────────┐
│                                                                   │
│  Step 1 of 4: Prerequisites                                       │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ ☑ cert-manager installed                                    │  │
│  │   ClusterIssuer found, ready to issue certs                 │  │
│  │                                                              │  │
│  │ ☑ MetalLB installed                                         │  │
│  │   LoadBalancer available for WireGuard UDP                  │  │
│  │                                                              │  │
│  │ ☐ Relay router deployed                                     │  │
│  │   Run: helm upgrade llmsafespace ... --set relay.enabled=true│  │
│  │                                                              │  │
│  │ ☐ InferenceRelay CRD installed                             │  │
│  │   Installed automatically by Helm chart                    │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                   │
│  Step 2 of 4: AWS IAM Roles Anywhere                              │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ The chart has auto-generated a root CA. Download it and     │  │
│  │ register it as an IAM Roles Anywhere trust anchor.          │  │
│  │                                                              │  │
│  │ [Download root-ca.pem]  ← button (GET /admin/relay/ca)      │  │
│  │                                                              │  │
│  │ Run these commands:                                          │  │
│  │ ┌─────────────────────────────────────────────────┐         │  │
│  │ │ aws rolesanywhere create-trust-anchor \         │ [Copy]  │  │
│  │ │   --name llmsafespace-relay \                   │         │  │
│  │ │   --source '...'                                │         │  │
│  │ │                                                  │         │  │
│  │ │ aws iam create-role --role-name llmsafespace-\  │         │  │
│  │ │   relay --assume-role-policy-document '...'     │         │  │
│  │ │                                                  │         │  │
│  │ │ aws iam put-role-policy ...                      │         │  │
│  │ │                                                  │         │  │
│  │ │ aws rolesanywhere create-profile \              │         │  │
│  │ │   --name llmsafespace-relay \                   │         │  │
│  │ │   --role-arns arn:aws:iam::... \                │         │  │
│  │ │   --enabled                                     │         │  │
│  │ └─────────────────────────────────────────────────┘         │  │
│  │                                                              │  │
│  │ Trust Anchor ID:  [ta-xxxxxxxxx___________]                 │  │
│  │ Profile ID:       [p-xxxxxxxxx__________]                   │  │
│  │ Role ARN:         [arn:aws:iam::__________]                 │  │
│  │                                                              │  │
│  │              [Test Connection]                               │  │
│  │              ✓ AWS credentials valid, EC2 API reachable     │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                   │
│  Step 3 of 4: OCI Credentials                                     │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ Tenancy OCID:    [ocid1.tenancy..._____________]           │  │
│  │ User OCID:       [ocid1.user...________________]           │  │
│  │ Fingerprint:     [aa:bb:cc:dd:ee:ff..._______]             │  │
│  │ API Private Key: [textarea - paste key contents]           │  │
│  │ Region:          [us-ashburn-1 ▾]                           │  │
│  │                                                              │  │
│  │              [Save & Test Connection]                        │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                   │
│  Step 4 of 4: Deploy                                              │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ WireGuard Endpoint: [relay-gw.safespaces.dev:51820]        │  │
│  │ Upstream URL:       [https://opencode.ai/zen/v1]           │  │
│  │                                                              │  │
│  │ Providers to deploy:                                         │  │
│  │   ☑ AWS (us-east-1) — primary, ~$7/month                   │  │
│  │   ☑ OCI (us-ashburn-1) — free secondary                    │  │
│  │   ☐ GCP (optional) — add for IP diversity                  │  │
│  │                                                              │  │
│  │              [Deploy Relay Fleet]                            │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                   │
│  [← Back]                                         [Next →]       │
└───────────────────────────────────────────────────────────────────┘
```

### Status Dashboard (`RelayStatusDashboard.tsx`)

Shown after the fleet is deployed. Replaces the setup wizard.

```
┌─ Relay Fleet ───────────────────────────────────────────────────┐
│                                                                  │
│  ● Healthy    2/2 relays active    Router: ● Active              │
│  Fallback: ○ Inactive    Active streams: 3                       │
│                                                                  │
│  ┌─ AWS (primary) ─────────── ● Healthy ───────────────────┐    │
│  │                                                          │    │
│  │  t4g.micro | us-east-1 | wg0: 10.42.42.4               │    │
│  │  Public IP: 54.210.123.45                               │    │
│  │  Provisioned: 3 days ago                                │    │
│  │                                                          │    │
│  │  Today          This Month                               │    │
│  │  Requests: 12,847   Egress: 142 MB / 100 GB free       │    │
│  │  429s: 3            Cost: $0.68 / ~$7.00 projected      │    │
│  │                                                          │    │
│  │  [Rotate]                                                │    │
│  └──────────────────────────────────────────────────────────┘    │
│                                                                  │
│  ┌─ OCI (secondary) ──────── ● Healthy ───────────────────┐     │
│  │                                                          │    │
│  │  VM.Standard.A1.Flex | us-ashburn-1 | wg0: 10.42.42.2  │    │
│  │  Public IP: 150.230.67.89                               │    │
│  │  Provisioned: 3 days ago                                │    │
│  │                                                          │    │
│  │  Today          This Month                               │    │
│  │  Requests: 0 (standby)   Egress: 0 MB / 10 TB free     │    │
│  │  Cost: $0.00 (free tier)                                │    │
│  │                                                          │    │
│  │  [Rotate]                                                │    │
│  └──────────────────────────────────────────────────────────┘    │
│                                                                  │
│  ┌─ Recent Events ─────────────────────────────────────────┐     │
│  │  2h ago   AWS relay health check: OK                    │     │
│  │  6h ago   OCI relay provisioned (replaced old VM)       │     │
│  │  1d ago   AWS relay rotated (429 storm detected)        │     │
│  │  3d ago   Relay fleet deployed                          │     │
│  └──────────────────────────────────────────────────────────┘    │
│                                                                  │
│  ┌─ Alerting Rules ────────────────────────────────────────┐     │
│  │  RelayFleetDegraded     < 2 healthy   ✅ Not firing     │     │
│  │  RelayFleetCritical     == 0 healthy   ✅ Not firing     │     │
│  │  RelayProvisioningFailed == 1           ✅ Not firing     │     │
│  │  Relay429RateHigh       > 30%          ✅ Not firing     │     │
│  └──────────────────────────────────────────────────────────┘    │
│                                                                  │
│  [Pause Relay]  [Redeploy Fleet]  [Edit Config]                 │
└──────────────────────────────────────────────────────────────────┘
```

### Error states

When a relay is unhealthy or provisioning failed:

```
┌─ AWS (primary) ─────────── ● Unhealthy ─────────────────┐
│                                                          │
│  ⚠ Provisioning failed — 3 consecutive attempts         │
│                                                          │
│  Error: InvalidParameterValue: Invalid AMI id           │
│  Last attempt: 5 minutes ago                            │
│                                                          │
│  The circuit breaker has tripped. The controller has    │
│  stopped retrying. Fix the root cause and clear the     │
│  condition:                                              │
│                                                          │
│  kubectl patch inferencerelay relay-fleet --type=json \ │
│    -p='[{"op":"remove","path":"/status/conditions/..."}]'│
│                                                          │
│  [Copy] [Clear Condition] [View Logs]                   │
└──────────────────────────────────────────────────────────┘
```

---

## API Design

### `GET /api/v1/admin/relay/status`

Returns the full fleet status, aggregating CR status + router metrics.

**Response:**
```json
{
  "deployed": true,
  "overall": "healthy",
  "healthyReplicas": 2,
  "totalReplicas": 2,
  "fallbackActive": false,
  "activeStreams": 3,
  "instances": [
    {
      "id": "aws-1",
      "provider": "aws",
      "region": "us-east-1",
      "shape": "t4g.micro",
      "wgIP": "10.42.42.4",
      "publicIP": "54.210.123.45",
      "state": "healthy",
      "healthy": true,
      "provisionedAt": "2026-06-11T10:30:00Z",
      "metrics": {
        "requestsToday": 12847,
        "requests429Today": 3,
        "totalRequests": 450000,
        "egressBytes": 149546362,
        "egressLimitBytes": 107374182400,
        "activeStreams": 3
      },
      "cost": {
        "monthlyEstimate": 700,
        "spentThisMonth": 68
      }
    },
    {
      "id": "oci-1",
      "provider": "oci",
      "region": "us-ashburn-1",
      "shape": "VM.Standard.A1.Flex",
      "wgIP": "10.42.42.2",
      "publicIP": "150.230.67.89",
      "state": "healthy",
      "healthy": true,
      "provisionedAt": "2026-06-11T10:35:00Z",
      "metrics": {
        "requestsToday": 0,
        "requests429Today": 0,
        "totalRequests": 0,
        "egressBytes": 0,
        "egressLimitBytes": 10995116277760,
        "activeStreams": 0
      },
      "cost": {
        "monthlyEstimate": 0,
        "spentThisMonth": 0
      }
    }
  ],
  "conditions": [],
  "recentEvents": [
    {
      "timestamp": "2026-06-14T08:00:00Z",
      "type": "HealthCheck",
      "message": "AWS relay health check: OK",
      "severity": "info"
    },
    {
      "timestamp": "2026-06-14T02:00:00Z",
      "type": "Provisioned",
      "message": "OCI relay provisioned (replaced terminated VM)",
      "severity": "info"
    },
    {
      "timestamp": "2026-06-13T10:00:00Z",
      "type": "Rotated",
      "message": "AWS relay rotated (429 storm detected)",
      "severity": "warning"
    }
  ],
  "alerts": [
    {
      "name": "RelayFleetDegraded",
      "expression": "llmsafespace_relay_healthy_replicas < 2",
      "firing": false
    },
    {
      "name": "RelayFleetCritical",
      "expression": "llmsafespace_relay_healthy_replicas == 0",
      "firing": false
    }
  ]
}
```

When the fleet is not deployed (`deployed: false`), the response includes setup checklist state instead:

```json
{
  "deployed": false,
  "setup": {
    "certManagerInstalled": true,
    "metalLBInstalled": true,
    "routerDeployed": false,
    "crdInstalled": false,
    "awsConfigured": false,
    "ociConfigured": false,
    "wireGuardEndpoint": ""
  }
}
```

### `GET /api/v1/admin/relay/ca`

Downloads the root CA certificate (public key only). Used in setup step 2.

**Response:** `application/x-pem-file` with the CA cert.

### `POST /api/v1/admin/relay/aws-config`

Saves AWS IAM Roles Anywhere configuration.

**Request:**
```json
{
  "trustAnchorId": "ta-xxxxxxxxx",
  "profileId": "p-xxxxxxxxx",
  "roleArn": "arn:aws:iam::123456789012:role/llmsafespace-relay",
  "region": "us-east-1"
}
```

Writes these values into a K8s Secret (`aws-relay-irwa`). Does not store in PostgreSQL.

### `POST /api/v1/admin/relay/test-aws`

Tests the AWS connection by calling `sts:GetCallerIdentity` with the IAM Roles Anywhere temp credentials.

**Response:**
```json
{
  "valid": true,
  "accountId": "123456789012",
  "roleArn": "arn:aws:iam::123456789012:role/llmsafespace-relay"
}
```

### `POST /api/v1/admin/relay/oci-creds`

Saves OCI credentials. Writes to a K8s Secret (`oci-credentials`).

**Request:**
```json
{
  "tenancy": "ocid1.tenancy...",
  "user": "ocid1.user...",
  "fingerprint": "aa:bb:cc:dd:ee:ff:...",
  "key": "-----BEGIN RSA PRIVATE KEY-----\n...",
  "region": "us-ashburn-1"
}
```

### `POST /api/v1/admin/relay/deploy`

Creates or updates the InferenceRelay CR.

**Request:**
```json
{
  "upstreamURL": "https://opencode.ai/zen/v1",
  "wireGuard": {
    "routerEndpoint": "relay-gw.safespaces.dev:51820"
  },
  "providers": ["aws", "oci"]
}
```

### `POST /api/v1/admin/relay/rotate/:relayId`

Triggers manual rotation of a specific relay. Sets a `rotationRequested` annotation on the InferenceRelay CR; the controller picks it up on the next reconcile.

### `POST /api/v1/admin/relay/pause`

Sets `spec.paused: true` on the InferenceRelay CR. The controller stops provisioning/replacing VMs. Existing relay VMs continue running.

### `GET /api/v1/admin/relay/events`

Returns recent relay events from the InferenceRelay CR's event stream (Kubernetes Events on the CR object). Paginated.

---

## Cost Tracking

Cost is computed by the API server, not stored:

| Provider | Calculation | Source |
|----------|-------------|--------|
| AWS | `hours_running × hourly_rate` | Instance `provisionedAt` timestamp + t4g.micro on-demand rate ($0.0084/hr us-east-1) |
| OCI | Always $0 (free tier) | Hardcoded |

The API server computes this on each `/status` request from the `provisionedAt` timestamp in the CR status. No billing API calls needed.

---

## Security

1. **Secrets are write-only from the UI.** The API accepts credential values via POST but never returns them via GET. The status endpoint shows "configured: true/false" only.

2. **Root CA private key never leaves cert-manager.** The `/ca` endpoint returns only the public CA certificate (needed for the AWS trust anchor setup). The private key stays in the `cert-manager` namespace Secret.

3. **All endpoints are admin-only.** The existing admin middleware (`requireRole("admin")`) gates the `/api/v1/admin/relay/*` route group.

4. **Rotation and pause require confirmation.** Frontend shows a confirmation dialog. Backend validates the InferenceRelay CR exists and the relay ID is valid before applying the annotation.

5. **WireGuard private keys are never exposed.** Generated by the controller, stored in K8s Secrets, mounted into the router pod. The UI never reads or displays them.

---

## Story Breakdown

| Story | Title | Effort | Depends On |
|-------|-------|--------|------------|
| US-43.1 | API: `GET /admin/relay/status` — aggregate CR status + router metrics | Medium (1d) | Epic 42 US-42.9 |
| US-43.2 | API: `GET /admin/relay/setup` — prerequisite checklist (cert-manager, MetalLB, CRD, router checks) | Small (0.5d) | None |
| US-43.3 | API: `GET /admin/relay/ca` — root CA cert download | Small (0.25d) | US-43.2 |
| US-43.4 | API: `POST /admin/relay/aws-config` + `POST /admin/relay/test-aws` — save IRWA config, test connection | Small-Medium (0.75d) | US-43.2 |
| US-43.5 | API: `POST /admin/relay/oci-creds` — save OCI credentials to K8s Secret | Small (0.5d) | US-43.2 |
| US-43.6 | API: `POST /admin/relay/deploy` — create/update InferenceRelay CR | Small (0.5d) | US-43.4, US-43.5 |
| US-43.7 | API: `POST /admin/relay/rotate/:id` + `POST /admin/relay/pause` — manual actions | Small (0.5d) | US-43.1 |
| US-43.8 | Frontend: `RelaySetupWizard` — 4-step wizard with copy-paste, validation, test buttons | Medium-Large (1.5d) | US-43.2–43.6 |
| US-43.9 | Frontend: `RelayStatusDashboard` — fleet overview, per-relay cards, metrics, cost, events | Medium-Large (1.5d) | US-43.1 |
| US-43.10 | Frontend: error states — provisioning failure display, copy commands, clear condition | Small-Medium (0.75d) | US-43.9 |
| US-43.11 | Frontend: alert status — show firing Prometheus alert rules | Small (0.5d) | US-43.9 |
| US-43.12 | E2E tests: setup wizard flow, status dashboard rendering, rotation trigger | Medium (1d) | US-43.8, US-43.9 |

**Total estimated effort:** 8.75-10.5 days

---

## Dependency Graph

```
US-43.2 (setup checklist) ─────────────┐
    ├── US-43.3 (CA download)          │
    ├── US-43.4 (AWS config + test)    │
    ├── US-43.5 (OCI creds)            │
    │         │                        │
    │         └── US-43.6 (deploy CR)  │
    │                                   │
    │              ┌────────────────────┘
    │              │
    │              ├── US-43.8 (setup wizard)
    │              │
    US-43.1 (status) ──┤
                       ├── US-43.9 (status dashboard)
                       │         │
                       │         ├── US-43.10 (error states)
                       │         └── US-43.11 (alert status)
                       │
                       ├── US-43.7 (rotate/pause actions)
                       │
                       └── US-43.12 (e2e tests)
```

**Critical path:** US-43.2 → US-43.4 → US-43.6 → US-43.8 (setup wizard) and US-43.1 → US-43.9 (status dashboard)

---

## Out of Scope

| # | What | Why |
|---|------|-----|
| 1 | Per-workspace relay metrics in the UI | The relay routes are aggregated; per-workspace breakdown is a future concern if needed. |
| 2 | Historical metrics graphs | Requires a time-series store (Prometheus already has this). The dashboard shows current state; Grafana handles historical. |
| 3 | GCP credential setup in the wizard | GCP is not in the default fleet (A11 — Always Free eliminated). Operators can add GCP manually via kubectl if needed. |
| 4 | Multi-region relay management | The fleet is single-region per provider. Multi-region adds complexity without clear benefit for the current scale. |
| 5 | Relay VM SSH access from the UI | Relay VMs are stateless and should never need SSH. If debugging is needed, the operator uses cloud CLI. |
| 6 | Billing alerts / budget caps | AWS billing alerts should be configured in AWS Budgets, not in the LLMSafeSpace UI. |

---

## Open Questions

| # | Question | Notes |
|---|----------|-------|
| OQ1 | Should the setup wizard support a "skip AWS, use OCI only" path? | Yes — OCI is free and AWS is optional. The wizard should allow deploying with just OCI if the operator doesn't want to pay for AWS. |
| OQ2 | How should the dashboard handle the transition from CF Worker (Epic 26) to the new relay? | Add a "Migration" banner showing the old `inferenceRelayURL` and a "Switch to New Relay" button that updates the controller flag. Needs coordination with the controller restart. |
| OQ3 | Should recent events come from Kubernetes Events or a dedicated audit log? | K8s Events are simpler (no extra storage) but expire after ~1 hour by default. A dedicated audit log in PostgreSQL would persist but adds complexity. Recommendation: K8s Events with an event TTL override to 24h. |
