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
    vscode.commands.registerCommand("llmsafespace.copyId", (item: any) => {
      if (item?.workspace?.id) {
        vscode.env.clipboard.writeText(item.workspace.id);
        vscode.window.showInformationMessage(`Copied: ${item.workspace.id}`);
      }
    }),
    registerCreateWorkspaceCommand(apiService, treeProvider),
    registerSuspendCommand(apiService, treeProvider),
    registerResumeCommand(apiService, treeProvider),
    registerActivateCommand(apiService, treeProvider),
    registerTerminateCommand(apiService, treeProvider),
    registerTerminalCommand(context, apiService),
  );

  // Register chat participant
  registerChatParticipant(context, apiService);

  // Status bar — shows workspace count
  const statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 50);
  statusBar.command = "llmsafespace.refresh";
  statusBar.text = "$(vm) LLMSafeSpace";
  statusBar.tooltip = "Click to refresh workspaces";
  statusBar.show();
  context.subscriptions.push(statusBar);

  // Update status bar on tree refresh
  const updateStatusBar = async () => {
    try {
      const workspaces = await apiService.listWorkspaces();
      const activeCount = workspaces.filter(w => w.phase === "Active").length;
      statusBar.text = `$(vm) LLMSafeSpace: ${activeCount} active`;
      statusBar.tooltip = `${workspaces.length} total workspaces, ${activeCount} active`;
    } catch {
      statusBar.text = "$(vm) LLMSafeSpace: ⚠️";
      statusBar.tooltip = "Disconnected — click to retry";
    }
  };
  updateStatusBar();

  // Auto-refresh every 30s
  refreshInterval = setInterval(() => {
    treeProvider.refresh();
    updateStatusBar();
  }, 30_000);

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
