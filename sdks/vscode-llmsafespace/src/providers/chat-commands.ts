/**
 * Chat slash command dispatch for the @llmsafespace chat participant.
 *
 * This module is intentionally free of `vscode` imports so it can be unit-tested
 * with vitest in isolation. The vscode-dependent surface (QuickPick, ChatStream)
 * is injected via the ChatCommandContext.
 */

export interface ChatCommand {
  /** Slash name without the leading slash, e.g. "new-session". */
  readonly name: string;
  readonly description: string;
}

export const KNOWN_COMMANDS: readonly ChatCommand[] = [
  { name: "new-session", description: "Start a fresh chat session on the active workspace" },
  { name: "switch-workspace", description: "Pick which workspace subsequent prompts route to" },
  { name: "history", description: "Stream the current session's prior messages" },
  { name: "status", description: "List workspaces and their current phase" },
];

export interface ChatCommandRequest {
  /** The slash command name (no leading slash) or empty string if none. */
  readonly command: string;
  /** The user's prompt text following the slash command (or the whole prompt). */
  readonly prompt: string;
}

export interface ChatCommandContext {
  listWorkspaces(): Promise<Array<{ id: string; name: string; phase?: string; runtime?: string }>>;
  ensureSession(workspaceId: string): Promise<string>;
  sendMessage(workspaceId: string, sessionId: string, content: string): Promise<string>;
  /** Show a QuickPick-style chooser; returns the selected id or undefined if cancelled. */
  promptUser(items: Array<{ id: string; name: string; phase?: string }>): Promise<string | undefined>;
  /** Record the user's workspace selection so subsequent prompts route there. */
  setWorkspaceSticky(workspaceId: string): void;
}

export interface ChatCommandResult {
  /** True when the dispatcher fully handled the command (no further routing needed). */
  handled: true;
  /** Markdown to render in the chat view. */
  markdown: string;
  /** When the command requests session-history streaming (handled=false). */
  historyRequested?: false;
}

export interface ChatCommandPassthrough {
  /** False when the dispatcher did not handle the command — caller should treat the prompt normally. */
  handled: false;
  /** True for /history — caller should stream the active session's history. */
  historyRequested?: boolean;
}

export type DispatchResult = ChatCommandResult | ChatCommandPassthrough;

export async function dispatchChatCommand(
  req: ChatCommandRequest,
  ctx: ChatCommandContext,
): Promise<DispatchResult> {
  if (!req.command) {
    return { handled: false };
  }
  switch (req.command) {
    case "status":
      return handleStatus(ctx);
    case "switch-workspace":
      return handleSwitchWorkspace(ctx);
    case "new-session":
      return handleNewSession(ctx);
    case "history":
      // Caller streams the active session's history; signal the request.
      return { handled: false, historyRequested: true };
    default:
      return {
        handled: true,
        markdown: renderUnknownCommand(req.command),
      };
  }
}

async function handleStatus(ctx: ChatCommandContext): Promise<ChatCommandResult> {
  let workspaces: Awaited<ReturnType<ChatCommandContext["listWorkspaces"]>>;
  try {
    workspaces = await ctx.listWorkspaces();
  } catch (e) {
    return {
      handled: true,
      markdown: "**Error:** Could not reach LLMSafeSpace. Run `LLMSafeSpace: Configure` first.\n\n```\n" + errMessage(e) + "\n```",
    };
  }
  if (workspaces.length === 0) {
    return { handled: true, markdown: "_No workspaces yet._ Use **LLMSafeSpace: Create Workspace** to make one." };
  }
  const lines = workspaces.map((ws) => `- **${ws.name}** (${ws.id}) — ${ws.phase ?? "Unknown"} — ${ws.runtime ?? ""}`);
  return {
    handled: true,
    markdown: "### Workspaces\n\n" + lines.join("\n"),
  };
}

async function handleSwitchWorkspace(ctx: ChatCommandContext): Promise<ChatCommandResult> {
  let workspaces: Awaited<ReturnType<ChatCommandContext["listWorkspaces"]>>;
  try {
    workspaces = await ctx.listWorkspaces();
  } catch (e) {
    return { handled: true, markdown: "**Error:** " + errMessage(e) };
  }
  if (workspaces.length === 0) {
    return { handled: true, markdown: "_No workspaces available to switch to._" };
  }
  const picked = await ctx.promptUser(workspaces.map((ws) => ({ id: ws.id, name: ws.name, phase: ws.phase })));
  if (!picked) {
    return { handled: true, markdown: "_Cancelled._" };
  }
  ctx.setWorkspaceSticky(picked);
  const found = workspaces.find((ws) => ws.id === picked);
  return {
    handled: true,
    markdown: `Switched to **${found?.name ?? picked}** (${picked}). Subsequent prompts route there.`,
  };
}

async function handleNewSession(ctx: ChatCommandContext): Promise<ChatCommandResult> {
  let workspaces: Awaited<ReturnType<ChatCommandContext["listWorkspaces"]>>;
  try {
    workspaces = await ctx.listWorkspaces();
  } catch (e) {
    return { handled: true, markdown: "**Error:** " + errMessage(e) };
  }
  const active = workspaces.find((ws) => ws.phase === "Active");
  if (!active) {
    return { handled: true, markdown: "_No active workspace. Activate one first (LLMSafeSpace: Activate Workspace)._" };
  }
  const sessionId = await ctx.ensureSession(active.id);
  ctx.setWorkspaceSticky(active.id);
  return {
    handled: true,
    markdown: `New session **${sessionId}** created on **${active.name}**. Send a prompt to begin.`,
  };
}

function renderUnknownCommand(cmd: string): string {
  const list = KNOWN_COMMANDS.map((c) => `- \`/${c.name}\` — ${c.description}`).join("\n");
  return `Unknown command \`/${cmd}\`.\n\nAvailable commands:\n\n${list}`;
}

function errMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
