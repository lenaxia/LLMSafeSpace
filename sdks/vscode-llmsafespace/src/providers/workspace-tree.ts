import * as vscode from "vscode";
import type { WorkspaceListItem } from "@llmsafespace/sdk";
import { ApiService } from "../services/api";

export class WorkspaceTreeProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private _onDidChangeTreeData = new vscode.EventEmitter<vscode.TreeItem | undefined>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  constructor(private apiService: ApiService) {}

  refresh(): void {
    this._onDidChangeTreeData.fire(undefined);
  }

  getTreeItem(element: vscode.TreeItem): vscode.TreeItem {
    return element;
  }

  async getChildren(): Promise<vscode.TreeItem[]> {
    try {
      const workspaces = await this.apiService.listWorkspaces();
      if (workspaces.length === 0) {
        return [new MessageTreeItem("No workspaces. Use command palette to create one.")];
      }
      return workspaces.map((ws) => new WorkspaceTreeItem(ws));
    } catch {
      return [new MessageTreeItem("⚠️ Disconnected — click Refresh or Configure")];
    }
  }
}

export class WorkspaceTreeItem extends vscode.TreeItem {
  constructor(public readonly workspace: WorkspaceListItem) {
    super(workspace.name || workspace.id, vscode.TreeItemCollapsibleState.None);

    const phase = workspace.phase?.toLowerCase() ?? "unknown";
    this.description = `${workspace.runtime} · ${phase}`;
    this.tooltip = `ID: ${workspace.id}\nPhase: ${workspace.phase}\nRuntime: ${workspace.runtime}`;
    this.contextValue = `workspace-${phase}`;

    switch (phase) {
      case "active":
        this.iconPath = new vscode.ThemeIcon("circle-filled", new vscode.ThemeColor("testing.iconPassed"));
        break;
      case "suspended":
        this.iconPath = new vscode.ThemeIcon("circle-filled", new vscode.ThemeColor("testing.iconQueued"));
        break;
      default:
        this.iconPath = new vscode.ThemeIcon("circle-outline");
    }
  }
}

class MessageTreeItem extends vscode.TreeItem {
  constructor(message: string) {
    super(message, vscode.TreeItemCollapsibleState.None);
    this.contextValue = "message";
  }
}
