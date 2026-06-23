import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { SessionActivityProvider, useIsSessionBusy, useIsSessionUnread, useWorkspaceBusyCount, useClearPendingUnread, useIsSessionPendingAction, useAddPendingAction, useRemovePendingAction, useSessionPendingActions, useAddPendingQuestion, useAddPendingPermission, usePendingQuestionsForSession, usePendingPermissionsForSession, useClearSessionPendingPrompts } from "./SessionActivityProvider";
import type { QuestionRequest, PermissionRequest } from "../api/types";

let capturedOnEvent: ((data: unknown) => void) | undefined;
let capturedOnReconnect: (() => void) | undefined;

vi.mock("../hooks/useUserEventStream", () => ({
  useUserEventStream: (options?: { onEvent?: (data: unknown) => void; onReconnect?: () => void }) => {
    capturedOnEvent = options?.onEvent;
    capturedOnReconnect = options?.onReconnect;
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
    capturedOnReconnect = undefined;
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

  // Regression: SSE idle event must not be clobbered by seedFromCache when
  // the query cache is populated (the clobbering bug introduced by the
  // queryCache.subscribe pattern). The idle handler now writes hasUnread:true
  // into the cache so seedFromCache re-seeds the correct unread state.
  it("SSE idle event preserves unread indicator even when cache is pre-populated (regression)", () => {
    function UnreadDisplay() {
      const isUnread = useIsSessionUnread("sess-1");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    // Pre-populate cache with hasUnread:false (simulates REST data loaded before SSE)
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "active", hasUnread: false },
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

    // Session starts not-unread
    expect(screen.getByTestId("unread").textContent).toBe("no");

    // SSE: session goes idle on a non-current workspace (user is not viewing it)
    act(() => {
      capturedOnEvent!({
        type: "session.status",
        workspace_id: "ws-1",
        session_id: "sess-1",
        status: "idle",
      });
    });

    // Unread indicator must survive — seedFromCache must read hasUnread:true
    // (written by the idle handler) and not clobber pendingUnread with stale data
    expect(screen.getByTestId("unread").textContent).toBe("yes");

    // The cache entry must also reflect hasUnread:true for the next seedFromCache
    const sessions = qc.getQueryData(["sessions", "ws-1"]) as Array<{ id: string; hasUnread: boolean }>;
    expect(sessions.find((s) => s.id === "sess-1")?.hasUnread).toBe(true);
  });

  // Regression: clearPendingUnread must suppress re-adding the session from a
  // stale REST refetch (markSessionSeen PUT racing the GET) until REST confirms
  // hasUnread:false. This replaces the old "write hasUnread:false to cache"
  // approach with clearedRef suppression that survives a stale refetch.
  it("clearPendingUnread suppresses stale refetch and releases on REST confirm", () => {
    function Display() {
      const isUnread = useIsSessionUnread("sess-1");
      const clear = useClearPendingUnread();
      return (
        <>
          <span data-testid="unread">{isUnread ? "yes" : "no"}</span>
          <button data-testid="clear" onClick={() => clear("sess-1")}>clear</button>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    // Start with session already unread in cache
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: true },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread").textContent).toBe("yes");

    // Clear unread (user navigates to the session)
    act(() => {
      screen.getByTestId("clear").click();
    });

    expect(screen.getByTestId("unread").textContent).toBe("no");

    // Stale refetch: REST still returns hasUnread:true (PUT not committed).
    // reconcileUnread must NOT re-add it (clearedRef suppression).
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("no");

    // Real refetch: REST now confirms hasUnread:false (PUT committed).
    // clearedRef is released so a future unread response will pulse again.
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: false },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("no");

    // New unread response arrives via REST — should pulse again now that
    // clearedRef was released.
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 2, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  // Regression: SSE-tracked busy session must survive a cache refetch that
  // returns status:"idle" for that session. This happens when the REST API
  // enrichment misses the busy state (multi-replica, timing gap, etc.).
  // seedFromCache must preserve SSE-tracked sessions instead of clobbering.
  it("SSE busy state survives cache refetch returning status:idle (regression)", () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
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

    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
      ]);
    });

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  // Variant: SSE busy in ws-1 survives a refetch of ws-2 sessions
  it("SSE busy state in ws-1 survives cache update for ws-2", () => {
    function Display() {
      const busy1 = useIsSessionBusy("sess-1");
      const busy2 = useIsSessionBusy("sess-2");
      return (
        <>
          <span data-testid="busy1">{busy1 ? "yes" : "no"}</span>
          <span data-testid="busy2">{busy2 ? "yes" : "no"}</span>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
    ]);
    qc.setQueryData(["sessions", "ws-2"], [
      { id: "sess-2", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    act(() => {
      capturedOnEvent!({
        type: "session.status",
        workspace_id: "ws-1",
        session_id: "sess-1",
        status: "busy",
      });
    });
    expect(screen.getByTestId("busy1").textContent).toBe("yes");
    expect(screen.getByTestId("busy2").textContent).toBe("no");

    act(() => {
      qc.setQueryData(["sessions", "ws-2"], [
        { id: "sess-2", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
      ]);
    });

    expect(screen.getByTestId("busy1").textContent).toBe("yes");
    expect(screen.getByTestId("busy2").textContent).toBe("no");
  });

  // Regression: workspace suspend/resume re-seeds from REST. After suspend
  // clears state and removes the workspace from seeded, the next cache update
  // (triggered by sidebar's invalidateQueries on activate) re-seeds busy
  // sessions that were active before the suspend.
  it("workspace suspend then resume re-seeds busy state from REST (regression)", () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "active", hasUnread: false },
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

    act(() => {
      capturedOnEvent!({ type: "workspace.phase", workspace_id: "ws-1", phase: "Suspended" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("no");

    // Simulate REST returning the session as active after resume
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "active", hasUnread: false },
      ]);
    });

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  // Regression: workspace Active phase resets seeding so stale REST data
  // doesn't block re-seeding. This handles the case where the SSE tracker
  // reconnects after a resume and the pod has sessions that are already busy.
  it("workspace.phase Active resets seeding for re-seed", () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
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

    expect(screen.getByTestId("busy").textContent).toBe("no");

    // Simulate resume: phase goes Active, REST now says session is active
    act(() => {
      capturedOnEvent!({ type: "workspace.phase", workspace_id: "ws-1", phase: "Active" });
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "active", hasUnread: false },
      ]);
    });

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  // Regression: SSE reconnect clears seeded set so workspaces get re-seeded
  // from current REST data. Covers the gap where events are missed during
  // reconnection and REST has the correct state.
  it("SSE reconnect re-seeds from REST (regression)", () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
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

    expect(screen.getByTestId("busy").textContent).toBe("no");

    // Session goes busy via SSE
    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("yes");

    // Simulate SSE reconnect — clears seeded
    act(() => {
      capturedOnReconnect!();
    });

    // REST now shows session as active (e.g., different replica or the
    // enrichment caught up). The re-seed should pick it up.
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "active", hasUnread: false },
      ]);
    });

    expect(screen.getByTestId("busy").textContent).toBe("yes");
  });

  // Regression: stale busy sessions must be cleared on SSE reconnect.
  // Without clearing, a session that completed during the disconnect gap
  // (idle event lost to replay buffer overflow) shows as permanently busy.
  it("SSE reconnect clears stale busy state (regression)", () => {
    function BusyDisplay() {
      const isBusy = useIsSessionBusy("sess-1");
      return <span data-testid="busy">{isBusy ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
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

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("yes");

    act(() => {
      capturedOnReconnect!();
    });
    expect(screen.getByTestId("busy").textContent).toBe("no");

    // Re-seed from REST (session completed during gap — REST says idle)
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
      ]);
    });
    expect(screen.getByTestId("busy").textContent).toBe("no");
  });

  // Regression: pendingUnread is preserved through SSE reconnect.
  // Unread represents durable information ("session completed that you
  // haven't looked at") and should survive reconnection. The REST re-seed
  // will also re-populate unread from hasUnread in the cache.
  it("SSE reconnect preserves pendingUnread state (regression)", () => {
    function Display() {
      const isBusy = useIsSessionBusy("sess-1");
      const isUnread = useIsSessionUnread("sess-1");
      return (
        <>
          <span data-testid="busy">{isBusy ? "yes" : "no"}</span>
          <span data-testid="unread">{isUnread ? "yes" : "no"}</span>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("busy").textContent).toBe("no");
    expect(screen.getByTestId("unread").textContent).toBe("yes");

    act(() => {
      capturedOnReconnect!();
    });

    // busy cleared by reconnect, unread preserved
    expect(screen.getByTestId("busy").textContent).toBe("no");
    expect(screen.getByTestId("unread").textContent).toBe("yes");

    // Re-seed from REST — hasUnread in cache preserves unread
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  // Regression (refresh): on a full page refresh, sessions that were already
  // idle with an unread response never receive an SSE idle event (only the
  // busy→idle transition emits one). The unread state must therefore come from
  // the REST hasUnread field. The old seed-once design locked in whatever the
  // FIRST cache read returned — if that read was stale (hasUnread:false because
  // last_message_at hadn't persisted), the session never pulsed. Reconcile
  // re-reads hasUnread on every cache update, so a delayed/stale first read
  // self-heals on the next refetch.
  it("refresh: stale first read self-heals on subsequent refetch (regression)", () => {
    function UnreadDisplay() {
      const isUnread = useIsSessionUnread("sess-1");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    // Simulate a stale first REST response (hasUnread:false — the async
    // RecordMessage queue hasn't persisted last_message_at yet)
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: false },
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

    // The queue drains and the next refetch returns the correct hasUnread:true.
    // Reconcile picks it up immediately — no SSE event required.
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  // Regression (refresh, multiple workspaces): reconcile must add unread
  // sessions from any workspace whose cache data arrives, not just the first.
  it("refresh: reconcile adds unread across multiple workspaces", () => {
    function Display() {
      const u1 = useIsSessionUnread("sess-a");
      const u2 = useIsSessionUnread("sess-b");
      return (
        <>
          <span data-testid="unread-a">{u1 ? "yes" : "no"}</span>
          <span data-testid="unread-b">{u2 ? "yes" : "no"}</span>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread-a").textContent).toBe("no");
    expect(screen.getByTestId("unread-b").textContent).toBe("no");

    // ws-1 data arrives
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-a", title: "A", messageCount: 2, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread-a").textContent).toBe("yes");

    // ws-2 data arrives later
    act(() => {
      qc.setQueryData(["sessions", "ws-2"], [
        { id: "sess-b", title: "B", messageCount: 1, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread-a").textContent).toBe("yes");
    expect(screen.getByTestId("unread-b").textContent).toBe("yes");
  });

  // Regression (clear then new activity): after clearPendingUnread, a new SSE
  // idle event (a genuinely new response) must release the clearedRef
  // suppression so the session pulses again.
  it("new SSE idle after clear releases suppression and re-pulses", () => {
    function Display() {
      const isUnread = useIsSessionUnread("sess-1");
      const clear = useClearPendingUnread();
      return (
        <>
          <span data-testid="unread">{isUnread ? "yes" : "no"}</span>
          <button data-testid="clear" onClick={() => clear("sess-1")}>clear</button>
        </>
      );
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: true },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread").textContent).toBe("yes");

    act(() => { screen.getByTestId("clear").click(); });
    expect(screen.getByTestId("unread").textContent).toBe("no");

    // A stale refetch still returns hasUnread:true but must stay suppressed
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 1, status: "idle", hasUnread: true },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("no");

    // A brand-new response arrives via SSE (busy → idle). The idle handler
    // releases clearedRef and re-marks unread.
    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });

  // Regression (add-only reconcile): an SSE-set unread (a response that just
  // arrived) must survive a stale REST refetch returning hasUnread:false. This
  // happens when RecordMessage hasn't persisted last_message_at yet but the
  // sessions query refetches (e.g. ChatPage invalidates on a session.status
  // event for the current session). The old seed-once design preserved this by
  // never re-reading; reconcile preserves it by being ADD-ONLY.
  it("SSE-set unread survives stale refetch returning hasUnread:false", () => {
    function Display() {
      const isUnread = useIsSessionUnread("sess-1");
      return <span data-testid="unread">{isUnread ? "yes" : "no"}</span>;
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
    ]);

    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("unread").textContent).toBe("no");

    // SSE: a response completes for a non-current session → unread
    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "busy" });
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");

    // A refetch returns hasUnread:false (RecordMessage hasn't persisted yet).
    // The unread MUST survive — reconcile is add-only.
    act(() => {
      qc.setQueryData(["sessions", "ws-1"], [
        { id: "sess-1", title: "Test", messageCount: 0, status: "idle", hasUnread: false },
      ]);
    });
    expect(screen.getByTestId("unread").textContent).toBe("yes");
  });
});

describe("SessionActivityProvider — pending actions", () => {
  it("addPendingAction marks session as pending", async () => {
    function PendingDisplay() {
      const add = useAddPendingAction();
      const isPending = useIsSessionPendingAction("sess-1");
      return (
        <>
          <span data-testid="pending">{isPending ? "yes" : "no"}</span>
          <button data-testid="add" onClick={() => add("ws-1", "sess-1", "req-1")} />
        </>
      );
    }

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <PendingDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("pending").textContent).toBe("no");

    await act(async () => { screen.getByTestId("add").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("yes");
  });

  it("removePendingAction clears pending state", async () => {
    function PendingDisplay() {
      const add = useAddPendingAction();
      const remove = useRemovePendingAction();
      const isPending = useIsSessionPendingAction("sess-1");
      return (
        <>
          <span data-testid="pending">{isPending ? "yes" : "no"}</span>
          <button data-testid="add" onClick={() => add("ws-1", "sess-1", "req-1")} />
          <button data-testid="remove" onClick={() => remove("req-1")} />
        </>
      );
    }

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <PendingDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await act(async () => { screen.getByTestId("add").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("yes");

    await act(async () => { screen.getByTestId("remove").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("no");
  });

  it("multiple pending requests — session stays pending until last removed", async () => {
    function PendingDisplay() {
      const add = useAddPendingAction();
      const remove = useRemovePendingAction();
      const isPending = useIsSessionPendingAction("sess-1");
      return (
        <>
          <span data-testid="pending">{isPending ? "yes" : "no"}</span>
          <button data-testid="add1" onClick={() => add("ws-1", "sess-1", "req-1")} />
          <button data-testid="add2" onClick={() => add("ws-1", "sess-1", "req-2")} />
          <button data-testid="rem1" onClick={() => remove("req-1")} />
          <button data-testid="rem2" onClick={() => remove("req-2")} />
        </>
      );
    }

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <PendingDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await act(async () => { screen.getByTestId("add1").click(); });
    await act(async () => { screen.getByTestId("add2").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("yes");

    await act(async () => { screen.getByTestId("rem1").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("yes");

    await act(async () => { screen.getByTestId("rem2").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("no");
  });

  it("session idle clears pending actions", async () => {
    function PendingDisplay() {
      const add = useAddPendingAction();
      const isPending = useIsSessionPendingAction("sess-1");
      return (
        <>
          <span data-testid="pending">{isPending ? "yes" : "no"}</span>
          <button data-testid="add" onClick={() => add("ws-1", "sess-1", "req-1")}>add</button>
        </>
      );
    }

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <PendingDisplay />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await act(async () => { screen.getByTestId("add").click(); });
    expect(screen.getByTestId("pending").textContent).toBe("yes");

    act(() => {
      capturedOnEvent!({ type: "session.status", workspace_id: "ws-1", session_id: "sess-1", status: "idle" });
    });
    expect(screen.getByTestId("pending").textContent).toBe("no");
  });

  it("useSessionPendingActions returns set of pending session IDs", () => {
    function Display() {
      const pending = useSessionPendingActions();
      return <span data-testid="count">{pending.size}</span>;
    }

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("count").textContent).toBe("0");
  });
});

// --- Pending prompt content (issue #346): content lives in the global layer so
// it survives within-tab session navigation. Filtered by session at read time. ---

function makeQuestion(id: string, sessionId: string, rootSessionId?: string): QuestionRequest {
  return { id, session_id: sessionId, root_session_id: rootSessionId ?? sessionId, questions: [] };
}

function makePermission(id: string, sessionId: string, rootSessionId?: string): PermissionRequest {
  return { id, session_id: sessionId, root_session_id: rootSessionId ?? sessionId, permission: "bash", patterns: [] };
}

describe("SessionActivityProvider — pending prompt content", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capturedOnEvent = undefined;
    capturedOnReconnect = undefined;
  });

  it("stores question content and returns it filtered by session", () => {
    function Display({ sessionId }: { sessionId: string }) {
      const addQ = useAddPendingQuestion();
      const questions = usePendingQuestionsForSession(sessionId);
      return (
        <>
          <span data-testid="count">{questions.length}</span>
          <span data-testid="ids">{questions.map((q) => q.id).join(",")}</span>
          <button data-testid="add" onClick={() => addQ("ws-1", makeQuestion("q1", "sess-A"))} />
        </>
      );
    }

    renderProvider(<Display sessionId="sess-A" />);
    expect(screen.getByTestId("count").textContent).toBe("0");

    act(() => {
      screen.getByTestId("add").click();
    });

    expect(screen.getByTestId("count").textContent).toBe("1");
    expect(screen.getByTestId("ids").textContent).toBe("q1");
  });

  it("isolates prompts per session (no clear-on-navigation) — the #346 fix", () => {
    // The provider holds content keyed by request, not by the viewed session.
    // Adding a prompt for session A must NOT be affected by also adding/viewing
    // session B; querying A after touching B still returns A's prompt.
    function Display({ sessionId }: { sessionId: string }) {
      const addQ = useAddPendingQuestion();
      const questions = usePendingQuestionsForSession(sessionId);
      return (
        <>
          <span data-testid={`count-${sessionId}`}>{questions.length}</span>
          <button data-testid="add-A" onClick={() => addQ("ws-1", makeQuestion("qA", "sess-A"))} />
          <button data-testid="add-B" onClick={() => addQ("ws-1", makeQuestion("qB", "sess-B"))} />
        </>
      );
    }

    const { rerender } = renderProvider(<Display sessionId="sess-A" />);
    act(() => screen.getByTestId("add-A").click());
    // "Navigate" to sess-B and add a prompt there.
    rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display sessionId="sess-B" />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    act(() => screen.getByTestId("add-B").click());
    expect(screen.getByTestId("count-sess-B").textContent).toBe("1");

    // Navigate back to sess-A — its prompt MUST still be present (the bug would
    // have cleared it on the session switch).
    rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <MemoryRouter>
          <SessionActivityProvider>
            <Display sessionId="sess-A" />
          </SessionActivityProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByTestId("count-sess-A").textContent).toBe("1");
  });

  it("matches a subtask prompt to its parent via root_session_id", () => {
    function Display({ sessionId }: { sessionId: string }) {
      const addQ = useAddPendingQuestion();
      const questions = usePendingQuestionsForSession(sessionId);
      return (
        <>
          <span data-testid={`count-${sessionId}`}>{questions.length}</span>
          <button
            data-testid="add"
            // Subtask session "child" whose root is the parent "parent".
            onClick={() => addQ("ws-1", makeQuestion("q1", "child", "parent"))}
          />
        </>
      );
    }

    renderProvider(<Display sessionId="parent" />);
    act(() => screen.getByTestId("add").click());
    // The prompt bubbles to the parent view (root_session_id match)…
    expect(screen.getByTestId("count-parent").textContent).toBe("1");
  });

  it("removePendingAction clears the stored content (resolved event)", () => {
    function Display({ sessionId }: { sessionId: string }) {
      const addQ = useAddPendingQuestion();
      const remove = useRemovePendingAction();
      const questions = usePendingQuestionsForSession(sessionId);
      return (
        <>
          <span data-testid="count">{questions.length}</span>
          <button data-testid="add" onClick={() => addQ("ws-1", makeQuestion("q1", "sess-A"))} />
          <button data-testid="resolve" onClick={() => remove("q1")} />
        </>
      );
    }

    renderProvider(<Display sessionId="sess-A" />);
    act(() => screen.getByTestId("add").click());
    expect(screen.getByTestId("count").textContent).toBe("1");
    act(() => screen.getByTestId("resolve").click());
    expect(screen.getByTestId("count").textContent).toBe("0");
  });

  it("clearSessionPendingPrompts clears content + indicator for one session only (US-16.12 idle/error)", () => {
    // Renders BOTH sessions' pulses/content independently of a viewed-session
    // prop, so we can assert sess-B is untouched after clearing sess-A.
    function Display() {
      const addQ = useAddPendingQuestion();
      const clear = useClearSessionPendingPrompts();
      const questionsA = usePendingQuestionsForSession("sess-A");
      const questionsB = usePendingQuestionsForSession("sess-B");
      const pendingA = useIsSessionPendingAction("sess-A");
      const pendingB = useIsSessionPendingAction("sess-B");
      return (
        <>
          <span data-testid="count-A">{questionsA.length}</span>
          <span data-testid="count-B">{questionsB.length}</span>
          <span data-testid="pulse-A">{pendingA ? "1" : "0"}</span>
          <span data-testid="pulse-B">{pendingB ? "1" : "0"}</span>
          <button data-testid="add-A" onClick={() => addQ("ws-1", makeQuestion("qA", "sess-A"))} />
          <button data-testid="add-B" onClick={() => addQ("ws-1", makeQuestion("qB", "sess-B"))} />
          <button data-testid="clear-A" onClick={() => clear("sess-A")} />
        </>
      );
    }

    renderProvider(<Display />);
    act(() => screen.getByTestId("add-A").click());
    act(() => screen.getByTestId("add-B").click());
    expect(screen.getByTestId("pulse-A").textContent).toBe("1");
    expect(screen.getByTestId("pulse-B").textContent).toBe("1");

    act(() => screen.getByTestId("clear-A").click());

    expect(screen.getByTestId("count-A").textContent).toBe("0");
    expect(screen.getByTestId("pulse-A").textContent).toBe("0");
    // sess-B untouched.
    expect(screen.getByTestId("count-B").textContent).toBe("1");
    expect(screen.getByTestId("pulse-B").textContent).toBe("1");
  });

  it("stores permission content and returns it filtered by session", () => {
    function Display({ sessionId }: { sessionId: string }) {
      const addP = useAddPendingPermission();
      const permissions = usePendingPermissionsForSession(sessionId);
      return (
        <>
          <span data-testid="count">{permissions.length}</span>
          <button data-testid="add" onClick={() => addP("ws-1", makePermission("p1", "sess-A"))} />
        </>
      );
    }

    renderProvider(<Display sessionId="sess-A" />);
    act(() => screen.getByTestId("add").click());
    expect(screen.getByTestId("count").textContent).toBe("1");
  });
});
