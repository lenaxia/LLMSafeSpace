import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { SessionActivityProvider, useIsSessionBusy, useIsSessionUnread, useWorkspaceBusyCount, useClearPendingUnread } from "./SessionActivityProvider";

let capturedOnEvent: ((data: unknown) => void) | undefined;

vi.mock("../hooks/useUserEventStream", () => ({
  useUserEventStream: (options?: { onEvent?: (data: unknown) => void }) => {
    capturedOnEvent = options?.onEvent;
  },
}));

function renderProvider(children?: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const result = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SessionActivityProvider>
          {children ?? <div data-testid="child" />}
        </SessionActivityProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { qc, ...result };
}

describe("SessionActivityProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capturedOnEvent = undefined;
  });

  it("registers onEvent callback with useUserEventStream", () => {
    renderProvider();
    expect(capturedOnEvent).toBeDefined();
  });

  it("tracks busy session on session.status busy event", () => {
    function BusyIndicator() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    renderProvider(<BusyIndicator />);
    expect(screen.getByTestId("busy").textContent).toBe("no");

    act(() => {
      capturedOnEvent!({
        type: "session.status",
        workspace_id: "ws-1",
        session_id: "sess-1",
        status: "busy",
      });
    });

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  it("clears busy and marks unread on session.status idle event (non-current)", () => {
    function StatusDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      const isUnread = useIsSessionUnread("sess-1");
      return (
        <>
          <span data-testid="busy">{isBusy ? "yes" : "no"}</span>
          <span data-testid="unread">{isUnread ? "yes" : "no"}</span>
        </>
      );
    }

    renderProvider(<StatusDisplay />);

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("no");
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  it("does not mark unread for current session on idle", () => {
    function UnreadDisplay() {
      const isUnread = useIsSessionUnread("sess-1");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={["/chat/ws-1/sess-1"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={
              <SessionActivityProvider>
                <UnreadDisplay />
              </SessionActivityProvider>
            } />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });

    expect(screen.getByTestId("unread").textContent).toBe("no");
  });

  it("clears all workspace sessions on workspace.phase non-Active", () => {
    function Display() {
      const busy1 = useIsSessionBusy("sess-1");
      const busy2 = useIsSessionBusy("sess-2");
      const unread1 = useIsSessionUnread("sess-1");
      return (
        <>
          <span data-testid="busy1">{busy1 ? "yes" : "no"}</span>
          <span data-testid="busy2">{busy2 ? "yes" : "no"}</span>
          <span data-testid="unread1">{unread1 ? "yes" : "no"}</span>
        </>
      );
    }

    renderProvider(<Display />);

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-2", status: "busy" });
    });
    expect(screen.getByTestId("busy1").textContent).toBe("yes");
    expect(screen.getByTestId("busy2").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "workspace.phase", workspace_id: "ws-1", phase: "Suspended" });
    });
    expect(screen.getByTestId("busy1").textContent).toBe("no");
    expect(screen.getByTestId("busy2").textContent).toBe("no");
    expect(screen.getByTestId("unread1").textContent).toBe("no");
  });

  it("workspace.phase only clears sessions for that workspace, not others", () => {
    function Display() {
      const busy1 = useIsSessionBusy("sess-1");
      const busy3 = useIsSessionBusy("sess-3");
      const unread1 = useIsSessionUnread("sess-1");
      const unread3 = useIsSessionUnread("sess-3");
      return (
        <>
          <span data-testid="busy1">{busy1 ? "yes" : "no"}</span>
          <span data-testid="busy3">{busy3 ? "yes" : "no"}</span>
          <span data-testid="unread1">{unread1 ? "yes" : "no"}</span>
          <span data-testid="unread3">{unread3 ? "yes" : "no"}</span>
        </>
      );
    }

    renderProvider(<Display />);

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-2", session_id: "sess-3", status: "busy" });
    });
    expect(screen.getByTestId("busy1").textContent).toBe("yes");
    expect(screen.getByTestId("busy3").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-2", session_id: "sess-3", status: "idle" });
    });
    expect(screen.getByTestId("unread1").textContent).toBe("yes");
    expect(screen.getByTestId("unread3").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "workspace.phase", workspace_id: "ws-1", phase: "Suspended" });
    });
    expect(screen.getByTestId("busy1").textContent).toBe("no");
    expect(screen.getByTestId("unread1").textContent).toBe("no");
    expect(screen.getByTestId("busy3").textContent).toBe("no");
    expect(screen.getByTestId("unread3").textContent).toBe("yes");
  });

  it("workspaceBusyCount returns correct count for workspace", () => {
    function CountDisplay() {
      const count = useWorkspaceBusyCount("ws-1");
      return <span data-testid="count">{count}</span>;
    }

    renderProvider(<CountDisplay />);
    expect(screen.getByTestId("count").textContent).toBe("0");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-2", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-2", session_id: "sess-3", status: "busy" });
    });
    expect(screen.getByTestId("count").textContent).toBe("2");
  });

  it("clearPendingUnread removes session from unread set", async () => {
    function UnreadWithClear() {
      const isUnread = useIsSessionUnread("sess-1");
      const clear = useClearPendingUnread();
      return (
        <>
          <span data-testid="unread">{isUnread ? "yes" : "no"}</span>
          <button data-testid="clear" onClick={() => clear("sess-1")} />
        </>
      );
    }

    renderProvider(<UnreadWithClear />);

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");

    await act(() => {
      screen.getByTestId("clear").click();
    });
    expect(screen.getByTestId("unread").textContent).toBe("no");
  });

  it("updates session cache query data on status events", () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle" },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <div />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });

    const sessions = qc.getQueryData(["sessions", "ws-1"]) as Array<{ id: string; status: string }>;
    expect(sessions.find((s) => s.id === "sess-1")?.status).toBe("active");
  });

  // Test 17: Provider initializes busySessions from REST session cache on mount.
  it("initializes busy state from cached REST data with status:active (#17)", async () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-active");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-active", title: "Active", messageCount: 0, status: "active", hasUnread: false },
      { id: "sess-idle",   title: "Idle",   messageCount: 0, status: "idle",   hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <BusyDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  // Test 23: Provider initializes pendingUnread from REST session cache on mount.
  it("initializes unread state from cached REST data with hasUnread:true (#23)", async () => {
    function UnreadDisplay() {
      const isUnread = useIsSessionUnread("sess-unread");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-unread", title: "Unread", messageCount: 3, status: "idle", hasUnread: true },
      { id: "sess-seen",   title: "Seen",   messageCount: 1, status: "idle", hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <UnreadDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  it("does not mark idle+read session as unread on REST init (#23 boundary)", async () => {
    function UnreadDisplay() {
      const isUnread = useIsSessionUnread("sess-seen");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-seen", title: "Seen", messageCount: 1, status: "idle", hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <UnreadDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread").textContent).toBe("no");
  });

  // Multi-workspace REST init: exercises the full loop (lines 31-41 of provider).
  it("initializes state from multiple workspaces' caches on mount", () => {
    function MultiDisplay() {
      const busyA  = useIsSessionBusy("sess-a");
      const busyB  = useIsSessionBusy("sess-b");
      const unreadC = useIsSessionUnread("sess-c");
      const unreadD = useIsSessionUnread("sess-d");
      return (
        <>
          <span data-testid="busyA">{busyA ? "yes" : "no"}</span>
          <span data-testid="busyB">{busyB ? "yes" : "no"}</span>
          <span data-testid="unreadC">{unreadC ? "yes" : "no"}</span>
          <span data-testid="unreadD">{unreadD ? "yes" : "no"}</span>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-a", status: "active", hasUnread: false, messageCount: 1 },
      { id: "sess-c", status: "idle",   hasUnread: true,  messageCount: 2 },
    ]);
    qc.setQueryData(["sessions", "ws-2"], [
      { id: "sess-b", status: "active", hasUnread: false, messageCount: 3 },
      { id: "sess-d", status: "idle",   hasUnread: true,  messageCount: 1 },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <MultiDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("busyA").textContent).toBe("yes");
    expect(screen.getByTestId("busyB").textContent).toBe("yes");
    expect(screen.getByTestId("unreadC").textContent).toBe("yes");
    expect(screen.getByTestId("unreadD").textContent).toBe("yes");
  });
});
