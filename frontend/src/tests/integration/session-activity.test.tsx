import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, act } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SessionActivityProvider, useIsSessionBusy, useIsSessionUnread } from "../../providers/SessionActivityProvider";

let capturedOnEvent: ((data: unknown) => void) | undefined;

vi.mock("../../hooks/useUserEventStream", () => ({
  useUserEventStream: (options?: { onEvent?: (data: unknown) => void }) => {
    capturedOnEvent = options?.onEvent;
  },
}));

vi.mock("../../api/workspaces", () => ({
  workspacesApi: {
    list: vi.fn().mockResolvedValue({
      items: [
        { id: "ws-1", name: "alpha", phase: "Active", userId: "u1", runtime: "python", storageSize: "5Gi", createdAt: "", updatedAt: "" },
        { id: "ws-2", name: "beta", phase: "Active", userId: "u1", runtime: "base", storageSize: "5Gi", createdAt: "", updatedAt: "" },
      ],
      pagination: { limit: 20, offset: 0, total: 2 },
    }),
    getStatus: vi.fn().mockResolvedValue({ phase: "Active" }),
    activate: vi.fn().mockResolvedValue({ resumed: "ws-1" }),
    getSessions: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue({ models: [], currentModel: "" }),
    markSessionSeen: vi.fn().mockResolvedValue(undefined),
    renameWorkspace: vi.fn().mockResolvedValue(undefined),
    deleteWorkspace: vi.fn().mockResolvedValue(undefined),
    suspend: vi.fn().mockResolvedValue(undefined),
    deleteSession: vi.fn().mockResolvedValue(undefined),
  },
}));
vi.mock("../../api/messages", () => {
  const gh = vi.fn().mockResolvedValue([]);
  return { messagesApi: { getHistory: gh, getHistoryPage: vi.fn().mockImplementation(async () => { const msgs = await gh(); return { messages: msgs, nextCursor: undefined }; }), sendAsync: vi.fn() } };
});
vi.mock("../../api/sessions", () => ({ sessionsApi: { create: vi.fn().mockResolvedValue({ sessionId: "sess-auto" }) } }));
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));
vi.mock("../../providers/AuthProvider", () => ({
  AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useAuth: () => ({ user: { id: "u1", username: "alice" }, logout: vi.fn() }),
}));

function BusyIndicator({ sessionId }: { sessionId: string }) {
  const isBusy = useIsSessionBusy(sessionId);
  return <span data-testid={`busy-${sessionId}`}>{isBusy ? "busy" : "idle"}</span>;
}

function UnreadIndicator({ sessionId }: { sessionId: string }) {
  const isUnread = useIsSessionUnread(sessionId);
  return <span data-testid={`unread-${sessionId}`}>{isUnread ? "unread" : "read"}</span>;
}

function IntegrationShell({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SessionActivityProvider>
          {children}
        </SessionActivityProvider>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("Integration: SSE → SessionActivityProvider → UI (#36-39)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capturedOnEvent = undefined;
  });

  it("#36: SSE busy → provider tracks → idle → pulsation", async () => {
    render(
      <IntegrationShell>
        <BusyIndicator sessionId="sess-1" />
        <UnreadIndicator sessionId="sess-1" />
      </IntegrationShell>,
    );

    expect(screen.getByTestId("busy-sess-1").textContent).toBe("idle");
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("read");

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy-sess-1").textContent).toBe("busy");

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("busy-sess-1").textContent).toBe("idle");
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("unread");
  });

  it("#36b: busy session in ws-2 does not affect ws-1 indicators", async () => {
    render(
      <IntegrationShell>
        <BusyIndicator sessionId="sess-1" />
        <BusyIndicator sessionId="sess-2" />
      </IntegrationShell>,
    );

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-2", session_id: "sess-2", status: "busy" });
    });
    expect(screen.getByTestId("busy-sess-1").textContent).toBe("idle");
    expect(screen.getByTestId("busy-sess-2").textContent).toBe("busy");
  });

  it("#38: simulate page refresh — REST data feeds busy state", () => {
    function RestSimulator() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy-sess-1">{isBusy ? "busy" : "idle"}</span>;
    }

    render(
      <IntegrationShell>
        <RestSimulator />
      </IntegrationShell>,
    );

    expect(screen.getByTestId("busy-sess-1").textContent).toBe("idle");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy-sess-1").textContent).toBe("busy");
  });

  it("#39: simulate page refresh — REST hasUnread feeds unread state", async () => {
    function RestUnreadSimulator() {
      const isUnread = useIsSessionUnread("sess-1");
      return <span data-testid="unread-sess-1">{isUnread ? "unread" : "read"}</span>;
    }

    render(
      <IntegrationShell>
        <RestUnreadSimulator />
      </IntegrationShell>,
    );

    expect(screen.getByTestId("unread-sess-1").textContent).toBe("read");

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("unread");
  });

  it("#37: navigate to unread session → clearPendingUnread → unread clears", async () => {
    function NavigateSimulator() {
      const isUnread = useIsSessionUnread("sess-1");
      return (
        <div>
          <span data-testid="unread-sess-1">{isUnread ? "unread" : "read"}</span>
        </div>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat"]}>
          <SessionActivityProvider>
            <NavigateSimulator />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("unread");
  });

  it("workspace.phase Suspended clears busy and unread for that workspace", async () => {
    render(
      <IntegrationShell>
        <BusyIndicator sessionId="sess-1" />
        <UnreadIndicator sessionId="sess-1" />
      </IntegrationShell>,
    );

    await act(async () => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("busy-sess-1").textContent).toBe("idle");
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("unread");

    await act(async () => {
      capturedOnEvent!({ type: "workspace.phase", workspace_id: "ws-1", phase: "Suspended" });
    });
    expect(screen.getByTestId("unread-sess-1").textContent).toBe("read");
  });
});
