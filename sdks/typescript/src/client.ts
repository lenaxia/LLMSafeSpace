import {
  AuthError,
  ConflictError,
  LLMSafeSpaceError,
  NotFoundError,
  RateLimitError,
  TimeoutError,
} from "./errors.js";
import type {
  ActivateWorkspaceResponse,
  ActiveSessionsResponse,
  APIKey,
  AuthResponse,
  ClientOptions,
  CreateSecretRequest,
  CreateWorkspaceRequest,
  EnsureSessionResponse,
  FetchFn,
  MessageResponse,
  SecretResponse,
  SessionListItem,
  TerminalTicket,
  User,
  Workspace,
  WorkspaceListResult,
  WorkspaceStatusResult,
} from "./types.js";

const DEFAULT_TIMEOUT = 120_000;

export class LLMSafeSpace {
  private readonly baseUrl: string;
  private readonly timeout: number;
  private readonly fetchFn: FetchFn;
  private token: string | undefined;
  private apiKey: string | undefined;
  private credentials: { email: string; password: string } | undefined;
  private loggingIn = false;

  public readonly workspaces: WorkspacesAPI;
  public readonly sessions: SessionsAPI;
  public readonly auth: AuthAPI;
  public readonly secrets: SecretsAPI;
  public readonly terminal: TerminalAPI;

  constructor(options: ClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/$/, "");
    this.timeout = options.timeout ?? DEFAULT_TIMEOUT;
    this.apiKey = options.apiKey;
    this.credentials = options.credentials;
    this.fetchFn = options.fetch ?? globalThis.fetch.bind(globalThis);

    this.workspaces = new WorkspacesAPI(this);
    this.sessions = new SessionsAPI(this);
    this.auth = new AuthAPI(this);
    this.secrets = new SecretsAPI(this);
    this.terminal = new TerminalAPI(this);
  }

  /** Internal: make an authenticated request. */
  async request<T>(method: string, path: string, body?: unknown, timeout?: number): Promise<T> {
    const url = `${this.baseUrl}/api/v1${path}`;
    const headers: Record<string, string> = { "Content-Type": "application/json" };

    if (this.apiKey) {
      headers["Authorization"] = `Bearer ${this.apiKey}`;
    } else if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    } else if (this.credentials && !this.loggingIn) {
      await this.login();
      headers["Authorization"] = `Bearer ${this.token}`;
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout ?? this.timeout);

    let res: Response;
    try {
      res = await this.fetchFn(url, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });
    } catch (e: unknown) {
      clearTimeout(timer);
      if (e instanceof Error && e.name === "AbortError") {
        throw new TimeoutError();
      }
      throw e;
    }
    clearTimeout(timer);

    // Handle 401 with auto-retry if credentials available (token expired)
    if (res.status === 401 && this.credentials && this.token) {
      this.token = undefined;
      return this.request<T>(method, path, body, timeout);
    }

    if (!res.ok) {
      const errBody = await res.json().catch(() => ({ error: res.statusText }));
      const msg = (errBody as { error?: string }).error ?? res.statusText;
      switch (res.status) {
        case 401:
        case 403:
          throw new AuthError(msg, res.status);
        case 404:
          throw new NotFoundError(msg);
        case 409:
          throw new ConflictError(msg);
        case 429:
          throw new RateLimitError(msg);
        default:
          throw new LLMSafeSpaceError(msg, res.status);
      }
    }

    if (res.status === 204 || res.status === 202) return undefined as T;
    return res.json() as Promise<T>;
  }

  private async login(): Promise<void> {
    if (!this.credentials) throw new AuthError("No credentials configured");
    this.loggingIn = true;
    try {
      const url = `${this.baseUrl}/api/v1/auth/login`;
      const res = await this.fetchFn(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(this.credentials),
      });
      if (!res.ok) throw new AuthError("Login failed", res.status);
      const data = (await res.json()) as AuthResponse;
      this.token = data.token;
    } finally {
      this.loggingIn = false;
    }
  }
}

class WorkspacesAPI {
  constructor(private client: LLMSafeSpace) {}

