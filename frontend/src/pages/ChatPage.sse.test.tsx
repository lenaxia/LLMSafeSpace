/**
 * Tests for ChatPage's SSE event handler (handleSSEEvent).
 *
 * These tests verify that:
 * 1. workspace.phase events invalidate both ["workspaces"] and
 *    ["workspace-status", workspaceId] so the sidebar icon and ChatPage
 *    banner both update.
 * 2. session.status events invalidate ["sessions", workspaceId] so the
 *    session list updates.
 * 3. Unknown event types are silently ignored.
 * 4. The old shape mismatch (checking event.session) is NOT present — the
 *    handler must use event.type, not event.session.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";
import type { WorkspaceStreamEvent } from "../api/types";

// --- Mocks ---

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    getWorkspaceSessions: vi.fn(),
    activate: vi.fn(),
    getSessions: vi.fn(),
    ensureSession: vi.fn(),
  },
}));
vi.mock("../api/messages", () => ({ messagesApi: { getHistory: vi.fn(), send: vi.fn() } }));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));

// We capture the SSE event handler that ChatPage passes to useEventStream
// so we can invoke it directly in tests.
let capturedSSEHandler: ((data: unknown) => void) | null = null;
vi.mock("../hooks/useEventStream", () => ({
  useEventStream: vi.fn((_workspaceId: string | undefined, handler: (data: unknown) => void) => {
    capturedSSEHandler = handler;
  }),
}));

import { workspacesApi } from "../api/workspaces";

// --- Helpers ---

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  });
}

function renderChat(qc: QueryClient, path: string) {
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/chat/:workspaceId" element={<ChatPage />} />
          <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function sendSSEEvent(event: WorkspaceStreamEvent) {
  act(() => {
    capturedSSEHandler?.(event);
  });
}

// --- Tests ---

describe("ChatPage SSE event handler", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });

  it("workspace.phase event invalidates workspace-status query", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "workspace.phase", phase: "Suspended" });

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["workspace-status", "ws-1"] }),
    );
  });

  it("workspace.phase event invalidates workspaces list query", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "workspace.phase", phase: "Active" });

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["workspaces"] }),
    );
  });

  it("session.status event invalidates sessions query", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "session.status", session_id: "s1", status: "idle" });

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["sessions", "ws-1"] }),
    );
  });

  it("session.status event does NOT invalidate workspace-status query", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "session.status", session_id: "s1", status: "busy" });

    const workspaceStatusCalls = invalidateSpy.mock.calls.filter((args) => {
      const key = (args[0] as { queryKey?: unknown })?.queryKey;
      return Array.isArray(key) && key[0] === "workspace-status";
    });
    expect(workspaceStatusCalls).toHaveLength(0);
  });

  it("workspace.phase event does NOT invalidate sessions query", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "workspace.phase", phase: "Suspended" });

    const sessionCalls = invalidateSpy.mock.calls.filter((args) => {
      const key = (args[0] as { queryKey?: unknown })?.queryKey;
      return Array.isArray(key) && key[0] === "sessions";
    });
    expect(sessionCalls).toHaveLength(0);
  });

  it("unknown event type is silently ignored", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    // Send something that doesn't match any known type.
    act(() => {
      capturedSSEHandler?.({ type: "unknown.event", foo: "bar" });
    });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("old event.session shape does NOT trigger session invalidation (regression)", async () => {
    // Previously the handler checked event.session which was a shape mismatch.
    // After the fix, only event.type is used.
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    // Send the old incorrect shape — handler must ignore it.
    act(() => {
      capturedSSEHandler?.({ session: { id: "s1", status: "active" } });
    });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });
});
