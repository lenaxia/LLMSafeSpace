import * as vscode from "vscode";
import { ApiService } from "../services/api";
import { WorkspaceTreeProvider, WorkspaceTreeItem } from "../providers/workspace-tree";

export function registerCreateWorkspaceCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.createWorkspace", async () => {
    const runtime = await vscode.window.showQuickPick(
      ["base", "python:3.11", "python:3.12", "nodejs:20", "go:1.23"],
      { placeHolder: "Select runtime", title: "Create Workspace" },
    );
    if (!runtime) return;

    const name = await vscode.window.showInputBox({
      prompt: "Workspace name",
      title: "Create Workspace",
      validateInput: v => v && v.length >= 1 ? null : "Name is required",
    });
    if (!name) return;

    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Creating workspace "${name}"...` },
      async () => {
        try {
          await api.createWorkspace(name, runtime);
          tree.refresh();
          vscode.window.showInformationMessage(`Workspace "${name}" created.`);
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to create workspace: ${e.message}`);
        }
      },
    );
  });
}

export function registerSuspendCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.suspendWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item?.workspace) return;
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Suspending "${item.workspace.name}"...` },
      async () => {
        try {
          await api.suspendWorkspace(item.workspace.id);
          tree.refresh();
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to suspend: ${e.message}`);
        }
      },
    );
  });
}

export function registerResumeCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.resumeWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item?.workspace) return;
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Resuming "${item.workspace.name}"...` },
      async () => {
        try {
          await api.resumeWorkspace(item.workspace.id);
          tree.refresh();
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to resume: ${e.message}`);
        }
      },
    );
  });
}

export function registerActivateCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.activateWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item?.workspace) return;
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Activating "${item.workspace.name}"...` },
      async () => {
        try {
          await api.activateWorkspace(item.workspace.id);
          tree.refresh();
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to activate: ${e.message}`);
        }
      },
    );
  });
}

export function registerTerminateCommand(api: ApiService, tree: WorkspaceTreeProvider) {
  return vscode.commands.registerCommand("llmsafespace.terminateWorkspace", async (item: WorkspaceTreeItem) => {
    if (!item?.workspace) return;
    const confirm = await vscode.window.showWarningMessage(
      `Terminate workspace "${item.workspace.name}"? This deletes the pod and PVC permanently.`,
      { modal: true },
      "Terminate",
    );
    if (confirm !== "Terminate") return;

    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Terminating "${item.workspace.name}"...` },
      async () => {
        try {
          await api.deleteWorkspace(item.workspace.id);
          tree.refresh();
          vscode.window.showInformationMessage(`Workspace "${item.workspace.name}" terminated.`);
        } catch (e: any) {
          vscode.window.showErrorMessage(`Failed to terminate: ${e.message}`);
        }
      },
    );
  });
}
