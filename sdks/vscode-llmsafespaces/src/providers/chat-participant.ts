import * as vscode from "vscode";
import { ApiService } from "../services/api";
import { dispatchChatCommand, KNOWN_COMMANDS, type ChatCommandContext } from "./chat-commands";

/**
 * @llmsafespaces Chat Participant — routes prompts to sandbox agent via the API.
 *
 * Slash commands (declared in package.json under contributes.chatParticipants
 * .commands) dispatch to the pure handler in chat-commands.ts. Plain prompts
 * (no slash) route to the active workspace's agent.
 */
export function registerChatParticipant(context: vscode.ExtensionContext, api: ApiService) {
  // Sticky workspace selection set by /switch-workspace. Undefined = auto-pick
  // the first Active workspace, matching pre-US-14.9 behavior.
  let stickyWorkspaceId: string | undefined;

  const ctx: ChatCommandContext = {
    listWorkspaces: () => api.listWorkspaces(),
    ensureSession: (workspaceId) => api.ensureSession(workspaceId),
    sendMessage: (workspaceId, sessionId, content) => api.sendMessage(workspaceId, sessionId, content),
    promptUser: (items) => promptForWorkspace(items),
    setWorkspaceSticky: (workspaceId) => { stickyWorkspaceId = workspaceId; },
  };

  const participant = vscode.chat.createChatParticipant(
    "llmsafespaces.agent",
    async (request, _chatContext, stream, token) => {
      if (token.isCancellationRequested) return;

      // Slash command dispatch — pure handler, no vscode APIs.
      const result = await dispatchChatCommand(
        { command: request.command ?? "", prompt: request.prompt },
        ctx,
      );
      if (result.handled) {
        stream.markdown(result.markdown);
        return;
      }
      if (result.historyRequested) {
        stream.markdown("_History streaming is not yet wired in this build._");
        return;
      }

      // No slash command — treat as a normal agent prompt.
      await routePromptToAgent(stream, token, api, request.prompt, () => stickyWorkspaceId);
    },
  );

  participant.iconPath = vscode.Uri.joinPath(context.extensionUri, "resources", "icon.svg");
  context.subscriptions.push(participant);
}

async function routePromptToAgent(
  stream: vscode.ChatResponseStream,
  token: vscode.CancellationToken,
  api: ApiService,
  prompt: string,
  getSticky: () => string | undefined,
): Promise<void> {
  stream.progress("Finding active workspace...");
  let workspaces;
  try {
    workspaces = await api.listWorkspaces();
  } catch {
    stream.markdown("**Error:** Could not connect to LLMSafeSpaces. Run `LLMSafeSpaces: Configure` to set up your connection.");
    return;
  }
  if (token.isCancellationRequested) return;

  const sticky = getSticky();
  const target = (sticky && workspaces.find((ws) => ws.id === sticky)) || workspaces.find((ws) => ws.phase === "Active");
  if (!target) {
    stream.markdown(
      "No active workspace found.\n\nUse the **LLMSafeSpaces: Create Workspace** command or activate an existing one from the sidebar.",
    );
    return;
  }

  try {
    stream.progress("Creating session...");
    if (token.isCancellationRequested) return;
    const sessionId = await api.ensureSession(target.id);

    stream.progress("Sending to agent...");
    if (token.isCancellationRequested) return;
    const content = await api.sendMessage(target.id, sessionId, prompt);

    if (token.isCancellationRequested) return;
    stream.markdown(content || "*No response from agent*");
  } catch (e: any) {
    if (token.isCancellationRequested) return;
    stream.markdown(`**Error:** ${e.message}`);
  }
}

async function promptForWorkspace(
  items: Array<{ id: string; name: string; phase?: string }>,
): Promise<string | undefined> {
  const picks = items.map((ws) => ({
    label: ws.name,
    description: ws.phase ?? "",
    detail: ws.id,
    id: ws.id,
  }));
  const picked = await vscode.window.showQuickPick(picks, {
    title: "Switch workspace",
    placeHolder: "Select a workspace for subsequent @llmsafespaces prompts",
  });
  return picked?.id;
}

// Re-exported for tests / external consumers.
export { KNOWN_COMMANDS };
