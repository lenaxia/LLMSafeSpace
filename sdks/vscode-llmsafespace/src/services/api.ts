import * as vscode from "vscode";
import { LLMSafeSpace, type WorkspaceListItem } from "@llmsafespace/sdk";

export class ApiService {
  private client: LLMSafeSpace | undefined;

  constructor(private context: vscode.ExtensionContext) {
    this.initClient();
  }

  isConfigured(): boolean {
    const url = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl");
    return !!url;
  }

  async configure(): Promise<void> {
    const url = await vscode.window.showInputBox({
      prompt: "LLMSafeSpace API URL",
      placeHolder: "https://llmsafespace.example.com",
      value: vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl") || "",
    });
    if (!url) return;

    const apiKey = await vscode.window.showInputBox({
      prompt: "API Key (lsp_...)",
      password: true,
    });
    if (!apiKey) return;

    await vscode.workspace.getConfiguration("llmsafespace").update("apiUrl", url, vscode.ConfigurationTarget.Global);
    await this.context.secrets.store("llmsafespace.apiKey", apiKey);
    this.initClient();
    vscode.window.showInformationMessage("LLMSafeSpace configured successfully");
  }

  async listWorkspaces(): Promise<WorkspaceListItem[]> {
    if (!this.client) return [];
    try {
      const result = await this.client.workspaces.list();
      return result.items;
    } catch {
      return [];
    }
  }

  async createWorkspace(name: string, runtime: string): Promise<void> {
    if (!this.client) throw new Error("Not configured");
    await this.client.workspaces.create({ name, runtime, storageSize: "10Gi" });
  }

  async suspendWorkspace(id: string): Promise<void> {
    if (!this.client) throw new Error("Not configured");
    await this.client.workspaces.suspend(id);
  }

  async resumeWorkspace(id: string): Promise<void> {
    if (!this.client) throw new Error("Not configured");
    await this.client.workspaces.resume(id);
  }

  async activateWorkspace(id: string): Promise<void> {
    if (!this.client) throw new Error("Not configured");
    await this.client.workspaces.activate(id);
  }

  async deleteWorkspace(id: string): Promise<void> {
    if (!this.client) throw new Error("Not configured");
    await this.client.workspaces.delete(id);
  }

  async getTerminalTicket(id: string): Promise<string> {
    if (!this.client) throw new Error("Not configured");
    const ticket = await this.client.terminal.getTicket(id);
    return ticket.ticket;
  }

  private async initClient(): Promise<void> {
    const url = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl");
    if (!url) return;
    const apiKey = await this.context.secrets.get("llmsafespace.apiKey");
    if (!apiKey) return;
    this.client = new LLMSafeSpace({ baseUrl: url, apiKey });
  }
}
