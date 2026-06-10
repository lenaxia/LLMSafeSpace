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
  instanceName: string;
  motd?: string;
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface LoginRequest {
  email: string;
  password: string;
  rememberMe?: boolean;
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
  imageTag?: string;
  agentVersion?: string;
  defaultModel?: string;
  maxActiveSessions?: number;
  agentNeedsRefresh?: boolean;
  credentialsPendingSince?: string;
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
  parentId?: string;
  lastMessageAt?: string;
  messageCount: number;
  status: string;
  lastSeenAt?: string;
  hasUnread: boolean;
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
  sessions?: AgentSessionInfo[];
  imageTag?: string;
  diskUsedBytes?: number;
  diskTotalBytes?: number;
  memoryUsedBytes?: number;
  memoryTotalBytes?: number;
  contextUsed?: number;
  contextTotal?: number;
}

export interface AgentSessionInfo {
  id: string;
  title?: string;
  status: string; // "idle" | "busy"
  contextUsed?: number;
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
  name?: string;
  input?: unknown;
  id?: string;
  files?: string[];
  hash?: string;
  toolState?: string;
  toolOutput?: string;
}

export interface Message {
  id: string;
  role: "user" | "assistant";
  parts: MessagePart[];
  createdAt?: string;
  modelID?: string;
}

export interface SendMessageRequest {
  parts: MessagePart[];
  model?: { providerID: string; modelID: string };
  messageID?: string;
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
  // The proxy synthesizes string "idle" | "busy" for this field.
  // The full retry shape (attempt, message, next, action) is NOT carried here —
  // it travels inside an opencode.event wrapper with event_type="session.status".
  status: "idle" | "busy";
}

export interface OpenCodeEvent {
  type: "opencode.event";
  event_type: string;
  data: unknown;
}

// --- Agent input request types (Epic 16) ---

export interface QuestionOption {
  label: string;
  description: string;
}

export interface QuestionInfo {
  question: string;
  header: string;
  options: QuestionOption[];
  multiple?: boolean;
}

export interface QuestionRequest {
  id: string;
  session_id: string;
  /**
   * Top-level session in the parent chain. Equals session_id for top-level
   * sessions; for subagent/subtask sessions (e.g. opencode `task` tool spawning
   * child sessions) it points at the user-visible ancestor session. The chat
   * UI matches incoming prompts against this so subtask prompts bubble up to
   * the parent session view.
   */
  root_session_id?: string;
  questions: QuestionInfo[];
  tool?: { message_id: string; call_id: string };
}

export interface PermissionRequest {
  id: string;
  session_id: string;
  /** See {@link QuestionRequest.root_session_id}. */
  root_session_id?: string;
  permission: string;
  patterns: string[];
  metadata?: Record<string, unknown>;
  always?: string[];
  tool?: { message_id: string; call_id: string };
}

export interface AgentQuestionEvent {
  type: "agent.question";
  data: QuestionRequest;
}

export interface AgentQuestionResolvedEvent {
  type: "agent.question.resolved";
  data: { request_id: string; session_id: string };
}

export interface AgentPermissionEvent {
  type: "agent.permission";
  data: PermissionRequest;
}

export interface AgentPermissionResolvedEvent {
  type: "agent.permission.resolved";
  data: { request_id: string; session_id: string; reply: string };
}

/**
 * Discriminated union of all event types delivered over the workspace SSE stream.
 * Narrow on `type` to access type-specific fields.
 */
export type WorkspaceStreamEvent =
  | WorkspacePhaseEvent
  | SessionStatusEvent
  | OpenCodeEvent
  | AgentQuestionEvent
  | AgentQuestionResolvedEvent
  | AgentPermissionEvent
  | AgentPermissionResolvedEvent;
