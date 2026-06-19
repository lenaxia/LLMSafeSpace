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
let apiService: ApiService | undefined;
let outputChannel: vscode.OutputChannel | undefined;

export function activate(context: vscode.ExtensionContext) {
  outputChannel = vscode.window.createOutputChannel("LLMSafeSpaces");
  context.subscriptions.push(outputChannel);
  outputChannel.appendLine(`LLMSafeSpaces extension activated (v${context.extension.packageJSON.version})`);

  apiService = new ApiService(context);
  const treeProvider = new WorkspaceTreeProvider(apiService);

  const treeView = vscode.window.createTreeView("llmsafespaces.workspaces", {
    treeDataProvider: treeProvider,
    showCollapseAll: true,
  });

  // Status bar
  const statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 50);
  statusBar.command = "llmsafespaces.refresh";
  statusBar.text = "$(vm) LLMSafeSpaces";
  statusBar.tooltip = "Click to refresh workspaces";
  statusBar.show();

  const updateStatusBar = async () => {
    try {
      const workspaces = await apiService!.listWorkspaces();
      const activeCount = workspaces.filter(w => w.phase === "Active").length;
      statusBar.text = `$(vm) LLMSafeSpaces: ${activeCount} active`;
      statusBar.tooltip = `${workspaces.length} total, ${activeCount} active`;
    } catch {
      statusBar.text = "$(warning) LLMSafeSpaces";
      statusBar.tooltip = "Disconnected — click to retry";
    }
  };

  // Register all commands
  context.subscriptions.push(
    treeView,
    statusBar,
    vscode.commands.registerCommand("llmsafespaces.refresh", () => {
      treeProvider.refresh();
      updateStatusBar();
    }),
    vscode.commands.registerCommand("llmsafespaces.configure", () => apiService!.configure()),
    vscode.commands.registerCommand("llmsafespaces.copyId", (item: any) => {
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

  // Auto-refresh with configurable interval
  const startRefreshTimer = () => {
    if (refreshInterval) clearInterval(refreshInterval);
    const seconds = vscode.workspace.getConfiguration("llmsafespaces").get<number>("refreshInterval") ?? 30;
    refreshInterval = setInterval(() => {
      treeProvider.refresh();
      updateStatusBar();
    }, seconds * 1000);
  };
  startRefreshTimer();

  // Re-start timer if config changes
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration(e => {
      if (e.affectsConfiguration("llmsafespaces.refreshInterval")) {
        startRefreshTimer();
      }
      if (e.affectsConfiguration("llmsafespaces.apiUrl")) {
        apiService!.reinitialize();
        treeProvider.refresh();
        updateStatusBar();
      }
    }),
  );

  // Initial status bar update
  updateStatusBar();

  // First-run experience
  if (!apiService.isConfigured()) {
    vscode.window
      .showInformationMessage("LLMSafeSpaces: Configure API connection to get started.", "Configure", "Later")
      .then(choice => {
        if (choice === "Configure") apiService!.configure();
      });
  }
}

export function deactivate() {
  if (refreshInterval) {
    clearInterval(refreshInterval);
    refreshInterval = undefined;
  }
  apiService?.dispose();
  apiService = undefined;
}
