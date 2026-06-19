import * as vscode from "vscode";
import type { WorkspaceListItem } from "@llmsafespaces/sdk";
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
    if (!this.apiService.isConfigured()) {
      return [];  // viewsWelcome handles this case
    }
    try {
      const workspaces = await this.apiService.listWorkspaces();
      if (workspaces.length === 0) {
        return [new MessageTreeItem("No workspaces yet. Click + to create one.")];
      }
      return workspaces.map(ws => new WorkspaceTreeItem(ws));
    } catch {
      return [new MessageTreeItem("⚠️ Could not connect to LLMSafeSpaces")];
    }
  }
}

export class WorkspaceTreeItem extends vscode.TreeItem {
  constructor(public readonly workspace: WorkspaceListItem) {
    super(workspace.name || workspace.id, vscode.TreeItemCollapsibleState.None);

    const phase = (workspace.phase ?? "unknown").toLowerCase();
    this.description = `${workspace.runtime} · ${phase}`;
    this.tooltip = new vscode.MarkdownString(
      `**${workspace.name}**\n\n` +
      `- **ID:** \`${workspace.id}\`\n` +
      `- **Phase:** ${workspace.phase}\n` +
      `- **Runtime:** ${workspace.runtime}\n` +
      `- **Storage:** ${workspace.storageSize}`,
    );
    this.tooltip.isTrusted = true;
    this.contextValue = `workspace-${phase}`;
    this.accessibilityInformation = {
      label: `${workspace.name}, ${workspace.runtime}, ${phase}`,
      role: "treeitem",
    };

    switch (phase) {
      case "active":
        this.iconPath = new vscode.ThemeIcon("circle-filled", new vscode.ThemeColor("testing.iconPassed"));
        break;
      case "suspended":
        this.iconPath = new vscode.ThemeIcon("circle-filled", new vscode.ThemeColor("testing.iconQueued"));
        break;
      case "pending":
      case "resuming":
      case "creating":
        this.iconPath = new vscode.ThemeIcon("loading~spin");
        break;
      case "failed":
        this.iconPath = new vscode.ThemeIcon("circle-filled", new vscode.ThemeColor("testing.iconFailed"));
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
