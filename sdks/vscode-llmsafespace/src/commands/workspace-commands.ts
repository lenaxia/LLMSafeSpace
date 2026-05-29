import * as vscode from "vscode";
import { ApiService } from "../services/api";
import { WorkspaceTreeProvider, WorkspaceTreeItem } from "../providers/workspace-tree";

export function registerCreateWorkspaceCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.createWorkspace", async () => {
    const runtime = await vscode.window.showQuickPick(
      ["python:3.11", "python:3.12", "nodejs:20", "go:1.23", "base"],
      { placeHolder: "Select runtime" },
    );
    if (!runtime) return;

    const name = await vscode.window.showInputBox({ prompt: "Workspace name" });
    if (!name) return;

    try {
      await api.createWorkspace(name, runtime);
      tree.refresh();
      vscode.window.showInformationMessage(`Workspace "${name}" created`);
    } catch (e: any) {
      vscode.window.showErrorMessage(`Failed to create workspace: ${e.message}`);
    }
  });
}

export function registerSuspendCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.suspendWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item) return;
    await api.suspendWorkspace(item.workspace.id);
    tree.refresh();
  });
}

export function registerResumeCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.resumeWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item) return;
    await api.resumeWorkspace(item.workspace.id);
    tree.refresh();
  });
}

export function registerActivateCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.activateWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item) return;
    await api.activateWorkspace(item.workspace.id);
    tree.refresh();
  });
}

export function registerTerminateCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.terminateWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item) return;
    const confirm = await vscode.window.showWarningMessage(
      `Terminate workspace "${item.workspace.name}"? This cannot be undone.`,
      { modal: true },
      "Terminate",
    );
    if (confirm !== "Terminate") return;
    await api.deleteWorkspace(item.workspace.id);
    tree.refresh();
  });
}
