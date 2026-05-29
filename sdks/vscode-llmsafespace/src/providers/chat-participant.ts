import * as vscode from "vscode";
import { ApiService } from "../services/api";

/**
 * @llmsafespace Chat Participant — routes prompts to sandbox agent via the API.
 */
export function registerChatParticipant(context: vscode.ExtensionContext, api: ApiService) {
  const participant = vscode.chat.createChatParticipant("llmsafespace.agent", async (request, chatContext, stream, token) => {
    stream.progress("Connecting to sandbox agent...");

    // Get or create a workspace + session
    const workspaces = await api.listWorkspaces();
    const active = workspaces.find((ws) => ws.phase === "Active");

    if (!active) {
      stream.markdown("No active workspace found. Create one with `LLMSafeSpace: Create Workspace` command.");
      return;
    }

    try {
      // Ensure session exists
      const { LLMSafeSpace } = await import("@llmsafespace/sdk");
      const url = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl") || "";
      const apiKey = await context.secrets.get("llmsafespace.apiKey");
      if (!url || !apiKey) {
        stream.markdown("LLMSafeSpace not configured. Run `LLMSafeSpace: Configure`.");
        return;
      }

      const client = new LLMSafeSpace({ baseUrl: url, apiKey, timeout: 120_000 });
      const session = await client.sessions.ensure(active.id);

      stream.progress("Sending to agent...");
      const response = await client.sessions.sendMessage(active.id, session.sessionId, request.prompt);

      stream.markdown(response.content || "*No response from agent*");
    } catch (e: any) {
      stream.markdown(`**Error:** ${e.message}`);
    }
  });

  participant.iconPath = vscode.Uri.joinPath(context.extensionUri, "resources", "icon.svg");
  context.subscriptions.push(participant);
}