  list(limit = 20, offset = 0) {
    return this.client.request<WorkspaceListResult>("GET", `/workspaces?limit=${limit}&offset=${offset}`);
  }
  create(req: CreateWorkspaceRequest) {
    return this.client.request<Workspace>("POST", "/workspaces", req);
  }
  get(id: string) {
    return this.client.request<Workspace>("GET", `/workspaces/${id}`);
  }
  rename(id: string, name: string) {
    return this.client.request<void>("PUT", `/workspaces/${id}`, { name });
  }
  delete(id: string) {
    return this.client.request<void>("DELETE", `/workspaces/${id}`);
  }
  getStatus(id: string) {
    return this.client.request<WorkspaceStatusResult>("GET", `/workspaces/${id}/status`);
  }
  activate(id: string) {
    return this.client.request<ActivateWorkspaceResponse>("POST", `/workspaces/${id}/activate`);
  }
  suspend(id: string) {
    return this.client.request<void>("POST", `/workspaces/${id}/suspend`);
  }
  resume(id: string) {
    return this.client.request<void>("POST", `/workspaces/${id}/resume`);
  }
  setBindings(id: string, secretIds: string[]) {
    return this.client.request<void>("PUT", `/workspaces/${id}/bindings`, { secretIds });
  }
  setEnv(id: string, env: Record<string, string>) {
    return this.client.request<void>("PUT", `/workspaces/${id}/env`, env);
  }
  getEnv(id: string) {
    return this.client.request<Record<string, string>>("GET", `/workspaces/${id}/env`);
  }
}

class SessionsAPI {
  constructor(private client: LLMSafeSpace) {}

  ensure(workspaceId: string) {
    return this.client.request<EnsureSessionResponse>("POST", `/workspaces/${workspaceId}/sessions/new`);
  }
  list(workspaceId: string) {
    return this.client.request<SessionListItem[]>("GET", `/workspaces/${workspaceId}/sessions`);
  }
  getActive(workspaceId: string) {
    return this.client.request<ActiveSessionsResponse>("GET", `/workspaces/${workspaceId}/sessions/active`);
  }
  rename(workspaceId: string, sessionId: string, title: string) {
    return this.client.request<void>("PUT", `/workspaces/${workspaceId}/sessions/${sessionId}/title`, { title });
  }
  async sendMessage(workspaceId: string, sessionId: string, content: string): Promise<MessageResponse> {
    const raw = await this.client.request<unknown>(
      "POST",
      `/workspaces/${workspaceId}/sessions/${sessionId}/message`,
      { content, parts: [{ type: "text", text: content }] },
    );
    return { raw, content: extractTextContent(raw) };
  }
  getHistory(workspaceId: string, sessionId: string) {
    return this.client.request<unknown[]>("GET", `/workspaces/${workspaceId}/sessions/${sessionId}/message`);
  }
  abort(workspaceId: string, sessionId: string) {
    return this.client.request<void>("POST", `/workspaces/${workspaceId}/sessions/${sessionId}/abort`);
  }
}

class AuthAPI {
  constructor(private client: LLMSafeSpace) {}

  me() {
    return this.client.request<User>("GET", "/auth/me");
  }
  listApiKeys() {
    return this.client.request<APIKey[]>("GET", "/auth/api-keys");
  }
  createApiKey(name: string) {
    return this.client.request<APIKey>("POST", "/auth/api-keys", { name });
  }
  deleteApiKey(id: string) {
    return this.client.request<void>("DELETE", `/auth/api-keys/${id}`);
  }
}

class SecretsAPI {
  constructor(private client: LLMSafeSpace) {}

  create(req: CreateSecretRequest) {
    return this.client.request<SecretResponse>("POST", "/secrets", req);
  }
  list() {
    return this.client.request<SecretResponse[]>("GET", "/secrets");
  }
  get(id: string) {
    return this.client.request<SecretResponse>("GET", `/secrets/${id}`);
  }
  delete(id: string) {
    return this.client.request<void>("DELETE", `/secrets/${id}`);
  }
  reveal(id: string) {
    return this.client.request<{ value: string }>("POST", `/secrets/${id}/reveal`);
  }
}

class TerminalAPI {
  constructor(private client: LLMSafeSpace) {}

  getTicket(workspaceId: string) {
    return this.client.request<TerminalTicket>("POST", `/workspaces/${workspaceId}/terminal/ticket`);
  }
}

/** Extract text content from opencode response parts. */
function extractTextContent(raw: unknown): string {
  if (!raw || typeof raw !== "object") return "";
  const obj = raw as { parts?: Array<{ type?: string; text?: string }> };
  if (!Array.isArray(obj.parts)) return "";
  return obj.parts
    .filter((p) => p.type === "text" && p.text)
    .map((p) => p.text!)
    .join("");
}
