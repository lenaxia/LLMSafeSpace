import * as vscode from "vscode";
import { ApiService } from "../services/api";

/**
 * Opens a WebSocket terminal connected to a workspace pod.
 * Uses the one-time ticket pattern from US-14.2.
 */
export function registerTerminalCommand(context: vscode.ExtensionContext, api: ApiService) {
  return vscode.commands.registerCommand("llmsafespace.openTerminal", async (item: any) => {
    if (!item?.workspace?.id) {
      vscode.window.showErrorMessage("No workspace selected");
      return;
    }

    const workspaceId = item.workspace.id;
    const workspaceName = item.workspace.name || workspaceId;

    try {
      // Get one-time ticket
      const ticket = await api.getTerminalTicket(workspaceId);

      // Build WebSocket URL
      const apiUrl = vscode.workspace.getConfiguration("llmsafespace").get<string>("apiUrl") || "";
      const wsUrl = apiUrl
        .replace(/^http/, "ws")
        .replace(/\/$/, "");
      const terminalUrl = `${wsUrl}/api/v1/workspaces/${workspaceId}/terminal?ticket=${ticket}`;

      // Create a pseudo-terminal that bridges WebSocket
      const pty = new WebSocketPty(terminalUrl);
      const terminal = vscode.window.createTerminal({
        name: `🔒 ${workspaceName}`,
        pty,
      });
      terminal.show();
    } catch (e: any) {
      vscode.window.showErrorMessage(`Failed to open terminal: ${e.message}`);
    }
  });
}

/**
 * WebSocket-backed pseudo-terminal.
 * Bridges VS Code's terminal I/O to the WebSocket terminal proxy.
 */
class WebSocketPty implements vscode.Pseudoterminal {
  private writeEmitter = new vscode.EventEmitter<string>();
  private closeEmitter = new vscode.EventEmitter<number>();
  onDidWrite = this.writeEmitter.event;
  onDidClose = this.closeEmitter.event;

  private ws: WebSocket | undefined;

  constructor(private url: string) {}

  open(): void {
    this.ws = new WebSocket(this.url);

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data as string);
        switch (msg.type) {
          case "output":
            this.writeEmitter.fire(msg.data);
            break;
          case "exit":
            this.closeEmitter.fire(msg.code ?? 0);
            break;
          case "error":
            this.writeEmitter.fire(`\r\n[Error: ${msg.message}]\r\n`);
            this.closeEmitter.fire(1);
            break;
        }
      } catch {
        // Non-JSON message, ignore
      }
    };

    this.ws.onerror = () => {
      this.writeEmitter.fire("\r\n[Connection error]\r\n");
      this.closeEmitter.fire(1);
    };

    this.ws.onclose = () => {
      this.closeEmitter.fire(0);
    };
  }

  close(): void {
    this.ws?.close();
  }

  handleInput(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "input", data }));
    }
  }

  setDimensions(dimensions: vscode.TerminalDimensions): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "resize", cols: dimensions.columns, rows: dimensions.rows }));
    }
  }
}
