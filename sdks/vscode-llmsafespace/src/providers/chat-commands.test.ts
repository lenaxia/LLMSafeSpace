import { describe, it, expect, vi, beforeEach } from "vitest";
import { dispatchChatCommand, KNOWN_COMMANDS, type ChatCommandContext } from "./chat-commands";

// Mock the ApiService surface that dispatchChatCommand uses. We pass it via the
// ChatCommandContext so the dispatch logic is purely functional — no vscode deps.
function makeCtx(overrides: Partial<ChatCommandContext> = {}): ChatCommandContext {
  return {
    listWorkspaces: vi.fn(async () => [
      { id: "ws-active", name: "active-ws", phase: "Active", runtime: "python" },
      { id: "ws-suspended", name: "suspended-ws", phase: "Suspended", runtime: "python" },
    ]),
    ensureSession: vi.fn(async (_workspaceId: string) => "ses-new"),
    sendMessage: vi.fn(async (_w: string, _s: string, _content: string) => "ok"),
    promptUser: vi.fn(async (_items: Array<{ id: string; name: string; phase?: string }>) => "ws-active" as string | undefined),
    setWorkspaceSticky: vi.fn(),
    ...overrides,
  };
}

describe("chat-commands dispatch", () => {
  beforeEach(() => vi.clearAllMocks());

  it("returns the list of known slash commands", () => {
    expect(KNOWN_COMMANDS.map((c) => c.name)).toEqual(
      expect.arrayContaining(["new-session", "switch-workspace", "history", "status"]),
    );
  });

  it("empty command falls through (caller treats it as a normal prompt)", async () => {
    const ctx = makeCtx();
    const result = await dispatchChatCommand({ command: "", prompt: "hi" }, ctx);
    expect(result.handled).toBe(false);
  });

  it("unknown command returns help text", async () => {
    const ctx = makeCtx();
    const result = await dispatchChatCommand({ command: "bogus", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toContain("Unknown command");
    expect(result.markdown).toMatch(/\/new-session|\/switch-workspace|\/history|\/status/);
  });

  it("/status renders active + suspended workspaces", async () => {
    const ctx = makeCtx();
    const result = await dispatchChatCommand({ command: "status", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toContain("active-ws");
    expect(result.markdown).toContain("suspended-ws");
    expect(result.markdown).toContain("Active");
    expect(result.markdown).toContain("Suspended");
  });

  it("/status renders helpful message when API fails", async () => {
    const ctx = makeCtx({
      listWorkspaces: vi.fn(async () => { throw new Error("401 unauthorized"); }),
    });
    const result = await dispatchChatCommand({ command: "status", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toContain("Error");
    expect(result.markdown).toContain("Configure");
  });

  it("/switch-workspace prompts the user and sets sticky selection", async () => {
    const sticky = vi.fn();
    const ctx = makeCtx({ setWorkspaceSticky: sticky });
    const result = await dispatchChatCommand({ command: "switch-workspace", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(ctx.promptUser).toHaveBeenCalled();
    expect(sticky).toHaveBeenCalledWith("ws-active");
    expect(result.markdown).toContain("active-ws");
  });

  it("/switch-workspace aborts cleanly when user cancels prompt", async () => {
    const ctx = makeCtx({ promptUser: vi.fn(async () => undefined) });
    const result = await dispatchChatCommand({ command: "switch-workspace", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toMatch(/cancel/i);
  });

  it("/switch-workspace renders message when no workspaces exist", async () => {
    const ctx = makeCtx({ listWorkspaces: vi.fn(async () => []) });
    const result = await dispatchChatCommand({ command: "switch-workspace", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toMatch(/no workspaces/i);
  });

  it("/new-session creates a fresh session on the active workspace", async () => {
    const ctx = makeCtx();
    const result = await dispatchChatCommand({ command: "new-session", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(ctx.ensureSession).toHaveBeenCalledWith("ws-active");
    expect(result.markdown).toContain("ses-new");
  });

  it("/new-session errors when no active workspace", async () => {
    const ctx = makeCtx({
      listWorkspaces: vi.fn(async () => [
        { id: "ws-x", name: "x", phase: "Suspended", runtime: "python" },
      ]),
    });
    const result = await dispatchChatCommand({ command: "new-session", prompt: "" }, ctx);
    if (!result.handled) throw new Error("expected handled");
    expect(result.markdown).toMatch(/no active workspace/i);
  });

  it("/history delegates to the caller (returns handled=false so the participant can stream history)", async () => {
    const ctx = makeCtx();
    const result = await dispatchChatCommand({ command: "history", prompt: "" }, ctx);
    // History requires session state — caller handles streaming from the active session.
    expect(result.handled).toBe(false);
    expect(result.historyRequested).toBe(true);
  });
});
