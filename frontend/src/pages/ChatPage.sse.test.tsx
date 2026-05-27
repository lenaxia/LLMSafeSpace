/**
 * Tests for ChatPage's SSE event handler (handleSSEEvent).
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, waitFor, act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";
import type { WorkspaceStreamEvent } from "../api/types";

// --- Mocks ---

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
  },
}));
vi.mock("../api/messages", () => ({ messagesApi: { getHistory: vi.fn().mockResolvedValue([]), sendAsync: vi.fn() } }));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));

// Capture the SSE handler ChatPage registers with useEventStream
let capturedSSEHandler: ((data: unknown) => void) | null = null;
vi.mock("../hooks/useEventStream", () => ({
  useEventStream: vi.fn((_workspaceId: string | undefined, handler: (data: unknown) => void) => {
    capturedSSEHandler = handler;
  }),
}));

// Mock ChatView to expose streaming text as data attributes
vi.mock("../components/chat/ChatView", () => ({
  ChatView: (props: Record<string, unknown>) => {
    return (
      <div
        data-testid="chat-view"
        data-streamed-text={String(props.streamedDisplayText ?? "")}
        data-streaming={String(props.streaming ?? false)}
      >
        <textarea
          disabled={props.disabled as boolean}
          onChange={() => {}}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              (props.onSend as (t: string) => void)((e.target as HTMLTextAreaElement).value);
            }
          }}
        />
      </div>
    );
  },
}));

import { workspacesApi } from "../api/workspaces";
import { messagesApi } from "../api/messages";

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
  act(() => { capturedSSEHandler?.(event); });
}

function makePartUpdatedEvent(sessionID: string, partType: string, text: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      payload: {
        type: "message.part.updated",
        properties: { sessionID, part: { type: partType, text } },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makePartDeltaEvent(sessionID: string, field: string, delta: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.delta",
    data: {
      payload: {
        type: "message.part.delta",
        properties: { sessionID, field, delta },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makePartUpdatedEventSnakeCase(session_id: string, text: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      payload: {
        type: "message.part.updated",
        properties: { session_id, part: { type: "text", text } },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makeSessionStatusEvent(session_id: string, status: "idle" | "busy"): WorkspaceStreamEvent {
  return { type: "session.status", session_id, status };
}

// --- Tests ---

describe("ChatPage SSE event handler", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });

  describe("workspace.phase events", () => {
    it("invalidates workspace-status query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "workspace.phase", phase: "Suspended" });
      expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ["workspace-status", "ws-1"] }));
    });

    it("invalidates workspaces list query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "workspace.phase", phase: "Active" });
      expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ["workspaces"] }));
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
      sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));
      expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ["sessions", "ws-1"] }));
    });

    it("does NOT invalidate workspace-status query", async () => {
      const qc = makeQueryClient();
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
      renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makeSessionStatusEvent("sess-1", "busy"));
      const wsCalls = invalidateSpy.mock.calls.filter((args) => {
        const key = (args[0] as { queryKey?: unknown })?.queryKey;
        return Array.isArray(key) && key[0] === "workspace-status";
      });
      expect(wsCalls).toHaveLength(0);
    });
  });

  describe("opencode.event with message.part.updated", () => {
    it("sets sseStreamText for text part with matching session (camelCase sessionID)", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Hello streaming!"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("Hello streaming!");
      });
    });

    it("sets sseStreamText for text part with snake_case session_id", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEventSnakeCase("sess-1", "snake case works"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("snake case works");
      });
    });

    it("ignores event with wrong session ID", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("other-session", "text", "Should not appear"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores non-text parts", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "thinking", "reasoning content"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("last part.updated event overwrites previous (snapshot semantics)", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "First"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Second"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Final text"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("Final text");
      });
    });
  });

  describe("opencode.event with message.part.delta (incremental streaming)", () => {
    it("accumulates text deltas incrementally", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Hello"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", " world"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "!"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("Hello world!");
      });
    });

    it("ignores delta with non-text field", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartDeltaEvent("sess-1", "reasoning", "thinking..."));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores delta with wrong session ID", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartDeltaEvent("other-session", "text", "should be ignored"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });
  });

  describe("opencode.event edge cases", () => {
    it("ignores event with missing payload", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: { wrong: "structure" } } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores event with missing properties", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: { payload: { type: "message.part.updated" } } } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });

    it("ignores event with null data", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: null } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
      });
    });
  });

  describe("handleSend clears sseStreamText", () => {
    it("clears sseStreamText when user submits a new message", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // Set some streaming text via SSE
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Old stream text"));
      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("Old stream text");
      });

      // Submit a new message — should clear sseStreamText
      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "new message");
      await user.keyboard("{Enter}");

      await waitFor(() => {
        expect(screen.getByTestId("chat-view").getAttribute("data-streamed-text")).toBe("");
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
      const chatView = screen.queryByTestId("chat-view");
      if (chatView) {
        expect(chatView.getAttribute("data-streamed-text")).toBe("");
      }
    });
  });
});
