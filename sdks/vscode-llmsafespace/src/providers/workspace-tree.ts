import * as vscode from "vscode";
import type { WorkspaceListItem } from "@llmsafespace/sdk";
import { ApiService } from "../services/api";

export class WorkspaceTreeProvider implements vscode.TreeDataProvider<WorkspaceTreeItem> {
  private _onDidChangeTreeData = new vscode.EventEmitter<WorkspaceTreeItem | undefined>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  constructor(private apiService: ApiService) {}

  refresh(): void {
    this._onDidChangeTreeData.fire(undefined);
  }

  getTreeItem(element: WorkspaceTreeItem): vscode.TreeItem {
    return element;
  }

  async getChildren(): Promise<WorkspaceTreeItem[]> {
    const workspaces = await this.apiService.listWorkspaces();
    return workspaces.map((ws) => new WorkspaceTreeItem(ws));
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
