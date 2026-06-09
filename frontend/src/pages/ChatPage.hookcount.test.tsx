/**
 * Regression test for React error #310:
 * "Rendered more hooks than during the previous render"
 *
 * Root cause: useEffect(() => { doSendNowRef.current = doSendNow; }) was
 * defined AFTER the early return `if (!workspaceId) { return ... }`.
 *
 * On the first render with no workspaceId the component exits early and that
 * useEffect is never registered.  On a subsequent render with a workspaceId
 * the component runs past the early return and tries to register the extra
 * useEffect — React detects the hook count change and throws error #310.
 *
 * The test reproduces the scenario by:
 *   1. Rendering ChatPage at /chat  (no workspaceId → early return path)
 *   2. Navigating to /chat/ws-1/ses-1 (workspaceId present → full render path)
 *
 * React error #310 manifests as a thrown error that React Testing Library
 * surfaces via the error boundary / console.error.  We assert that no such
 * error is thrown after the navigation.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, render } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";

// ── minimal API mocks ──────────────────────────────────────────────────────
vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn().mockResolvedValue({ phase: "Active", sessions: [] }),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    listModels: vi.fn().mockResolvedValue({ models: [], currentModel: "" }),
    setModel: vi.fn().mockResolvedValue({ model: "", applied: false }),
    renameWorkspace: vi.fn().mockResolvedValue({}),
    renameSession: vi.fn().mockResolvedValue({}),
    abortSession: vi.fn().mockResolvedValue({}),
    getSession: vi.fn().mockResolvedValue({ title: "" }),
  },
}));
vi.mock("../api/messages", () => ({
  messagesApi: {
    getHistory: vi.fn().mockResolvedValue([]),
    getHistoryPage: vi.fn().mockResolvedValue({ messages: [], nextCursor: undefined }),
    sendAsync: vi.fn().mockResolvedValue(undefined),
  },
}));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn().mockResolvedValue({ sessionId: "ses-1" }) } }));
vi.mock("../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));
vi.mock("../hooks/useUserEventStream", () => ({ useUserEventStream: vi.fn() }));

// ── helper: a button that navigates to the chat route ─────────────────────
function NavButton({ to }: { to: string }) {
  const navigate = useNavigate();
  return <button onClick={() => navigate(to)}>go</button>;
}

function makeQC() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

describe("ChatPage hook count stability (React error #310)", () => {
  let consoleError: typeof console.error;

  beforeEach(() => {
    // Capture console.error so we can assert React did NOT emit a hooks error.
    consoleError = console.error;
    console.error = vi.fn();
  });

  afterEach(() => {
    console.error = consoleError;
    vi.clearAllMocks();
  });

  it("does not throw 'Rendered more hooks' when navigating from no-workspace to workspace route", async () => {
    const qc = makeQC();

    // Pre-populate query cache so the workspace route renders without async fetches
    // interfering with the hook-count check.
    qc.setQueryData(["workspace-status", "ws-1"], { phase: "Suspended", sessions: [] });
    qc.setQueryData(["workspaces"], { items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    qc.setQueryData(["messages", "ws-1", "ses-1"], {
      pages: [{ messages: [], nextCursor: undefined }],
      pageParams: [undefined],
    });

    const { getByText } = render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat"]}>
          <Routes>
            {/* Route with no params — triggers the early return in ChatPage */}
            <Route path="/chat" element={
              <>
                <ChatPage />
                <NavButton to="/chat/ws-1/ses-1" />
              </>
            } />
            {/* Route with params — would call the extra useEffect if not fixed */}
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    // Step 1: initial render hits the early return (no workspaceId)
    expect(getByText("Select a workspace to start chatting")).toBeTruthy();

    // Step 2: navigate — ChatPage re-mounts on the new route with workspaceId set.
    // If the hook count differs between renders React throws error #310.
    await act(async () => {
      getByText("go").click();
    });

    // Assert no React hooks error was emitted
    const calls = (console.error as ReturnType<typeof vi.fn>).mock.calls;
    const hooksError = calls.some((args) =>
      args.some((a: unknown) => typeof a === "string" && a.includes("more hooks")),
    );
    expect(hooksError).toBe(false);
  });
});
