import { api } from "./client";

// ---------------------------------------------------------------------------
// Relay Admin API (Epic 43)
// Routes: /api/v1/admin/relay
// Providers: AWS (paid primary) + OCI (free secondary) + GCP (optional)
// ---------------------------------------------------------------------------

export interface RelaySetup {
  deployed: boolean;
  routerDeployed: boolean;
  crdInstalled: boolean;
  awsConfigured: boolean;
  ociConfigured: boolean;
  gcpConfigured: boolean;
  wireGuardEndpoint: string;
}

export interface RelayInstance {
  id: string;
  provider: string;
  region: string;
  shape?: string;
  wgIP: string;
  publicIP: string;
  state: string;
  healthy: boolean;
  metrics: {
    requestsToday: number;
    requests429Today: number;
    totalRequests: number;
    egressBytes: number;
    egressLimitBytes: number;
    activeStreams: number;
  };
  cost: {
    monthlyEstimate: number;
    spentThisMonth: number;
  };
  lastProvisionError?: string;
}

export interface RelayCondition {
  type: string;
  status: string;
  reason: string;
  message: string;
}

export interface RelayAlert {
  name: string;
  expression: string;
  firing: boolean;
}

export interface RelayEvent {
  timestamp: string;
  type: string;
  message: string;
  severity: string;
}

export interface RelayStatus {
  deployed: boolean;
  overall: string;
  healthyReplicas: number;
  totalReplicas: number;
  fallbackActive: boolean;
  activeStreams: number;
  instances: RelayInstance[];
  conditions: RelayCondition[];
  recentEvents: RelayEvent[];
  alerts: RelayAlert[];
}

export interface OCICredsRequest {
  tenancy: string;
  user: string;
  fingerprint: string;
  key: string;
  region: string;
}

export interface GCPCredsRequest {
  serviceAccountJson: string;
}

export interface AWSCredsRequest {
  accessKeyId: string;
  secretAccessKey: string;
  region: string;
}

export interface DeployRequest {
  upstreamURL?: string;
  routerEndpoint: string;
  wireGuardPort?: number;
  providers: string[];
}

export const relayApi = {
  getSetup: () => api.get<RelaySetup>("/admin/relay/setup"),
  getStatus: () => api.get<RelayStatus>("/admin/relay/status"),
  saveOCICreds: (req: OCICredsRequest) =>
    api.post<{ configured: boolean }>("/admin/relay/oci-creds", req),
  saveGCPCreds: (req: GCPCredsRequest) =>
    api.post<{ configured: boolean }>("/admin/relay/gcp-creds", req),
  saveAWSCreds: (req: AWSCredsRequest) =>
    api.post<{ configured: boolean }>("/admin/relay/aws-creds", req),
  deploy: (req: DeployRequest) =>
    api.post<{ deployed: boolean }>("/admin/relay/deploy", req),
  rotate: (id: string) =>
    api.post<{ rotating: string }>(`/admin/relay/rotate/${id}`),
  pause: () => api.post<{ paused: boolean }>("/admin/relay/pause"),
  resume: () => api.post<{ paused: boolean }>("/admin/relay/resume"),
};
