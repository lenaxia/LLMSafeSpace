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
        data-stream-parts={JSON.stringify(props.streamParts ?? [])}
        data-streaming={String(props.streaming ?? false)}
        data-messages={JSON.stringify(props.messages ?? [])}
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

function getStreamParts(): Array<{ type: string; text: string }> {
  const el = screen.getByTestId("chat-view");
  return JSON.parse(el.getAttribute("data-stream-parts") || "[]");
}

function makePartUpdatedEvent(sessionID: string, partType: string, text: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      type: "message.part.updated",
      properties: { sessionID, part: { type: partType, text } },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makePartDeltaEvent(sessionID: string, field: string, delta: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.delta",
    data: {
      type: "message.part.delta",
      properties: { sessionID, field, delta },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makePartUpdatedEventSnakeCase(session_id: string, text: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      type: "message.part.updated",
      properties: { session_id, part: { type: "text", text } },
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

    it("REGRESSION: idle event triggers reconcile that does NOT cause duplicate localMessage rendering", async () => {
      // Prior bug: localMessages accumulated user+assistant messages on send,
      // and reconcileOnIdle refetched history (which now contained the same
      // messages), but localMessages was never cleared. The merge in
      // `allMessages = [...history, ...localMessages]` rendered every
      // message twice.
      //
      // Fix: clearing localMessages after the post-idle history refetch
      // succeeds. History is the single source of truth once idle.
      const user = userEvent.setup();
      const qc = makeQueryClient();

      // sendAsync resolves immediately; history starts empty then returns
      // the persisted message after idle reconcile triggers a refetch.
      let historyCallCount = 0;
      (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockImplementation(() => {
        historyCallCount++;
        if (historyCallCount === 1) return Promise.resolve([]);
        return Promise.resolve([
          { id: "msg-user-real", role: "user", parts: [{ type: "text", text: "ping" }] },
          { id: "msg-asst-real", role: "assistant", parts: [{ type: "text", text: "pong" }] },
        ]);
      });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      const { container } = renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      await waitFor(() => expect(container.querySelector("textarea")).not.toBeDisabled());

      // User sends a message
      await user.click(container.querySelector("textarea")!);
      await user.type(container.querySelector("textarea")!, "ping");
      await user.keyboard("{Enter}");
      await waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalled());

      // Drive the idle SSE event — this triggers reconcileOnIdle which
      // refetches history and SHOULD clear localMessages so the merged
      // view (history + localMessages) does not duplicate.
      sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

      // Wait for the reconcile refetch to land
      await waitFor(() => expect(historyCallCount).toBeGreaterThanOrEqual(2));

      // Wait for the merged messages render to update with deduped content.
      // Read the actual rendered messages from the ChatView mock's data attr.
      await waitFor(() => {
        const view = container.querySelector('[data-testid="chat-view"]');
        const messagesAttr = view?.getAttribute("data-messages") ?? "[]";
        const messages = JSON.parse(messagesAttr) as Array<{ id: string; role: string; parts: Array<{ text?: string }> }>;
        // EXACTLY 2 messages — no duplicates from localMessages+history merge
        expect(messages).toHaveLength(2);
        expect(messages.filter((m) => m.role === "user")).toHaveLength(1);
        expect(messages.filter((m) => m.role === "assistant")).toHaveLength(1);
      }, { timeout: 5_000 });
    });

    it("REGRESSION: assistant response is not duplicated when reconcileOnIdle's history fetch resolves BEFORE useChatStream's onComplete", async () => {
      // Validated against production via DevTools Network panel:
      // After session.status idle, two GET /message requests fire — one
      // from useChatStream.send (line 70 of useChatStream.ts) and one
      // from reconcileOnIdle's queryClient.refetchQueries.
      //
      // Race: if reconcileOnIdle's fetch resolves first, it clears
      // localMessages and populates history. Then useChatStream.send's
      // onComplete callback fires and re-adds the assistant message to
      // localMessages → assistant renders TWICE (history + localMessages).
      //
      // Fix: handleSend's onComplete must NOT add the assistant message
      // to localMessages. The streaming bubble shows it during streaming;
      // history (refetched by reconcileOnIdle) is authoritative after.
      const user = userEvent.setup();
      const qc = makeQueryClient();

      // history fetch resolution order:
      //   call 1 (initial mount) → empty
      //   call 2 (reconcileOnIdle's refetch) → [user, assistant]
      //   call 3 (useChatStream.send's await) → [user, assistant]
      // The race is the order of resolution between calls 2 and 3.
      //
      // Simulate the production race: reconcileOnIdle's fetch resolves
      // first (e.g., its Promise hits microtask queue earlier), then
      // useChatStream.send's fetch resolves second. We deliberately order
      // resolutions to expose the bug.
      let resolveCall3!: (history: unknown[]) => void;
      let historyCallCount = 0;
      (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockImplementation(() => {
        historyCallCount++;
        if (historyCallCount === 1) return Promise.resolve([]);
        if (historyCallCount === 2) {
          // reconcileOnIdle's refetch — resolve immediately
          return Promise.resolve([
            { id: "msg-user-real", role: "user", parts: [{ type: "text", text: "ping" }] },
            { id: "msg-asst-real", role: "assistant", parts: [{ type: "text", text: "pong" }] },
          ]);
        }
        // call 3: useChatStream.send's history fetch — defer resolution
        return new Promise<unknown[]>((res) => { resolveCall3 = res; });
      });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      const { container } = renderChat(qc, "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      await waitFor(() => expect(container.querySelector("textarea")).not.toBeDisabled());

      await user.click(container.querySelector("textarea")!);
      await user.type(container.querySelector("textarea")!, "ping");
      await user.keyboard("{Enter}");
      await waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalled());

      // Idle SSE — drives BOTH paths concurrently:
      //   1. notifySessionIdle → useChatStream.send's await resolves → call 3 begins
      //   2. reconcileOnIdle → call 2 fires
      sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

      // Wait for reconcileOnIdle's refetch (call 2) to land and update history
      await waitFor(() => expect(historyCallCount).toBeGreaterThanOrEqual(3));

      // Now resolve useChatStream.send's history fetch (call 3) AFTER
      // reconcileOnIdle has cleared localMessages. This causes onComplete
      // to fire and re-add the assistant message to the just-cleared
      // localMessages — exactly the production race.
      await act(async () => {
        resolveCall3([
          { id: "msg-user-real", role: "user", parts: [{ type: "text", text: "ping" }] },
          { id: "msg-asst-real", role: "assistant", parts: [{ type: "text", text: "pong" }] },
        ]);
        await new Promise((r) => setTimeout(r, 50));
      });

      // Critical assertion: assistant renders EXACTLY ONCE despite the race
      const view = container.querySelector('[data-testid="chat-view"]');
      const messagesAttr = view?.getAttribute("data-messages") ?? "[]";
      const messages = JSON.parse(messagesAttr) as Array<{ id: string; role: string; parts: Array<{ text?: string }> }>;
      expect(messages.filter((m) => m.role === "assistant")).toHaveLength(1);
      expect(messages.filter((m) => m.role === "user")).toHaveLength(1);
    });
  });

  describe("opencode.event with message.part.updated", () => {
    it("text part with matching session creates a text entry", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Hello streaming!"));
      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(1);
        expect(parts[0]).toEqual({ type: "text", text: "Hello streaming!" });
      });
    });

    it("text part with snake_case session_id works", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEventSnakeCase("sess-1", "snake case works"));
      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts[0]).toEqual({ type: "text", text: "snake case works" });
      });
    });

    it("ignores event with wrong session ID", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("other-session", "text", "Should not appear"));
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });

    it("last text snapshot overwrites previous text in same part", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "First"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Final text"));
      await waitFor(() => {
        const parts = getStreamParts();
        // Second text part.updated with content updates the existing text part
        expect(parts[parts.length - 1]!.text).toBe("Final text");
      });
    });
  });

  describe("opencode.event with message.part.delta", () => {
    it("accumulates text deltas incrementally", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Hello"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", " world"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "!"));
      await waitFor(() => {
        expect(getStreamParts()[0]!.text).toBe("Hello world!");
      });
    });

    it("discards deltas without preceding part.updated", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "orphan"));
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });

    it("ignores delta with wrong session ID", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("other-session", "text", "should be ignored"));
      await waitFor(() => {
        expect(getStreamParts()[0]!.text).toBe("");
      });
    });
  });

  describe("opencode.event edge cases", () => {
    it("ignores event with missing payload", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: { wrong: "structure" } } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });

    it("ignores event with missing properties", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: { payload: { type: "message.part.updated" } } } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });

    it("ignores event with null data", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({ type: "opencode.event", event_type: "message.part.updated", data: null } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });
  });

  describe("nested SSE format unwrapping", () => {
    it("unwraps nested payload and processes message.part.updated", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: {
          directory: "ws-1",
          payload: {
            type: "message.part.updated",
            properties: { sessionID: "sess-1", part: { type: "text", text: "Nested format works!" } },
          },
        },
      } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts()[0]!.text).toBe("Nested format works!");
      });
    });

    it("unwraps nested payload and processes message.part.delta", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      // Activate text routing first
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.delta",
        data: {
          directory: "ws-1",
          payload: {
            type: "message.part.delta",
            properties: { sessionID: "sess-1", field: "text", delta: "nested delta" },
          },
        },
      } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts()[0]!.text).toBe("nested delta");
      });
    });
  });

  describe("user echo filtering — sent-text tracking", () => {
    it("strips exact user echo from message.part.updated snapshot", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // Send a message to populate sentTextRef
      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "my question");
      await user.keyboard("{Enter}");

      // Simulate opencode echoing the user's message back as a part.updated
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "my question"));
      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });

      // Now the real assistant response arrives — should be accepted
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "Here is the answer!"));
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "text")?.text).toBe("Here is the answer!");
      });
    });

    it("strips user echo prefix from message.part.updated snapshot", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "hello");
      await user.keyboard("{Enter}");

      // Opencode echoes user text + assistant response in one snapshot
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "helloThe answer is 42"));
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "text")?.text).toBe("The answer is 42");
      });
    });

    it("strips user echo prefix from accumulated deltas", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "hi");
      await user.keyboard("{Enter}");

      // User echo arrives as part.updated — suppresses subsequent deltas
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "hi"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "echo junk"));

      // Then reasoning starts, routing switches
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "thinking"));

      // Then text response starts
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "response text"));

      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "text")?.text).toBe("response text");
      });
    });
  });

  describe("thinking/reasoning streaming (Bug 2)", () => {
    it("accumulates thinking deltas with field=reasoning", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      // A reasoning part.updated must precede deltas to activate thinking routing
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "reasoning", "Hmm "));
      sendSSEEvent(makePartDeltaEvent("sess-1", "reasoning", "let me think"));
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "thinking")?.text).toBe("Hmm let me think");
      });
    });

    it("accumulates thinking deltas with field=thinking", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent(makePartUpdatedEvent("sess-1", "thinking", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "thinking", "I wonder..."));
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "thinking")?.text).toBe("I wonder...");
      });
    });

    it("captures thinking part from message.part.updated", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: {
          type: "message.part.updated",
          properties: { sessionID: "sess-1", part: { type: "thinking", text: "Deep thoughts" } },
        },
      } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "thinking")?.text).toBe("Deep thoughts");
      });
    });

    it("captures reasoning part from message.part.updated", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: {
          type: "message.part.updated",
          properties: { sessionID: "sess-1", part: { type: "reasoning", text: "Chain of thought" } },
        },
      } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "thinking")?.text).toBe("Chain of thought");
      });
    });

    it("handleSend clears thinking text", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent({
        type: "opencode.event",
        event_type: "message.part.updated",
        data: {
          type: "message.part.updated",
          properties: { sessionID: "sess-1", part: { type: "thinking", text: "Old thinking" } },
        },
      } as unknown as WorkspaceStreamEvent);
      await waitFor(() => {
        expect(getStreamParts().find(p => p.type === "thinking")?.text).toBe("Old thinking");
      });

      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "new message");
      await user.keyboard("{Enter}");

      await waitFor(() => {
        expect(getStreamParts().filter(p => p.type === "thinking")).toHaveLength(0);
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
        expect(getStreamParts().find(p => p.type === "text")?.text).toBe("Old stream text");
      });

      // Submit a new message — should clear sseStreamText
      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "new message");
      await user.keyboard("{Enter}");

      await waitFor(() => {
        expect(getStreamParts()).toHaveLength(0);
      });
    });
  });

  describe("streaming parts array (ordered accumulation)", () => {
    it("single thinking block followed by text produces two parts", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "thinking content"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "response content"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(2);
        expect(parts[0]).toEqual({ type: "thinking", text: "thinking content" });
        expect(parts[1]).toEqual({ type: "text", text: "response content" });
      });
    });

    it("multiple thinking blocks produce separate entries (not overwritten)", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // First thinking block
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "thought 1"));

      // Tool interrupts
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));

      // Second thinking block
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "thought 2"));

      // Response
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "answer"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(4);
        expect(parts[0]).toEqual({ type: "thinking", text: "thought 1" });
        expect(parts[1]).toMatchObject({ type: "tool", text: "" });
        expect(parts[2]).toEqual({ type: "thinking", text: "thought 2" });
        expect(parts[3]).toEqual({ type: "text", text: "answer" });
      });
    });

    it("tool events produce tool entries in the array", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "let me search"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts[0]).toEqual({ type: "thinking", text: "let me search" });
        // Each tool event produces its own entry
        expect(parts.filter(p => p.type === "tool")).toHaveLength(3);
      });
    });

    it("full realistic sequence: echo → thinking → tools → thinking → tools → text", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // User sends
      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "fetch repo info");
      await user.keyboard("{Enter}");

      // Echo
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "fetch repo info"));
      // Step 1
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-start", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "I'll use gh CLI"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-finish", ""));
      // Step 2
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-start", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Let me try curl"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-finish", ""));
      // Step 3: response
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-start", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Got the data"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Here is the repo info"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-finish", ""));

      await waitFor(() => {
        const parts = getStreamParts();
        // Should have: thinking, tool(s), thinking, tool(s), thinking, text
        expect(parts.filter(p => p.type === "thinking")).toHaveLength(3);
        expect(parts.filter(p => p.type === "tool")).toHaveLength(3);
        expect(parts.filter(p => p.type === "text")).toHaveLength(1);
        expect(parts[parts.length - 1]).toEqual({ type: "text", text: "Here is the repo info" });
      });
    });

    it("deltas append to the last part in the array", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "Hello"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", " world"));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "!"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(1);
        expect(parts[0]).toEqual({ type: "text", text: "Hello world!" });
      });
    });

    it("handleSend clears the parts array", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "old content"));

      await waitFor(() => expect(getStreamParts()).toHaveLength(1));

      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "new msg");
      await user.keyboard("{Enter}");

      await waitFor(() => expect(getStreamParts()).toHaveLength(0));
    });

    it("user echo is suppressed — no parts created", async () => {
      const user = userEvent.setup();
      (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
      (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      await waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await user.click(document.querySelector("textarea")!);
      await user.type(document.querySelector("textarea")!, "hello");
      await user.keyboard("{Enter}");

      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", "hello"));

      await waitFor(() => expect(getStreamParts()).toHaveLength(0));
    });

    it("reasoning snapshot updates existing thinking part text", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "partial"));
      // Snapshot arrives with full text
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", "full thinking text"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(1);
        expect(parts[0]).toEqual({ type: "thinking", text: "full thinking text" });
      });
    });

    it("reasoning snapshot after tool events updates the correct thinking part", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // Thinking block with deltas
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "partial thought"));
      // Tools arrive
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      // Reasoning snapshot arrives (after tools, updates the tracked thinking part)
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", "complete thought from snapshot"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts).toHaveLength(3); // thinking + 2 tools
        expect(parts[0]).toEqual({ type: "thinking", text: "complete thought from snapshot" });
        expect(parts[1]).toMatchObject({ type: "tool", text: "" });
        expect(parts[2]).toMatchObject({ type: "tool", text: "" });
      });
    });

    it("multiple thinking blocks are preserved across steps (snapshots don't overwrite other blocks)", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // Step 1: thinking + tool
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "step 1 thinking"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", "step 1 complete"));
      // Step 2: thinking + tool
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-finish", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-start", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "step 2 thinking"));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", "step 2 complete"));
      // Step 3: text output
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-finish", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "step-start", ""));
      sendSSEEvent(makePartUpdatedEvent("sess-1", "text", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "final answer"));

      await waitFor(() => {
        const parts = getStreamParts();
        const thinkingParts = parts.filter(p => p.type === "thinking");
        expect(thinkingParts).toHaveLength(2);
        expect(thinkingParts[0]).toEqual({ type: "thinking", text: "step 1 complete" });
        expect(thinkingParts[1]).toEqual({ type: "thinking", text: "step 2 complete" });
        expect(parts[parts.length - 1]).toEqual({ type: "text", text: "final answer" });
      });
    });

    it("deltas are discarded if last part type doesn't match active route", async () => {
      renderChat(makeQueryClient(), "/chat/ws-1/sess-1");
      await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

      // Thinking block
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", ""));
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "thought"));
      // Tool arrives (last part is now tool)
      sendSSEEvent(makePartUpdatedEvent("sess-1", "tool", ""));
      // Reasoning snapshot sets activePartType back to "reasoning"
      sendSSEEvent(makePartUpdatedEvent("sess-1", "reasoning", "thought snapshot"));
      // A stray delta arrives — last part is tool, not thinking, so discard
      sendSSEEvent(makePartDeltaEvent("sess-1", "text", "SHOULD NOT APPEAR"));

      await waitFor(() => {
        const parts = getStreamParts();
        expect(parts[0]!.text).toBe("thought snapshot");
        expect(parts[1]).toMatchObject({ type: "tool", text: "" });
        // No part should contain the stray delta
        expect(parts.every(p => !p.text.includes("SHOULD NOT APPEAR"))).toBe(true);
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
        expect(JSON.parse(chatView.getAttribute("data-stream-parts") || "[]")).toHaveLength(0);
      }
    });
  });
});
