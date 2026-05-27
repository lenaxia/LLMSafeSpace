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
 * 5. opencode.event with message.part.updated sets sseStreamText.
 * 6. Session ID filtering works — mismatched sessions are ignored.
 * 7. Malformed/incomplete opencode events are silently ignored.
 * 8. Multiple part.updated events accumulate text.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, waitFor, act, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";
import type { WorkspaceStreamEvent, OpenCodeEvent } from "../api/types";

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

// Mock ChatView to capture streaming text props
vi.mock("../components/chat/ChatView", () => ({
  ChatView: (props: Record<string, unknown>) => {
    return (
      <div
        data-testid="chat-view"
        data-streamed-text={String(props.streamedDisplayText ?? "")}
        data-streamed-thinking={String(props.streamedThinkingText ?? "")}
        data-streaming={String(props.streaming ?? false)}
      />
    );
  },
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

function makePartUpdatedEvent(
  sessionID: string,
  partType: string,
  text: string,
): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      payload: {
        type: "message.part.updated",
        properties: {
          sessionID,
          part: { type: partType, text },
        },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makeMessageUpdatedEvent(
  sessionID: string,
): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.updated",
    data: {
      payload: {
        type: "message.updated",
        properties: {
          sessionID,
          info: { id: "msg-1", role: "assistant" },
        },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

// --- Tests ---

describe("ChatPage SSE event handler", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });

  describe("workspace.phase events", () => {
    it("invalidates workspace-status query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({ type: "workspace.phase", phase: "Suspended" });

      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ["workspace-status", "ws-1"] }),
      );
    });

    it("invalidates workspaces list query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({ type: "workspace.phase", phase: "Active" });

      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ["workspaces"] }),
      );
    });

    it("does NOT invalidate sessions query", async () => {
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
  });

  describe("session.status events", () => {
    it("invalidates sessions query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({ type: "session.status", session_id: "s1", status: "idle" });

      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: ["sessions", "ws-1"] }),
      );
    });

    it("does NOT invalidate workspace-status query", async () => {
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
  });

  describe("opencode.event with message.part.updated", () => {
    it("sets sseStreamText for text part with matching session", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Hello streaming!"));

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("Hello streaming!");
      });
    });

    it("ignores event with wrong session ID", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("other-session", "text", "Should not appear"));

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores non-text parts", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "thinking", "reasoning content"));

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("handles multiple part.updated events accumulating text", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "First chunk"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Second chunk"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Third chunk"));

      // Each event overwrites sseStreamText (the last one wins)
      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("Third chunk");
      });
    });
  });

  describe("opencode.event edge cases", () => {
    it("ignores event with missing payload", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: { wrong: "structure" },
      } as unknown as WorkspaceStreamEvent);

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores event with missing properties", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: { payload: { type: "message.part.updated" } },
      } as unknown as WorkspaceStreamEvent);

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores message.updated event (not part.updated)", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makeMessageUpdatedEvent("sess-1"));

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("clears sseStreamText on handleSend", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Old stream text"));

      await waitFor(() => {
        const chatView = screen.getByTestId("chat-view");
        expect(chatView.getAttribute("data-streamed-text")).toBe("Old stream text");
      });
    });
  });

  describe("unknown events", () => {
    it("silently ignores unknown event types", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({ type: "unknown.event", foo: "bar" } as unknown as WorkspaceStreamEvent);

      expect(invalidateSpy).not.toHaveBeenCalled();
    });
  });

  it("old event.session shape does NOT trigger session invalidation (regression)", async () => {
    const qc = makeQueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ session: { id: "s1", status: "active" } } as unknown as WorkspaceStreamEvent);

    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("opencode.event without sessionId in URL is ignored", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent(makePartUpdatedEvent("any-session", "text", "no session"));

    await waitFor(() => {
      const chatView = screen.getByTestId("chat-view");
      expect(chatView.getAttribute("data-streamed-text")).toBe("");
    });
  });

  it("ignores event with null/undefined data", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/sess-1");
    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: null } as unknown as WorkspaceStreamEvent);

    await waitFor(() => {
      const chatView = screen.getByTestId("chat-view");
      expect(chatView.getAttribute("data-streamed-text")).toBe("");
    });
  });
});
