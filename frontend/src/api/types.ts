// Hand-written TypeScript types matching Go pkg/types/types.go
// Contract-tested via CI (see §17 of FRONTEND.md)

export interface User {
  id: string;
  username: string;
  email: string;
  role: string;
  active: boolean;
  createdAt: string;
}

export interface AuthConfig {
  registrationEnabled: boolean;
  oidcEnabled: boolean;
  ssoProviders?: string[];
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  username: string;
  email: string;
  password: string;
}

export interface WorkspaceListItem {
  id: string;
  name: string;
  userId: string;
  runtime: string;
  storageSize: string;
  createdAt: string;
  updatedAt: string;
  phase?: string;
  maxActiveSessions?: number;
}

export interface WorkspaceListResponse {
  items: WorkspaceListItem[];
  pagination: { limit: number; offset: number; total: number };
}

export interface ActivateWorkspaceResponse {
  resumed: string;
  suspended?: string;
}

export interface SessionListItem {
  id: string;
  title?: string;
  lastMessageAt?: string;
  messageCount: number;
  status: string; // "active" | "idle"
}

// Shape returned by the opencode agent GET /session/:id (proxied through)
export interface OpenCodeSession {
  id: string;
  title?: string;
  parentID?: string;
  share?: string;
}

export interface ActiveSessionsResponse {
  active: string[];
  maxActive: number;
}

export interface WorkspaceSessionItem {
  id: string;
  phase: string;
  podIP?: string;
  workspaceRef?: string;
}

export interface WorkspaceStatus {
  phase: string;
  podName?: string;
  endpoint?: string;
  credentialState?: CredentialState;
  agentHealth?: AgentHealth;
}

export interface CredentialState {
  available: boolean;
  reason?: string;
  message?: string;
}

export interface AgentHealth {
  status: string;
  providersConfigured?: number;
  agentVersion?: string;
  connected?: string[];
  message?: string;
  lastCheckedAt?: string;
}

export interface MessagePart {
  type: string;
  text?: string;
}

export interface Message {
  id: string;
  role: "user" | "assistant";
  parts: MessagePart[];
  createdAt?: string;
}

export interface SendMessageRequest {
  parts: MessagePart[];
}

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt?: string;
}

export interface CreateApiKeyRequest {
  name: string;
}

export interface CreateApiKeyResponse {
  key: string;
  apiKey: ApiKey;
}

export interface ApiError {
  error: string;
  code?: string;
}

// --- Workspace SSE event types ---
// These match the WorkspaceSSEEvent struct emitted by the backend broker.

export interface WorkspacePhaseEvent {
  type: "workspace.phase";
  phase: string;
}

export interface SessionStatusEvent {
  type: "session.status";
  session_id: string;
  status: "idle" | "busy";
}

/**
 * Discriminated union of all event types delivered over the workspace SSE stream.
 * Narrow on `type` to access type-specific fields.
 */
export type WorkspaceStreamEvent = WorkspacePhaseEvent | SessionStatusEvent;
