import * as vscode from "vscode";
import { ApiService } from "../services/api";

/**
 * @llmsafespace Chat Participant — routes prompts to sandbox agent via the API.
 */
export function registerChatParticipant(context: vscode.ExtensionContext, api: ApiService) {
  const participant = vscode.chat.createChatParticipant(
    "llmsafespace.agent",
    async (request, _chatContext, stream, token) => {
      // Check cancellation before starting
      if (token.isCancellationRequested) return;

      stream.progress("Finding active workspace...");

      let workspaces;
      try {
        workspaces = await api.listWorkspaces();
      } catch (e: any) {
        stream.markdown("**Error:** Could not connect to LLMSafeSpace. Run `LLMSafeSpace: Configure` to set up your connection.");
        return;
      }

      if (token.isCancellationRequested) return;

      const active = workspaces.find(ws => ws.phase === "Active");
      if (!active) {
        stream.markdown(
          "No active workspace found.\n\n" +
          "Use the **LLMSafeSpace: Create Workspace** command or activate an existing one from the sidebar."
        );
        return;
      }

      try {
        stream.progress("Creating session...");
        if (token.isCancellationRequested) return;

        const sessionId = await api.ensureSession(active.id);

        stream.progress("Sending to agent...");
        if (token.isCancellationRequested) return;

        const content = await api.sendMessage(active.id, sessionId, request.prompt);

        if (token.isCancellationRequested) return;
        stream.markdown(content || "*No response from agent*");
      } catch (e: any) {
        if (token.isCancellationRequested) return;
        stream.markdown(`**Error:** ${e.message}`);
      }
    }
  );

  participant.iconPath = vscode.Uri.joinPath(context.extensionUri, "resources", "icon.svg");
  context.subscriptions.push(participant);
}
