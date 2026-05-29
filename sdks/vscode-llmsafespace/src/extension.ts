import * as vscode from "vscode";
import { WorkspaceTreeProvider } from "./providers/workspace-tree";
import { registerChatParticipant } from "./providers/chat-participant";
import { registerTerminalCommand } from "./providers/terminal-provider";
import { ApiService } from "./services/api";
import {
  registerCreateWorkspaceCommand,
  registerSuspendCommand,
  registerResumeCommand,
  registerActivateCommand,
  registerTerminateCommand,
} from "./commands/workspace-commands";

let refreshInterval: ReturnType<typeof setInterval> | undefined;

export function activate(context: vscode.ExtensionContext) {
  const apiService = new ApiService(context);
  const treeProvider = new WorkspaceTreeProvider(apiService);

  const treeView = vscode.window.createTreeView("llmsafespace.workspaces", {
    treeDataProvider: treeProvider,
    showCollapseAll: true,
  });

  // Register commands
  context.subscriptions.push(
    treeView,
    vscode.commands.registerCommand("llmsafespace.refresh", () => treeProvider.refresh()),
    vscode.commands.registerCommand("llmsafespace.configure", () => apiService.configure()),
    registerCreateWorkspaceCommand(apiService, treeProvider),
    registerSuspendCommand(apiService, treeProvider),
    registerResumeCommand(apiService, treeProvider),
    registerActivateCommand(apiService, treeProvider),
    registerTerminateCommand(apiService, treeProvider),
    registerTerminalCommand(context, apiService),
  );

  // Register chat participant
  registerChatParticipant(context, apiService);

  // Status bar
  const statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 50);
  statusBar.command = "llmsafespace.refresh";
  statusBar.show();
  context.subscriptions.push(statusBar);

  // Auto-refresh every 30s
  refreshInterval = setInterval(() => treeProvider.refresh(), 30_000);

  // First-run check
  if (!apiService.isConfigured()) {
    vscode.window
      .showInformationMessage("LLMSafeSpace: Configure API connection?", "Configure")
      .then((choice) => {
        if (choice === "Configure") apiService.configure();
      });
  }
}

export function deactivate() {
  if (refreshInterval) clearInterval(refreshInterval);
}
